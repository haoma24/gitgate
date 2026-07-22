//go:build windows

// Package sysproc holds small, platform-specific helpers for configuring how
// child processes are spawned.
package sysproc

import (
	"os/exec"
	"syscall"
)

// createNoWindow (CREATE_NO_WINDOW) tells Windows not to allocate a console
// window for a child process. See:
// https://learn.microsoft.com/windows/win32/procthread/process-creation-flags
const createNoWindow = 0x08000000

// Hide configures cmd so it does not pop up a console window when spawned. The
// GitGate daemon runs detached (no console of its own), so without this flag
// every git/go/gh invocation flashes a cmd.exe window on screen. It preserves
// any CreationFlags already set (e.g. process-group flags) by OR-ing the flag in.
func Hide(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
