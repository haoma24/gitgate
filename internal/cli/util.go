package cli

import (
	"bytes"
	"os/exec"
)

// runCaptured runs a command and returns its combined output.
func runCaptured(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
