package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGhConfigDir(t *testing.T) {
	home := "/home/u"

	t.Setenv("GH_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := ghConfigDir(home), "/home/u/.config/gh"; got != want {
		t.Errorf("default: got %q, want %q", got, want)
	}

	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := ghConfigDir(home), "/xdg/gh"; got != want {
		t.Errorf("XDG_CONFIG_HOME: got %q, want %q", got, want)
	}

	t.Setenv("GH_CONFIG_DIR", "~/ghconf")
	if got, want := ghConfigDir(home), "/home/u/ghconf"; got != want {
		t.Errorf("GH_CONFIG_DIR: got %q, want %q", got, want)
	}
}

// fakeGh puts a gh script on PATH that runs the given shell body.
func fakeGh(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
}

func TestGithubEnvFromGh(t *testing.T) {
	fakeGh(t, `[ "$1 $2" = "auth token" ] && echo gho_abc123`)
	got := githubEnv()
	want := []string{"GH_TOKEN=gho_abc123"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("githubEnv = %v, want %v", got, want)
	}
}

func TestGithubEnvNotLoggedIn(t *testing.T) {
	fakeGh(t, `echo "no oauth token" >&2; exit 1`)
	if got := githubEnv(); got != nil {
		t.Errorf("githubEnv = %v, want nil", got)
	}
}

func TestGithubEnvTokenAlreadySet(t *testing.T) {
	fakeGh(t, `echo should-not-run`)
	t.Setenv("GITHUB_TOKEN", "from-env")
	if got := githubEnv(); got != nil {
		t.Errorf("githubEnv = %v, want nil (inherit existing token)", got)
	}
}

func TestGithubEnvNoGh(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	if got := githubEnv(); got != nil {
		t.Errorf("githubEnv = %v, want nil", got)
	}
}
