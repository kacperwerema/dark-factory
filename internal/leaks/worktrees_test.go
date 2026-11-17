package leaks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func treeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDoctorLeaksNamesScratchpadAndSubagentWorktrees(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	treeGit(t, root, "init", "-q", "-b", "main")
	treeGit(t, root, "config", "user.email", "t@example.com")
	treeGit(t, root, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	treeGit(t, root, "add", "a.txt")
	treeGit(t, root, "commit", "-q", "-m", "init")

	pad := filepath.Join(tmp, "session", "scratchpad")
	scratch := filepath.Join(pad, "main")
	treeGit(t, root, "worktree", "add", "-q", "--detach", scratch, "main")
	busy := filepath.Join(pad, "busy")
	treeGit(t, root, "worktree", "add", "-q", "--detach", busy, "main")
	gone := filepath.Join(pad, "gone")
	treeGit(t, root, "worktree", "add", "-q", "--detach", gone, "main")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	issue := filepath.Join(root, ".worktrees", "x") // the sweep's, not a leak
	treeGit(t, root, "worktree", "add", "-q", "-b", "work/x", issue, "main")
	agents := filepath.Join(root, ".claude", "worktrees")
	merged := filepath.Join(agents, "agent-merged")
	treeGit(t, root, "worktree", "add", "-q", "-b", "fix/merged", merged, "main")
	treeGit(t, root, "worktree", "lock", merged)
	ahead := filepath.Join(agents, "agent-ahead")
	treeGit(t, root, "worktree", "add", "-q", "-b", "fix/ahead", ahead, "main")
	if err := os.WriteFile(filepath.Join(ahead, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	treeGit(t, ahead, "add", "b.txt")
	treeGit(t, ahead, "commit", "-q", "-m", "work")
	fresh := filepath.Join(agents, "agent-fresh")
	treeGit(t, root, "worktree", "add", "-q", "-b", "fix/fresh", fresh, "main")

	now := time.Now().Add(2 * SubagentTreeIdle)
	recent := now.Add(-time.Hour / 2)
	if err := os.Chtimes(filepath.Join(fresh, "a.txt"), recent, recent); err != nil {
		t.Fatal(err)
	}

	projects := []*tracker.Project{{Slug: "p", WorkDir: issue}} // a worktree: read up to the main checkout
	rep := Find(Options{Now: now, Projects: projects, TmpRoot: t.TempDir(), TracePath: filepath.Join(tmp, "no-trace"), LiveDirs: func() []string { return []string{"zsh " + busy} }})
	got := map[string]Finding{}
	for _, f := range rep.Leaks {
		if f.Kind == KindWorktree {
			got[strings.Fields(f.What)[0]] = f
		}
	}
	for _, p := range []string{busy, issue, ahead, fresh} {
		if f, ok := got[p]; ok {
			t.Errorf("%s listed, want left alone: %+v", p, f)
		}
	}
	for _, c := range []struct{ path, why, stop string }{
		{scratch, "temp or scratchpad folder", "git -C " + shell.Quote(root) + " worktree remove " + shell.Quote(scratch)},
		{gone, "folder is gone", "worktree prune"},
		{merged, "branch fix/merged is on main", "worktree unlock " + shell.Quote(merged) + " && git -C " + shell.Quote(root) + " worktree remove " + shell.Quote(merged)},
	} {
		f, ok := got[c.path]
		if !ok {
			t.Errorf("%s not listed; got %+v", c.path, got)
			continue
		}
		if !strings.Contains(f.Why, c.why) || !strings.Contains(f.StopCommand, c.stop) {
			t.Errorf("%s: why %q stop %q, want %q and %q", c.path, f.Why, f.StopCommand, c.why, c.stop)
		}
	}
	if t.Failed() {
		return
	}

	if err := os.WriteFile(filepath.Join(scratch, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := got[scratch].Stop(); err == nil {
		t.Error("--stop removed a dirty tree")
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Error("the dirty scratchpad tree is gone")
	}
	for _, p := range []string{merged, gone} {
		if err := got[p].Stop(); err != nil {
			t.Errorf("stop %s: %v", p, err)
		}
	}
	list := treeGit(t, root, "worktree", "list")
	if _, err := os.Stat(merged); err == nil || strings.Contains(list, merged) || strings.Contains(list, gone) {
		t.Errorf("after --stop the trees are still there:\n%s", list)
	}
	if treeGit(t, root, "branch", "--list", "fix/merged") == "" {
		t.Error("the subagent tree's branch was deleted; it must stay")
	}
}
