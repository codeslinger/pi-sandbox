# pi-sandbox

Run a [Pi](https://pi.dev) coding agent session sandboxed to the current
directory.

- **macOS**: `sandbox-exec` (Seatbelt) with a deny-by-default profile
- **Linux**: [bubblewrap](https://github.com/containers/bubblewrap) (`bwrap`)

## Build

```sh
go build -o pi-sandbox .
# optional: install onto PATH
go install .
```

Linux also needs `bwrap` (`apt install bubblewrap`, `dnf install bubblewrap`, ...).

## Usage

```sh
cd ~/src/my-project
pi-sandbox                         # interactive Pi session
pi-sandbox -- -c                   # pass flags to pi after --
pi-sandbox -- -p "fix the tests"   # non-interactive
pi-sandbox --ro ~/data --rw ~/.cache/uv   # expose extra paths
pi-sandbox --dry-run               # show the policy and command, don't run
pi-sandbox --exec bash             # open a shell in the same sandbox (debugging)
```

| Flag | Meaning |
|------|---------|
| `--ro PATH` | Extra path readable (and executable), repeatable |
| `--rw PATH` | Extra path readable and writable, repeatable |
| `--exec PROG` | Run `PROG` instead of `pi` |
| `--dry-run` | Print the policy and the full sandbox command, then exit |
| `-v` | Print the policy before starting |
| `--allow-home` | Allow running when the current directory is `$HOME` or `/` |

Inside the sandbox `PI_SANDBOXED=1` is set.

## What the session can access

| Access | Paths |
|--------|-------|
| read-write | current directory; Pi config dir (`$PI_CODING_AGENT_DIR` or `~/.pi/agent`) for sessions, auth and lock files; `$PI_CODING_AGENT_SESSION_DIR` if set; a private temp dir (`TMPDIR`) |
| read-only | system dirs (`/usr`, `/bin`, `/etc`, `/System`, `/Library/Developer`, ...); every `$PATH` directory and its install prefix (e.g. `/opt/homebrew` for `/opt/homebrew/bin`); the targets of symlinks in `$PATH` dirs; toolchain homes (`~/.nvm`, `~/.pyenv`, `~/.cargo`, `~/.rustup`, ...); `~/.agents` (skills); `~/.gitconfig`, `~/.config/git`, `~/.config/mcp` |
| network | Allowed for TCP/UDP so Pi can reach LLM providers. Unix-domain sockets work only inside the writable dirs, so host daemons such as `docker.sock` and `ssh-agent` are not reachable. |
| denied | everything else, including the rest of `$HOME` (`~/.ssh`, `~/.aws`, shell rc files, other projects) |

The automatic `$PATH` expansion never exposes `/`, `$HOME` itself, or broad
data directories such as `~/.local`, `~/.config`, and `~/Library`. The tool
refuses to run when the current directory is `$HOME` or `/`.

### Platform notes

**macOS**: The profile is deny-by-default. It allows a small set of Mach
services: DNS, TLS trust evaluation, preferences, logging, FSEvents, and the
pasteboard. LaunchServices and AppleEvents are blocked, so `open` and
`osascript` cannot start processes outside the sandbox. `/tmp` is not
writable. Instead, the session gets a private `TMPDIR`, removed when Pi exits.
Pass `-v` to see the paths. If something fails, check the denials with:

```sh
log stream --style compact --predicate 'eventMessage CONTAINS "deny"'
```

**Linux**: The tool uses `--unshare-all --share-net`. The session gets new
user, PID, IPC, UTS and cgroup namespaces, a fresh `/proc`, `/dev` and tmpfs
`/tmp`, and a read-only synthetic root. It keeps the host network. It does not
use `--new-session`, because that would break terminal resize signals for the
TUI. On kernels older than 6.2, or with `dev.tty.legacy_tiocsti=1`, a
sandboxed process could therefore inject keystrokes into the parent terminal
via `TIOCSTI`.

## Caveats

- **MCP servers and other subprocesses run inside the sandbox.** Pi starts
  them, so they get the same limits. A server that needs paths outside the
  defaults (its own install dir, a cache such as `~/.cache/uv`, browser
  cookies) needs `--ro`/`--rw`.
- **Writable config can be used to escape later.** The session can write
  `~/.pi/agent` (extensions, settings) and project files such as `.pi/`,
  `.git/hooks`, `Makefile`, and `.envrc`. Code planted there runs *unsandboxed*
  the next time you use Pi, git, or other tools outside the sandbox. Review
  changes before you run them unsandboxed.
- **The network is open.** Anything the session can read, it can send
  somewhere. The default read set is kept small for this reason.
- Language caches outside the defaults (e.g. `~/.cache/go-build`, `~/.npm`)
  are not writable. Builds that need them either fail or need `--rw`.
