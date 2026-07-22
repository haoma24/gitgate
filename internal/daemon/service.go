// Package daemon/service provides OS-specific daemon lifecycle management.
package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ServiceManager manages the GitGate daemon as an OS service.
type ServiceManager interface {
	Start() error
	Stop() error
	Restart() error
	IsRunning() (bool, error)
}

// NewServiceManager returns the appropriate service manager for the current OS.
func NewServiceManager() (ServiceManager, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot determine executable path: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		return &LaunchdManager{gitgateBin: self}, nil
	case "linux":
		if isSystemdAvailable() {
			return &SystemdManager{gitgateBin: self}, nil
		}
		return &DetachedManager{gitgateBin: self}, nil
	case "windows":
		return &WindowsManager{gitgateBin: self}, nil
	default:
		return &DetachedManager{gitgateBin: self}, nil
	}
}

// ── macOS: launchd ────────────────────────────────────────────────────────────

// LaunchdManager manages the daemon via macOS launchd.
type LaunchdManager struct {
	gitgateBin string
}

func (m *LaunchdManager) plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.gitgate.daemon.plist")
}

func (m *LaunchdManager) label() string { return "com.gitgate.daemon" }

func (m *LaunchdManager) Start() error {
	// Write plist
	ggHome := GitGateHome()
	logPath := filepath.Join(ggHome, "daemon.log")
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>daemon</string>
        <string>start</string>
        <string>--foreground</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>`, m.label(), m.gitgateBin, logPath, logPath)

	plistPath := m.plistPath()
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}

	return exec.Command("launchctl", "load", "-w", plistPath).Run()
}

func (m *LaunchdManager) Stop() error {
	return exec.Command("launchctl", "unload", m.plistPath()).Run()
}

func (m *LaunchdManager) Restart() error {
	_ = m.Stop()
	return m.Start()
}

func (m *LaunchdManager) IsRunning() (bool, error) {
	out, err := exec.Command("launchctl", "list", m.label()).Output()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), m.label()), nil
}

// ── Linux: systemd ────────────────────────────────────────────────────────────

// SystemdManager manages the daemon via systemd user service.
type SystemdManager struct {
	gitgateBin string
}

func (m *SystemdManager) serviceName() string { return "gitgate.service" }

func (m *SystemdManager) serviceFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", m.serviceName()), nil
}

func (m *SystemdManager) Start() error {
	ggHome := GitGateHome()
	logPath := filepath.Join(ggHome, "daemon.log")

	unit := fmt.Sprintf(`[Unit]
Description=GitGate Daemon
After=network.target

[Service]
ExecStart=%s daemon start --foreground
Restart=on-failure
RestartSec=5
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, m.gitgateBin, logPath, logPath)

	path, err := m.serviceFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}

	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return err
	}
	return exec.Command("systemctl", "--user", "enable", "--now", m.serviceName()).Run()
}

func (m *SystemdManager) Stop() error {
	return exec.Command("systemctl", "--user", "stop", m.serviceName()).Run()
}

func (m *SystemdManager) Restart() error {
	return exec.Command("systemctl", "--user", "restart", m.serviceName()).Run()
}

func (m *SystemdManager) IsRunning() (bool, error) {
	err := exec.Command("systemctl", "--user", "is-active", "--quiet", m.serviceName()).Run()
	return err == nil, nil
}

// ── Windows: Task Scheduler + detached process ────────────────────────────────

// WindowsManager manages the daemon via Windows Task Scheduler.
type WindowsManager struct {
	gitgateBin string
}

func (m *WindowsManager) taskName() string { return "GitGate Daemon" }

func (m *WindowsManager) Start() error {
	// Try Task Scheduler first
	cmd := exec.Command("schtasks", "/Create", "/F",
		"/TN", m.taskName(),
		"/TR", fmt.Sprintf(`"%s" daemon start --foreground`, m.gitgateBin),
		"/SC", "ONLOGON",
		"/RL", "HIGHEST",
		"/IT",
	)
	if err := cmd.Run(); err == nil {
		// Run it now too
		return exec.Command("schtasks", "/Run", "/TN", m.taskName()).Run()
	}

	// Fallback to detached process
	det := &DetachedManager{gitgateBin: m.gitgateBin}
	return det.Start()
}

func (m *WindowsManager) Stop() error {
	_ = exec.Command("schtasks", "/End", "/TN", m.taskName()).Run()
	return m.killPID()
}

func (m *WindowsManager) Restart() error {
	_ = m.Stop()
	return m.Start()
}

func (m *WindowsManager) IsRunning() (bool, error) {
	return isPIDRunning(GitGateHome())
}

func (m *WindowsManager) killPID() error {
	pid, err := readPID(GitGateHome())
	if err != nil {
		return nil // Not running
	}
	return exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
}

// ── Fallback: detached process ────────────────────────────────────────────────

// DetachedManager starts the daemon as a detached background process.
type DetachedManager struct {
	gitgateBin string
}

func (m *DetachedManager) Start() error {
	cmd := exec.Command(m.gitgateBin, "daemon", "start", "--foreground")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Detach from current process group
	setDetachSysProcAttr(cmd)
	return cmd.Start()
}

func (m *DetachedManager) Stop() error {
	return killByPID(GitGateHome())
}

func (m *DetachedManager) Restart() error {
	_ = m.Stop()
	return m.Start()
}

func (m *DetachedManager) IsRunning() (bool, error) {
	return isPIDRunning(GitGateHome())
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func isSystemdAvailable() bool {
	_, err := exec.LookPath("systemctl")
	return err == nil
}

func readPID(home string) (int, error) {
	pidPath := filepath.Join(home, pidFileName)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID in file: %w", err)
	}
	return pid, nil
}

func isPIDRunning(home string) (bool, error) {
	pid, err := readPID(home)
	if err != nil {
		return false, nil
	}
	return isProcessRunning(pid), nil
}

func killByPID(home string) error {
	pid, err := readPID(home)
	if err != nil {
		return nil // Not running
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	return proc.Kill()
}

// GitGateHome returns the home directory (re-exported for service.go usage).
// The actual implementation is in gitutil; we reuse it here to avoid import cycles.
func GitGateHome() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gitgate")
}
