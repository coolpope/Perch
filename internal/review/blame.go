package review

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Lengths of commit names in SHA-1 and SHA-256 repositories.
const (
	sha1Length   = 40
	sha256Length = 64
)

// BlameCommit is a commit that last changed some lines.
type BlameCommit struct {
	Hash    string
	Short   string
	Author  string
	When    time.Time
	Summary string
	// Uncommitted marks the lines changed since the last commit.
	Uncommitted bool
}

// Blame says which commit last changed each line of a text.
type Blame struct {
	Commits []BlameCommit
	// Lines holds, for each line counted from 0, its commit's index in
	// Commits.
	Lines []int
}

// BlameText reads which commit last changed each line of text, the file's
// contents as they are now, edits not yet saved included.
func BlameText(ctx context.Context, root string, path string, text string) (Blame, error) {
	output, err := gitInput(ctx, root, nil, []byte(text), "blame", "--porcelain", "--contents", "-", "--", path)
	if err != nil {
		return Blame{}, fmt.Errorf("blame %q: %w", path, err)
	}
	return parseBlame(string(output))
}

// parseBlame reads the output of git blame --porcelain.
func parseBlame(output string) (Blame, error) {
	var blame Blame
	index := map[string]int{}
	current := -1
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "\t") {
			blame.Lines = append(blame.Lines, current)
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		if (len(key) == sha1Length || len(key) == sha256Length) && isHex(key) {
			found, seen := index[key]
			if !seen {
				found = len(blame.Commits)
				index[key] = found
				blame.Commits = append(blame.Commits, BlameCommit{
					Hash: key, Short: key[:shortHashLength], Uncommitted: strings.Trim(key, "0") == "",
				})
			}
			current = found
			continue
		}
		if current < 0 {
			continue
		}
		commit := &blame.Commits[current]
		switch key {
		case "author":
			commit.Author = value
		case "author-time":
			seconds, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return Blame{}, fmt.Errorf("read the time of %s: %w", commit.Hash, err)
			}
			commit.When = time.Unix(seconds, 0)
		case "summary":
			commit.Summary = value
		}
	}
	if err := scanner.Err(); err != nil {
		return Blame{}, fmt.Errorf("read blame: %w", err)
	}
	return blame, nil
}

func isHex(text string) bool {
	return strings.Trim(text, "0123456789abcdef") == ""
}
