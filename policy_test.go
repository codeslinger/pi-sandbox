package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIsWithin(t *testing.T) {
	cases := []struct {
		p, root string
		want    bool
	}{
		{"/a/b", "/a", true},
		{"/a", "/a", true},
		{"/ab", "/a", false},
		{"/a", "/a/b", false},
		{"/anything", "/", true},
	}
	for _, c := range cases {
		if got := isWithin(c.p, c.root); got != c.want {
			t.Errorf("isWithin(%q, %q) = %v, want %v", c.p, c.root, got, c.want)
		}
	}
}

func TestToolPrefix(t *testing.T) {
	home := "/home/u"
	cases := map[string]string{
		"/opt/homebrew/bin":             "/opt/homebrew",
		"/usr/local/sbin":               "/usr/local",
		"/home/u/.pyenv/shims":          "/home/u/.pyenv",
		"/home/u/.nvm/versions/v20/bin": "/home/u/.nvm/versions/v20",
		"/home/u/bin":                   "", // would expose $HOME
		"/home/u/.local/bin":            "", // ~/.local holds app data
		"/bin":                          "", // would expose /
		"/opt/tool/libexec":             "", // not bin-like
	}
	for dir, want := range cases {
		if got := toolPrefix(dir, home); got != want {
			t.Errorf("toolPrefix(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestMinimize(t *testing.T) {
	got := minimize([]string{"/usr/bin", "/usr", "/opt/a/b", "/opt/a", "/opt/ab"})
	want := []string{"/opt/a", "/opt/ab", "/usr"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("minimize = %v, want %v", got, want)
	}
}

func TestBinaryRootsFollowsSymlinks(t *testing.T) {
	root := canonical(t.TempDir())
	home := filepath.Join(root, "home")
	bin := filepath.Join(home, ".local", "bin")
	venv := filepath.Join(home, ".local", "share", "pipx", "venvs", "tool", "bin")
	for _, d := range []string{bin, venv} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(venv, "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(venv, "tool"), filepath.Join(bin, "tool")); err != nil {
		t.Fatal(err)
	}

	got := binaryRoots(bin, home)
	want := []string{bin, filepath.Dir(venv)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("binaryRoots = %v, want %v", got, want)
	}
}

func TestSymlinkChain(t *testing.T) {
	root := canonical(t.TempDir())
	real := filepath.Join(root, "real", "bin")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink("real", link); err != nil {
		t.Fatal(err)
	}
	got := symlinkChain(filepath.Join(link, "bin"))
	if !reflect.DeepEqual(got, []string{link}) {
		t.Errorf("symlinkChain = %v, want [%s]", got, link)
	}
}
