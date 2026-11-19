package lsp

import "runtime"

// Language is a language that Perch can ask a server about.
type Language struct {
	// ID names the language in the settings and the configuration file.
	ID   string
	Name string
	// Extensions maps lowercase file extensions to the identifiers the
	// protocol knows those files by.
	Extensions map[string]string
	Server     Server
	// Custom marks a language from the person's configuration file.
	Custom bool
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
	cServer    = Server{Name: "clangd", Program: "clangd", Install: installs{darwin: "brew install llvm", linux: "sudo apt install clangd", windows: "winget install LLVM.LLVM"}.forHere()}
)

// installs are the commands that install a server on each platform, empty
// where there is no one command to give.
type installs struct {
	darwin  string
	linux   string
	windows string
}

// forHere returns the command for the platform Perch runs on.
func (i installs) forHere() string {
	return i.on(runtime.GOOS)
}

// on returns the command for a platform, Linux's for the other Unix ones.
func (i installs) on(goos string) string {
	switch goos {
	case "darwin":
		return i.darwin
	case "windows":
		return i.windows
	default:
		return i.linux
	}
}

// sameLanguage maps each extension to one protocol identifier.
func sameLanguage(identifier string, extensions ...string) map[string]string {
	mapped := make(map[string]string, len(extensions))
	for _, extension := range extensions {
		mapped[extension] = identifier
	}
	return mapped
}

// Catalog returns the languages Perch knows without any configuration.
func Catalog() []Language {
	return []Language{
		{ID: "go", Name: "Go", Extensions: sameLanguage("go", ".go"), Server: goServer},
		{ID: "typescript", Name: "TypeScript / JavaScript", Server: typescriptServer, Extensions: map[string]string{
			".ts": "typescript", ".mts": "typescript", ".cts": "typescript", ".tsx": "typescriptreact",
			".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascriptreact",
		}},
		{ID: "python", Name: "Python", Extensions: sameLanguage("python", ".py", ".pyi"), Server: pythonServer},
		{ID: "rust", Name: "Rust", Extensions: sameLanguage("rust", ".rs"), Server: rustServer},
		{ID: "c", Name: "C / C++ / Objective-C", Server: cServer, Extensions: map[string]string{
			".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp",
			".m": "objective-c", ".mm": "objective-cpp",
		}},
		{ID: "swift", Name: "Swift", Extensions: sameLanguage("swift", ".swift"), Server: Server{
			Name: "sourcekit-lsp", Program: "sourcekit-lsp", Install: installs{darwin: "xcode-select --install"}.forHere(),
		}},
		{ID: "java", Name: "Java", Extensions: sameLanguage("java", ".java"), Server: Server{
			Name: "jdtls", Program: "jdtls", Install: installs{darwin: "brew install jdtls"}.forHere(),
		}},
		{ID: "kotlin", Name: "Kotlin", Extensions: sameLanguage("kotlin", ".kt", ".kts"), Server: Server{
			Name: "kotlin-language-server", Program: "kotlin-language-server", Install: installs{darwin: "brew install kotlin-language-server"}.forHere(),
		}},
		{ID: "csharp", Name: "C#", Extensions: sameLanguage("csharp", ".cs"), Server: Server{
			Name: "csharp-ls", Program: "csharp-ls", Install: "dotnet tool install --global csharp-ls",
		}},
		{ID: "ruby", Name: "Ruby", Extensions: sameLanguage("ruby", ".rb", ".rake"), Server: Server{
			Name: "ruby-lsp", Program: "ruby-lsp", Install: "gem install ruby-lsp",
		}},
		{ID: "php", Name: "PHP", Extensions: sameLanguage("php", ".php"), Server: Server{
			Name: "intelephense", Program: "intelephense", Args: []string{"--stdio"}, Install: "npm install -g intelephense",
		}},
		{ID: "lua", Name: "Lua", Extensions: sameLanguage("lua", ".lua"), Server: Server{
			Name: "lua-language-server", Program: "lua-language-server", Install: installs{darwin: "brew install lua-language-server"}.forHere(),
		}},
		{ID: "zig", Name: "Zig", Extensions: sameLanguage("zig", ".zig"), Server: Server{
			Name: "zls", Program: "zls", Install: installs{darwin: "brew install zls"}.forHere(),
		}},
		{ID: "bash", Name: "Shell", Extensions: sameLanguage("shellscript", ".sh", ".bash", ".zsh"), Server: Server{
			Name: "bash-language-server", Program: "bash-language-server", Args: []string{"start"},
			Install: "npm install -g bash-language-server",
		}},
		{ID: "dart", Name: "Dart", Extensions: sameLanguage("dart", ".dart"), Server: Server{
			Name: "dart", Program: "dart", Args: []string{"language-server", "--protocol=lsp"},
			Install: installs{darwin: "brew tap dart-lang/dart && brew install dart"}.forHere(),
		}},
		{ID: "elixir", Name: "Elixir", Extensions: sameLanguage("elixir", ".ex", ".exs"), Server: Server{
			Name: "elixir-ls", Program: "elixir-ls", Install: installs{darwin: "brew install elixir-ls"}.forHere(),
		}},
		{ID: "haskell", Name: "Haskell", Extensions: sameLanguage("haskell", ".hs"), Server: Server{
			Name: "haskell-language-server", Program: "haskell-language-server-wrapper", Args: []string{"--lsp"},
			Install: "ghcup install hls",
		}},
		{ID: "scala", Name: "Scala", Extensions: sameLanguage("scala", ".scala", ".sc"), Server: Server{
			Name: "metals", Program: "metals", Install: "cs install metals",
		}},
		{ID: "ocaml", Name: "OCaml", Extensions: sameLanguage("ocaml", ".ml", ".mli"), Server: Server{
			Name: "ocamllsp", Program: "ocamllsp", Install: "opam install ocaml-lsp-server",
		}},
		{ID: "clojure", Name: "Clojure", Extensions: sameLanguage("clojure", ".clj", ".cljs", ".cljc"), Server: Server{
			Name: "clojure-lsp", Program: "clojure-lsp", Install: installs{darwin: "brew install clojure-lsp/brew/clojure-lsp-native"}.forHere(),
		}},
		{ID: "svelte", Name: "Svelte", Extensions: sameLanguage("svelte", ".svelte"), Server: Server{
			Name: "svelte-language-server", Program: "svelteserver", Args: []string{"--stdio"},
			Install: "npm install -g svelte-language-server",
		}},
		{ID: "terraform", Name: "Terraform", Extensions: sameLanguage("terraform", ".tf"), Server: Server{
			Name: "terraform-ls", Program: "terraform-ls", Args: []string{"serve"},
			Install: installs{darwin: "brew install hashicorp/tap/terraform-ls"}.forHere(),
		}},
	}
}
