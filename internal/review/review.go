// Package review remembers how far a person has read the changes in a Git work
// tree. It keeps its snapshots as Git trees under refs/perch, which survive an
// agent's commits, and never touches the index, HEAD, branches or the files of
// the work tree.
package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"perch/internal/workspace"
)

const (
	// MaxSnapshotFileBytes is the largest file a snapshot records, which
	// keeps build outputs out of the repository's objects.
	MaxSnapshotFileBytes int64 = 5 << 20

	refKeyBytes = 12
)

// ErrNotRepository reports a folder outside any Git work tree.
var ErrNotRepository = errors.New("folder is not in a Git repository")

// Kind says what happened to a file.
type Kind uint8

// The kinds of change.
const (
	Modified Kind = iota
	Added
	Deleted
)

// Change is a file that differs from the last finished review.
type Change struct {
	// Path is relative to the opened folder and separated by slashes.
	Path string
	Kind Kind
	// Added and Removed count the lines left to read, or those of the
	// whole change once the file is reviewed.
	Added   int
	Removed int
	Binary  bool
	// Reviewed reports that the file has not changed since it was marked.
	Reviewed bool
}

// Listing is the changes of one recorded version of the work tree. Marks name
// the version, so that only what was on screen counts as read.
type Listing struct {
	Version string
	Changes []Change
}

// Sides are the two texts of a change.
type Sides struct {
	Old string
	New string
	// Unreadable reports a side that is binary or too large to show.
	Unreadable bool
}

// Checkpoint is where a review stood before it was finished.
type Checkpoint struct {
	base string
	seen string
}

// Store is the review of one work tree. Its methods are safe to call from any
// goroutine, one at a time.
type Store struct {
	mu      sync.Mutex
	root    string
	prefix  string
	index   string
	refBase string
	refSeen string
}

// Open prepares the review of the work tree at root.
func Open(ctx context.Context, root string) (*Store, error) {
	prefix, err := git(ctx, root, nil, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, fmt.Errorf("open review of %q: %w: %w", root, ErrNotRepository, err)
	}
	index, err := os.CreateTemp("", "perch-review-*.index")
	if err != nil {
		return nil, fmt.Errorf("open review of %q: %w", root, err)
	}
	if err := index.Close(); err != nil {
		return nil, fmt.Errorf("open review of %q: %w", root, err)
	}
	// Git refuses an empty index file, and makes its own when there is none.
	if err := os.Remove(index.Name()); err != nil {
		return nil, fmt.Errorf("open review of %q: %w", root, err)
	}
	sum := sha256.Sum256([]byte(root))
	key := hex.EncodeToString(sum[:refKeyBytes])
	return &Store{
		root:    root,
		prefix:  strings.TrimSpace(string(prefix)),
		index:   index.Name(),
		refBase: "refs/perch/review/" + key + "/base",
		refSeen: "refs/perch/review/" + key + "/seen",
	}, nil
}

// Close removes the store's temporary files. The review stays in the repository.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range []string{s.index, s.index + ".lock"} {
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("close review of %q: %w", s.root, err)
		}
	}
	return nil
}

// Begin fixes where the first review starts, so that commits made from now on
// count as changes to read.
func (s *Store) Begin(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, err := s.marks(ctx)
	return err
}

// Changes records the work tree as it is now and lists what differs from the
// last finished review, or from HEAD before the first one.
func (s *Store) Changes(ctx context.Context) (Listing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	version, err := s.snapshot(ctx)
	if err != nil {
		return Listing{}, err
	}
	base, seen, err := s.marks(ctx)
	if err != nil {
		return Listing{}, err
	}
	changes, err := s.listChanges(ctx, base, seen, version)
	if err != nil {
		return Listing{}, err
	}
	return Listing{Version: version, Changes: changes}, nil
}

// Sides returns the texts to compare for a file of a version: what is left to
// read, or the whole change once the file is reviewed.
func (s *Store) Sides(ctx context.Context, version string, change Change) (Sides, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	base, seen, err := s.marks(ctx)
	if err != nil {
		return Sides{}, err
	}
	from := seen
	if change.Reviewed {
		from = base
	}
	return s.readSides(ctx, from, version, change.Path)
}

// MarkReviewed records the given files as read in a version, so that an edit
// arriving later still counts as unread.
func (s *Store) MarkReviewed(ctx context.Context, version string, paths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, seen, err := s.marks(ctx)
	if err != nil {
		return err
	}
	return s.copyEntries(ctx, version, seen, paths)
}

// MarkUnreviewed returns the given files to unread.
func (s *Store) MarkUnreviewed(ctx context.Context, paths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	base, seen, err := s.marks(ctx)
	if err != nil {
		return err
	}
	return s.copyEntries(ctx, base, seen, paths)
}

// Finish starts a new review from a version, and returns the checkpoint that
// Reopen goes back to.
func (s *Store) Finish(ctx context.Context, version string) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	base, seen, err := s.marks(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	if err := s.setMarks(ctx, version, version); err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{base: base, seen: seen}, nil
}

// Reopen returns the review to where it stood before it was finished.
func (s *Store) Reopen(ctx context.Context, checkpoint Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setMarks(ctx, checkpoint.base, checkpoint.seen)
}

func (s *Store) setMarks(ctx context.Context, base string, seen string) error {
	if err := s.setRef(ctx, s.refBase, base); err != nil {
		return err
	}
	return s.setRef(ctx, s.refSeen, seen)
}

// marks returns the trees of the last finished review and of what was read
// since, creating them the first time.
func (s *Store) marks(ctx context.Context) (base string, seen string, err error) {
	base, err = s.readRef(ctx, s.refBase)
	if err != nil {
		return "", "", err
	}
	if base == "" {
		if base, err = s.headTree(ctx); err != nil {
			return "", "", err
		}
		if err = s.setRef(ctx, s.refBase, base); err != nil {
			return "", "", err
		}
	}
	seen, err = s.readRef(ctx, s.refSeen)
	if err != nil {
		return "", "", err
	}
	if seen == "" {
		seen = base
		if err = s.setRef(ctx, s.refSeen, seen); err != nil {
			return "", "", err
		}
	}
	return base, seen, nil
}

func (s *Store) readRef(ctx context.Context, ref string) (string, error) {
	output, err := git(ctx, s.root, nil, "for-each-ref", "--format=%(objectname)", ref)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", ref, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (s *Store) setRef(ctx context.Context, ref string, tree string) error {
	if _, err := git(ctx, s.root, nil, "update-ref", ref, tree); err != nil {
		return fmt.Errorf("record %s: %w", ref, err)
	}
	return nil
}

// headTree returns the tree of HEAD, or the empty tree before the first commit.
func (s *Store) headTree(ctx context.Context) (string, error) {
	output, err := git(ctx, s.root, nil, "rev-parse", "--verify", "--quiet", "HEAD^{tree}")
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return s.writeEmptyTree(ctx)
	}
	if err != nil {
		return "", fmt.Errorf("read HEAD of %q: %w", s.root, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (s *Store) writeEmptyTree(ctx context.Context) (string, error) {
	output, err := git(ctx, s.root, nil, "mktree", "--missing")
	if err != nil {
		return "", fmt.Errorf("write the empty tree of %q: %w", s.root, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// indexEnv points Git at the store's own index, leaving the person's alone.
func (s *Store) indexEnv() []string {
	return []string{"GIT_INDEX_FILE=" + s.index}
}

// snapshot writes the work tree's files to a tree, leaving out what Git ignores
// and files above MaxSnapshotFileBytes.
func (s *Store) snapshot(ctx context.Context) (string, error) {
	top, err := s.topLevel(ctx)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(s.index); errors.Is(err, fs.ErrNotExist) {
		if _, err := gitInput(ctx, top, s.indexEnv(), nil, "read-tree", "HEAD"); err != nil {
			if _, err := gitInput(ctx, top, s.indexEnv(), nil, "read-tree", "--empty"); err != nil {
				return "", fmt.Errorf("prepare snapshot of %q: %w", s.root, err)
			}
		}
	}
	listed, err := git(ctx, top, s.indexEnv(), "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return "", fmt.Errorf("list files of %q: %w", s.root, err)
	}
	var input bytes.Buffer
	for name := range strings.SplitSeq(string(listed), "\x00") {
		if name != "" && belongsInSnapshot(top, name) {
			input.WriteString(name)
			input.WriteByte(0)
		}
	}
	if _, err := gitInput(ctx, top, s.indexEnv(), input.Bytes(), "update-index", "--add", "--remove", "-z", "--stdin"); err != nil {
		return "", fmt.Errorf("record files of %q: %w", s.root, err)
	}
	tree, err := git(ctx, top, s.indexEnv(), "write-tree")
	if err != nil {
		return "", fmt.Errorf("write snapshot of %q: %w", s.root, err)
	}
	return strings.TrimSpace(string(tree)), nil
}

func (s *Store) topLevel(ctx context.Context) (string, error) {
	output, err := git(ctx, s.root, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("find the work tree of %q: %w", s.root, err)
	}
	return strings.TrimRight(string(output), "\n"), nil
}

// belongsInSnapshot keeps files that can be read, and missing ones, which the
// index then drops.
func belongsInSnapshot(top string, name string) bool {
	if workspace.IsIgnored(name) {
		return false
	}
	info, err := os.Lstat(filepath.Join(top, filepath.FromSlash(name)))
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return err == nil && !info.IsDir() && info.Size() <= MaxSnapshotFileBytes
}

// listChanges compares the trees. A file differing from seen is unread.
func (s *Store) listChanges(ctx context.Context, base string, seen string, current string) ([]Change, error) {
	whole, err := s.kinds(ctx, base, current)
	if err != nil {
		return nil, err
	}
	unread, err := s.kinds(ctx, seen, current)
	if err != nil {
		return nil, err
	}
	wholeCounts, err := s.counts(ctx, base, current)
	if err != nil {
		return nil, err
	}
	unreadCounts, err := s.counts(ctx, seen, current)
	if err != nil {
		return nil, err
	}
	changes := make([]Change, 0, len(whole))
	for path, kind := range whole {
		_, pending := unread[path]
		count := wholeCounts[path]
		if pending {
			count = unreadCounts[path]
		}
		changes = append(changes, Change{
			Path: path, Kind: kind, Added: count.added, Removed: count.removed, Binary: count.binary, Reviewed: !pending,
		})
	}
	slices.SortFunc(changes, func(a Change, b Change) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}

// kinds returns the files of the opened folder that differ between two trees.
func (s *Store) kinds(ctx context.Context, from string, to string) (map[string]Kind, error) {
	output, err := git(ctx, s.root, nil, "diff-tree", "-r", "-z", "--no-renames", "--relative", "--name-status", from, to)
	if err != nil {
		return nil, fmt.Errorf("compare %s with %s: %w", from, to, err)
	}
	fields := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	kinds := make(map[string]Kind, len(fields)/2)
	for index := 0; index+1 < len(fields); index += 2 {
		if !workspace.IsIgnored(fields[index+1]) {
			kinds[fields[index+1]] = kindOf(fields[index])
		}
	}
	return kinds, nil
}

func kindOf(status string) Kind {
	switch status {
	case "A":
		return Added
	case "D":
		return Deleted
	default:
		return Modified
	}
}

// lineCount is the size of a file's change.
type lineCount struct {
	added   int
	removed int
	binary  bool
}

func (s *Store) counts(ctx context.Context, from string, to string) (map[string]lineCount, error) {
	output, err := git(ctx, s.root, nil, "diff-tree", "-r", "-z", "--no-renames", "--relative", "--numstat", from, to)
	if err != nil {
		return nil, fmt.Errorf("count changes between %s and %s: %w", from, to, err)
	}
	counts := map[string]lineCount{}
	for record := range strings.SplitSeq(string(output), "\x00") {
		fields := strings.SplitN(record, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		added, addedErr := strconv.Atoi(fields[0])
		removed, removedErr := strconv.Atoi(fields[1])
		counts[fields[2]] = lineCount{added: added, removed: removed, binary: addedErr != nil || removedErr != nil}
	}
	return counts, nil
}

func (s *Store) readSides(ctx context.Context, from string, to string, path string) (Sides, error) {
	old, oldReadable, err := s.readBlob(ctx, from, path)
	if err != nil {
		return Sides{}, err
	}
	updated, newReadable, err := s.readBlob(ctx, to, path)
	if err != nil {
		return Sides{}, err
	}
	if !oldReadable || !newReadable {
		return Sides{Unreadable: true}, nil
	}
	return Sides{Old: old, New: updated}, nil
}

// readBlob returns a file's text in a tree, empty for a file the tree lacks, and
// reports whether it can be shown as text.
func (s *Store) readBlob(ctx context.Context, tree string, path string) (string, bool, error) {
	object := tree + ":" + s.prefix + path
	size, err := git(ctx, s.root, nil, "cat-file", "-s", object)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", object, err)
	}
	bytesLong, err := strconv.ParseInt(strings.TrimSpace(string(size)), 10, 64)
	if err != nil {
		return "", false, fmt.Errorf("read the size of %s: %w", object, err)
	}
	if bytesLong > MaxSnapshotFileBytes {
		return "", false, nil
	}
	data, err := git(ctx, s.root, nil, "cat-file", "blob", object)
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", object, err)
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", false, nil
	}
	return string(data), true, nil
}

// copyEntries makes paths in the seen tree match their versions in source.
func (s *Store) copyEntries(ctx context.Context, source string, seen string, paths []string) error {
	scratch := s.index + ".marks"
	defer func() { _ = os.Remove(scratch) }()
	env := []string{"GIT_INDEX_FILE=" + scratch}
	if _, err := git(ctx, s.root, env, "read-tree", seen); err != nil {
		return fmt.Errorf("read review marks: %w", err)
	}
	var input bytes.Buffer
	for _, path := range paths {
		entry, err := s.treeEntry(ctx, source, s.prefix+path)
		if err != nil {
			return err
		}
		input.WriteString(entry)
	}
	if _, err := gitInput(ctx, s.root, env, input.Bytes(), "update-index", "-z", "--index-info"); err != nil {
		return fmt.Errorf("update review marks: %w", err)
	}
	tree, err := git(ctx, s.root, env, "write-tree")
	if err != nil {
		return fmt.Errorf("write review marks: %w", err)
	}
	return s.setRef(ctx, s.refSeen, strings.TrimSpace(string(tree)))
}

// treeEntry returns the index line that gives a path its version in a tree, or
// removes it when the tree lacks it.
func (s *Store) treeEntry(ctx context.Context, tree string, path string) (string, error) {
	output, err := git(ctx, s.root, nil, "ls-tree", "-z", "--full-tree", tree, "--", path)
	if err != nil {
		return "", fmt.Errorf("read %s in %s: %w", path, tree, err)
	}
	line := strings.TrimSuffix(string(output), "\x00")
	if line == "" {
		return "0 0000000000000000000000000000000000000000\t" + path + "\x00", nil
	}
	meta, _, _ := strings.Cut(line, "\t")
	fields := strings.Fields(meta)
	return fields[0] + " " + fields[2] + "\t" + path + "\x00", nil
}

func git(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	return gitInput(ctx, dir, env, nil, args...)
}

// gitInput runs Git in dir and returns what it prints.
func gitInput(ctx context.Context, dir string, env []string, input []byte, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), env...)
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}
