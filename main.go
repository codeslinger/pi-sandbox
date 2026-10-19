// Command pi-sandbox runs a Pi coding agent session inside an OS sandbox.
//
// The working directory is the only project location the session can write
// to. Pi's config directory (~/.pi/agent) is writable too, because Pi stores
// sessions, auth tokens, and lock files there. Binaries on $PATH and the
// system toolchain directories are mounted read-only so Pi can run them as
// tools. Network access is allowed so Pi can reach LLM providers.
//
// On macOS the sandbox uses sandbox-exec (Seatbelt). On Linux it uses
// bubblewrap (bwrap).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type options struct {
	readOnly  []string
	readWrite []string
	program   string
	args      []string
	dryRun    bool
	verbose   bool
	allowHome bool
}

func main() {
	os.Exit(run())
}

func run() int {
	opts, err := parseFlags(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "pi-sandbox:", err)
		return 2
	}

	pol, err := buildPolicy(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pi-sandbox:", err)
		return 1
	}
	defer pol.cleanup()

	cmd, err := sandboxCommand(pol)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pi-sandbox:", err)
		return 1
	}

	if opts.verbose || opts.dryRun {
		pol.describe(os.Stderr)
		fmt.Fprintln(os.Stderr, "command:", shellQuote(cmd.Args))
	}
	if opts.dryRun {
		return 0
	}

	return runChild(cmd)
}

func parseFlags(argv []string) (*options, error) {
	opts := &options{}
	fs := flag.NewFlagSet("pi-sandbox", flag.ContinueOnError)
	var ro, rw stringList
	fs.Var(&ro, "ro", "extra `path` to expose read-only (repeatable)")
	fs.Var(&rw, "rw", "extra `path` to expose read-write (repeatable)")
	fs.StringVar(&opts.program, "exec", "pi", "`program` to run inside the sandbox")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "print the sandbox policy and command, then exit")
	fs.BoolVar(&opts.verbose, "v", false, "print the sandbox policy before starting")
	fs.BoolVar(&opts.allowHome, "allow-home", false, "allow the working directory to be $HOME or /")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "Usage: pi-sandbox [flags] [--] [pi arguments...]")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Runs Pi sandboxed to the current directory. Network access is allowed;")
		fmt.Fprintln(out, "binaries on $PATH are available read-only.")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return nil, err
	}
	opts.readOnly = ro
	opts.readWrite = rw
	opts.args = fs.Args()
	return opts, nil
}

// runChild starts the sandboxed process and waits for it. Terminal signals
// (SIGINT, SIGQUIT, SIGTSTP, SIGWINCH) reach the child directly because it
// shares our process group. SIGTERM and SIGHUP are forwarded explicitly.
func runChild(cmd *exec.Cmd) int {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	signal.Ignore(syscall.SIGINT, syscall.SIGQUIT)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "pi-sandbox:", err)
		return 1
	}
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()

	err := cmd.Wait()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return exitErr.ExitCode()
	}
	fmt.Fprintln(os.Stderr, "pi-sandbox:", err)
	return 1
}

func shellQuote(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`!*?[](){};&|<>#~") {
			out[i] = a
			continue
		}
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
