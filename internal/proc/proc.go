// Package proc builds the commands Perch runs in the background, such as Git
// and language servers, so that none of them opens a window of its own.
package proc

import (
	"context"
	"os/exec"
)

// Command returns a command that runs name with args without a window.
func Command(name string, args ...string) *exec.Cmd {
	command := exec.Command(name, args...)
	command.SysProcAttr = backgroundAttr()
	return command
}

// CommandContext is Command with a context that kills the process when done.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, args...)
	command.SysProcAttr = backgroundAttr()
	return command
}
