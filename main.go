// Perch is a lightweight native code reader and editor for working alongside
// coding agents.
package main

import (
	"log/slog"
	"os"

	"github.com/egoist/mygo"

	"perch/internal/app"
)

func main() {
	perch := app.New()
	mygo.App.WhenReady(func() {
		perch.Start(initialFolder(os.Args))
	})
	if err := mygo.App.Run(); err != nil {
		slog.Error("perch stopped", "error", err)
		os.Exit(1)
	}
}

// initialFolder returns the folder named on the command line, if any.
func initialFolder(args []string) string {
	if len(args) < 2 {
		return ""
	}
	return args[1]
}
