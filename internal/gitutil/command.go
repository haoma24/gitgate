package gitutil

import (
	"io"
	"os/exec"
)

// Command wraps exec.Cmd for testability and convenience.
type Command struct {
	cmd *exec.Cmd
}

// NewCommand creates a new Command.
func NewCommand(name string, args ...string) *Command {
	return &Command{cmd: exec.Command(name, args...)}
}

// NewCommandCapture creates a Command with stdout captured.
type CaptureCommand struct {
	cmd *exec.Cmd
}

func NewCommandCapture(name string, args ...string) *CaptureCommand {
	return &CaptureCommand{cmd: exec.Command(name, args...)}
}

func (c *CaptureCommand) SetStdout(w io.Writer) {
	c.cmd.Stdout = w
}

func (c *CaptureCommand) Run() error {
	return c.cmd.Run()
}

func (c *Command) Run() error {
	return c.cmd.Run()
}
