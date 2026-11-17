package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ownTreeProject is a git repository on main whose workflow has no
// worktree: true and a worktree_setup that appends a line to setupLog
// (outside the repository), with cli/sample in progress.
func ownTreeProject(t *testing.T) (proj *tracker.Project, setupLog string) {
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
	for name, body := range map[string]string{".gitignore": "issues/\nworkflow.yaml\n", "app.txt": "app\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", ".gitignore", "app.txt")
	git("commit", "-q", "-m", "init")
	// A side branch checked out in the primary checkout: the tree must be
	// cut from main, not from whatever the checkout has.
	git("checkout", "-q", "-b", "side")
	if err := os.WriteFile(filepath.Join(dir, "side.txt"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "side.txt")
	git("commit", "-q", "-m", "side")

	setupLog = filepath.Join(realTempDir(t), "setup.log")
	if err := os.MkdirAll(filepath.Join(dir, "issues", "CLI"), 0o755); err != nil {
		t.Fatal(err)
	}
	wf := "worktree_setup: \"pwd >> '" + setupLog + "'\"\nstatuses:\n  - name: \"in progress\"\n  - name: \"done\"\ntransitions:\n  - from: \"in progress\"\n    to: \"done\"\n"
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	issue := "---\ntitle: \"sample\"\nstatus: \"in progress\"\nsystem: \"CLI\"\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "issues", "CLI", "sample.md"), []byte(issue), 0o644); err != nil {
		t.Fatal(err)
	}
	proj = &tracker.Project{Name: "test", Slug: "test", IssueDir: filepath.Join(dir, "issues"), WorkflowFile: filepath.Join(dir, "workflow.yaml"), WorkDir: dir}
	if proj.LoadWorkflow().WorktreeEnabled() {
		t.Fatal("fixture: worktree: true is on")
	}
	return proj, setupLog
}

func setupRuns(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

func TestWorktreeAddRunsSetup(t *testing.T) {
	proj, log := ownTreeProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if c := lookupCommand("worktree"); c == nil || c.Name != "worktrees" {
		t.Fatalf("worktree is not an alias of worktrees: %v", c)
	}
	if err := runWorktrees(ctx, []string{"add", "cli/sample"}); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, stdout)
	}
	tree := filepath.Join(proj.WorkDir, ".worktrees", "cli", "sample")
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if last := lines[len(lines)-1]; last != tree {
		t.Errorf("last line %q, want the path %s:\n%s", last, tree, stdout)
	}
	assertContains(t, stdout.String(), "from main")
	branch, err := exec.Command("git", "-C", tree, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(branch)) != "work/cli/sample" {
		t.Fatalf("worktree branch %q (%v), want work/cli/sample", branch, err)
	}
	if _, err := os.Stat(filepath.Join(tree, "app.txt")); err != nil {
		t.Error("the tree is not checked out: app.txt missing")
	}
	if _, err := os.Stat(filepath.Join(tree, "side.txt")); err == nil {
		t.Error("the tree was cut from the checkout's side branch, not main")
	}
	runs := setupRuns(t, log)
	if len(runs) != 1 {
		t.Fatalf("setup ran %d times, want once: %v", len(runs), runs)
	}
	if got, _ := filepath.EvalSymlinks(runs[0]); got != tree {
		t.Errorf("setup ran in %s, want the new tree %s", runs[0], tree)
	}
}

func TestWorktreeAddIsIdempotent(t *testing.T) {
	proj, log := ownTreeProject(t)
	t.Setenv("XDG_STATE_HOME", realTempDir(t))
	t.Setenv(tracker.OwnSlugEnv, "cli/sample")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorktrees(ctx, []string{"add"}); err != nil {
		t.Fatalf("first worktree add: %v\n%s", err, stdout)
	}
	tree := filepath.Join(proj.WorkDir, ".worktrees", "cli", "sample")
	head := func() string {
		out, err := exec.Command("git", "-C", tree, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	before := head()
	if err := os.WriteFile(filepath.Join(tree, "work.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx2, stdout2, _ := newTestContext(proj, false)
	if err := runWorktrees(ctx2, []string{"add"}); err != nil {
		t.Fatalf("second worktree add: %v\n%s", err, stdout2)
	}
	out := stdout2.String()
	assertContains(t, out, "worktree exists: "+tree)
	if lines := strings.Split(strings.TrimSpace(out), "\n"); lines[len(lines)-1] != tree {
		t.Errorf("second call's last line is not the path:\n%s", out)
	}
	if runs := setupRuns(t, log); len(runs) != 1 {
		t.Errorf("setup ran %d times over two calls, want once", len(runs))
	}
	if head() != before {
		t.Error("the second call moved the tree's HEAD")
	}
	if data, err := os.ReadFile(filepath.Join(tree, "work.txt")); err != nil || string(data) != "mine\n" {
		t.Errorf("the second call touched the worker's file: %q %v", data, err)
	}
}

// A branch work/<slug> that exists with no folder (removed after a ship, or
// by hand) is checked out as is, and setup runs on the new folder.
func TestWorktreeAddReusesAnExistingBranch(t *testing.T) {
	proj, log := ownTreeProject(t)
	if out, err := exec.Command("git", "-C", proj.WorkDir, "branch", "work/cli/sample", "side").CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v %s", err, out)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorktrees(ctx, []string{"add", "cli/sample"}); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, stdout)
	}
	tree := filepath.Join(proj.WorkDir, ".worktrees", "cli", "sample")
	if _, err := os.Stat(filepath.Join(tree, "side.txt")); err != nil {
		t.Error("the existing branch was not checked out as is")
	}
	if runs := setupRuns(t, log); len(runs) != 1 {
		t.Errorf("setup ran %d times, want once", len(runs))
	}
}

func TestWorktreeAddUsage(t *testing.T) {
	proj, _ := ownTreeProject(t)
	t.Setenv(tracker.OwnSlugEnv, "")
	ctx, _, _ := newTestContext(proj, false)
	err := runWorktrees(ctx, []string{"add"})
	if err == nil || !strings.Contains(err.Error(), "usage: issue-cli worktree add [<slug>]") {
		t.Errorf("no slug, no env: %v", err)
	}
	if err := runWorktrees(ctx, []string{"add", "cli/nope"}); err == nil {
		t.Error("an unknown slug made a worktree")
	}
}
