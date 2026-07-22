package gitutil

import (
	"io"
	"os/exec"

	"github.com/jvrsantacruz/gitgate/internal/sysproc"
)

// Command wraps exec.Cmd for testability and convenience.
type Command struct {
	cmd *exec.Cmd
}

// NewCommand creates a new Command.
func NewCommand(name string, args ...string) *Command {
	cmd := exec.Command(name, args...)
	sysproc.Hide(cmd)
	return &Command{cmd: cmd}
}

// NewCommandCapture creates a Command with stdout captured.
type CaptureCommand struct {
	cmd *exec.Cmd
}

func NewCommandCapture(name string, args ...string) *CaptureCommand {
	cmd := exec.Command(name, args...)
	sysproc.Hide(cmd)
	return &CaptureCommand{cmd: cmd}
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
