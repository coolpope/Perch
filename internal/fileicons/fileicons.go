// Package fileicons names the icon of a file from its name or path, and of a
// folder from its name, with the icons of Atom Material Icons (MIT, Elior "Mallowigi" Boukhobza; see
// assets/LICENSE). Its rules and icons are generated from that project.
package fileicons

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

//go:generate go run ./generate

//go:embed assets/rules.json assets/icons assets/folders.json assets/folders assets/foldersOpen assets/LICENSE
var assets embed.FS

// Icon is the icon of a file, and the colors of the parts its rule colors.
type Icon struct {
	// Name names the icon's file.
	Name string
	// Light and Dark are #RRGGBB colors for light and dark themes.
	Light string
	Dark  string
}

// rule is a rule of assets/rules.json: a pattern, or the extensions or whole
// names it stands for.
type rule struct {
	Pattern    string   `json:"pattern"`
	Literal    string   `json:"literal"`
	Extensions []string `json:"extensions"`
	Names      []string `json:"names"`
	Priority   int      `json:"priority"`
	Icon       string   `json:"icon"`
	Light      string   `json:"light"`
	Dark       string   `json:"dark"`
}

// patternRule is a rule that needs its pattern matched. Its pattern compiles
// the first time a path holds its literal, so that rules about files the
// project lacks cost no memory.
type patternRule struct {
	order    int
	source   string
	literal  string
	compiled *regexp.Regexp
}

// matches reports whether the rule matches a file's name or path.
func (p *patternRule) matches(name string, relPath string) (bool, error) {
	if !strings.Contains(relPath, p.literal) {
		return false, nil
	}
	if p.compiled == nil {
		compiled, err := regexp.Compile(p.source)
		if err != nil {
			return false, fmt.Errorf("compile file icon rule %q: %w", p.source, err)
		}
		p.compiled = compiled
	}
	return p.compiled.MatchString(name) || p.compiled.MatchString(relPath), nil
}

// ruleSet is the rules ready to use. Rules are tried in order, and the first
// that matches gives the icon: extensions and names are looked up, and only
// the patterns before the best of those are matched.
type ruleSet struct {
	icons      []Icon
	extensions map[string]int
	names      map[string]int
	patterns   []patternRule
}

// loadRules reads the rules once.
var loadRules = sync.OnceValues(func() (*ruleSet, error) {
	data, err := assets.ReadFile("assets/rules.json")
	if err != nil {
		return nil, fmt.Errorf("read file icon rules: %w", err)
	}
	var rules []rule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("decode file icon rules: %w", err)
	}
	set := &ruleSet{extensions: map[string]int{}, names: map[string]int{}}
	for order, r := range rules {
		set.icons = append(set.icons, Icon{Name: r.Icon, Light: r.Light, Dark: r.Dark})
		for _, extension := range r.Extensions {
			if _, taken := set.extensions[extension]; !taken {
				set.extensions[extension] = order
			}
		}
		for _, name := range r.Names {
			if _, taken := set.names[name]; !taken {
				set.names[name] = order
			}
		}
		if r.Pattern != "" {
			set.patterns = append(set.patterns, patternRule{order: order, source: r.Pattern, literal: r.Literal})
		}
	}
	return set, nil
})

// matching serializes For, which compiles patterns as they are first needed.
var matching sync.Mutex

// For returns the icon of a file by its path, and false when no rule names one.
// Rules match the file's name or its slash-separated path.
func For(file string) (Icon, bool, error) {
	set, err := loadRules()
	if err != nil {
		return Icon{}, false, err
	}
	slashed := filepath.ToSlash(file)
	matching.Lock()
	defer matching.Unlock()
	name := path.Base(slashed)
	order, found := set.lookup(name)
	for index := range set.patterns {
		p := &set.patterns[index]
		if found && p.order >= order {
			break
		}
		matched, err := p.matches(name, slashed)
		if err != nil {
			return Icon{}, false, err
		}
		if matched {
			order, found = p.order, true
			break
		}
	}
	if !found {
		return Icon{}, false, nil
	}
	return set.icons[order], true, nil
}

// lookup returns the first rule that names a file's whole name or one of its
// extensions, as the extension "d.ts" and then "ts" of index.d.ts.
func (set *ruleSet) lookup(name string) (int, bool) {
	best, found := set.names[name]
	for index := strings.IndexByte(name, '.'); index >= 0; {
		if order, named := set.extensions[name[index+1:]]; named && (!found || order < best) {
			best, found = order, true
		}
		next := strings.IndexByte(name[index+1:], '.')
		if next < 0 {
			break
		}
		index += next + 1
	}
	return best, found
}

// SVG returns an icon's drawing, whose colored parts fill with currentColor.
func SVG(icon Icon) ([]byte, error) {
	return readCompressed("assets/icons/" + icon.Name + ".svg.gz")
}

// GenericFolder is the icon of a folder no rule names, which fills with
// currentColor.
const GenericFolder = "folder"

// folderRule is a rule of assets/folders.json.
type folderRule struct {
	Names []string `json:"names"`
	Icon  string   `json:"icon"`
}

// loadFolders reads the folder rules once, as a map from a folder's name to
// its icon.
var loadFolders = sync.OnceValues(func() (map[string]string, error) {
	data, err := assets.ReadFile("assets/folders.json")
	if err != nil {
		return nil, fmt.Errorf("read folder icon rules: %w", err)
	}
	var rules []folderRule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("decode folder icon rules: %w", err)
	}
	icons := map[string]string{}
	for _, r := range rules {
		for _, name := range r.Names {
			if _, taken := icons[name]; !taken {
				icons[name] = r.Icon
			}
		}
	}
	return icons, nil
})

// ForFolder returns the icon of a folder by its name: one of its own, which
// holds its colors, or GenericFolder.
func ForFolder(name string) (string, error) {
	icons, err := loadFolders()
	if err != nil {
		return "", err
	}
	if icon, found := icons[name]; found {
		return icon, nil
	}
	return GenericFolder, nil
}

// FolderSVG returns a folder icon's drawing, open or closed.
func FolderSVG(icon string, open bool) ([]byte, error) {
	dir := "assets/folders/"
	if open {
		dir = "assets/foldersOpen/"
	}
	return readCompressed(dir + icon + ".svg.gz")
}

// readCompressed returns a drawing stored with gzip.
func readCompressed(name string) ([]byte, error) {
	file, err := assets.Open(name)
	if err != nil {
		return nil, fmt.Errorf("read icon %q: %w", name, err)
	}
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("read icon %q: %w", name, err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read icon %q: %w", name, err)
	}
	return data, nil
}
