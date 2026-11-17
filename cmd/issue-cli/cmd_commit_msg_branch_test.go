package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// In a project without worktree: true a worker branches as it likes: the
// Refs: hint still finds the issue, by a branch name ending in its slug or
// by the worktree's HANDOFF.md, and names the real branch. A branch that
// matches nothing stays quiet.
func TestCommitMsgHintsRefsOnABranchNotNamedWork(t *testing.T) {
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, c := range []struct {
		name, branch, handoff, want string
	}{
		{"handoff", "work/short-name", "# HANDOFF: cli/sample\n", "no commit on work/short-name references the issue yet"},
		{"branch name", "feature/sample", "", "no commit on feature/sample references the issue yet"},
		{"nothing matches", "work/unrelated", "# notes\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			proj, _, _ := makeProofFixture(t, "2026-10-23")
			requireCommitReference(t, proj)
			dir := filepath.Dir(proj.WorkflowFile)
			if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
				git(dir, "init", "-q", "-b", "main")
				git(dir, "commit", "-q", "--allow-empty", "-m", "init")
			}
			wt := filepath.Join(dir, ".worktrees", "short")
			git(dir, "worktree", "add", "-q", "-b", c.branch, wt)
			if c.handoff != "" {
				if err := os.WriteFile(filepath.Join(wt, "HANDOFF.md"), []byte(c.handoff), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(wt)
			t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "") // the note, not the added line
			ctx, _, stderr := newTestContext(proj, false)
			if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject)}); err != nil {
				t.Fatalf("message refused: %v\n%s", err, stderr)
			}
			out := stderr.String()
			if c.want == "" {
				if strings.Contains(out, "Refs:") {
					t.Errorf("a branch of no issue got a hint:\n%s", out)
				}
				return
			}
			assertContains(t, out, c.want)
			assertContains(t, out, "\n"+refsLine+"\n")
		})
	}
}
