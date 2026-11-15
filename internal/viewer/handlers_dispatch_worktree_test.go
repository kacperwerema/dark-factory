package viewer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestResolveWorktree_OptInEnablesCreation(t *testing.T) {
	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true)}
	path, branch, enabled := resolveWorktree("/work/proj", "fix-foo", wf)
	if !enabled {
		t.Fatalf("expected enabled=true when worktree:true")
	}
	if path != filepath.Join("/work/proj", ".worktrees", "fix-foo") {
		t.Fatalf("unexpected worktree path: %s", path)
	}
	if branch != "work/fix-foo" {
		t.Fatalf("unexpected branch: %s", branch)
	}
}

func TestResolveWorktree_DefaultsDisabled(t *testing.T) {
	// Worktree field absent → opt-in default is off so projects that haven't
	// reviewed the feature keep their existing dispatch behaviour.
	wf := &tracker.WorkflowConfig{}
	_, _, enabled := resolveWorktree("/work/proj", "fix-foo", wf)
	if enabled {
		t.Fatalf("expected enabled=false by default")
	}
}

func TestResolveWorktree_ExplicitDisableSkipsCreation(t *testing.T) {
	wf := &tracker.WorkflowConfig{Worktree: boolPtr(false)}
	_, _, enabled := resolveWorktree("/work/proj", "fix-foo", wf)
	if enabled {
		t.Fatalf("expected enabled=false when worktree:false")
	}
}

func TestResolveWorktree_NoSlugSkipsCreation(t *testing.T) {
	// Retros review and other dispatches without an issue slug must not
	// trigger worktree creation — there's no per-issue branch to make.
	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true)}
	_, _, enabled := resolveWorktree("/work/proj", "", wf)
	if enabled {
		t.Fatalf("expected enabled=false when slug is empty")
	}
}

func TestResolveWorktree_NilWorkflowSkipsCreation(t *testing.T) {
	// nil wf is the bootstrap fallback path; without a workflow we never
	// create a worktree.
	_, _, enabled := resolveWorktree("/work/proj", "fix-foo", nil)
	if enabled {
		t.Fatalf("expected enabled=false when wf is nil")
	}
}

func TestEnsureWorktree_DisabledReturnsOriginalDir(t *testing.T) {
	dir := "/work/proj"
	got, steps, ok := ensureWorktree(dir, "fix-foo", &tracker.WorkflowConfig{Worktree: boolPtr(false)})
	if !ok {
		t.Fatalf("expected ok=true when disabled")
	}
	if got != dir {
		t.Fatalf("expected workdir unchanged, got %s", got)
	}
	if len(steps) != 0 {
		t.Fatalf("expected no dispatch steps when disabled, got %+v", steps)
	}
}

func TestEnsureWorktree_EnabledStubsGitAndReportsSuccess(t *testing.T) {
	tmp := t.TempDir()
	var gotWorkdir, gotBranch, gotPath string
	orig := agentrun.RunGitWorktreeAdd
	agentrun.RunGitWorktreeAdd = func(workdir, branch, wtPath, _ string) ([]byte, error) {
		gotWorkdir, gotBranch, gotPath = workdir, branch, wtPath
		return nil, nil
	}
	t.Cleanup(func() { agentrun.RunGitWorktreeAdd = orig })
	stubSparseCheckoutOK(t)

	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true)}
	got, steps, ok := ensureWorktree(tmp, "fix-foo", wf)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	want := filepath.Join(tmp, ".worktrees", "fix-foo")
	if got != want {
		t.Fatalf("expected workdir %s, got %s", want, got)
	}
	if gotWorkdir != tmp || gotBranch != "work/fix-foo" || gotPath != want {
		t.Fatalf("unexpected git args: workdir=%s branch=%s path=%s", gotWorkdir, gotBranch, gotPath)
	}
	if len(steps) != 2 || steps[0].Status != "ok" || steps[1].Status != "ok" {
		t.Fatalf("expected create + sparse-checkout ok steps, got %+v", steps)
	}
	if !strings.Contains(steps[1].Name, "Sparse-checkout") {
		t.Fatalf("expected sparse-checkout step second, got %+v", steps[1])
	}
}

func TestEnsureWorktree_GitFailureSurfacesErrorAndAborts(t *testing.T) {
	tmp := t.TempDir()
	orig := agentrun.RunGitWorktreeAdd
	agentrun.RunGitWorktreeAdd = func(workdir, branch, wtPath, _ string) ([]byte, error) {
		return []byte("fatal: dirty working tree\n"), errStub{}
	}
	t.Cleanup(func() { agentrun.RunGitWorktreeAdd = orig })

	got, steps, ok := ensureWorktree(tmp, "fix-foo", &tracker.WorkflowConfig{Worktree: boolPtr(true)})
	if ok {
		t.Fatalf("expected ok=false on git failure")
	}
	if got != tmp {
		t.Fatalf("expected workdir to fall back to %s, got %s", tmp, got)
	}
	if len(steps) != 1 || steps[0].Status != "error" {
		t.Fatalf("expected single error step, got %+v", steps)
	}
	if !strings.Contains(steps[0].Detail, "dirty working tree") {
		t.Fatalf("expected git stderr in step detail, got %q", steps[0].Detail)
	}
}

func TestEnsureWorktree_ReusesExistingDir(t *testing.T) {
	tmp := t.TempDir()
	// Pre-create the worktree directory so ensureWorktree treats this as a
	// re-dispatch and skips git instead of erroring.
	wt := filepath.Join(tmp, ".worktrees", "fix-foo")
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}

	called := false
	orig := agentrun.RunGitWorktreeAdd
	agentrun.RunGitWorktreeAdd = func(string, string, string, string) ([]byte, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() { agentrun.RunGitWorktreeAdd = orig })

	got, steps, ok := ensureWorktree(tmp, "fix-foo", &tracker.WorkflowConfig{Worktree: boolPtr(true)})
	if !ok {
		t.Fatalf("expected ok=true on re-dispatch")
	}
	if called {
		t.Fatalf("expected git not to run when worktree dir already exists")
	}
	if got != wt {
		t.Fatalf("expected workdir %s, got %s", wt, got)
	}
	if len(steps) != 1 || !strings.Contains(steps[0].Name, "Reuse worktree") {
		t.Fatalf("expected single reuse step, got %+v", steps)
	}
}

func TestEnsureWorktree_RunsSetupAfterCreate(t *testing.T) {
	tmp := t.TempDir()
	origGit := agentrun.RunGitWorktreeAdd
	agentrun.RunGitWorktreeAdd = func(string, string, string, string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { agentrun.RunGitWorktreeAdd = origGit })
	stubSparseCheckoutOK(t)

	var gotPath, gotCmd string
	origSetup := agentrun.RunWorktreeSetup
	agentrun.RunWorktreeSetup = func(wtPath, cmd string) ([]byte, error) {
		gotPath, gotCmd = wtPath, cmd
		return []byte("built\n"), nil
	}
	t.Cleanup(func() { agentrun.RunWorktreeSetup = origSetup })

	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true), WorktreeSetup: "make"}
	got, steps, ok := ensureWorktree(tmp, "fix-foo", wf)
	if !ok {
		t.Fatalf("expected ok=true on successful setup")
	}
	wantWT := filepath.Join(tmp, ".worktrees", "fix-foo")
	if got != wantWT {
		t.Fatalf("expected workdir %s, got %s", wantWT, got)
	}
	if gotPath != wantWT || gotCmd != "make" {
		t.Fatalf("setup invoked with wtPath=%s cmd=%s, want %s + make", gotPath, gotCmd, wantWT)
	}
	if len(steps) != 3 {
		t.Fatalf("expected create + sparse-checkout + setup steps, got %+v", steps)
	}
	if !strings.Contains(steps[2].Name, "Worktree setup: make") || steps[2].Status != "ok" {
		t.Fatalf("expected ok setup step last, got %+v", steps[2])
	}
}

func TestEnsureWorktree_SetupFailureAborts(t *testing.T) {
	tmp := t.TempDir()
	origGit := agentrun.RunGitWorktreeAdd
	agentrun.RunGitWorktreeAdd = func(string, string, string, string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { agentrun.RunGitWorktreeAdd = origGit })
	stubSparseCheckoutOK(t)

	origSetup := agentrun.RunWorktreeSetup
	agentrun.RunWorktreeSetup = func(string, string) ([]byte, error) {
		return []byte("missing target\n"), errStub{}
	}
	t.Cleanup(func() { agentrun.RunWorktreeSetup = origSetup })

	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true), WorktreeSetup: "make"}
	got, steps, ok := ensureWorktree(tmp, "fix-foo", wf)
	if ok {
		t.Fatalf("expected ok=false when setup fails")
	}
	if got != tmp {
		t.Fatalf("expected workdir to fall back to %s on setup failure, got %s", tmp, got)
	}
	if len(steps) != 3 {
		t.Fatalf("expected create + sparse-checkout + setup steps, got %+v", steps)
	}
	last := steps[len(steps)-1]
	if last.Status != "error" || !strings.Contains(last.Detail, "missing target") {
		t.Fatalf("expected error setup step with stderr last, got %+v", last)
	}
}

// stubSparseCheckoutOK stubs both sparse-checkout and checkout-HEAD seams to
// succeed. Tests that don't care about the sparse-checkout details use it so
// the create path completes without invoking real git.
func stubSparseCheckoutOK(t *testing.T) {
	t.Helper()
	origSparse := agentrun.RunGitSparseCheckout
	agentrun.RunGitSparseCheckout = func(string, []string) ([]byte, error) { return nil, nil }
	origCheckout := agentrun.RunGitCheckoutHead
	agentrun.RunGitCheckoutHead = func(string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() {
		agentrun.RunGitSparseCheckout = origSparse
		agentrun.RunGitCheckoutHead = origCheckout
	})
}

func TestEnsureWorktree_SparseCheckoutUsesDefaultExclude(t *testing.T) {
	tmp := t.TempDir()
	orig := agentrun.RunGitWorktreeAdd
	agentrun.RunGitWorktreeAdd = func(string, string, string, string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { agentrun.RunGitWorktreeAdd = orig })

	var gotPath string
	var gotExcludes []string
	origSparse := agentrun.RunGitSparseCheckout
	agentrun.RunGitSparseCheckout = func(wtPath string, excludes []string) ([]byte, error) {
		gotPath, gotExcludes = wtPath, excludes
		return nil, nil
	}
	t.Cleanup(func() { agentrun.RunGitSparseCheckout = origSparse })

	checkoutCalled := false
	origCheckout := agentrun.RunGitCheckoutHead
	agentrun.RunGitCheckoutHead = func(string) ([]byte, error) {
		checkoutCalled = true
		return nil, nil
	}
	t.Cleanup(func() { agentrun.RunGitCheckoutHead = origCheckout })

	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true)}
	_, steps, ok := ensureWorktree(tmp, "fix-foo", wf)
	if !ok {
		t.Fatalf("expected ok=true; steps=%+v", steps)
	}
	wantWT := filepath.Join(tmp, ".worktrees", "fix-foo")
	if gotPath != wantWT {
		t.Fatalf("sparse-checkout invoked with %s, want %s", gotPath, wantWT)
	}
	if len(gotExcludes) != 1 || gotExcludes[0] != "issues/" {
		t.Fatalf("expected default exclude [issues/], got %v", gotExcludes)
	}
	// `git worktree add --no-checkout` leaves the tree empty even after
	// sparse-checkout writes its pattern file, so checkout HEAD must always run
	// to materialize the included files. Asserting it does NOT run encoded the
	// original empty-tree bug.
	if !checkoutCalled {
		t.Fatalf("checkout HEAD must run after sparse-checkout to materialize the tree")
	}
}

// TestEnsureWorktree_RealGitMaterializesTree drives ensureWorktree against real
// git (no stubbed seams). It reproduces the original bug — `git worktree add
// --no-checkout` followed only by `sparse-checkout set` left the worktree empty
// with every tracked file staged-for-deletion — and asserts the tree is now
// actually materialized: included files exist on disk, sparse excludes are
// honored, and `git status --porcelain` shows no staged deletions. It fails
// against the pre-fix code.
func TestEnsureWorktree_RealGitMaterializesTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Build a real repo: a tracked top-level file plus a tracked issues/ file,
	// so we can verify the default sparse exclude keeps issues/ out while
	// everything else lands on disk.
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "issues"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "issues", "1.md"), []byte("issue\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "init")

	// Default config: worktree enabled, default sparse exclude [issues/].
	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true)}
	gotDir, steps, ok := ensureWorktree(repo, "fix-foo", wf)
	if !ok {
		t.Fatalf("ensureWorktree failed: %+v", steps)
	}
	wantWT := filepath.Join(repo, ".worktrees", "fix-foo")
	if gotDir != wantWT {
		t.Fatalf("workdir = %s, want %s", gotDir, wantWT)
	}

	// Included files must exist on disk — the bug left the tree empty.
	if _, err := os.Stat(filepath.Join(wantWT, "README.md")); err != nil {
		t.Fatalf("README.md not materialized in worktree: %v", err)
	}
	// issues/ must be excluded by the sparse pattern.
	if _, err := os.Stat(filepath.Join(wantWT, "issues")); !os.IsNotExist(err) {
		t.Fatalf("issues/ should be excluded by sparse-checkout, stat err = %v", err)
	}
	// git status --porcelain must be clean — no staged deletions.
	out, err := exec.Command("git", "-C", wantWT, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("worktree not clean — staged deletions present:\n%s", out)
	}
}

func TestEnsureWorktree_SparseCheckoutHonorsCustomList(t *testing.T) {
	tmp := t.TempDir()
	orig := agentrun.RunGitWorktreeAdd
	agentrun.RunGitWorktreeAdd = func(string, string, string, string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { agentrun.RunGitWorktreeAdd = orig })

	var gotExcludes []string
	origSparse := agentrun.RunGitSparseCheckout
	agentrun.RunGitSparseCheckout = func(_ string, excludes []string) ([]byte, error) {
		gotExcludes = excludes
		return nil, nil
	}
	t.Cleanup(func() { agentrun.RunGitSparseCheckout = origSparse })

	origCheckout := agentrun.RunGitCheckoutHead
	agentrun.RunGitCheckoutHead = func(string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { agentrun.RunGitCheckoutHead = origCheckout })

	custom := []string{"issues/", "docs/", ".images/"}
	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true), WorktreeSparseExclude: &custom}
	_, _, ok := ensureWorktree(tmp, "fix-foo", wf)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if len(gotExcludes) != 3 || gotExcludes[0] != "issues/" || gotExcludes[1] != "docs/" || gotExcludes[2] != ".images/" {
		t.Fatalf("expected configured excludes passed through, got %v", gotExcludes)
	}
}

func TestEnsureWorktree_EmptyExcludeFallsBackToPlainCheckout(t *testing.T) {
	tmp := t.TempDir()
	orig := agentrun.RunGitWorktreeAdd
	agentrun.RunGitWorktreeAdd = func(string, string, string, string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { agentrun.RunGitWorktreeAdd = orig })

	sparseCalled := false
	origSparse := agentrun.RunGitSparseCheckout
	agentrun.RunGitSparseCheckout = func(string, []string) ([]byte, error) {
		sparseCalled = true
		return nil, nil
	}
	t.Cleanup(func() { agentrun.RunGitSparseCheckout = origSparse })

	var gotCheckoutPath string
	origCheckout := agentrun.RunGitCheckoutHead
	agentrun.RunGitCheckoutHead = func(wtPath string) ([]byte, error) {
		gotCheckoutPath = wtPath
		return nil, nil
	}
	t.Cleanup(func() { agentrun.RunGitCheckoutHead = origCheckout })

	empty := []string{}
	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true), WorktreeSparseExclude: &empty}
	_, _, ok := ensureWorktree(tmp, "fix-foo", wf)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if sparseCalled {
		t.Fatalf("sparse-checkout must not run when excludes are empty")
	}
	wantWT := filepath.Join(tmp, ".worktrees", "fix-foo")
	if gotCheckoutPath != wantWT {
		t.Fatalf("checkout-HEAD invoked with %s, want %s", gotCheckoutPath, wantWT)
	}
}

func TestEnsureWorktree_SparseCheckoutFailureAborts(t *testing.T) {
	tmp := t.TempDir()
	orig := agentrun.RunGitWorktreeAdd
	agentrun.RunGitWorktreeAdd = func(string, string, string, string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { agentrun.RunGitWorktreeAdd = orig })

	origSparse := agentrun.RunGitSparseCheckout
	agentrun.RunGitSparseCheckout = func(string, []string) ([]byte, error) {
		return []byte("bad pattern\n"), errStub{}
	}
	t.Cleanup(func() { agentrun.RunGitSparseCheckout = origSparse })

	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true)}
	got, steps, ok := ensureWorktree(tmp, "fix-foo", wf)
	if ok {
		t.Fatalf("expected ok=false when sparse-checkout fails")
	}
	if got != tmp {
		t.Fatalf("expected workdir to fall back to %s, got %s", tmp, got)
	}
	if len(steps) != 2 {
		t.Fatalf("expected create + sparse-checkout error steps, got %+v", steps)
	}
	if steps[1].Status != "error" || !strings.Contains(steps[1].Detail, "bad pattern") {
		t.Fatalf("expected error sparse-checkout step with stderr, got %+v", steps[1])
	}
}

func TestEnsureWorktree_SetupSkippedOnReuse(t *testing.T) {
	tmp := t.TempDir()
	wt := filepath.Join(tmp, ".worktrees", "fix-foo")
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}

	called := false
	origSetup := agentrun.RunWorktreeSetup
	agentrun.RunWorktreeSetup = func(string, string) ([]byte, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() { agentrun.RunWorktreeSetup = origSetup })

	wf := &tracker.WorkflowConfig{Worktree: boolPtr(true), WorktreeSetup: "make"}
	_, steps, ok := ensureWorktree(tmp, "fix-foo", wf)
	if !ok {
		t.Fatalf("expected ok=true on reuse")
	}
	if called {
		t.Fatalf("expected setup not to run on reuse")
	}
	if len(steps) != 1 || !strings.Contains(steps[0].Name, "Reuse worktree") {
		t.Fatalf("expected single reuse step, got %+v", steps)
	}
}
