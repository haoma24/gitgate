//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// setDetachSysProcAttr configures a command to run detached on Unix.
func setDetachSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session, detach from terminal
	}
}

// isProcessRunning checks if a process with the given PID is still running.
func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, kill(pid, 0) checks if the process exists
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
