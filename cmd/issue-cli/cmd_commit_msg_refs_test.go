package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// runHookOn runs the commit-msg check on msg and returns the message file's
// content afterwards and what the hook printed.
func runHookOn(t *testing.T, proj *tracker.Project, msg string, wantRefused bool) (after, out string) {
	t.Helper()
	ctx, _, stderr := newTestContext(proj, false)
	file := commitMsgFile(t, msg)
	err := runCommitMsg(ctx, []string{file})
	if (err != nil) != wantRefused {
		t.Fatalf("refused=%v, want %v: %v\n%s", err != nil, wantRefused, err, stderr)
	}
	data, rerr := os.ReadFile(file)
	if rerr != nil {
		t.Fatal(rerr)
	}
	return string(data), stderr.String()
}

// In a session dispatched for the branch's issue, the hook writes the
// Refs: line into the message itself and says so: in the trailer block,
// above git's comment lines and the scissors line, so git keeps it.
func TestCommitMsgAddsRefsForTheSessionsIssue(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	requireCommitReference(t, proj)
	dir := filepath.Dir(proj.WorkflowFile)
	onWorkBranch(t, dir)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "cli/sample")

	after, out := runHookOn(t, proj, "feat: x\n", false)
	if after != "feat: x\n\nRefs: cli/sample\n" {
		t.Errorf("a bare subject became:\n%q", after)
	}
	assertContains(t, out, "note: added Refs: cli/sample to the message: no commit on work/cli/sample referenced the issue yet")
	if strings.Contains(out, "git commit --amend") {
		t.Errorf("the line was added, yet the hook still asks for an amend:\n%s", out)
	}

	msg := commitSubject + "Co-Authored-By: A <a@example.com>\n# Please enter the commit message\n# ------------------------ >8 ------------------------\ndiff --git a/x b/x\n"
	after, _ = runHookOn(t, proj, msg, false)
	want := commitSubject + "Co-Authored-By: A <a@example.com>\nRefs: cli/sample\n# Please enter the commit message\n"
	if !strings.HasPrefix(after, want) {
		t.Errorf("the line isn't in the trailer block above the comments:\n%s", after)
	}
	if !proof.MessageReferences(stripCommitComments(after), "cli/sample") {
		t.Errorf("the rule's own reading doesn't see the added line:\n%s", after)
	}
	// The slug compares as cmd_ask compares it: case and spaces aside.
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", " CLI/Sample ")
	if after, _ := runHookOn(t, proj, "feat: x\n", false); !strings.Contains(after, "Refs: cli/sample") {
		t.Errorf("a session slug in another case added nothing:\n%s", after)
	}
}

// Everything else keeps its message byte for byte: one that references
// the issue already, a merge, a session without a slug or for another
// issue, a branch that has its Refs: commit, and a refused message.
func TestCommitMsgLeavesRefsAlone(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	requireCommitReference(t, proj)
	dir := filepath.Dir(proj.WorkflowFile)
	onWorkBranch(t, dir)
	same := func(name, session, msg string, refused bool, wantNote string) {
		t.Helper()
		t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", session)
		after, out := runHookOn(t, proj, msg, refused)
		if after != msg {
			t.Errorf("%s: message changed:\n%q\n→\n%q", name, msg, after)
		}
		if strings.Contains(out, "note: added") {
			t.Errorf("%s: the hook says it added a line:\n%s", name, out)
		}
		if wantNote != "" && !strings.Contains(out, wantNote) {
			t.Errorf("%s: want %q in:\n%s", name, wantNote, out)
		}
	}
	hint := "no commit on work/cli/sample references the issue yet"

	same("has Refs:", "cli/sample", commitSubject+"Refs: cli/sample\n", false, "")
	same("subject names the slug", "cli/sample", "chore(merge): main into cli/sample\n", false, "")
	same("no session slug", "", commitSubject, false, hint)
	same("another issue's session", "cli/other", commitSubject, false, hint)
	same("refused message", "cli/sample", commitSubject+"Proves: AC1 internal/memory TestX\n", true, "")

	// A merge under way: MERGE_HEAD names a commit.
	head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	mergeHead := filepath.Join(dir, ".git", "MERGE_HEAD")
	if err := os.WriteFile(mergeHead, head, 0o644); err != nil {
		t.Fatal(err)
	}
	same("merge commit", "cli/sample", "Merge branch 'main' into work/cli/sample\n", false, hint)
	if err := os.Remove(mergeHead); err != nil {
		t.Fatal(err)
	}

	// The branch has its Refs: commit: nothing left to add.
	commitOnBranch(t, dir, "feat: more\n\nRefs: cli/sample")
	same("branch already references it", "cli/sample", commitSubject, false, "")
}
