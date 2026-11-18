package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// currentDirPrefix keeps Git from reading a file name as an option or as
// pathspec magic, which a name starting with a colon would otherwise be.
const currentDirPrefix = "./"

// IgnoredNames returns which of the named entries of a directory Git ignores.
// A tracked file is never ignored. It runs inside dir, so that a submodule
// answers by its own rules, and reports ErrNotRepository outside a work tree.
func IgnoredNames(ctx context.Context, dir string, names []string) ([]string, error) {
	var input bytes.Buffer
	for _, name := range names {
		input.WriteString(currentDirPrefix + name + "\x00")
	}
	output, err := gitInput(ctx, dir, nil, input.Bytes(), "check-ignore", "--stdin", "-z")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == gitCheckIgnoreNoMatch {
		return nil, nil
	}
	if err != nil {
		if _, outside := git(ctx, dir, nil, "rev-parse", "--git-dir"); outside != nil {
			return nil, fmt.Errorf("list ignored names of %q: %w: %w", dir, ErrNotRepository, outside)
		}
		return nil, fmt.Errorf("list ignored names of %q: %w", dir, err)
	}
	return parseIgnoredNames(output), nil
}

// parseIgnoredNames reads the paths git check-ignore -z printed.
func parseIgnoredNames(output []byte) []string {
	paths := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != "" {
			names = append(names, strings.TrimPrefix(path, currentDirPrefix))
		}
	}
	return names
}
