// Perch is a lightweight native code reader and editor for working alongside
// coding agents.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/egoist/mygo"

	"perch/internal/agentlink"
	"perch/internal/agentsetup"
	"perch/internal/app"
)

// mcpCommand makes this process the bridge between a coding agent and the
// running app, with no window of its own.
const mcpCommand = "mcp"

// setupCommand connects Perch to the coding agents on this machine.
const setupCommand = "setup"

func main() {
	if len(os.Args) == 2 && os.Args[1] == mcpCommand {
		if err := agentlink.RunMCP(context.Background(), os.Stdin, os.Stdout); err != nil {
			slog.Error("perch mcp stopped", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == setupCommand {
		os.Exit(agentsetup.RunCLI(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
	}
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	desktop := app.NewDesktop()
	mygo.App.WhenReady(func() {
		desktop.Start(os.Args[1:])
	})
	if err := mygo.App.Run(); err != nil {
		slog.Error("perch stopped", "error", err)
		os.Exit(1)
	}
}
