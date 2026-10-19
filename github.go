package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GitHub CLI support.
//
// gh reads its config directory (config.yml, hosts.yml) at startup and fails
// if the directory exists but is unreadable, so that directory is exposed
// read-only. On macOS gh normally keeps its token in the login keychain,
// which the sandbox cannot reach. Instead of exposing the keychain, the token
// is fetched on the host with `gh auth token` and passed in as GH_TOKEN. This
// also lets git push over HTTPS when gh is the git credential helper.

// ghConfigDir returns the directory gh reads its configuration from, using
// the same lookup order as gh: $GH_CONFIG_DIR, $XDG_CONFIG_HOME/gh,
// ~/.config/gh.
func ghConfigDir(home string) string {
	if d := os.Getenv("GH_CONFIG_DIR"); d != "" {
		return expandPath(d, home)
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(expandPath(d, home), "gh")
	}
	return filepath.Join(home, ".config", "gh")
}

// ghTokenTimeout bounds `gh auth token`, which may wait on the keychain.
const ghTokenTimeout = 10 * time.Second

// githubEnv returns the environment entries that give the sandboxed session
// GitHub credentials. It returns nothing if a token is already set in the
// environment (it is inherited as is), if gh is not installed, or if gh is
// not logged in.
func githubEnv() []string {
	if os.Getenv("GH_TOKEN") != "" || os.Getenv("GITHUB_TOKEN") != "" {
		return nil
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghTokenTimeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, gh, "auth", "token")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	token := strings.TrimSpace(out.String())
	if token == "" || strings.ContainsAny(token, "\n\r\x00") {
		return nil
	}
	return []string{"GH_TOKEN=" + token}
}
