package review

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marukolibro/Perch/internal/workspace"
)

// readOnlyEnv keeps Git from refreshing the person's index while it only
// reads the work tree.
var readOnlyEnv = []string{"GIT_OPTIONAL_LOCKS=0"}

// Head is where the work tree stands.
type Head struct {
	// Branch is empty when HEAD is detached.
	Branch string
	// Commit is HEAD's short name, empty before the first commit.
	Commit   string
	Upstream string
	// Ahead and Behind count the commits HEAD and its upstream lack of each
	// other.
	Ahead  int
	Behind int
	// Uncommitted counts the files of the opened folder that differ from HEAD,
	// untracked ones too.
	Uncommitted int
}

// ReadHead reads the branch of the work tree at root and how far it is from
// its upstream.
func ReadHead(ctx context.Context, root string) (Head, error) {
	output, err := git(ctx, root, readOnlyEnv, "status", "--porcelain=v2", "--branch", "-z", "--find-renames", "--untracked-files=all", "--", ".")
	if err != nil {
		return Head{}, fmt.Errorf("read the status of %q: %w", root, err)
	}
	return parseStatus(string(output)), nil
}

// parseStatus reads the output of git status --porcelain=v2 --branch -z, where
// a rename's record is followed by its old path.
func parseStatus(output string) Head {
	var head Head
	records := strings.Split(output, "\x00")
	for index := 0; index < len(records); index++ {
		record := records[index]
		header, isHeader := strings.CutPrefix(record, "# ")
		if !isHeader {
			if record != "" {
				head.Uncommitted++
			}
			if strings.HasPrefix(record, "2 ") {
				index++
			}
			continue
		}
		name, value, _ := strings.Cut(header, " ")
		switch name {
		case "branch.oid":
			if value != "(initial)" {
				head.Commit = value[:min(len(value), shortHashLength)]
			}
		case "branch.head":
			if value != "(detached)" {
				head.Branch = value
			}
		case "branch.upstream":
			head.Upstream = value
		case "branch.ab":
			ahead, behind, _ := strings.Cut(value, " ")
			head.Ahead, _ = strconv.Atoi(strings.TrimPrefix(ahead, "+"))
			head.Behind, _ = strconv.Atoi(strings.TrimPrefix(behind, "-"))
		}
	}
	return head
}

const shortHashLength = 7

// Branches lists the local and remote branches, local ones first.
func Branches(ctx context.Context, root string) ([]string, error) {
	output, err := git(ctx, root, nil, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, fmt.Errorf("list the branches of %q: %w", root, err)
	}
	var branches []string
	for ref := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if ref == "" || strings.HasSuffix(ref, "/HEAD") {
			continue
		}
		if name, local := strings.CutPrefix(ref, "refs/heads/"); local {
			branches = append(branches, name)
			continue
		}
		branches = append(branches, strings.TrimPrefix(ref, "refs/remotes/"))
	}
	return branches, nil
}

// Comparison is the files of the opened folder that differ from a commit.
type Comparison struct {
	// Base is the commit the work tree is compared with.
	Base    string
	Changes []Change
}

// CompareWithHead lists what differs from HEAD: what is not committed yet.
func CompareWithHead(ctx context.Context, root string) (Comparison, error) {
	base, err := headCommit(ctx, root)
	if err != nil {
		return Comparison{}, err
	}
	return compareWorkTree(ctx, root, base)
}

// CompareWithBranch lists all that the work tree changes since it left branch:
// commits and work not committed yet.
func CompareWithBranch(ctx context.Context, root string, branch string) (Comparison, error) {
	output, err := git(ctx, root, nil, "merge-base", branch, "HEAD")
	if err != nil {
		return Comparison{}, fmt.Errorf("find where HEAD left %s: %w", branch, err)
	}
	return compareWorkTree(ctx, root, strings.TrimSpace(string(output)))
}

// headCommit returns HEAD, or the empty tree before the first commit.
func headCommit(ctx context.Context, root string) (string, error) {
	output, err := git(ctx, root, nil, "rev-parse", "--verify", "--quiet", "HEAD")
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return emptyTree(ctx, root)
	}
	if err != nil {
		return "", fmt.Errorf("read HEAD of %q: %w", root, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// emptyTree names the tree without files, without writing it.
func emptyTree(ctx context.Context, root string) (string, error) {
	output, err := git(ctx, root, nil, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", fmt.Errorf("name the empty tree of %q: %w", root, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// compareWorkTree lists the files that differ between base and the work tree,
// with the untracked ones.
func compareWorkTree(ctx context.Context, root string, base string) (Comparison, error) {
	changes, err := renamedChanges(ctx, root, readOnlyEnv, "diff", base, "--")
	if err != nil {
		return Comparison{}, fmt.Errorf("compare the work tree with %s: %w", base, err)
	}
	untracked, err := git(ctx, root, nil, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return Comparison{}, fmt.Errorf("list untracked files of %q: %w", root, err)
	}
	for name := range strings.SplitSeq(string(untracked), "\x00") {
		if name != "" && !workspace.IsIgnored(name) {
			changes = append(changes, Change{Path: name, Kind: Untracked})
		}
	}
	slices.SortFunc(changes, func(a Change, b Change) int { return strings.Compare(a.Path, b.Path) })
	return Comparison{Base: base, Changes: changes}, nil
}

// renamedChanges runs a Git comparison, given as its command and revisions,
// and lists the files of the opened folder it finds changed, a renamed file
// once, at its new path.
func renamedChanges(ctx context.Context, root string, env []string, comparison ...string) ([]Change, error) {
	options := []string{"-z", "--find-renames", "--relative"}
	named, err := git(ctx, root, env, slices.Concat(comparison[:1], options, []string{"--name-status"}, comparison[1:])...)
	if err != nil {
		return nil, err
	}
	counted, err := git(ctx, root, env, slices.Concat(comparison[:1], options, []string{"--numstat"}, comparison[1:])...)
	if err != nil {
		return nil, err
	}
	counts := parseRenamedCounts(string(counted))
	changes := parseRenamedKinds(string(named))
	for index := range changes {
		count := counts[changes[index].Path]
		changes[index].Added, changes[index].Removed, changes[index].Binary = count.added, count.removed, count.binary
	}
	slices.SortFunc(changes, func(a Change, b Change) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}

// parseRenamedKinds reads the output of --name-status -z --find-renames, where
// a rename is its status, its old path and its new one.
func parseRenamedKinds(output string) []Change {
	fields := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	var changes []Change
	for index := 0; index+1 < len(fields); {
		status := fields[index]
		if strings.HasPrefix(status, "R") && index+2 < len(fields) {
			change := Change{Path: fields[index+2], OldPath: fields[index+1], Kind: Renamed}
			index += 3
			if !workspace.IsIgnored(change.Path) {
				changes = append(changes, change)
			}
			continue
		}
		change := Change{Path: fields[index+1], Kind: kindOf(status)}
		index += 2
		if !workspace.IsIgnored(change.Path) {
			changes = append(changes, change)
		}
	}
	return changes
}

// parseRenamedCounts reads the output of --numstat -z --find-renames, where a
// rename's counts have an empty path followed by its old and new paths.
func parseRenamedCounts(output string) map[string]lineCount {
	records := strings.Split(output, "\x00")
	counts := map[string]lineCount{}
	for index := 0; index < len(records); index++ {
		fields := strings.SplitN(records[index], "\t", 3)
		if len(fields) != 3 {
			continue
		}
		path := fields[2]
		if path == "" && index+2 < len(records) {
			path = records[index+2]
			index += 2
		}
		added, addedErr := strconv.Atoi(fields[0])
		removed, removedErr := strconv.Atoi(fields[1])
		counts[path] = lineCount{added: added, removed: removed, binary: addedErr != nil || removedErr != nil}
	}
	return counts
}

// oldPathOf is where a change's file was before it: its old path when it was
// renamed.
func oldPathOf(change Change) string {
	if change.OldPath != "" {
		return change.OldPath
	}
	return change.Path
}

// WorkTreeSides returns a file's text in base and on disk.
func WorkTreeSides(ctx context.Context, root string, base string, change Change) (Sides, error) {
	path := change.Path
	old, oldReadable, err := readObject(ctx, root, base+":./"+oldPathOf(change))
	if err != nil {
		return Sides{}, err
	}
	updated, newReadable, err := readDisk(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return Sides{}, err
	}
	if !oldReadable || !newReadable {
		return Sides{Unreadable: true}, nil
	}
	return Sides{Old: old, New: updated}, nil
}

// readDisk returns a file's text, empty for a missing file, and reports whether
// it can be shown as text.
func readDisk(file string) (string, bool, error) {
	info, err := os.Stat(file)
	if errors.Is(err, fs.ErrNotExist) {
		return "", true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %q: %w", file, err)
	}
	if info.Size() > MaxSnapshotFileBytes {
		return "", false, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", false, fmt.Errorf("read %q: %w", file, err)
	}
	text, readable := textOf(data)
	return text, readable, nil
}

// Commit is one entry of the history.
type Commit struct {
	Hash    string
	Short   string
	Author  string
	When    time.Time
	Subject string
	// Refs are the branches and tags that point at it, and HEAD.
	Refs []string
}

// commitFields separates the fields of a commit in the output of git log.
const commitFields = "%H%x1f%h%x1f%an%x1f%at%x1f%s%x1f%D"

// Log returns count commits of the history of the opened folder, newest first,
// after skipping the first skip of them.
func Log(ctx context.Context, root string, skip int, count int) ([]Commit, error) {
	return readLog(ctx, root, skip, count, "HEAD", "--", ".")
}

// FileLog returns count commits that changed a file, newest first and following
// it across renames, after skipping the first skip of them.
func FileLog(ctx context.Context, root string, path string, skip int, count int) ([]Commit, error) {
	return readLog(ctx, root, skip, count, "--follow", "HEAD", "--", path)
}

// readLog reads a page of git log, given what to read after its options.
func readLog(ctx context.Context, root string, skip int, count int, args ...string) ([]Commit, error) {
	if _, err := git(ctx, root, nil, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, nil // No commit yet.
		}
		return nil, fmt.Errorf("read HEAD of %q: %w", root, err)
	}
	options := []string{"log", "-z", "--format=" + commitFields, "--skip=" + strconv.Itoa(skip), "--max-count=" + strconv.Itoa(count)}
	output, err := git(ctx, root, nil, append(options, args...)...)
	if err != nil {
		return nil, fmt.Errorf("read the history of %q: %w", root, err)
	}
	var commits []Commit
	for record := range strings.SplitSeq(string(output), "\x00") {
		if commit, ok := parseCommit(strings.TrimPrefix(record, "\n")); ok {
			commits = append(commits, commit)
		}
	}
	return commits, nil
}

func parseCommit(record string) (Commit, bool) {
	fields := strings.Split(record, "\x1f")
	if len(fields) != 6 {
		return Commit{}, false
	}
	seconds, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return Commit{}, false
	}
	return Commit{
		Hash: fields[0], Short: fields[1], Author: fields[2], When: time.Unix(seconds, 0),
		Subject: fields[4], Refs: parseRefs(fields[5]),
	}, true
}

// parseRefs reads the decorations of a commit, as "HEAD -> main, tag: v1".
func parseRefs(decoration string) []string {
	var refs []string
	for ref := range strings.SplitSeq(decoration, ", ") {
		ref = strings.TrimPrefix(ref, "tag: ")
		if head, branch, found := strings.Cut(ref, " -> "); found {
			refs = append(refs, head, branch)
			continue
		}
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

// CommitDetail is what a commit changed in the opened folder.
type CommitDetail struct {
	Hash string
	// Parent is the commit it follows, or the empty tree for the first one.
	Parent  string
	Message string
	Changes []Change
}

// ReadCommit reads a commit's message and the files it changed, compared with
// its first parent.
func ReadCommit(ctx context.Context, root string, hash string) (CommitDetail, error) {
	message, err := git(ctx, root, nil, "show", "-s", "--format=%B", hash)
	if err != nil {
		return CommitDetail{}, fmt.Errorf("read commit %s: %w", hash, err)
	}
	parent, err := firstParent(ctx, root, hash)
	if err != nil {
		return CommitDetail{}, err
	}
	changes, err := renamedChanges(ctx, root, nil, "diff-tree", "-r", parent, hash)
	if err != nil {
		return CommitDetail{}, fmt.Errorf("compare commit %s with %s: %w", hash, parent, err)
	}
	return CommitDetail{Hash: hash, Parent: parent, Message: strings.TrimSpace(string(message)), Changes: changes}, nil
}

func firstParent(ctx context.Context, root string, hash string) (string, error) {
	output, err := git(ctx, root, nil, "rev-parse", "--verify", "--quiet", hash+"^")
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return emptyTree(ctx, root)
	}
	if err != nil {
		return "", fmt.Errorf("read the parent of %s: %w", hash, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// CommitSides returns a file's text before and after a commit.
func CommitSides(ctx context.Context, root string, detail CommitDetail, change Change) (Sides, error) {
	return objectSides(ctx, root, detail.Parent+":./"+oldPathOf(change), detail.Hash+":./"+change.Path)
}

// FileStatuses returns what happened to each file of the opened folder that
// differs from HEAD, untracked ones too, or ErrNotRepository outside Git.
func FileStatuses(ctx context.Context, root string) (map[string]Kind, error) {
	if _, err := git(ctx, root, nil, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, fmt.Errorf("read the status of %q: %w: %w", root, ErrNotRepository, err)
	}
	comparison, err := CompareWithHead(ctx, root)
	if err != nil {
		return nil, err
	}
	kinds := make(map[string]Kind, len(comparison.Changes))
	for _, change := range comparison.Changes {
		kinds[change.Path] = change.Kind
	}
	return kinds, nil
}

// GitDir returns the folder where Git keeps HEAD and the index of the work
// tree at root, or ErrNotRepository outside Git.
func GitDir(ctx context.Context, root string) (string, error) {
	output, err := git(ctx, root, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("find the Git folder of %q: %w: %w", root, ErrNotRepository, err)
	}
	return strings.TrimRight(string(output), "\n"), nil
}
