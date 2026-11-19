package review

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

func TestReadingTheRepository(t *testing.T) {
	ctx := context.Background()
	root := newRepository(t)
	run(t, root, "branch", "-M", "main")
	run(t, root, "switch", "--quiet", "-c", "feature")
	write(t, root, "a.go", "one\n2\n")
	run(t, root, "commit", "--quiet", "-am", "change a\n\nWhy it changed.")
	write(t, root, "b.go", "bee\nmore\n")
	write(t, root, "new.go", "fresh\n")

	head, err := ReadHead(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if head.Branch != "feature" || head.Uncommitted != 2 || head.Commit == "" {
		t.Errorf("head is %+v", head)
	}

	uncommitted, err := CompareWithHead(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := summary(uncommitted.Changes); !slices.Equal(got, []string{"b.go M +1-0", "new.go U +0-0"}) {
		t.Errorf("uncommitted changes are %v", got)
	}
	sides, err := WorkTreeSides(ctx, root, uncommitted.Base, Change{Path: "b.go", Kind: Modified})
	if err != nil || sides.Old != "bee\n" || sides.New != "bee\nmore\n" {
		t.Errorf("sides of b.go are %+v, %v", sides, err)
	}

	sinceMain, err := CompareWithBranch(ctx, root, "main")
	if err != nil {
		t.Fatal(err)
	}
	if got := summary(sinceMain.Changes); !slices.Equal(got, []string{"a.go M +1-1", "b.go M +1-0", "new.go U +0-0"}) {
		t.Errorf("changes since main are %v", got)
	}

	branches, err := Branches(ctx, root)
	if err != nil || !slices.Equal(branches, []string{"feature", "main"}) {
		t.Errorf("branches are %v, %v", branches, err)
	}

	commits, err := Log(ctx, root, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].Subject != "change a" || !slices.Equal(commits[0].Refs, []string{"HEAD", "feature"}) {
		t.Fatalf("history is %+v", commits)
	}
	older, err := Log(ctx, root, 1, 10)
	if err != nil || len(older) != 1 || older[0].Subject != "start" {
		t.Errorf("history past the first commit is %+v, %v", older, err)
	}

	detail, err := ReadCommit(ctx, root, commits[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Message != "change a\n\nWhy it changed." || !slices.Equal(summary(detail.Changes), []string{"a.go M +1-1"}) {
		t.Errorf("commit is %+v", detail)
	}
	sides, err = CommitSides(ctx, root, detail, detail.Changes[0])
	if err != nil || sides.Old != "one\ntwo\n" || sides.New != "one\n2\n" {
		t.Errorf("sides of the commit are %+v, %v", sides, err)
	}

	first, err := ReadCommit(ctx, root, commits[1].Hash)
	if err != nil || !slices.Equal(summary(first.Changes), []string{"a.go A +2-0", "b.go A +1-0"}) {
		t.Errorf("first commit is %+v, %v", first, err)
	}
}

func TestFileHistoryAndBlame(t *testing.T) {
	ctx := context.Background()
	root := newRepository(t)
	write(t, root, "a.go", "one\nchanged\n")
	run(t, root, "commit", "--quiet", "-am", "change two")
	write(t, root, "b.go", "unrelated\n")
	run(t, root, "commit", "--quiet", "-am", "touch b")

	commits, err := FileLog(ctx, root, "a.go", 0, 10)
	if err != nil || len(commits) != 2 || commits[0].Subject != "change two" || commits[1].Subject != "start" {
		t.Fatalf("history of a.go is %+v, %v", commits, err)
	}

	blame, err := BlameText(ctx, root, "a.go", "one\nchanged\nunsaved\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(blame.Lines) != 3 {
		t.Fatalf("blame %+v", blame)
	}
	summaries := []string{}
	for _, index := range blame.Lines {
		commit := blame.Commits[index]
		summaries = append(summaries, fmt.Sprintf("%s %v", commit.Summary, commit.Uncommitted))
	}
	if want := []string{"start false", "change two false", "Version of a.go from standard input true"}; !slices.Equal(summaries, want) {
		t.Errorf("blame says %q", summaries)
	}
}

func TestRenamesShowOnce(t *testing.T) {
	ctx := context.Background()
	root := newRepository(t)
	run(t, root, "mv", "a.go", "moved.go")
	write(t, root, "moved.go", "one\ntwo\nthree\n")

	uncommitted, err := CompareWithHead(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := summary(uncommitted.Changes); !slices.Equal(got, []string{"moved.go R +1-0"}) || uncommitted.Changes[0].OldPath != "a.go" {
		t.Fatalf("uncommitted changes are %v, %+v", got, uncommitted.Changes)
	}
	if head, err := ReadHead(ctx, root); err != nil || head.Uncommitted != 1 {
		t.Errorf("head is %+v, %v", head, err)
	}
	sides, err := WorkTreeSides(ctx, root, uncommitted.Base, uncommitted.Changes[0])
	if err != nil || sides.Old != "one\ntwo\n" || sides.New != "one\ntwo\nthree\n" {
		t.Errorf("sides of the rename are %+v, %v", sides, err)
	}

	run(t, root, "commit", "--quiet", "-am", "move a")
	commits, err := Log(ctx, root, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := ReadCommit(ctx, root, commits[0].Hash)
	if err != nil || !slices.Equal(summary(detail.Changes), []string{"moved.go R +1-0"}) {
		t.Fatalf("the commit is %+v, %v", detail, err)
	}
	sides, err = CommitSides(ctx, root, detail, detail.Changes[0])
	if err != nil || sides.Old != "one\ntwo\n" {
		t.Errorf("sides of the committed rename are %+v, %v", sides, err)
	}
}

func summary(changes []Change) []string {
	letters := map[Kind]string{Modified: "M", Added: "A", Deleted: "D", Untracked: "U", Renamed: "R"}
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		lines = append(lines, fmt.Sprintf("%s %s +%d-%d", change.Path, letters[change.Kind], change.Added, change.Removed))
	}
	return lines
}
