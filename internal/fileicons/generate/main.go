// Command generate brings the file icons of Atom Material Icons into Perch:
// the rules that name a file's icon, and the icons those rules use, from a
// fixed commit of the project. Run it with go generate in internal/fileicons.
//
// It keeps the rules outside the icon packs of particular frameworks, which
// the plugin leaves off until chosen, whose patterns Go's regexp reads. A rule's dark
// variant, named with " Dark", gives the color of its icon on dark themes.
// The parts of an icon that a rule colors fill with currentColor, which Perch
// tints, so that each icon is stored once.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// commit is the commit of AtomMaterialUI/iconGenerator the icons come from.
	commit  = "2196346f2b52a9f5367c6f710f22b1ad2c108d27"
	rawBase = "https://raw.githubusercontent.com/AtomMaterialUI/iconGenerator/" + commit + "/"
	// darkSuffix names the variant of a rule for dark themes.
	darkSuffix = " Dark"
	attempts   = 3
)

// upstreamRule is a rule of icon_associations.json.
type upstreamRule struct {
	Name      string `json:"name"`
	Pattern   string `json:"pattern"`
	Priority  string `json:"priority"`
	Icon      string `json:"icon"`
	IconColor string `json:"iconColor"`
	IconType  string `json:"iconType"`
	IconPack  string `json:"iconPack"`
}

type upstreamFile struct {
	Associations struct {
		Associations struct {
			Regex []struct {
				Value upstreamRule `json:"value"`
			} `json:"regex"`
		} `json:"associations"`
	} `json:"associations"`
}

// Rule is a rule as Perch reads it: assets/rules.json, in the order rules are
// tried. A rule names extensions or whole names when its pattern says no more,
// which Perch looks up rather than matching, and keeps its pattern otherwise.
type Rule struct {
	Pattern string `json:"pattern,omitempty"`
	// Literal is text every match of Pattern holds, which spares matching
	// the pattern against a path without it.
	Literal    string   `json:"literal,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
	Names      []string `json:"names,omitempty"`
	Priority   int      `json:"priority"`
	// Icon names a file of assets/icons, without .svg.
	Icon  string `json:"icon"`
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

var (
	comments    = regexp.MustCompile(`(?s)<!--.*?-->`)
	declaration = regexp.MustCompile(`<\?xml.*?\?>`)
	spaces      = regexp.MustCompile(`\s+`)
	sizes       = regexp.MustCompile(` (width|height)="[^"]*"`)
	// coloredFill is the fill of an element a rule colors.
	coloredFill = regexp.MustCompile(`<[^>]*data-(iconColor|themed)[^>]*>`)
	fill        = regexp.MustCompile(`fill="[^"]*"`)
	// extensionPattern is a pattern of extensions only, as .*\.(css|tss)$.
	extensionPattern = regexp.MustCompile(`^\.\*\\\.\(?([A-Za-z0-9_+-]+(?:\|[A-Za-z0-9_+-]+)*)\)?\$$`)
	// namePattern is a pattern of whole names only, as ^(Makefile|makefile)$.
	namePattern = regexp.MustCompile(`^\^\(?((?:[A-Za-z0-9_-]|\\\.)+(?:\|(?:[A-Za-z0-9_-]|\\\.)+)*)\)?\$$`)
)

func main() {
	out := "assets"
	if err := generate(out); err != nil {
		slog.Error("generate file icons", "error", err)
		os.Exit(1)
	}
}

func generate(out string) error {
	if err := generateFiles(out); err != nil {
		return err
	}
	if err := generateFolders(out); err != nil {
		return err
	}
	license, err := fetch(rawBase + "LICENSE")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "LICENSE"), license, 0o644); err != nil {
		return fmt.Errorf("write license: %w", err)
	}
	return nil
}

// generateFiles writes the rules of file icons and their icons.
func generateFiles(out string) error {
	data, err := fetch(rawBase + "icon_associations.json")
	if err != nil {
		return err
	}
	var file upstreamFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("read icon_associations.json: %w", err)
	}
	upstream := make([]upstreamRule, 0, len(file.Associations.Associations.Regex))
	for _, entry := range file.Associations.Associations.Regex {
		upstream = append(upstream, entry.Value)
	}
	rules := chooseRules(upstream)
	if err := os.RemoveAll(filepath.Join(out, "icons")); err != nil {
		return fmt.Errorf("clear icons: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(out, "icons"), 0o755); err != nil {
		return fmt.Errorf("make icons folder: %w", err)
	}
	var kept []Rule
	written := map[string]bool{}
	for _, rule := range rules {
		if !written[rule.Icon] {
			svg, err := fetch(rawBase + "assets/icons/files/" + rule.Icon + ".svg")
			if errors.Is(err, errNotFound) {
				slog.Warn("skip a rule whose icon is missing", "icon", rule.Icon, "pattern", rule.Pattern)
				continue
			}
			if err != nil {
				return err
			}
			if err := writeCompressed(filepath.Join(out, "icons", rule.Icon+".svg.gz"), []byte(compact(string(svg)))); err != nil {
				return fmt.Errorf("write icon %s: %w", rule.Icon, err)
			}
			written[rule.Icon] = true
		}
		kept = append(kept, rule)
	}
	encoded, err := json.MarshalIndent(kept, "", " ")
	if err != nil {
		return fmt.Errorf("encode rules: %w", err)
	}
	if err := os.WriteFile(filepath.Join(out, "rules.json"), append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write rules: %w", err)
	}
	slog.Info("generated file icons", "rules", len(kept), "icons", len(written), "commit", commit)
	return nil
}

// upstreamFolderRule is a rule of folder_associations.json. Its icon holds
// its colors.
type upstreamFolderRule struct {
	Name        string `json:"name"`
	FolderNames string `json:"folderNames"`
	Priority    string `json:"priority"`
	Icon        string `json:"icon"`
	IconPack    string `json:"iconPack"`
}

type upstreamFolderFile struct {
	Associations struct {
		Associations struct {
			Regex []struct {
				Value upstreamFolderRule `json:"value"`
			} `json:"regex"`
		} `json:"associations"`
	} `json:"associations"`
}

// FolderRule names the icon of folders by their names: assets/folders.json,
// in the order rules are tried.
type FolderRule struct {
	Names []string `json:"names"`
	// Icon names a file of assets/folders and of assets/foldersOpen.
	Icon string `json:"icon"`
}

// genericFolder is the icon of folders no rule names, which Perch tints.
const genericFolder = "folder"

// generateFolders writes the rules of folder icons, and each icon closed and
// open. Folders are named in the rules, so no pattern is kept.
func generateFolders(out string) error {
	data, err := fetch(rawBase + "folder_associations.json")
	if err != nil {
		return err
	}
	var file upstreamFolderFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("read folder_associations.json: %w", err)
	}
	type ranked struct {
		rule     FolderRule
		priority int
	}
	var rules []ranked
	for _, entry := range file.Associations.Associations.Regex {
		upstream := entry.Value
		priority, err := strconv.Atoi(upstream.Priority)
		if upstream.IconPack != "" || err != nil {
			continue
		}
		var names []string
		for name := range strings.SplitSeq(upstream.FolderNames, ",") {
			if name = strings.TrimSpace(name); name != "" && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		icon := strings.TrimSuffix(path.Base(upstream.Icon), ".svg")
		rules = append(rules, ranked{rule: FolderRule{Names: names, Icon: icon}, priority: priority})
	}
	slices.SortStableFunc(rules, func(a ranked, b ranked) int { return b.priority - a.priority })
	for _, dir := range []string{"folders", "foldersOpen"} {
		if err := os.RemoveAll(filepath.Join(out, dir)); err != nil {
			return fmt.Errorf("clear %s: %w", dir, err)
		}
		if err := os.MkdirAll(filepath.Join(out, dir), 0o755); err != nil {
			return fmt.Errorf("make %s: %w", dir, err)
		}
	}
	var kept []FolderRule
	written := map[string]bool{}
	icons := []string{genericFolder}
	for _, r := range rules {
		icons = append(icons, r.rule.Icon)
	}
	for _, icon := range icons {
		if written[icon] {
			continue
		}
		complete, err := writeFolderIcon(out, icon)
		if err != nil {
			return err
		}
		if !complete {
			slog.Warn("skip a folder icon that is missing", "icon", icon)
			continue
		}
		written[icon] = true
	}
	for _, r := range rules {
		if written[r.rule.Icon] {
			kept = append(kept, r.rule)
		}
	}
	encoded, err := json.MarshalIndent(kept, "", " ")
	if err != nil {
		return fmt.Errorf("encode folder rules: %w", err)
	}
	if err := os.WriteFile(filepath.Join(out, "folders.json"), append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write folder rules: %w", err)
	}
	slog.Info("generated folder icons", "rules", len(kept), "icons", len(written))
	return nil
}

// writeFolderIcon writes a folder icon closed and open, and reports false when
// either is missing.
func writeFolderIcon(out string, icon string) (bool, error) {
	dirs := []string{"folders", "foldersOpen"}
	drawings := make([][]byte, 0, len(dirs))
	for _, dir := range dirs {
		svg, err := fetch(rawBase + "assets/icons/" + dir + "/" + icon + ".svg")
		if errors.Is(err, errNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		drawings = append(drawings, []byte(compact(string(svg))))
	}
	for index, dir := range dirs {
		if err := writeCompressed(filepath.Join(out, dir, icon+".svg.gz"), drawings[index]); err != nil {
			return false, fmt.Errorf("write folder icon %s: %w", icon, err)
		}
	}
	return true, nil
}

// chooseRules keeps the rules outside icon packs whose patterns
// Go reads, with the color of their dark variants, highest priority first.
func chooseRules(upstream []upstreamRule) []Rule {
	dark := map[string]string{}
	for _, rule := range upstream {
		if base, isDark := strings.CutSuffix(rule.Name, darkSuffix); isDark {
			dark[base+"\x00"+rule.Pattern] = rule.IconColor
		}
	}
	var rules []Rule
	for _, rule := range upstream {
		if strings.HasSuffix(rule.Name, darkSuffix) || rule.IconPack != "" || rule.IconType != "FILE" {
			continue
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			slog.Warn("skip a rule Go cannot read", "name", rule.Name, "pattern", rule.Pattern, "error", err)
			continue
		}
		priority, err := strconv.Atoi(rule.Priority)
		if err != nil {
			slog.Warn("skip a rule without a priority", "name", rule.Name, "priority", rule.Priority)
			continue
		}
		light := "#" + strings.ToUpper(rule.IconColor)
		darkColor := light
		if color, found := dark[rule.Name+"\x00"+rule.Pattern]; found {
			darkColor = "#" + strings.ToUpper(color)
		}
		icon := strings.TrimSuffix(path.Base(rule.Icon), ".svg")
		rules = append(rules, lookup(Rule{Pattern: rule.Pattern, Priority: priority, Icon: icon, Light: light, Dark: darkColor}))
	}
	slices.SortStableFunc(rules, func(a Rule, b Rule) int { return b.Priority - a.Priority })
	return rules
}

// lookup turns a pattern of extensions or of whole names into lists of them.
func lookup(rule Rule) Rule {
	if match := extensionPattern.FindStringSubmatch(rule.Pattern); match != nil {
		rule.Extensions, rule.Pattern = strings.Split(match[1], "|"), ""
		return rule
	}
	if match := namePattern.FindStringSubmatch(rule.Pattern); match != nil {
		rule.Names, rule.Pattern = strings.Split(strings.ReplaceAll(match[1], `\.`, "."), "|"), ""
		return rule
	}
	parsed, err := syntax.Parse(rule.Pattern, syntax.Perl)
	if err == nil {
		rule.Literal = requiredLiteral(parsed.Simplify())
	}
	return rule
}

// requiredLiteral returns the longest text that every match of a pattern holds
// as it is written, or nothing.
func requiredLiteral(re *syntax.Regexp) string {
	switch re.Op {
	case syntax.OpLiteral:
		if re.Flags&syntax.FoldCase != 0 {
			return ""
		}
		return string(re.Rune)
	case syntax.OpCapture, syntax.OpPlus:
		return requiredLiteral(re.Sub[0])
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return requiredLiteral(re.Sub[0])
		}
		return ""
	case syntax.OpConcat:
		longest := ""
		for _, sub := range re.Sub {
			if literal := requiredLiteral(sub); len(literal) > len(longest) {
				longest = literal
			}
		}
		return longest
	default:
		return ""
	}
}

// compact removes what an icon does not draw, and lets Perch tint the parts a
// rule colors.
func compact(svg string) string {
	svg = comments.ReplaceAllString(svg, "")
	svg = declaration.ReplaceAllString(svg, "")
	svg = spaces.ReplaceAllString(svg, " ")
	svg = sizes.ReplaceAllString(svg, "")
	svg = coloredFill.ReplaceAllStringFunc(svg, func(tag string) string {
		if fill.MatchString(tag) {
			return fill.ReplaceAllString(tag, `fill="currentColor"`)
		}
		return strings.Replace(tag, " data-", ` fill="currentColor" data-`, 1)
	})
	return strings.ReplaceAll(strings.TrimSpace(svg), "> <", "><")
}

// writeCompressed writes data compressed with gzip, which shrinks the
// drawings to about a third.
func writeCompressed(file string, data []byte) error {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return os.WriteFile(file, buffer.Bytes(), 0o644)
}

var errNotFound = errors.New("not found")

// fetch downloads a file, retrying a failed request.
func fetch(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		data, err := get(client, url)
		if err == nil || errors.Is(err, errNotFound) {
			return data, err
		}
		last = err
		slog.Warn("retry a download", "url", url, "attempt", attempt, "error", err)
		time.Sleep(time.Duration(attempt) * time.Second)
	}
	return nil, last
}

func get(client *http.Client, url string) ([]byte, error) {
	response, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	switch {
	case response.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("download %s: %w", url, errNotFound)
	case response.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("download %s: status %d: %s", url, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}
