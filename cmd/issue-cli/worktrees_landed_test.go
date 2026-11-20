package main

import (
	"os"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// noWorktreeWorkflow drops worktree: true from the fixture: the tree is
// one the worker made with issue-cli worktree add.
func noWorktreeWorkflow(t *testing.T, proj *tracker.Project) {
	t.Helper()
	wf := "statuses:\n  - name: \"shipping\"\n  - name: \"done\"\ntransitions:\n  - from: \"shipping\"\n    to: \"done\"\n"
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	if proj.LoadWorkflow().WorktreeEnabled() {
		t.Fatal("fixture: worktree: true still on")
	}
}

func stubIssueSession(t *testing.T, live bool) {
	t.Helper()
	old := issueSessionLive
	issueSessionLive = func(*tracker.Project, *tracker.Issue) bool { return live }
	t.Cleanup(func() { issueSessionLive = old })
}

// TestLeadTickRemovesAWorkerMadeTree: in a project without worktree: true
// the tree worktree add made goes at done, as a dispatched one does.
func TestLeadTickRemovesAWorkerMadeTree(t *testing.T) {
	noLiveDirs(t)
	stubIssueSession(t, false)
	t.Setenv("XDG_STATE_HOME", realTempDir(t))
	proj, tree := shippedTreeProject(t, "done")
	noWorktreeWorkflow(t, proj)
	out := leadTickAt(t, proj)
	if _, err := os.Stat(tree); err == nil {
		t.Fatalf("the worker-made tree of a done issue is still there:\n%s", out)
	}
}

// TestLeadTickRemovesALandedOpenIssuesTree: an issue still open whose
// branch landed on main goes at the next tick when no session works on it;
// a live session, unmerged work, or a branch with no commits keeps it.
func TestLeadTickRemovesALandedOpenIssuesTree(t *testing.T) {
	t.Run("fresh branch, no commits", func(t *testing.T) {
		noLiveDirs(t)
		stubIssueSession(t, false)
		t.Setenv("XDG_STATE_HOME", realTempDir(t))
		proj, tree := shippedTreeProject(t, "shipping")
		noWorktreeWorkflow(t, proj)
		leadTickAt(t, proj)
		if _, err := os.Stat(tree); err != nil {
			t.Fatal("a tree whose branch has no commits yet was removed")
		}
	})
	t.Run("unmerged, then landed", func(t *testing.T) {
		noLiveDirs(t)
		t.Setenv("XDG_STATE_HOME", realTempDir(t))
		proj, tree := shippedTreeProject(t, "shipping")
		noWorktreeWorkflow(t, proj)
		commitInTree(t, tree, "work.txt")
		stubIssueSession(t, false)
		leadTickAt(t, proj)
		if _, err := os.Stat(tree); err != nil {
			t.Fatal("an unmerged tree was removed")
		}
		treeGit(t, proj.WorkDir, "merge", "-q", "--ff-only", "work/cli/sample")
		stubIssueSession(t, true)
		leadTickAt(t, proj)
		if _, err := os.Stat(tree); err != nil {
			t.Fatal("a tree whose session is live was removed")
		}
		stubIssueSession(t, false)
		out := leadTickAt(t, proj)
		if _, err := os.Stat(tree); err == nil {
			t.Fatalf("the landed open issue's tree is still there:\n%s", out)
		}
	})
}
