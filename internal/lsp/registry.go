package lsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ErrInvalidConfig reports a configuration file that Perch cannot use.
var ErrInvalidConfig = errors.New("invalid language server configuration")

// ConfigTemplate is the content of a new configuration file.
const ConfigTemplate = "{\n  \"languageServers\": {}\n}\n"

// ConfigExample shows how to add a language to the configuration file.
const ConfigExample = `"zig": {
  "name": "Zig",
  "extensions": [".zig"],
  "command": "zls",
  "args": [],
  "rootMarkers": ["build.zig"],
  "install": "brew install zls"
}`

// customServer is a language of the configuration file.
type customServer struct {
	Name                  string          `json:"name"`
	Extensions            []string        `json:"extensions"`
	LanguageID            string          `json:"languageId"`
	Command               string          `json:"command"`
	Args                  []string        `json:"args"`
	RootMarkers           []string        `json:"rootMarkers"`
	Install               string          `json:"install"`
	InitializationOptions json.RawMessage `json:"initializationOptions"`
}

// configFile is the person's configuration of language servers.
type configFile struct {
	LanguageServers map[string]customServer `json:"languageServers"`
}

// LoadCustom reads the languages of the person's configuration file. It is the
// only source of commands besides the catalog: Perch never runs a command named
// by a project. A file that does not exist has no languages.
func LoadCustom(file string) ([]Language, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read language servers %q: %w", file, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config configFile
	if err := decoder.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidConfig, file, err)
	}
	return customLanguages(file, config)
}

// customLanguages checks the entries of a configuration file.
func customLanguages(file string, config configFile) ([]Language, error) {
	ids := make([]string, 0, len(config.LanguageServers))
	for id := range config.LanguageServers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	claimed := map[string]string{}
	languages := make([]Language, 0, len(ids))
	for _, id := range ids {
		language, err := customLanguage(id, config.LanguageServers[id])
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %q: %w", ErrInvalidConfig, file, id, err)
		}
		for extension := range language.Extensions {
			if owner, taken := claimed[extension]; taken {
				return nil, fmt.Errorf("%w: %s: %q and %q both claim %s", ErrInvalidConfig, file, owner, id, extension)
			}
			claimed[extension] = id
		}
		languages = append(languages, language)
	}
	return languages, nil
}

func customLanguage(id string, entry customServer) (Language, error) {
	if strings.TrimSpace(id) == "" {
		return Language{}, errors.New("a language needs a name")
	}
	program, err := customProgram(entry.Command)
	if err != nil {
		return Language{}, err
	}
	if len(entry.Extensions) == 0 {
		return Language{}, errors.New(`"extensions" is empty`)
	}
	identifier := entry.LanguageID
	if identifier == "" {
		identifier = id
	}
	extensions := make(map[string]string, len(entry.Extensions))
	for _, extension := range entry.Extensions {
		if !validExtension(extension) {
			return Language{}, fmt.Errorf("extension %q must look like .zig", extension)
		}
		extensions[strings.ToLower(extension)] = identifier
	}
	name := entry.Name
	if name == "" {
		name = id
	}
	return Language{ID: id, Name: name, Extensions: extensions, Custom: true, Server: Server{
		Name: filepath.Base(program), Program: program, Args: entry.Args, Install: entry.Install,
		RootMarkers: entry.RootMarkers, InitializationOptions: entry.InitializationOptions,
	}}, nil
}

// customProgram accepts a program on the search path or an absolute path. A
// relative path is refused, since servers start inside the opened project and
// it would run a file of that project.
func customProgram(command string) (string, error) {
	if rest, home := strings.CutPrefix(command, "~/"); home {
		dir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %q: %w", command, err)
		}
		return filepath.Join(dir, rest), nil
	}
	if command == "" {
		return "", errors.New(`"command" is empty`)
	}
	if !filepath.IsAbs(command) && strings.ContainsRune(command, filepath.Separator) {
		return "", fmt.Errorf("command %q must be a program name or an absolute path", command)
	}
	return command, nil
}

func validExtension(extension string) bool {
	return len(extension) > 1 && extension[0] == '.' && !strings.ContainsAny(extension[1:], `./\ `)
}

// Registry is the languages in use: the catalog, the person's own, and which of
// them are turned off.
type Registry struct {
	languages   []Language
	byExtension map[string]Language
	disabled    []string
}

// NewRegistry combines the catalog with custom languages, which replace a
// catalog language of the same ID and take over the extensions they name.
func NewRegistry(custom []Language, disabled []string) Registry {
	languages := Catalog()
	for _, language := range custom {
		index := slices.IndexFunc(languages, func(known Language) bool { return known.ID == language.ID })
		if index >= 0 {
			languages[index] = language
			continue
		}
		languages = append(languages, language)
	}
	byExtension := map[string]Language{}
	for _, language := range languages {
		if !language.Custom {
			addExtensions(byExtension, language)
		}
	}
	for _, language := range languages {
		if language.Custom {
			addExtensions(byExtension, language)
		}
	}
	return Registry{languages: languages, byExtension: byExtension, disabled: disabled}
}

func addExtensions(byExtension map[string]Language, language Language) {
	for extension := range language.Extensions {
		byExtension[extension] = language
	}
}

// Languages returns every language, the catalog's first.
func (r Registry) Languages() []Language {
	return r.languages
}

// Enabled reports whether a language has not been turned off.
func (r Registry) Enabled(id string) bool {
	return !slices.Contains(r.disabled, id)
}

// Match returns the language of a file and the identifier the protocol knows
// the file by.
func (r Registry) Match(file string) (Language, string, bool) {
	extension := strings.ToLower(filepath.Ext(file))
	language, found := r.byExtension[extension]
	return language, language.Extensions[extension], found
}
