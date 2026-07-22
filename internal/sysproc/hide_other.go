//go:build !windows

// Package sysproc holds small, platform-specific helpers for configuring how
// child processes are spawned.
package sysproc

import "os/exec"

// Hide is a no-op on non-Windows platforms, where console child processes do not
// allocate their own windows.
func Hide(cmd *exec.Cmd) {}
