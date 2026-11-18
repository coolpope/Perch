// Package lsp asks language servers where symbols are defined. Servers start
// when first needed and leave when idle, so that Perch costs nothing for
// languages that are not being read.
package lsp

import (
	"context"
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
}

var (
	goServer         = Server{Name: "gopls", Program: "gopls", Install: "go install golang.org/x/tools/gopls@latest"}
	typescriptServer = Server{
		Name: "typescript-language-server", Program: "typescript-language-server", Args: []string{"--stdio"},
		Install: "npm install -g typescript typescript-language-server",
	}
	pythonServer = Server{
		Name: "pyright", Program: "pyright-langserver", Args: []string{"--stdio"}, Install: "npm install -g pyright",
	}
	rustServer = Server{Name: "rust-analyzer", Program: "rust-analyzer", Install: "rustup component add rust-analyzer"}
	cServer    = Server{Name: "clangd", Program: "clangd", Install: "brew install llvm"}
)

// language is how a kind of file is served.
type language struct {
	id     string
	server Server
}

var languages = map[string]language{
	".go":  {"go", goServer},
	".ts":  {"typescript", typescriptServer},
	".tsx": {"typescriptreact", typescriptServer},
	".mts": {"typescript", typescriptServer},
	".cts": {"typescript", typescriptServer},
	".js":  {"javascript", typescriptServer},
	".jsx": {"javascriptreact", typescriptServer},
	".mjs": {"javascript", typescriptServer},
	".cjs": {"javascript", typescriptServer},
	".py":  {"python", pythonServer},
	".pyi": {"python", pythonServer},
	".rs":  {"rust", rustServer},
	".c":   {"c", cServer},
	".h":   {"c", cServer},
	".cc":  {"cpp", cServer},
	".cpp": {"cpp", cServer},
	".cxx": {"cpp", cServer},
	".hpp": {"cpp", cServer},
	".hh":  {"cpp", cServer},
	".m":   {"objective-c", cServer},
	".mm":  {"objective-cpp", cServer},
}

// ServerFor returns the server of a file and the language it knows the file as.
func ServerFor(file string) (Server, string, bool) {
	found, ok := languages[strings.ToLower(filepath.Ext(file))]
	return found.server, found.id, ok
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
	for _, folder := range filepath.SplitList(toolPath()) {
		candidate := filepath.Join(folder, server.Program)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("find %s: %w", server.Program, ErrServerMissing)
}
