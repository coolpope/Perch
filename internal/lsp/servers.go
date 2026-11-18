// Package lsp asks language servers where symbols are defined. Servers start
// when first needed and leave when idle, so that Perch costs nothing for
// languages that are not being read.
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const shellPathTimeout = 3 * time.Second

// ErrServerMissing reports a language server that is not installed.
var ErrServerMissing = errors.New("language server is not installed")

// Server describes a language server.
type Server struct {
	// Name identifies the server, and is the program people install.
	Name    string
	Program string
	Args    []string
	// Install is the command that installs the server.
	Install string
	// RootMarkers are files that mark the folder a server works from: the
	// nearest one above a file, inside the project. Without any, the server
	// works from the project.
	RootMarkers []string
	// InitializationOptions are passed to the server as it starts.
	InitializationOptions json.RawMessage
}

// toolPath is the search path of programs. An app opened from the Dock inherits
// a short PATH without the folders where developers install their tools.
var toolPath = sync.OnceValue(func() string {
	folders := filepath.SplitList(os.Getenv("PATH"))
	folders = append(folders, filepath.SplitList(loginShellPath())...)
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range []string{"go/bin", ".cargo/bin", ".local/bin", ".bun/bin", ".volta/bin", ".npm-global/bin"} {
			folders = append(folders, filepath.Join(home, dir))
		}
	}
	folders = append(folders, "/opt/homebrew/bin", "/usr/local/bin", "/usr/local/go/bin")
	return strings.Join(uniqueFolders(folders), string(os.PathListSeparator))
})

// loginShellPath asks the person's shell for its PATH, and is empty when the
// shell does not answer in time.
func loginShellPath() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellPathTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, shell, "-lc", `printf %s "$PATH"`).Output()
	if err != nil {
		return ""
	}
	return string(output)
}

func uniqueFolders(folders []string) []string {
	seen := make(map[string]bool, len(folders))
	unique := make([]string, 0, len(folders))
	for _, folder := range folders {
		if folder != "" && !seen[folder] {
			seen[folder] = true
			unique = append(unique, folder)
		}
	}
	return unique
}

// findProgram returns the full path of a server's program.
func findProgram(server Server) (string, error) {
	if filepath.IsAbs(server.Program) {
		if isProgram(server.Program) {
			return server.Program, nil
		}
		return "", fmt.Errorf("find %s: %w", server.Program, ErrServerMissing)
	}
	for _, folder := range filepath.SplitList(toolPath()) {
		// A relative folder of the search path would name a file of the project.
		if candidate := filepath.Join(folder, server.Program); filepath.IsAbs(folder) && isProgram(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("find %s: %w", server.Program, ErrServerMissing)
}

func isProgram(file string) bool {
	info, err := os.Stat(file)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// Installed reports whether a server's program can be found.
func Installed(server Server) bool {
	_, err := findProgram(server)
	return err == nil
}

// serverRoot returns the folder a server works from for a file of the project.
func serverRoot(project string, file string, markers []string) string {
	relative, err := filepath.Rel(project, filepath.Dir(file))
	if len(markers) == 0 || err != nil || !filepath.IsLocal(relative) {
		return project
	}
	for dir := filepath.Dir(file); dir != project; dir = filepath.Dir(dir) {
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
	}
	return project
}
