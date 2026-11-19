package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

const (
	// MaxHits is the most matches one search reports.
	MaxHits int = 2000
	// MaxScannedFileBytes is the largest file a search reads.
	MaxScannedFileBytes int64 = 4 << 20

	previewBytes int = 200
	previewLead  int = 48

	// searchWorkers is how many files are scanned at once. Each holds a
	// buffer as large as the largest file it read, up to MaxScannedFileBytes,
	// so more workers would cost memory for little speed.
	searchWorkers int = 4
	// batchFiles is how many files the workers share between two reports,
	// which keep the order of paths.
	batchFiles int = 256
)

// Pattern is a compiled query.
type Pattern struct {
	expression *regexp.Regexp
	// needle, when set, is text every match holds, compared ignoring ASCII
	// case when fold is set: a file without it is skipped unread by the
	// expression.
	needle []byte
	fold   bool
}

// Query is what to look for in the text of files.
type Query struct {
	Text          string
	CaseSensitive bool
	Regexp        bool
}

// Compile returns the pattern of a query. It fails only for a Regexp query
// that is not a valid regular expression.
func (q Query) Compile() (*Pattern, error) {
	expression := q.Text
	if !q.Regexp {
		expression = regexp.QuoteMeta(q.Text)
	}
	if !q.CaseSensitive {
		expression = "(?i)" + expression
	}
	compiled, err := regexp.Compile("(?m)" + expression)
	if err != nil {
		return nil, fmt.Errorf("compile search %q: %w", q.Text, err)
	}
	pattern := &Pattern{expression: compiled}
	if !q.Regexp && q.Text != "" && (q.CaseSensitive || foldsOnlyInASCII(q.Text)) {
		pattern.needle, pattern.fold = []byte(q.Text), !q.CaseSensitive
	}
	return pattern, nil
}

// foldsOnlyInASCII reports text whose letters match, ignoring case, only ASCII
// letters: not k and s, which also match the Kelvin sign and the long s.
func foldsOnlyInASCII(text string) bool {
	for _, r := range text {
		if r >= utf8.RuneSelf || r == 'k' || r == 'K' || r == 's' || r == 'S' {
			return false
		}
	}
	return true
}

// mayMatch reports whether data could hold a match, by its needle.
func (p *Pattern) mayMatch(data []byte) bool {
	switch {
	case p.needle == nil:
		return true
	case !p.fold:
		return bytes.Contains(data, p.needle)
	default:
		return containsFold(data, p.needle)
	}
}

// commonLetters ranks letters from the most common in code and prose.
const commonLetters = "etaoinsrhldcumfpgwybvkxjqz"

// containsFold reports whether data holds needle, ignoring ASCII case. It looks
// for the needle's rarest letter first, which few places of data hold.
func containsFold(data []byte, needle []byte) bool {
	anchor := rarestByte(needle)
	// Past end, the needle would not fit around its anchor.
	end := len(data) - len(needle) + anchor + 1
	lower := byteCursor{target: asciiLower(needle[anchor]), at: -1}
	upper := byteCursor{target: asciiUpper(needle[anchor]), at: -1}
	for from := anchor; from < end; {
		index := earliest(lower.next(data[:end], from), upper.next(data[:end], from))
		if index < 0 {
			return false
		}
		start := index - anchor
		if bytes.EqualFold(data[start:start+len(needle)], needle) {
			return true
		}
		from = index + 1
	}
	return false
}

// earliest returns the smaller of two indexes, -1 meaning none.
func earliest(a int, b int) int {
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	default:
		return min(a, b)
	}
}

// byteCursor finds the occurrences of one byte in order, remembering the next
// one, so that no part of data is searched twice for it.
type byteCursor struct {
	target byte
	// at is the occurrence found last, -1 before the first search.
	at   int
	done bool
}

// next returns the first index of the target at or after from, or -1.
func (c *byteCursor) next(data []byte, from int) int {
	switch {
	case c.done:
		return -1
	case c.at >= from:
		return c.at
	}
	index := bytes.IndexByte(data[from:], c.target)
	if index < 0 {
		c.done = true
		return -1
	}
	c.at = from + index
	return c.at
}

// rarestByte returns the index of the byte of needle least likely in text: a
// byte that is not a letter, or else the letter commonLetters ranks last.
func rarestByte(needle []byte) int {
	best, bestRank := 0, -1
	for index, c := range needle {
		rank := strings.IndexByte(commonLetters, asciiLower(c))
		if rank < 0 {
			rank = len(commonLetters)
		}
		if rank > bestRank {
			best, bestRank = index, rank
		}
	}
	return best
}

func asciiLower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func asciiUpper(c byte) byte {
	if 'a' <= c && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}

// Hit is a match in a line of a file.
type Hit struct {
	// Line and Column count from 0, Column in bytes. EndColumn stays on the
	// matched line.
	Line      int
	Column    int
	EndColumn int
	// Preview is the part of the line to show, and the match is the bytes
	// from PreviewStart to PreviewEnd of it.
	Preview      string
	PreviewStart int
	PreviewEnd   int
}

// FileHits are the matches in one file.
type FileHits struct {
	Path string
	Hits []Hit
}

// Summary says how a finished search went.
type Summary struct {
	Files int
	Hits  int
	// Truncated reports a search that stopped at MaxHits.
	Truncated bool
	// Unreadable counts the files that could not be read.
	Unreadable int
}

// scanned is what scanning one file found.
type scanned struct {
	hits       []Hit
	more       bool
	unreadable bool
}

// Text scans paths and calls emit, in the order of paths, with the matches of
// each file that has any. It returns ctx's error when ctx ends first. Files
// are scanned a batch at a time by a few workers, each with one buffer, so
// memory follows the largest files rather than the size of the folder.
func Text(ctx context.Context, root string, paths []string, pattern *Pattern, emit func(FileHits)) (Summary, error) {
	var summary Summary
	buffers := make([][]byte, searchWorkers)
	for start := 0; start < len(paths); start += batchFiles {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		batch := paths[start:min(start+batchFiles, len(paths))]
		results := scanBatch(ctx, root, batch, pattern, MaxHits-summary.Hits, buffers)
		for index, result := range results {
			if result.unreadable {
				summary.Unreadable++
				continue
			}
			hits := result.hits[:min(len(result.hits), MaxHits-summary.Hits)]
			if len(hits) > 0 {
				summary.Files++
				summary.Hits += len(hits)
				emit(FileHits{Path: batch[index], Hits: hits})
			}
			if result.more || len(hits) < len(result.hits) {
				summary.Truncated = true
				return summary, nil
			}
		}
	}
	return summary, ctx.Err()
}

// scanBatch scans files with searchWorkers goroutines, which end before it
// returns, each reusing one of buffers.
func scanBatch(ctx context.Context, root string, batch []string, pattern *Pattern, limit int, buffers [][]byte) []scanned {
	results := make([]scanned, len(batch))
	var next atomic.Int64
	var group sync.WaitGroup
	for worker := range buffers {
		group.Go(func() {
			for {
				index := int(next.Add(1) - 1)
				if index >= len(batch) || ctx.Err() != nil {
					return
				}
				data, err := readScannable(filepath.Join(root, filepath.FromSlash(batch[index])), buffers[worker])
				if err != nil {
					results[index].unreadable = true
					continue
				}
				buffers[worker] = data[:0]
				if pattern.mayMatch(data) {
					results[index].hits, results[index].more = scan(data, pattern.expression, limit)
				}
			}
		})
	}
	group.Wait()
	return results
}

// readScannable returns the content of a text file, reusing buffer, and nothing
// for a file too large to scan or one that is not UTF-8 text.
func readScannable(file string, buffer []byte) (content []byte, err error) {
	handle, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, handle.Close()) }()
	info, err := handle.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxScannedFileBytes {
		return buffer[:0], nil
	}
	data := buffer[:0]
	if int64(cap(data)) < info.Size() {
		data = make([]byte, 0, info.Size())
	}
	data = data[:info.Size()]
	count, err := io.ReadFull(handle, data)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	data = data[:count]
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return data[:0], nil
	}
	return data, nil
}

// scan returns at most limit matches of data, and whether it holds more.
func scan(data []byte, pattern *regexp.Regexp, limit int) ([]Hit, bool) {
	if len(data) == 0 {
		return nil, false
	}
	matches := pattern.FindAllIndex(data, limit+1)
	hits := make([]Hit, 0, min(len(matches), limit))
	line := 0
	lineStart := 0
	for _, match := range matches {
		if match[0] == match[1] {
			continue
		}
		if len(hits) == limit {
			return hits, true
		}
		newlines := bytes.Count(data[lineStart:match[0]], []byte{'\n'})
		if newlines > 0 {
			line += newlines
			lineStart += bytes.LastIndexByte(data[lineStart:match[0]], '\n') + 1
		}
		hits = append(hits, newHit(data, line, lineStart, match[0], match[1]))
	}
	return hits, false
}

func newHit(data []byte, line int, lineStart int, start int, end int) Hit {
	lineEnd := len(data)
	if index := bytes.IndexByte(data[lineStart:], '\n'); index >= 0 {
		lineEnd = lineStart + index
	}
	if lineEnd > lineStart && data[lineEnd-1] == '\r' {
		lineEnd--
	}
	end = min(end, lineEnd)
	from := previewFrom(data, lineStart, start)
	to := previewTo(data, from, lineEnd)
	return Hit{
		Line:         line,
		Column:       start - lineStart,
		EndColumn:    end - lineStart,
		Preview:      string(data[from:to]),
		PreviewStart: start - from,
		PreviewEnd:   min(end, to) - from,
	}
}

// previewFrom skips the indentation of a line, and the start of a line whose
// match lies too far along it to show.
func previewFrom(data []byte, lineStart int, start int) int {
	from := lineStart
	for from < start && (data[from] == ' ' || data[from] == '\t') {
		from++
	}
	if start-from <= previewLead {
		return from
	}
	from = start - previewLead
	for from < start && !utf8.RuneStart(data[from]) {
		from++
	}
	return from
}

func previewTo(data []byte, from int, lineEnd int) int {
	to := min(lineEnd, from+previewBytes)
	for to > from && to < lineEnd && !utf8.RuneStart(data[to]) {
		to--
	}
	return to
}
