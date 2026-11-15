package tracker

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func refsRepo(t *testing.T) *gitRepo {
	t.Helper()
	r := newGitRepo(t)
	r.git("config", "maintenance.auto", "false")
	r.git("config", "gc.auto", "0")
	return r
}

// A page asks for many branches and for main many times: one for-each-ref
// answers them all, and the rev-parse and symbolic-ref each ask used to run
// are gone (performance audit 2026-11-03, cause 8).
func TestBranchHeadsComeFromOneRead(t *testing.T) {
	r := refsRepo(t)
	wt := r.worktree("work/a", "main")
	r.writeIn(wt, "new.txt", "a\n")
	r.commitIn(wt, "a")
	calls := countGitCalls(t)
	for i := 0; i < 20; i++ {
		if BranchHead(r.root, "work/a") == "" {
			t.Fatal("work/a has no head")
		}
		if BranchHead(r.root, "work/none") != "" {
			t.Fatal("a branch that isn't there has a head")
		}
		if name, _ := MainBranch(r.root); name != "main" {
			t.Fatalf("main branch %q", name)
		}
		if b, m := BranchHeads(r.root, "b"); b != "" || m != "" {
			t.Fatalf("heads of a branchless slug: %q %q", b, m)
		}
	}
	if n := calls(""); n != 1 {
		t.Errorf("%d git calls over 20 rounds, want 1", n)
	}
}

// Every way a ref changes shows at once, from any process.
func TestBranchHeadFollowsEveryChange(t *testing.T) {
	r := refsRepo(t)
	wt := r.worktree("work/x/y", "main")
	base := BranchHead(r.root, "work/x/y")
	if base == "" {
		t.Fatal("no head")
	}

	r.writeIn(wt, "new.txt", "a\n")
	r.commitIn(wt, "a")
	want := strings.TrimSpace(r.git("rev-parse", "work/x/y"))
	if got := BranchHead(r.root, "work/x/y"); got != want || got == base {
		t.Errorf("after a commit in the worktree: %s, want %s", got, want)
	}

	r.git("branch", "work/new/deep/er", "main")
	if BranchHead(r.root, "work/new/deep/er") == "" {
		t.Error("a branch in a new folder doesn't show")
	}

	r.git("pack-refs", "--all", "--prune")
	if got := BranchHead(r.root, "work/x/y"); got != want {
		t.Errorf("after pack-refs: %s, want %s", got, want)
	}
	r.git("update-ref", "refs/heads/work/x/y", base)
	if got := BranchHead(r.root, "work/x/y"); got != base {
		t.Errorf("a packed ref moved: %s, want %s", got, base)
	}

	r.git("branch", "-D", "work/new/deep/er")
	if BranchHead(r.root, "work/new/deep/er") != "" {
		t.Error("a deleted branch still shows")
	}
}

// Commits made faster than a clock second are each seen: the snapshot is kept
// on the refs folder's time in nanoseconds, never on a stamp in seconds.
func TestBranchHeadSeesCommitsWithinOneSecond(t *testing.T) {
	r := refsRepo(t)
	wt := r.worktree("work/a", "main")
	seen := map[string]bool{}
	for i := 0; i < 30; i++ {
		r.writeIn(wt, "new.txt", strconv.Itoa(i)+"\n")
		r.commitIn(wt, "round "+strconv.Itoa(i))
		want := strings.TrimSpace(r.git("rev-parse", "work/a"))
		if got := BranchHead(r.root, "work/a"); got != want {
			t.Fatalf("round %d: %s, want %s", i, got, want)
		}
		seen[want] = true
	}
	if len(seen) != 30 {
		t.Fatalf("%d heads over 30 commits", len(seen))
	}
}

// The worktree answers for the same repository, and shares the reading.
func TestBranchHeadFromAWorktree(t *testing.T) {
	r := refsRepo(t)
	wt := r.worktree("work/a", "main")
	want := BranchHead(r.root, "work/a")
	calls := countGitCalls(t)
	if got := BranchHead(wt, "work/a"); got != want {
		t.Errorf("from the worktree: %q, want %q", got, want)
	}
	if n := calls(""); n != 0 {
		t.Errorf("%d git calls for a repository already read", n)
	}
}

// MainBranch keeps its order: origin's HEAD, main, master, the checkout's
// branch; and its errors.
func TestMainBranchOrder(t *testing.T) {
	r := refsRepo(t)
	r.git("branch", "develop", "main")
	r.git("update-ref", "refs/remotes/origin/develop", "main")
	if got, _ := MainBranch(r.root); got != "main" {
		t.Errorf("no origin HEAD: %q", got)
	}
	r.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	if got, _ := MainBranch(r.root); got != "develop" {
		t.Errorf("origin HEAD on develop: %q", got)
	}
	if name, sha, err := MainHead(r.root); err != nil || name != "develop" || sha != strings.TrimSpace(r.git("rev-parse", "develop")) {
		t.Errorf("MainHead: %q %q %v", name, sha, err)
	}
	r.git("symbolic-ref", "-d", "refs/remotes/origin/HEAD")
	if got, _ := MainBranch(r.root); got != "main" {
		t.Errorf("origin HEAD gone: %q", got)
	}
	r.git("checkout", "-q", "-b", "trunk")
	r.git("branch", "-D", "main")
	if got, _ := MainBranch(r.root); got != "trunk" {
		t.Errorf("neither main nor master: %q", got)
	}
	if name, sha, err := MainHead(r.root); err != nil || name != "trunk" || sha == "" {
		t.Errorf("MainHead of a checkout's branch: %q %q %v", name, sha, err)
	}

	if _, err := MainBranch(t.TempDir()); !errors.Is(err, ErrNoGit) {
		t.Errorf("outside a repository: %v", err)
	}
}

// A folder inside a checkout is no checkout's top: nothing is kept, and the
// answer is still right.
func TestBranchHeadFromASubfolder(t *testing.T) {
	r := refsRepo(t)
	sub := filepath.Join(r.root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := BranchHead(sub, "main"), strings.TrimSpace(r.git("rev-parse", "main")); got != want {
		t.Errorf("from a subfolder: %q, want %q", got, want)
	}
}

// An exit 1 that is a result stays one: an ancestor check that says no and
// a grep that finds nothing run once. The input of a call fed on stdin is
// part of its key.
func TestGitCachedKeepsAnswers(t *testing.T) {
	r := refsRepo(t)
	first := strings.TrimSpace(r.git("rev-parse", "main"))
	r.write("b.txt", "b\n")
	r.commit("b")
	second := strings.TrimSpace(r.git("rev-parse", "main"))
	resetGitCacheMemory()
	t.Cleanup(resetGitCacheMemory)
	calls := countGitCalls(t)
	for i := 0; i < 3; i++ {
		if _, err := GitCached(r.root, "merge-base", "--is-ancestor", second, first); err == nil {
			t.Error("the newer commit is an ancestor")
		}
		if _, err := GitCached(r.root, "merge-base", "--is-ancestor", first, second); err != nil {
			t.Errorf("the older commit is no ancestor: %v", err)
		}
		if out, err := GitCached(r.root, "grep", "-w", "-F", "-e", "nothinglikethis", second, "--"); err == nil || out != "" {
			t.Errorf("grep found %q, %v", out, err)
		}
		for _, sha := range []string{first, second} {
			out, err := GitCachedIn(r.root, sha+"\n", "rev-list", "--no-walk", "--stdin")
			if err != nil || strings.TrimSpace(out) != sha {
				t.Errorf("stdin %s: %q %v", sha, out, err)
			}
		}
	}
	if n := calls("") - calls("rev-parse --git-common-dir"); n != 5 {
		t.Errorf("%d git calls, want one per question (5)", n)
	}
}

// A parsed session is reused while its file is unchanged, and read again
// when it isn't.
func TestPlaytestSessionFilesFollowTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "playtest", "sessions", "one.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("issue: cli/a\nsteps:\n  - id: TP1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if got := IssuePlaytestSessions(dir, "cli/a"); len(got) != 1 || got[0].Session != "one" || len(got[0].Steps) != 1 {
			t.Fatalf("round %d: %+v", i, got)
		}
	}
	if err := os.WriteFile(path, []byte("issue: cli/b\nsteps:\n  - id: TP1\n  - id: TP2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := IssuePlaytestSessions(dir, "cli/a"); len(got) != 0 {
		t.Errorf("the session moved to cli/b, cli/a still has %+v", got)
	}
	if got := IssuePlaytestSessions(dir, "cli/b"); len(got) != 1 || len(got[0].Steps) != 2 {
		t.Errorf("cli/b: %+v", got)
	}
	if err := os.WriteFile(path, []byte("issue: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := IssuePlaytestSessions(dir, "cli/b"); len(got) != 0 {
		t.Errorf("a file that doesn't parse: %+v", got)
	}
}
