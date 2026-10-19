//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const sandboxExec = "/usr/bin/sandbox-exec"

func systemReadOnlyPaths() []string {
	return []string{
		"/System",
		"/usr",
		"/bin",
		"/sbin",
		"/Library/Apple",
		"/Library/Developer", // Command Line Tools (git, clang, xcrun)
		"/Library/Frameworks",
		"/Library/Java",
		"/Library/Preferences",
		"/Applications/Xcode.app",
		"/private/etc", // /etc: passwd, hosts, ssl certs, shells
		"/private/var/db/timezone",
		"/private/var/db/xcode_select_link",
		"/private/var/select",
		"/private/var/run/resolv.conf",
		"/dev",
	}
}

func preparePlatform(p *policy) error {
	// macOS cannot mount a fresh /tmp, so give the session its own TMPDIR
	// instead of exposing the shared per-user temp directory.
	dir, err := os.MkdirTemp("", "pi-sandbox-")
	if err != nil {
		return fmt.Errorf("cannot create private TMPDIR: %w", err)
	}
	p.TmpDir = canonical(dir)
	p.ReadWrite = append(p.ReadWrite, p.TmpDir)
	return nil
}

func sandboxCommand(p *policy) (*exec.Cmd, error) {
	if _, err := os.Stat(sandboxExec); err != nil {
		return nil, fmt.Errorf("%s not found: %w", sandboxExec, err)
	}
	profile, params := seatbeltProfile(p)

	args := []string{"-p", profile}
	for _, kv := range params {
		args = append(args, "-D", kv)
	}
	args = append(args, p.Program)
	args = append(args, p.Args...)

	cmd := exec.Command(sandboxExec, args...)
	cmd.Dir = p.Workdir
	cmd.Env = append(os.Environ(),
		"TMPDIR="+p.TmpDir+"/",
		"PI_SANDBOXED=1",
	)
	return cmd, nil
}

// seatbeltProfile renders an SBPL profile. Paths are passed as -D parameters
// so they never need escaping inside the profile text.
func seatbeltProfile(p *policy) (string, []string) {
	var b strings.Builder
	var params []string
	param := func(prefix string, i int, path string) string {
		name := fmt.Sprintf("%s_%d", prefix, i)
		params = append(params, name+"="+path)
		return fmt.Sprintf("(param %q)", name)
	}

	b.WriteString(seatbeltBase)

	b.WriteString("\n; read-only: system, toolchains, binaries on PATH\n(allow file-read* file-map-executable\n")
	for i, d := range withRealPaths(p.ReadOnly) {
		fmt.Fprintf(&b, "  (subpath %s)\n", param("RO", i, d))
	}
	b.WriteString(")\n")

	b.WriteString("\n; read-write: workdir, Pi config, private TMPDIR\n")
	rw := withRealPaths(p.ReadWrite)
	b.WriteString("(allow file-read* file-write* file-map-executable\n")
	for i, d := range rw {
		fmt.Fprintf(&b, "  (subpath %s)\n", param("RW", i, d))
	}
	b.WriteString(")\n")

	// Unix-domain sockets are only reachable inside writable roots, which
	// keeps host daemons (docker.sock, ssh-agent, ...) out of reach.
	b.WriteString("(allow network-outbound network-bind\n")
	for i := range rw {
		fmt.Fprintf(&b, "  (subpath (param \"RW_%d\"))\n", i)
	}
	b.WriteString(")\n")

	return b.String(), params
}

// withRealPaths returns the paths together with their symlink-resolved forms;
// Seatbelt matches against resolved vnode paths (e.g. /private/etc for /etc).
func withRealPaths(paths []string) []string {
	set := newPathSet()
	for _, p := range paths {
		set.add(p)
		if real, err := filepath.EvalSymlinks(p); err == nil {
			set.add(real)
		}
	}
	return set.order
}

const seatbeltBase = `(version 1)
(deny default)

; --- processes ---------------------------------------------------------------
(allow process-exec)
(allow process-fork)
(allow signal (target same-sandbox))
(allow process-info* (target same-sandbox))

; --- system information --------------------------------------------------------
(allow sysctl-read)
(allow user-preference-read)
(allow ipc-posix-sem)
(allow ipc-posix-shm-read-data ipc-posix-shm-read-metadata)
(allow iokit-open (iokit-user-client-class "RootDomainUserClient"))

; stat() anywhere so path resolution (realpath, lstat walks) works; contents
; stay unreadable outside the allowed roots.
(allow file-read-metadata)
(allow file-read-data (literal "/")) ; dyld opens the root directory at startup

; --- devices / terminal --------------------------------------------------------
(allow pseudo-tty)
(allow file-read* file-write* file-ioctl
  (literal "/dev/null")
  (literal "/dev/zero")
  (literal "/dev/random")
  (literal "/dev/urandom")
  (literal "/dev/tty")
  (literal "/dev/ptmx")
  (literal "/dev/dtracehelper")
  (regex #"^/dev/ttys[0-9]+$")
  (regex #"^/dev/fd/[0-9]+$"))

; --- mach services -------------------------------------------------------------
; Deliberately excludes LaunchServices, AppleEvents and other services that
; could start processes outside the sandbox.
(allow mach-lookup
  (global-name "com.apple.system.opendirectoryd.libinfo")
  (global-name "com.apple.system.opendirectoryd.membership")
  (global-name "com.apple.system.notification_center")
  (global-name "com.apple.system.logger")
  (global-name "com.apple.logd")
  (global-name "com.apple.diagnosticd")
  (global-name "com.apple.cfprefsd.daemon")
  (global-name "com.apple.cfprefsd.agent")
  (global-name "com.apple.bsd.dirhelper")
  (global-name "com.apple.SecurityServer")
  (global-name "com.apple.trustd")
  (global-name "com.apple.trustd.agent")
  (global-name "com.apple.ocspd")
  (global-name "com.apple.networkd")
  (global-name "com.apple.SystemConfiguration.configd")
  (global-name "com.apple.SystemConfiguration.DNSConfiguration")
  (global-name "com.apple.dnssd.service")
  (global-name "com.apple.FSEvents")
  (global-name "com.apple.pasteboard.1"))

; --- network -------------------------------------------------------------------
(allow system-socket)
(allow network-outbound (remote ip))
(allow network-inbound (local ip))
(allow network-bind (local ip))
(allow network-outbound (literal "/private/var/run/mDNSResponder"))
`
