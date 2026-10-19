//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func systemReadOnlyPaths() []string {
	return []string{
		"/usr",
		"/bin",
		"/sbin",
		"/lib",
		"/lib32",
		"/lib64",
		"/libx32",
		"/etc",
		"/etc/resolv.conf", // often a symlink into /run/systemd/resolve
		"/run/systemd/resolve",
		"/nix/store",
		"/run/current-system", // NixOS
	}
}

func preparePlatform(p *policy) error { return nil }

func sandboxCommand(p *policy) (*exec.Cmd, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("bubblewrap (bwrap) not found on PATH; install it (e.g. apt install bubblewrap): %w", err)
	}

	args := bwrapArgs(p)
	args = append(args, "--", p.Program)
	args = append(args, p.Args...)

	cmd := exec.Command(bwrap, args...)
	cmd.Dir = p.Workdir
	return cmd, nil
}

type mount struct {
	path     string
	writable bool
}

func bwrapArgs(p *policy) []string {
	args := []string{
		"--die-with-parent",
		"--unshare-all", // user, ipc, pid, net, uts, cgroup ...
		"--share-net",   // ... but keep the host network for LLM access
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	}

	// Bind every root at its resolved path, then recreate the host's
	// symlinks (e.g. /bin -> usr/bin) so the original paths keep working.
	var mounts []mount
	var originals []string
	seen := map[string]int{}
	addMount := func(orig string, writable bool) {
		originals = append(originals, orig)
		real := canonical(orig)
		if i, ok := seen[real]; ok {
			mounts[i].writable = mounts[i].writable || writable
			return
		}
		seen[real] = len(mounts)
		mounts = append(mounts, mount{real, writable})
	}
	for _, d := range p.ReadOnly {
		addMount(d, false)
	}
	for _, d := range p.ReadWrite {
		addMount(d, true)
	}

	// Drop read-only mounts already covered by another read-only mount, and
	// order parents before children so nested binds are not shadowed.
	var roots []string
	for _, m := range mounts {
		roots = append(roots, m.path)
	}
	sortByDepth(roots)
	byPath := map[string]mount{}
	for _, m := range mounts {
		byPath[m.path] = m
	}
	var placed []string
	for _, r := range roots {
		m := byPath[r]
		if !m.writable && coveredByMount(r, placed, byPath, false) {
			continue
		}
		if m.writable && coveredByMount(r, placed, byPath, true) {
			continue
		}
		placed = append(placed, r)
		flag := "--ro-bind"
		if m.writable {
			flag = "--bind"
		}
		args = append(args, flag, r, r)
	}

	links := newPathSet()
	for _, o := range originals {
		for _, l := range symlinkChain(o) {
			links.add(l)
		}
	}
	sorted := append([]string(nil), links.order...)
	sortByDepth(sorted)
	for _, l := range sorted {
		if coveredBy(l, placed) {
			continue // already visible through a bind mount
		}
		target, err := os.Readlink(l)
		if err != nil {
			continue
		}
		if parent := filepath.Dir(l); parent != "/" && !coveredBy(parent, placed) {
			args = append(args, "--dir", parent)
		}
		args = append(args, "--symlink", target, l)
	}

	args = append(args,
		// The synthetic root is a tmpfs; make it read-only so writes outside
		// the bound directories fail instead of silently vanishing.
		"--remount-ro", "/",
		"--chdir", p.Workdir,
		"--setenv", "TMPDIR", "/tmp",
		"--setenv", "PI_SANDBOXED", "1",
	)
	return args
}

// coveredByMount reports whether path is inside an already placed mount whose
// writability matches (a writable root inside a read-only one still needs its
// own bind).
func coveredByMount(path string, placed []string, byPath map[string]mount, writable bool) bool {
	for _, r := range placed {
		if isWithin(path, r) && byPath[r].writable == writable {
			return true
		}
	}
	return false
}
