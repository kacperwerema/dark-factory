package lint

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func lintGit(t *testing.T, root string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root, "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A project without worktree: true whose workers keep worktrees under
// .worktrees/ all the same gets one warning suggesting worktree: true; the
// same board with worktree: true gets none.
func TestWorktreesSuggestsWorktreeTrue(t *testing.T) {
	issues := map[string]string{
		"Sys/short.md": "---\ntitle: \"Short\"\nstatus: idea\ncreated: \"2026-11-01\"\n---\nBody.\n",
		"Sys/other.md": "---\ntitle: \"Other\"\nstatus: idea\ncreated: \"2026-11-01\"\n---\nBody.\n",
	}
	env := lintProject(t, minimalWorkflow, issues)
	root := env.Project.Root()
	lintGit(t, root, "init", "-q", "-b", "main")
	lintGit(t, root, "commit", "-q", "--allow-empty", "-m", "init")
	if got := run(t, env, "worktrees"); len(got) != 0 {
		t.Fatalf("no worktrees yet: %+v, want none", got)
	}
	lintGit(t, root, "worktree", "add", "-q", "-b", "work/sys/short", filepath.Join(root, ".worktrees", "s"))

	got := run(t, env, "worktrees")
	if len(got) != 1 || !has(got, "1 open issue has a worktree under .worktrees/", "sys/short on work/sys/short", "add `worktree: true`", "A warning only") {
		t.Errorf("findings = %+v, want one worktree: true suggestion", got)
	}

	on := lintProject(t, "worktree: true\n"+minimalWorkflow, issues)
	if got := checkWorktrees(on); len(got) != 0 {
		t.Errorf("worktree: true: %+v, want none", got)
	}
}
