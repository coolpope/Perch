// Package review reads the changes and history of a Git work tree, and
// remembers how far a person has read its changes. It keeps its snapshots as
// Git trees under refs/perch, which survive an agent's commits, and never
// touches the index, HEAD, branches or the files of the work tree.
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
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/marukolibro/Perch/internal/proc"
	"github.com/marukolibro/Perch/internal/workspace"
)

const (
	// MaxSnapshotFileBytes is the largest file a snapshot records, which
	// keeps build outputs out of the repository's objects.
	MaxSnapshotFileBytes int64 = 5 << 20

	refKeyBytes = 12

	// gitlinkMode is the mode of a submodule in the index.
	gitlinkMode = "160000"

	// gitCheckIgnoreNoMatch is the exit code of git check-ignore for a path
	// that no rule ignores.
	gitCheckIgnoreNoMatch = 1
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
	// Untracked is a new file Git does not follow yet.
	Untracked
	// Renamed is a file moved from OldPath, and perhaps changed too.
	Renamed
)

// Change is a file that differs from the last finished review.
type Change struct {
	// Path is relative to the opened folder and separated by slashes.
	Path string
	Kind Kind
	// OldPath is where a Renamed file was.
	OldPath string
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

// Baseline is the text of a file as far as it has been read.
type Baseline struct {
	Text string
	// Known reports a file the review follows, whose text can be compared.
	Known bool
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
	// unreadFrom maps an unread renamed file to its path in the seen tree,
	// as the last listing found it.
	unreadFrom map[string]string
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
	if change.Reviewed {
		return s.readSides(ctx, base, version, oldPathOf(change), change.Path)
	}
	return s.readSides(ctx, seen, version, s.seenPath(change.Path), change.Path)
}

// seenPath is where a file was in the seen tree: its old path when it was
// renamed since it was last read.
func (s *Store) seenPath(path string) string {
	if old, renamed := s.unreadFrom[path]; renamed {
		return old
	}
	return path
}

// Baseline returns a file's text as far as it has been read. A file the review
// does not record, as one Git ignores or one too large, is not Known.
func (s *Store) Baseline(ctx context.Context, path string) (Baseline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !belongsInSnapshot(s.root, path) {
		return Baseline{}, nil
	}
	ignored, err := s.ignoredByGit(ctx, path)
	if err != nil || ignored {
		return Baseline{}, err
	}
	_, seen, err := s.marks(ctx)
	if err != nil {
		return Baseline{}, err
	}
	text, readable, err := s.readBlob(ctx, seen, s.seenPath(path))
	if err != nil {
		return Baseline{}, err
	}
	return Baseline{Text: text, Known: readable}, nil
}

// ignoredByGit reports whether Git ignores a file of the opened folder.
func (s *Store) ignoredByGit(ctx context.Context, path string) (bool, error) {
	_, err := git(ctx, s.root, nil, "check-ignore", "--quiet", "--", path)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == gitCheckIgnoreNoMatch {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check whether Git ignores %q: %w", path, err)
	}
	return true, nil
}

// MarkReviewed records the given changes as read in a version, so that an
// edit arriving later still counts as unread.
func (s *Store) MarkReviewed(ctx context.Context, version string, changes []Change) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, seen, err := s.marks(ctx)
	if err != nil {
		return err
	}
	return s.copyEntries(ctx, version, seen, s.markedPaths(changes))
}

// MarkUnreviewed returns the given changes to unread.
func (s *Store) MarkUnreviewed(ctx context.Context, changes []Change) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	base, seen, err := s.marks(ctx)
	if err != nil {
		return err
	}
	return s.copyEntries(ctx, base, seen, s.markedPaths(changes))
}

// markedPaths are the paths a mark copies: each file's path, with the paths a
// renamed file had, so that the seen tree does not keep it twice.
func (s *Store) markedPaths(changes []Change) []string {
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.Path)
		if change.OldPath != "" {
			paths = append(paths, change.OldPath)
		}
		if seen := s.seenPath(change.Path); seen != change.Path && seen != change.OldPath {
			paths = append(paths, seen)
		}
	}
	return paths
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
// and new files above MaxSnapshotFileBytes. Files Git already follows go in
// without a look at each, which keeps a refresh of a large tree cheap.
func (s *Store) snapshot(ctx context.Context) (string, error) {
	top, err := s.topLevel(ctx)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(s.index); errors.Is(err, fs.ErrNotExist) {
		if err := s.seedIndex(ctx, top); err != nil {
			return "", err
		}
	}
	tracked, err := git(ctx, top, s.indexEnv(), "ls-files", "-z", "--cached", "--stage")
	if err != nil {
		return "", fmt.Errorf("list files of %q: %w", s.root, err)
	}
	untracked, err := git(ctx, top, s.indexEnv(), "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return "", fmt.Errorf("list new files of %q: %w", s.root, err)
	}
	input := make([]byte, 0, len(tracked)/2+len(untracked))
	for entry := range bytes.SplitSeq(tracked, []byte{0}) {
		meta, name, found := bytes.Cut(entry, []byte{'\t'})
		// A submodule is recorded by its commit, which update-index leaves.
		if found && !bytes.HasPrefix(meta, []byte(gitlinkMode)) && !workspace.IsIgnored(string(name)) {
			input = append(append(input, name...), 0)
		}
	}
	for name := range strings.SplitSeq(string(untracked), "\x00") {
		if name != "" && belongsInSnapshot(top, name) {
			input = append(append(input, name...), 0)
		}
	}
	if _, err := gitInput(ctx, top, s.indexEnv(), input, "update-index", "--add", "--remove", "-z", "--stdin"); err != nil {
		return "", fmt.Errorf("record files of %q: %w", s.root, err)
	}
	tree, err := git(ctx, top, s.indexEnv(), "write-tree")
	if err != nil {
		return "", fmt.Errorf("write snapshot of %q: %w", s.root, err)
	}
	return strings.TrimSpace(string(tree)), nil
}

// seedIndex starts the store's index as a copy of the work tree's own, whose
// record of each file's state spares Git reading every file again, or from
// HEAD when the work tree has no index yet.
func (s *Store) seedIndex(ctx context.Context, top string) error {
	output, err := git(ctx, top, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return fmt.Errorf("find the index of %q: %w", s.root, err)
	}
	source := strings.TrimRight(string(output), "\n")
	info, err := os.Stat(source)
	switch {
	case err == nil:
		return s.copyIndex(source, info.ModTime())
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("read the index of %q: %w", s.root, err)
	}
	if _, err := gitInput(ctx, top, s.indexEnv(), nil, "read-tree", "HEAD"); err == nil {
		return nil
	}
	if _, err := gitInput(ctx, top, s.indexEnv(), nil, "read-tree", "--empty"); err != nil {
		return fmt.Errorf("prepare snapshot of %q: %w", s.root, err)
	}
	return nil
}

// copyIndex copies the work tree's index, last written at written, into the
// store's. The copy keeps that time: Git reads the content of a file changed
// in the same second as the index was written, as its size and time alone
// cannot tell an edit then, and a copy written later would hide such edits.
// The time is read before the index, so an index rewritten in between only
// makes Git read more files.
func (s *Store) copyIndex(source string, written time.Time) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read the index of %q: %w", s.root, err)
	}
	if err := os.WriteFile(s.index, data, 0o600); err != nil {
		return fmt.Errorf("prepare snapshot of %q: %w", s.root, err)
	}
	if err := os.Chtimes(s.index, written, written); err != nil {
		return fmt.Errorf("prepare snapshot of %q: %w", s.root, err)
	}
	return nil
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

// listChanges compares the trees. A file differing from seen is unread. A
// file moved since it was last read is remembered with the path it was read
// at, where its unread diff starts.
func (s *Store) listChanges(ctx context.Context, base string, seen string, current string) ([]Change, error) {
	whole, err := renamedChanges(ctx, s.root, nil, "diff-tree", "-r", base, current)
	if err != nil {
		return nil, fmt.Errorf("compare %s with %s: %w", base, current, err)
	}
	unread, err := renamedChanges(ctx, s.root, nil, "diff-tree", "-r", seen, current)
	if err != nil {
		return nil, fmt.Errorf("compare %s with %s: %w", seen, current, err)
	}
	pending := make(map[string]Change, len(unread))
	s.unreadFrom = map[string]string{}
	for _, change := range unread {
		pending[change.Path] = change
		if change.OldPath != "" {
			s.unreadFrom[change.Path] = change.OldPath
		}
	}
	for index, change := range whole {
		left, found := pending[change.Path]
		if !found {
			whole[index].Reviewed = true
			continue
		}
		whole[index].Added, whole[index].Removed, whole[index].Binary = left.Added, left.Removed, left.Binary
	}
	return whole, nil
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

// readSides reads a file at oldPath in one tree and at path in another.
func (s *Store) readSides(ctx context.Context, from string, to string, oldPath string, path string) (Sides, error) {
	return objectSides(ctx, s.root, from+":"+s.prefix+oldPath, to+":"+s.prefix+path)
}

// objectSides reads the texts of two objects, as "rev:path", to compare.
func objectSides(ctx context.Context, root string, from string, to string) (Sides, error) {
	old, oldReadable, err := readObject(ctx, root, from)
	if err != nil {
		return Sides{}, err
	}
	updated, newReadable, err := readObject(ctx, root, to)
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
	return readObject(ctx, s.root, tree+":"+s.prefix+path)
}

// readObject returns the text of an object named as "rev:path", empty for a
// path the revision lacks, and reports whether it can be shown as text.
func readObject(ctx context.Context, root string, object string) (string, bool, error) {
	size, err := git(ctx, root, nil, "cat-file", "-s", object)
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
	data, err := git(ctx, root, nil, "cat-file", "blob", object)
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", object, err)
	}
	text, readable := textOf(data)
	return text, readable, nil
}

// textOf returns data as text, and reports false for binary data.
func textOf(data []byte) (string, bool) {
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", false
	}
	return string(data), true
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
	command := proc.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
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
