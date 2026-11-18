package review

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}

func write(t *testing.T, root string, name string, text string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run(t, root, "init", "--quiet")
	write(t, root, "a.go", "one\ntwo\n")
	write(t, root, "b.go", "bee\n")
	run(t, root, "add", ".")
	run(t, root, "commit", "--quiet", "-m", "start")
	return root
}

func openStore(t *testing.T, root string) *Store {
	t.Helper()
	store, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func listing(t *testing.T, store *Store) Listing {
	t.Helper()
	listed, err := store.Changes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return listed
}

func list(t *testing.T, store *Store) map[string]Change {
	t.Helper()
	listed := listing(t, store)
	byPath := make(map[string]Change, len(listed.Changes))
	for _, change := range listed.Changes {
		byPath[change.Path] = change
	}
	return byPath
}

func TestChangesSurviveAnAgentsCommits(t *testing.T) {
	root := newRepository(t)
	store := openStore(t, root)
	if got := list(t, store); len(got) != 0 {
		t.Fatalf("a clean tree lists %+v", got)
	}

	write(t, root, "a.go", "one\ntwo\nthree\n")
	write(t, root, "new.go", "fresh\n")
	run(t, root, "add", ".")
	run(t, root, "commit", "--quiet", "-m", "agent work")
	write(t, root, "b.go", "bee\nsting\n")
	if err := os.Remove(filepath.Join(root, "new.go")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "later.go", "later\n")

	got := list(t, store)
	if len(got) != 3 {
		t.Fatalf("listed %+v", got)
	}
	if got["a.go"].Kind != Modified || got["a.go"].Added != 1 || got["later.go"].Kind != Added {
		t.Errorf("listed %+v", got)
	}
	if status := run(t, root, "status", "--porcelain"); !strings.Contains(status, " M b.go") || strings.Contains(status, "A ") {
		t.Errorf("the person's index changed: %q", status)
	}
}

func TestReviewedFileReturnsOnlyWithNewEdits(t *testing.T) {
	root := newRepository(t)
	store := openStore(t, root)
	ctx := context.Background()
	write(t, root, "a.go", "one\ntwo\nthree\n")
	if err := store.MarkReviewed(ctx, listing(t, store).Version, []string{"a.go"}); err != nil {
		t.Fatal(err)
	}
	if got := list(t, store)["a.go"]; !got.Reviewed {
		t.Fatalf("a.go is %+v after marking", got)
	}

	write(t, root, "a.go", "one\ntwo\nthree\nfour\n")
	listed := listing(t, store)
	got := listed.Changes[0]
	if got.Reviewed || got.Added != 1 {
		t.Fatalf("a.go is %+v after a new edit", got)
	}
	sides, err := store.Sides(ctx, listed.Version, got)
	if err != nil {
		t.Fatal(err)
	}
	if sides.Old != "one\ntwo\nthree\n" || sides.New != "one\ntwo\nthree\nfour\n" {
		t.Errorf("unread sides %+v", sides)
	}

	if err := store.MarkUnreviewed(ctx, []string{"a.go"}); err != nil {
		t.Fatal(err)
	}
	if got := list(t, store)["a.go"]; got.Reviewed || got.Added != 2 {
		t.Errorf("a.go is %+v after unmarking", got)
	}
}

func TestAnEditAfterListingStaysUnread(t *testing.T) {
	root := newRepository(t)
	store := openStore(t, root)
	ctx := context.Background()
	write(t, root, "a.go", "seen\n")
	version := listing(t, store).Version
	write(t, root, "a.go", "not seen\n")
	listing(t, store)
	if err := store.MarkReviewed(ctx, version, []string{"a.go"}); err != nil {
		t.Fatal(err)
	}
	if got := list(t, store)["a.go"]; got.Reviewed {
		t.Errorf("an edit nobody saw counts as read: %+v", got)
	}
}

func TestFinishStartsANewReviewThatOutlivesTheStore(t *testing.T) {
	root := newRepository(t)
	store := openStore(t, root)
	ctx := context.Background()
	write(t, root, "a.go", "changed\n")
	if _, err := store.Finish(ctx, listing(t, store).Version); err != nil {
		t.Fatal(err)
	}
	if got := list(t, store); len(got) != 0 {
		t.Fatalf("listed %+v after finishing", got)
	}

	write(t, root, "b.go", "changed\n")
	reopened := openStore(t, root)
	got := list(t, reopened)
	if len(got) != 1 || got["b.go"].Path == "" {
		t.Errorf("listed %+v after reopening", got)
	}
}

func TestSnapshotsSkipLargeAndIgnoredFiles(t *testing.T) {
	root := newRepository(t)
	write(t, root, ".gitignore", "out/\n")
	write(t, root, "out/skip.go", "ignored\n")
	write(t, root, "server", strings.Repeat("x", int(MaxSnapshotFileBytes)+1))
	store := openStore(t, root)
	got := list(t, store)
	if len(got) != 1 || got[".gitignore"].Path == "" {
		t.Errorf("listed %+v", got)
	}
}

func TestSubfolderListsOnlyItsOwnFiles(t *testing.T) {
	root := newRepository(t)
	write(t, root, "pkg/x.go", "x\n")
	run(t, root, "add", ".")
	run(t, root, "commit", "--quiet", "-m", "pkg")
	store := openStore(t, filepath.Join(root, "pkg"))
	write(t, root, "pkg/x.go", "y\n")
	write(t, root, "a.go", "elsewhere\n")
	listed := listing(t, store)
	if len(listed.Changes) != 1 || listed.Changes[0].Path != "x.go" || listed.Changes[0].Kind != Modified {
		t.Fatalf("listed %+v", listed)
	}
	sides, err := store.Sides(context.Background(), listed.Version, listed.Changes[0])
	if err != nil {
		t.Fatal(err)
	}
	if sides.Old != "x\n" || sides.New != "y\n" {
		t.Errorf("sides %+v", sides)
	}
	if err := store.MarkReviewed(context.Background(), listed.Version, []string{"x.go"}); err != nil {
		t.Fatal(err)
	}
	if got := list(t, store)["x.go"]; !got.Reviewed {
		t.Errorf("x.go is %+v", got)
	}
}

func TestFolderOutsideGitIsRefused(t *testing.T) {
	if _, err := Open(context.Background(), t.TempDir()); !errors.Is(err, ErrNotRepository) {
		t.Errorf("opened with %v", err)
	}
}
