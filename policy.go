package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// policy is the platform-independent description of what the sandboxed
// process may access.
type policy struct {
	Workdir   string   // current directory, read-write
	ReadOnly  []string // absolute paths readable (and executable)
	ReadWrite []string // absolute paths readable and writable (includes Workdir)
	Program   string   // absolute path of the program to run
	Args      []string // program arguments
	Home      string

	// TmpDir is a private temporary directory created for the session on
	// platforms that cannot mount a fresh /tmp (macOS). It is removed on exit.
	TmpDir string
}

func (p *policy) cleanup() {
	if p.TmpDir != "" {
		_ = os.RemoveAll(p.TmpDir)
	}
}

func (p *policy) describe(w io.Writer) {
	fmt.Fprintln(w, "workdir:", p.Workdir)
	for _, d := range p.ReadWrite {
		fmt.Fprintln(w, "  rw", d)
	}
	for _, d := range p.ReadOnly {
		fmt.Fprintln(w, "  ro", d)
	}
}

func buildPolicy(opts *options) (*policy, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	home = canonical(home)

	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	wd = canonical(wd)
	if !opts.allowHome && (wd == "/" || isWithin(home, wd)) {
		return nil, fmt.Errorf("refusing to sandbox to %s: it contains your home directory (use --allow-home to override)", wd)
	}

	program, err := exec.LookPath(opts.program)
	if err != nil {
		return nil, fmt.Errorf("cannot find %q on PATH: %w", opts.program, err)
	}
	program, err = filepath.Abs(program)
	if err != nil {
		return nil, err
	}

	p := &policy{Workdir: wd, Program: program, Args: opts.args, Home: home}

	// --- read-write ---------------------------------------------------------
	rw := newPathSet()
	rw.add(wd)
	agentDir := envPath("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"), home)
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create Pi config dir %s: %w", agentDir, err)
	}
	rw.add(agentDir)
	if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
		d = expandPath(d, home)
		if err := os.MkdirAll(d, 0o700); err == nil {
			rw.add(d)
		}
	}
	for _, d := range opts.readWrite {
		d = expandPath(d, home)
		if !exists(d) {
			return nil, fmt.Errorf("--rw path does not exist: %s", d)
		}
		rw.add(d)
	}

	// --- read-only ----------------------------------------------------------
	ro := newPathSet()
	for _, d := range systemReadOnlyPaths() {
		ro.add(d)
	}
	for _, d := range binaryRoots(os.Getenv("PATH"), home) {
		ro.add(d)
	}
	// The program itself may live outside PATH (e.g. --exec ./bin/tool).
	if real, err := filepath.EvalSymlinks(program); err == nil {
		ro.add(program)
		ro.add(real)
		if pre := toolPrefix(filepath.Dir(real), home); pre != "" {
			ro.add(pre)
		}
	}
	for _, rel := range homeReadOnlyPaths {
		ro.add(filepath.Join(home, rel))
	}
	for _, d := range opts.readOnly {
		d = expandPath(d, home)
		if !exists(d) {
			return nil, fmt.Errorf("--ro path does not exist: %s", d)
		}
		ro.add(d)
	}

	p.ReadWrite = minimize(rw.existing())
	// Read-only entries that live inside a writable root are redundant.
	var roList []string
	for _, d := range minimize(ro.existing()) {
		if !coveredBy(d, p.ReadWrite) {
			roList = append(roList, d)
		}
	}
	p.ReadOnly = roList

	if err := preparePlatform(p); err != nil {
		p.cleanup()
		return nil, err
	}
	return p, nil
}

// homeReadOnlyPaths are paths below $HOME exposed read-only when they exist:
// global agent skills, git and MCP config, and common language toolchain homes that
// PATH shims depend on.
var homeReadOnlyPaths = []string{
	".agents",
	".gitconfig",
	".gitignore_global",
	".config/git",
	".config/mcp", // MCP server config shared across agents
	".CFUserTextEncoding",
	".cargo",
	".rustup",
	".nvm",
	".pyenv",
	".rbenv",
	".asdf",
	".sdkman",
	".volta",
	".bun",
	".deno",
	".local/share/mise",
}

// binaryRoots returns directories that must be readable so the programs on
// PATH can execute: each PATH directory, its install prefix (the parent of a
// bin/sbin/shims directory, so that sibling lib/ and share/ trees resolve),
// and the targets of symlinks inside PATH directories.
func binaryRoots(pathEnv, home string) []string {
	var out []string
	for _, dir := range filepath.SplitList(pathEnv) {
		// Relative PATH entries (".", "") would resolve against the
		// sandboxed workdir; they need nothing extra.
		if dir == "" || !filepath.IsAbs(dir) || !isDir(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		if isSensitive(dir, home) {
			continue
		}
		out = append(out, dir)
		if pre := toolPrefix(dir, home); pre != "" {
			out = append(out, pre)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			target, err := filepath.EvalSymlinks(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			tdir := filepath.Dir(target)
			if isSensitive(tdir, home) {
				continue
			}
			if pre := toolPrefix(tdir, home); pre != "" {
				out = append(out, pre)
			} else {
				out = append(out, tdir)
			}
		}
	}
	return out
}

// toolPrefix returns the install prefix for a bin-like directory, or "" if
// the directory is not bin-like or the prefix would expose too much.
func toolPrefix(dir, home string) string {
	switch filepath.Base(dir) {
	case "bin", "sbin", "shims":
	default:
		return ""
	}
	pre := filepath.Dir(dir)
	if isSensitive(pre, home) {
		return ""
	}
	return pre
}

// isSensitive reports whether a path must never be auto-exposed as a whole:
// the filesystem root, $HOME or any ancestor of it, and broad per-user data
// directories directly under $HOME.
func isSensitive(p, home string) bool {
	p = filepath.Clean(p)
	if p == "/" || isWithin(home, p) {
		return true
	}
	for _, rel := range []string{".local", ".local/share", ".config", "Library", "Documents", "Desktop", "Downloads"} {
		if p == filepath.Join(home, rel) {
			return true
		}
	}
	return false
}

// --- path helpers -----------------------------------------------------------

type pathSet struct {
	seen  map[string]bool
	order []string
}

func newPathSet() *pathSet { return &pathSet{seen: map[string]bool{}} }

func (s *pathSet) add(p string) {
	if p == "" || !filepath.IsAbs(p) {
		return
	}
	p = filepath.Clean(p)
	if !s.seen[p] {
		s.seen[p] = true
		s.order = append(s.order, p)
	}
}

func (s *pathSet) existing() []string {
	var out []string
	for _, p := range s.order {
		if exists(p) {
			out = append(out, p)
		}
	}
	return out
}

// minimize sorts paths and removes entries nested inside another entry.
func minimize(paths []string) []string {
	sorted := append([]string(nil), paths...)
	sortByDepth(sorted)
	var out []string
	for _, p := range sorted {
		if !coveredBy(p, out) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func sortByDepth(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		di, dj := depth(paths[i]), depth(paths[j])
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})
}

func depth(p string) int {
	if p == "/" {
		return 0
	}
	return strings.Count(filepath.Clean(p), "/")
}

func coveredBy(p string, roots []string) bool {
	for _, r := range roots {
		if isWithin(p, r) {
			return true
		}
	}
	return false
}

// isWithin reports whether p equals root or is a descendant of it.
func isWithin(p, root string) bool {
	p, root = filepath.Clean(p), filepath.Clean(root)
	if p == root || root == "/" {
		return true
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}

func canonical(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

func expandPath(p, home string) string {
	if p == "~" {
		p = home
	} else if strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

func envPath(key, def, home string) string {
	if v := os.Getenv(key); v != "" {
		return expandPath(v, home)
	}
	return def
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// symlinkChain returns every path component of p (from the root down) that
// is itself a symlink on the host.
func symlinkChain(p string) []string {
	var out []string
	cur := "/"
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(p), "/"), "/") {
		if part == "" {
			continue
		}
		next := filepath.Join(cur, part)
		fi, err := os.Lstat(next)
		if err != nil {
			return out
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			out = append(out, next)
			target, err := filepath.EvalSymlinks(next)
			if err != nil {
				return out
			}
			next = target
		}
		cur = next
	}
	return out
}
