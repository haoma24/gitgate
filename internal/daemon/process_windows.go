//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// setDetachSysProcAttr configures a command to run detached on Windows.
func setDetachSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}
}

// isProcessRunning checks if a process with the given PID is still running on Windows.
func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Windows, OpenProcess with SYNCHRONIZE permission
	// We use a trick: try to get the exit code; if the process doesn't exist, it fails
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(proc.Pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(handle)
	return true
}
