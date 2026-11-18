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
	"unicode/utf8"
)

const (
	// MaxHits is the most matches one search reports.
	MaxHits int = 2000
	// MaxScannedFileBytes is the largest file a search reads.
	MaxScannedFileBytes int64 = 4 << 20

	previewBytes int = 200
	previewLead  int = 48
)

// Query is what to look for in the text of files.
type Query struct {
	Text          string
	CaseSensitive bool
	Regexp        bool
}

// Compile returns the pattern of a query. It fails only for a Regexp query
// that is not a valid regular expression.
func (q Query) Compile() (*regexp.Regexp, error) {
	expression := q.Text
	if !q.Regexp {
		expression = regexp.QuoteMeta(q.Text)
	}
	if !q.CaseSensitive {
		expression = "(?i)" + expression
	}
	pattern, err := regexp.Compile("(?m)" + expression)
	if err != nil {
		return nil, fmt.Errorf("compile search %q: %w", q.Text, err)
	}
	return pattern, nil
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

// Text scans paths in order and calls emit with the matches of each file that
// has any. It returns ctx's error when ctx ends first. One buffer serves every
// file, so memory follows the largest file rather than the size of the folder.
func Text(ctx context.Context, root string, paths []string, pattern *regexp.Regexp, emit func(FileHits)) (Summary, error) {
	var summary Summary
	buffer := make([]byte, 0, 64<<10)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		data, err := readScannable(filepath.Join(root, filepath.FromSlash(path)), buffer)
		if err != nil {
			summary.Unreadable++
			continue
		}
		buffer = data[:0]
		remaining := MaxHits - summary.Hits
		hits, more := scan(data, pattern, remaining)
		if len(hits) > 0 {
			summary.Files++
			summary.Hits += len(hits)
			emit(FileHits{Path: path, Hits: hits})
		}
		if more {
			summary.Truncated = true
			return summary, nil
		}
	}
	return summary, nil
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
