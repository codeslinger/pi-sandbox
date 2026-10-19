//go:build !darwin && !linux

package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

func systemReadOnlyPaths() []string { return nil }

func preparePlatform(p *policy) error {
	return fmt.Errorf("unsupported platform %s: only macOS and Linux are supported", runtime.GOOS)
}

func sandboxCommand(p *policy) (*exec.Cmd, error) {
	return nil, fmt.Errorf("unsupported platform %s", runtime.GOOS)
}
