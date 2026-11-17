package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// shippedTreeProject is a git repository whose workflow runs issues in
// worktrees, with cli/sample at status and its worktree .worktrees/cli/sample
// on work/cli/sample, cut from main (so its branch is on main).
func shippedTreeProject(t *testing.T, status string) (*tracker.Project, string) {
	t.Helper()
	dir := realTempDir(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("issues/\nworkflow.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".gitignore")
	git("commit", "-q", "-m", "init")
	if err := os.MkdirAll(filepath.Join(dir, "issues", "CLI"), 0o755); err != nil {
		t.Fatal(err)
	}
	wf := "worktree: true\nstatuses:\n  - name: \"shipping\"\n  - name: \"done\"\ntransitions:\n  - from: \"shipping\"\n    to: \"done\"\n"
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	issue := "---\ntitle: \"sample\"\nstatus: \"" + status + "\"\nsystem: \"CLI\"\n---\n\n- [x] shipped\n"
	if err := os.WriteFile(filepath.Join(dir, "issues", "CLI", "sample.md"), []byte(issue), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(dir, ".worktrees", "cli", "sample")
	git("worktree", "add", "-q", "-b", "work/cli/sample", tree, "main")
	proj := &tracker.Project{Name: "test", Slug: "test", IssueDir: filepath.Join(dir, "issues"), WorkflowFile: filepath.Join(dir, "workflow.yaml"), WorkDir: dir}
	return proj, tree
}

func noLiveDirs(t *testing.T) {
	t.Helper()
	old := agentrun.LiveDirs
	agentrun.LiveDirs = func() []string { return nil }
	t.Cleanup(func() { agentrun.LiveDirs = old })
}

// The repro: on main the worktree stayed on disk after the done transition.
func TestIssueWorktreeRemovedAtDone(t *testing.T) {
	noLiveDirs(t)
	proj, tree := shippedTreeProject(t, "shipping")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatalf("runTransition: %v\n%s", err, stdout)
	}
	if _, err := os.Stat(tree); err == nil {
		t.Fatalf("worktree %s still on disk after done:\n%s", tree, stdout)
	}
	assertContains(t, stdout.String(), "Worktree: removed "+tree)
	out, _ := exec.Command("git", "-C", proj.WorkDir, "branch", "--list", "work/cli/sample").Output()
	if !strings.Contains(string(out), "work/cli/sample") {
		t.Error("the branch went with the worktree; it must stay")
	}
}

func TestIssueWorktreeKeptAtDoneWhenDirtyOrLive(t *testing.T) {
	noLiveDirs(t)
	proj, tree := shippedTreeProject(t, "shipping")
	if err := os.WriteFile(filepath.Join(tree, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatalf("runTransition: %v", err)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatal("a dirty worktree was removed")
	}
	assertContains(t, stdout.String(), "Worktree: kept "+tree+": dirty")

	os.Remove(filepath.Join(tree, "notes.txt"))
	agentrun.LiveDirs = func() []string { return []string{tree} }
	all, _ := tracker.LoadIssues(proj.IssueDir)
	issue := tracker.FindIssue(all, "cli/sample")
	var buf strings.Builder
	removeShippedWorktree(ctx, &buf, issue)
	if !strings.Contains(buf.String(), "kept "+tree+": live") {
		t.Fatalf("live tree: %q", buf.String())
	}
}

// The worker's own pane was in the tree at its done: the lead's next tick
// removes it, and its daily sweep runs once a day.
func TestLeadTickRemovesADoneIssuesWorktree(t *testing.T) {
	noLiveDirs(t)
	t.Setenv("XDG_STATE_HOME", realTempDir(t))
	proj, tree := shippedTreeProject(t, "done")
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.Local) }
	if _, err := leadTickOnce(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tree); err == nil {
		t.Fatalf("the done issue's worktree is still there:\n%s", stdout)
	}
	st := loadLeadTickState(leadTickPath(proj))
	if st.PrunedOn != "2026-11-15" {
		t.Fatalf("daily sweep not recorded: %+v", st)
	}
}

func TestLeadTickSaysTheDailySweep(t *testing.T) {
	noLiveDirs(t)
	t.Setenv("XDG_STATE_HOME", realTempDir(t))
	proj, tree := shippedTreeProject(t, "shipping") // open: only the sweep can take it
	stamp := time.Date(2026, 11, 14, 0, 0, 0, 0, time.Local)
	entries, _ := os.ReadDir(tree)
	for _, e := range entries {
		_ = os.Chtimes(filepath.Join(tree, e.Name()), stamp, stamp)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.Local) }
	// HEAD of the tree's git dir is fresh: age it too.
	gd := filepath.Join(proj.WorkDir, ".git", "worktrees")
	ds, _ := os.ReadDir(gd)
	for _, d := range ds {
		_ = os.Chtimes(filepath.Join(gd, d.Name(), "HEAD"), stamp, stamp)
	}
	if _, err := leadTickOnce(ctx, false); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "worktrees: removed 1 worktree merged, clean and idle")
	if _, err := os.Stat(tree); err == nil {
		t.Fatal("the sweep left the merged idle tree")
	}

	// Same day: no second sweep, nothing said.
	_, tree2 := addSecondTree(t, proj.WorkDir)
	stdout.Reset()
	if _, err := leadTickOnce(ctx, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "worktrees:") {
		t.Fatalf("swept twice in a day:\n%s", stdout)
	}
	if _, err := os.Stat(tree2); err != nil {
		t.Fatal("a second sweep the same day removed a tree")
	}
}

func addSecondTree(t *testing.T, root string) (string, string) {
	t.Helper()
	p := filepath.Join(root, ".worktrees", "other")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-q", "-b", "work/other", p, "main").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return "work/other", p
}

func TestWorktreesPruneDryRunListsAndKeeps(t *testing.T) {
	noLiveDirs(t)
	proj, tree := shippedTreeProject(t, "shipping")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorktrees(ctx, []string{"prune", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "kept .worktrees/cli/sample: recent")
	assertContains(t, stdout.String(), "Would remove 0 worktrees; kept 1 recent.")
	if _, err := os.Stat(tree); err != nil {
		t.Fatal("--dry-run removed a tree")
	}
}

// The critic reads a removed worktree's branch from a tree made again, not
// from the main checkout; --brief (read-only) makes none.
func TestCritiqueRecreatesARemovedWorktree(t *testing.T) {
	proj, tree := shippedTreeProject(t, "shipping")
	if err := agentrun.RemoveTree(proj.WorkDir, tree); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ := newTestContext(proj, false)
	all, _ := tracker.LoadIssues(proj.IssueDir)
	issue := tracker.FindIssue(all, "cli/sample")
	wf := proj.LoadWorkflowForIssue(issue)
	if got := critiqueDir(ctx, proj.WorkDir, issue, wf, false); got != proj.WorkDir {
		t.Fatalf("--brief made a tree: %s", got)
	}
	if got := critiqueDir(ctx, proj.WorkDir, issue, wf, true); got != tree || !dirExists(tree) {
		t.Fatalf("critiqueDir = %s, want the recreated %s", got, tree)
	}
}

// treeGit runs git in dir for a test, failing it on an error.
func treeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// commitInTree commits one new file in tree: the
// branch checked out there gets one commit main doesn't have.
func commitInTree(t *testing.T, tree, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(tree, name), []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	treeGit(t, tree, "add", name)
	treeGit(t, tree, "commit", "-q", "-m", "feat: "+name)
}

func leadTickAt(t *testing.T, proj *tracker.Project) string {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.Local) }
	if _, err := leadTickOnce(ctx, false); err != nil {
		t.Fatal(err)
	}
	return stdout.String()
}

// A parked issue (its branch has no commits of its own) and a side exit
// after done (its branch merged into main) lose their worktree on the
// tick, as a done one does; the branch stays.
func TestLeadTickRemovesParkedIssuesWorktree(t *testing.T) {
	t.Run("parked, empty branch", func(t *testing.T) {
		noLiveDirs(t)
		t.Setenv("XDG_STATE_HOME", realTempDir(t))
		proj, tree := shippedTreeProject(t, "parked")
		out := leadTickAt(t, proj)
		if _, err := os.Stat(tree); err == nil {
			t.Fatalf("the parked issue's worktree is still there:\n%s", out)
		}
		branches, _ := exec.Command("git", "-C", proj.WorkDir, "branch", "--list", "work/cli/sample").Output()
		if !strings.Contains(string(branches), "work/cli/sample") {
			t.Error("the branch went with the worktree; it must stay")
		}
	})
	t.Run("side exit, merged branch", func(t *testing.T) {
		noLiveDirs(t)
		t.Setenv("XDG_STATE_HOME", realTempDir(t))
		proj, tree := shippedTreeProject(t, "obsolete")
		wf := "worktree: true\nstatuses:\n  - name: \"shipping\"\n  - name: \"done\"\n  - name: \"obsolete\"\ntransitions:\n  - from: \"shipping\"\n    to: \"done\"\n"
		if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
			t.Fatal(err)
		}
		commitInTree(t, tree, "work.txt")
		treeGit(t, proj.WorkDir, "merge", "-q", "--no-ff", "-m", "chore(merge): sample", "work/cli/sample")
		out := leadTickAt(t, proj)
		if _, err := os.Stat(tree); err == nil {
			t.Fatalf("the side exit's merged worktree is still there:\n%s", out)
		}
	})
}

// Parked with work not on main: the tree stays and nothing asks to land it.
func TestLeadTickKeepsParkedIssuesUnmergedWorktree(t *testing.T) {
	noLiveDirs(t)
	t.Setenv("XDG_STATE_HOME", realTempDir(t))
	proj, tree := shippedTreeProject(t, "parked")
	commitInTree(t, tree, "work.txt")
	out := leadTickAt(t, proj)
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("a parked issue's unmerged worktree was removed:\n%s", out)
	}
	assertNotContains(t, out, "land:")

	// Dirty: kept, and said once.
	treeGit(t, proj.WorkDir, "merge", "-q", "--no-ff", "-m", "chore(merge): sample", "work/cli/sample")
	if err := os.WriteFile(filepath.Join(tree, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = leadTickAt(t, proj)
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("a parked issue's dirty worktree was removed:\n%s", out)
	}
	assertContains(t, out, "worktree: cli/sample is parked but "+tree+" stays")
}

// A done issue whose work is on the branch its branch: field names is named
// for landing by that branch; a parked one's unmerged branch is not.
func TestLeadTickNamesCustomBranch(t *testing.T) {
	noLiveDirs(t)
	t.Setenv("XDG_STATE_HOME", realTempDir(t))
	proj, tree := shippedTreeProject(t, "done")
	file := filepath.Join(proj.IssueDir, "CLI", "sample.md")
	issue := "---\ntitle: \"sample\"\nstatus: \"done\"\nsystem: \"CLI\"\nbranch: \"feature/custom\"\n---\n\n- [x] shipped\n"
	if err := os.WriteFile(file, []byte(issue), 0o644); err != nil {
		t.Fatal(err)
	}
	treeGit(t, tree, "checkout", "-q", "-b", "feature/custom")
	commitInTree(t, tree, "work.txt")

	parked := "---\ntitle: \"other\"\nstatus: \"parked\"\nsystem: \"CLI\"\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "CLI", "other.md"), []byte(parked), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(proj.WorkDir, ".worktrees", "cli", "other")
	treeGit(t, proj.WorkDir, "worktree", "add", "-q", "-b", "work/cli/other", other, "main")
	commitInTree(t, other, "other.txt")

	out := leadTickAt(t, proj)
	assertContains(t, out, "land: cli/sample is done but feature/custom has 1 commit(s) not on main")
	assertNotContains(t, out, "cli/other")
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("the unlanded custom branch's worktree was removed:\n%s", out)
	}
}
