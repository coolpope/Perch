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
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marukolibro/Perch/internal/proc"
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

// defaultPathExt is the extensions of programs on Windows when PATHEXT is not
// set.
const defaultPathExt = ".COM;.EXE;.BAT;.CMD"

// toolPath is the search path of programs. An app opened from the Dock or the
// Start menu inherits a short PATH without the folders where developers
// install their tools.
var toolPath = sync.OnceValue(func() string {
	folders := filepath.SplitList(os.Getenv("PATH"))
	folders = append(folders, filepath.SplitList(loginShellPath())...)
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	folders = append(folders, toolFolders(runtime.GOOS, home, os.Getenv)...)
	return strings.Join(uniqueFolders(folders), string(os.PathListSeparator))
})

// toolFolders are the folders where developers install tools on a platform,
// which the PATH of an app may lack. Those under home are left out without
// one, and those under a folder of the environment without it.
func toolFolders(goos string, home string, getenv func(string) string) []string {
	var folders []string
	under := func(base string, dirs ...string) {
		if base == "" {
			return
		}
		for _, dir := range dirs {
			folders = append(folders, filepath.Join(base, filepath.FromSlash(dir)))
		}
	}
	under(home, "go/bin", ".cargo/bin", ".local/bin", ".bun/bin", ".volta/bin", ".npm-global/bin", ".dotnet/tools")
	switch goos {
	case "windows":
		under(getenv("APPDATA"), "npm")
		under(getenv("LOCALAPPDATA"), "Microsoft/WinGet/Links")
		under(home, "scoop/shims")
		under(getenv("ProgramFiles"), "LLVM/bin", "Go/bin")
	case "darwin":
		folders = append(folders, "/opt/homebrew/bin", "/usr/local/bin", "/usr/local/go/bin")
	default:
		folders = append(folders, "/usr/local/bin", "/usr/local/go/bin", "/snap/bin", "/home/linuxbrew/.linuxbrew/bin")
	}
	return folders
}

// loginShellPath asks the person's shell for its PATH, and is empty when the
// shell does not answer in time.
func loginShellPath() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellPathTimeout)
	defer cancel()
	output, err := proc.CommandContext(ctx, shell, "-lc", `printf %s "$PATH"`).Output()
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
	pathExt := os.Getenv("PATHEXT")
	if pathExt == "" {
		pathExt = defaultPathExt
	}
	for _, folder := range filepath.SplitList(toolPath()) {
		// A relative folder of the search path would name a file of the project.
		if !filepath.IsAbs(folder) {
			continue
		}
		for _, name := range programNames(runtime.GOOS, server.Program, pathExt) {
			if candidate := filepath.Join(folder, name); isProgram(candidate) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("find %s: %w", server.Program, ErrServerMissing)
}

// programNames are the names a program's file may have: on Windows the name
// with each extension of pathExt, unless it has one of them already.
func programNames(goos string, program string, pathExt string) []string {
	if goos != "windows" {
		return []string{program}
	}
	extensions := strings.Split(strings.ToLower(pathExt), ";")
	if slices.Contains(extensions, strings.ToLower(filepath.Ext(program))) {
		return []string{program}
	}
	names := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		if extension != "" {
			names = append(names, program+extension)
		}
	}
	return names
}

// isProgram reports whether a file can be run: one marked executable, or on
// Windows, which has no such mark, any file, its extension having been chosen
// from PATHEXT.
func isProgram(file string) bool {
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode()&0o111 != 0
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
