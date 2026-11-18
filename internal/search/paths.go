package search

import (
	"slices"
	"strconv"
	"strings"
)

// maxQueryBytes bounds the part of a query that takes part in matching.
const maxQueryBytes int = 64

const (
	scoreMatch       int = 16
	scoreConsecutive int = 24
	scoreBoundary    int = 20
	scoreInBaseName  int = 12
	scoreBasePrefix  int = 200
	scoreBaseExact   int = 1000
	maxGapPenalty    int = 8
	lengthDivisor    int = 4
)

// PathMatch is a path that matches a query.
type PathMatch struct {
	Path string
	// Positions are the byte offsets in Path of the matched characters, in
	// increasing order.
	Positions []int
	score     int
}

// Target is what a quick open query asks for: a file and a line in it.
type Target struct {
	Path string
	// Line counts from 1, and is 0 when the query names no line.
	Line int
}

// ParseTarget splits a query such as "view.go:42" into its file and its line.
func ParseTarget(query string) Target {
	trimmed := strings.TrimSpace(query)
	head, tail, found := cutLast(trimmed, ":")
	if !found {
		return Target{Path: trimmed}
	}
	line, err := strconv.Atoi(tail)
	if err != nil || line < 1 {
		return Target{Path: trimmed}
	}
	return Target{Path: head, Line: line}
}

func cutLast(text string, separator string) (string, string, bool) {
	index := strings.LastIndex(text, separator)
	if index < 0 {
		return text, "", false
	}
	return text[:index], text[index+len(separator):], true
}

// RankPaths returns the best limit paths that hold the characters of query in
// order, best first. An empty query matches the first limit paths.
func RankPaths(paths []string, query string, limit int) []PathMatch {
	needle := normalizeQuery(query)
	if needle == "" {
		return firstPaths(paths, limit)
	}
	best := make([]PathMatch, 0, limit+1)
	var positions [maxQueryBytes]int
	for _, path := range paths {
		if !fit(path, needle, &positions) {
			continue
		}
		score := scorePath(path, needle, positions[:len(needle)])
		index, _ := slices.BinarySearchFunc(best, PathMatch{Path: path, score: score}, comparePathMatches)
		if index >= limit {
			continue
		}
		match := PathMatch{Path: path, Positions: slices.Clone(positions[:len(needle)]), score: score}
		best = slices.Insert(best, index, match)
		if len(best) > limit {
			best = best[:limit]
		}
	}
	return best
}

func firstPaths(paths []string, limit int) []PathMatch {
	matches := make([]PathMatch, 0, min(limit, len(paths)))
	for _, path := range paths[:min(limit, len(paths))] {
		matches = append(matches, PathMatch{Path: path})
	}
	return matches
}

// comparePathMatches orders higher scores first, then shorter paths.
func comparePathMatches(a PathMatch, b PathMatch) int {
	if a.score != b.score {
		return b.score - a.score
	}
	if len(a.Path) != len(b.Path) {
		return len(a.Path) - len(b.Path)
	}
	return strings.Compare(a.Path, b.Path)
}

func normalizeQuery(query string) string {
	needle := strings.ToLower(strings.ReplaceAll(query, " ", ""))
	return needle[:min(len(needle), maxQueryBytes)]
}

// fit finds needle in path, preferring the file's own name over its folders.
func fit(path string, needle string, positions *[maxQueryBytes]int) bool {
	base := baseNameStart(path)
	return fitFrom(path, needle, base, positions) || (base > 0 && fitFrom(path, needle, 0, positions))
}

// fitFrom scans forward to where needle ends, then backward from there, which
// leaves the tightest match that ends at that place.
func fitFrom(path string, needle string, start int, positions *[maxQueryBytes]int) bool {
	at := 0
	end := start
	for ; end < len(path) && at < len(needle); end++ {
		if lowerByte(path[end]) == needle[at] {
			at++
		}
	}
	if at < len(needle) {
		return false
	}
	at--
	for index := end - 1; index >= start && at >= 0; index-- {
		if lowerByte(path[index]) == needle[at] {
			positions[at] = index
			at--
		}
	}
	return true
}

func baseNameStart(path string) int {
	return strings.LastIndexByte(path, '/') + 1
}

func lowerByte(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func scorePath(path string, needle string, positions []int) int {
	base := baseNameStart(path)
	score := -len(path) / lengthDivisor
	for index, position := range positions {
		score += scoreMatch
		if position >= base {
			score += scoreInBaseName
		}
		if isBoundary(path, position) {
			score += scoreBoundary
		}
		if index == 0 {
			continue
		}
		gap := position - positions[index-1] - 1
		if gap == 0 {
			score += scoreConsecutive
		}
		score -= min(gap, maxGapPenalty)
	}
	return score + baseNameBonus(path[base:], needle)
}

func baseNameBonus(name string, needle string) int {
	lower := strings.ToLower(name)
	stem, _, _ := cutLast(lower, ".")
	switch {
	case lower == needle || stem == needle:
		return scoreBaseExact
	case strings.HasPrefix(lower, needle):
		return scoreBasePrefix
	default:
		return 0
	}
}

// isBoundary reports a character that starts a word of the path.
func isBoundary(path string, position int) bool {
	if position == 0 {
		return true
	}
	previous := path[position-1]
	if strings.IndexByte("/_-. ", previous) >= 0 {
		return true
	}
	return previous >= 'a' && previous <= 'z' && path[position] >= 'A' && path[position] <= 'Z'
}
