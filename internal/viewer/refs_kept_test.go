package viewer

import (
	"path/filepath"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/recent"
)

// keptRepo is a repository with one commit on main and a release branch,
// its refs aged past refsKeptYoung, and readings kept as viewer.Run keeps
// them.
func keptRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "a\n")
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "init")
	gitRun(t, root, "branch", "release/v0.9")
	ageGitRefs(t, root)
	recent.Keep(machineReadingKeep)
	t.Cleanup(func() { recent.Keep(0); recent.Forget() }) // no reading outlives the test
	return root
}

// readCounter is a refsKept read that counts its calls.
func readCounter(n *int) func() int {
	return func() int { *n++; return *n }
}

// TestRefsKeptReadsAgainWhenARefChanges: an answer stands while the refs
// are as they were, and each kind of change makes the next ask read again:
// a new branch, a commit moving a branch, a new tag, packed refs, a deleted
// branch, a git config write, a checkout moving HEAD.
func TestRefsKeptReadsAgainWhenARefChanges(t *testing.T) {
	root := keptRepo(t)
	young := refsKeptYoung
	refsKeptYoung = 0 // each change below is moments old
	t.Cleanup(func() { refsKeptYoung = young })
	n := 0
	ask := func() int { return refsKept(root, []string{"t"}, readCounter(&n)) }
	if ask(); ask() != 1 {
		t.Fatalf("read %d times with the refs unchanged, want 1", n)
	}
	for _, c := range []struct {
		what string
		do   func()
	}{
		{"a new branch", func() { gitRun(t, root, "branch", "work/x") }},
		{"a commit on main", func() { gitRun(t, root, "commit", "-q", "--allow-empty", "-m", "more") }},
		{"a new tag", func() { gitRun(t, root, "tag", "v0.9.1-rc.1") }},
		{"packed refs", func() { gitRun(t, root, "pack-refs", "--all") }},
		{"a deleted branch", func() { gitRun(t, root, "branch", "-D", "work/x") }},
		{"a git config write", func() { gitRun(t, root, "config", "issue-cli-release.release/v0.9.validated", "abc") }},
		{"a checkout", func() { gitRun(t, root, "checkout", "-q", "release/v0.9") }},
	} {
		before := n
		c.do()
		if ask(); n == before {
			t.Errorf("%s: kept the old answer", c.what)
		}
		if mid := n; ask() != mid {
			t.Errorf("%s: read again with the refs unchanged after it", c.what)
		}
	}
}

// TestRefsKeptYoungStampNotKept: an answer read while a ref is younger than
// refsKeptYoung is not kept, so a second write in the same clock tick can't
// hide behind an unchanged stamp.
func TestRefsKeptYoungStampNotKept(t *testing.T) {
	root := keptRepo(t)
	gitRun(t, root, "branch", "work/young")
	n := 0
	refsKept(root, []string{"t"}, readCounter(&n))
	refsKept(root, []string{"t"}, readCounter(&n))
	if n != 2 {
		t.Errorf("read %d times under a young stamp, want 2 (nothing kept)", n)
	}
}

// TestRefsKeptOffWhenReadingsAre: tests, issue-cli and a scenario viewer
// (recent.Window 0) read git every time; so does a folder that is no
// checkout.
func TestRefsKeptOffWhenReadingsAre(t *testing.T) {
	root := keptRepo(t)
	recent.Keep(0)
	n := 0
	refsKept(root, []string{"t"}, readCounter(&n))
	refsKept(root, []string{"t"}, readCounter(&n))
	if n != 2 {
		t.Errorf("read %d times with readings off, want 2", n)
	}
	recent.Keep(machineReadingKeep)
	plain := t.TempDir()
	n = 0
	refsKept(plain, []string{"t"}, readCounter(&n))
	refsKept(plain, []string{"t"}, readCounter(&n))
	if n != 2 {
		t.Errorf("read %d times outside a checkout, want 2", n)
	}
}

// TestRefsKeptKeysByCallAndRepo: two calls, or the same call on two
// repositories, never share an answer.
func TestRefsKeptKeysByCallAndRepo(t *testing.T) {
	a, b := keptRepo(t), keptRepo(t)
	if got := refsKept(a, []string{"tip", "main"}, func() string { return "a-main" }); got != "a-main" {
		t.Fatal(got)
	}
	if got := refsKept(a, []string{"tip", "release/v0.9"}, func() string { return "a-rel" }); got != "a-rel" {
		t.Errorf("another ref got %q", got)
	}
	if got := refsKept(b, []string{"tip", "main"}, func() string { return "b-main" }); got != "b-main" {
		t.Errorf("another repo got %q", got)
	}
	if got := refsKept(a, []string{"tip", "main"}, func() string { return "again" }); got != "a-main" {
		t.Errorf("the kept answer = %q, want a-main", got)
	}
}

// TestGitTipFollowsTheBranch: the real readings go through refsKept, and a
// branch that moved is read again at once.
func TestGitTipFollowsTheBranch(t *testing.T) {
	root := keptRepo(t)
	first := gitTip(root, "main")
	if first == "" {
		t.Fatal("no tip for main")
	}
	gitRun(t, root, "commit", "-q", "--allow-empty", "-m", "more")
	if next := gitTip(root, "main"); next == first {
		t.Errorf("gitTip kept %s after main moved", first)
	}
}
