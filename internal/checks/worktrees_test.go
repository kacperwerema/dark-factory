package checks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

// countGit puts a git on PATH that logs each call before running the real
// one, and returns how many worktree listings it has seen.
func countGit(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls.log")
	// The log sits beside the stand-in, so its body is the same every run.
	standin.Write(t, filepath.Join(bin, "git"), "printf '%s\\n' \"$*\" >> \"${0%/*}/calls.log\"\nexec '"+real+"' \"$@\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		data, _ := os.ReadFile(log)
		return strings.Count(string(data), "worktree list")
	}
}

func quietRepo(t *testing.T) string {
	t.Helper()
	main := t.TempDir()
	gitRun(t, main, "init", "-q", "-b", "main")
	gitRun(t, main, "config", "maintenance.auto", "false")
	gitRun(t, main, "config", "gc.auto", "0")
	gitRun(t, main, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	return main
}

// The waiting-on-you count asks IssueWorktree once for every issue in
// human-testing: on a board with 65 of them that was 65 `git worktree list`
// runs on every page. The listing is read once and kept until the
// repository's worktrees change.
func TestIssueWorktreeListsTheWorktreesOncePerChange(t *testing.T) {
	main := quietRepo(t)
	wt := filepath.Join(main, ".worktrees", "fix")
	gitRun(t, main, "worktree", "add", "-q", "-b", "work/plan/fix", wt)
	want, _ := filepath.EvalSymlinks(wt)
	calls := countGit(t)

	for i := 0; i < 40; i++ {
		IssueWorktree(main, "plan/other-"+string(rune('a'+i%26)))
	}
	if got, _ := filepath.EvalSymlinks(IssueWorktree(main, "plan/fix")); got != want {
		t.Fatalf("IssueWorktree = %q, want %q", got, want)
	}
	if n := calls(); n > 1 {
		t.Fatalf("41 lookups ran git worktree list %d times, want at most 1", n)
	}

	// A new worktree is seen at once.
	wt2 := filepath.Join(main, ".worktrees", "two")
	gitRun(t, main, "worktree", "add", "-q", "-b", "work/plan/two", wt2)
	if IssueWorktree(main, "plan/two") == "" {
		t.Fatal("a worktree added after the first listing isn't found")
	}
	// So is a worktree switched to another branch.
	gitRun(t, wt2, "checkout", "-q", "-b", "work/plan/three")
	if IssueWorktree(main, "plan/three") == "" {
		t.Fatal("a worktree moved to another branch isn't found on it")
	}
	if got := IssueWorktree(main, "plan/two"); got != "" {
		t.Fatalf("the branch the worktree left still names it: %q", got)
	}
	// And a removed one is gone.
	gitRun(t, main, "worktree", "remove", "--force", wt)
	if got := IssueWorktree(main, "plan/fix"); got != "" {
		t.Fatalf("a removed worktree is still found: %q", got)
	}
	// From a linked worktree's folder too: the same repository's listing.
	if IssueWorktree(wt2, "plan/three") == "" {
		t.Fatal("asked from the linked worktree, its own branch isn't found")
	}
}
