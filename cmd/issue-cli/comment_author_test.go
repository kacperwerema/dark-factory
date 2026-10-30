package main

import (
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// commentOn runs issue-cli comment on slug and returns the stored comment
// and the success line.
func commentOn(t *testing.T, proj *tracker.Project, slug, text string) (tracker.Comment, string) {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runComment(ctx, []string{slug, text}); err != nil {
		t.Fatal(err)
	}
	comments, err := tracker.LoadComments(loadLeadIssue(t, proj, slug).FilePath)
	if err != nil || len(comments) == 0 {
		t.Fatalf("comments = %v, %v", comments, err)
	}
	return comments[len(comments)-1], stdout.String()
}

func TestComment_SignedBySession(t *testing.T) {
	proj := makeLeadProject(t)

	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "ui/child")
	c, out := commentOn(t, proj, "ui/child", "progress: tests green")
	if c.Author != "agent-ui-child" || !strings.Contains(out, "✓ Comment added to ui/child by agent-ui-child") {
		t.Errorf("worker: author %q, output %q", c.Author, out)
	}

	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	if c, _ := commentOn(t, proj, "ui/child", "question: which branch?"); c.Author != "lead-proj" {
		t.Errorf("lead: author %q", c.Author)
	}
}

// A shell with no session signs nothing and says so; a worker found only by
// its worktree signs with the issue's assignee.
func TestComment_NoSessionNoAuthor(t *testing.T) {
	proj := makeLeadProject(t)
	c, out := commentOn(t, proj, "loner", "a note")
	if c.Author != "" || !strings.Contains(out, "(no author: no lead or worker session detected)") {
		t.Errorf("shell: author %q, output %q", c.Author, out)
	}

	orig := workingDir
	workingDir = func() (string, error) { return "/repo/.worktrees/ui/child", nil }
	t.Cleanup(func() { workingDir = orig })
	issue := loadLeadIssue(t, proj, "ui/child")
	issue.Assignee = "agent-ui-child"
	if got := commentAuthor(proj, issue); got != "agent-ui-child" {
		t.Errorf("worktree worker: author %q, want the assignee", got)
	}
	issue.Assignee = ""
	if got := commentAuthor(proj, issue); got != "" {
		t.Errorf("worktree worker, no assignee: author %q", got)
	}
}
