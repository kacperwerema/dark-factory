package main

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestCommentEditOwnLatestOnly(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	for _, c := range []struct{ text, author string }{
		{"looks good, go", tracker.OwnerAuthor},
		{"progress: other agent", "agent-someone-else"},
		{tracker.ApprovalComment("go", "viewer", "2026-10-31", "in progress"), "agent-calc-other"},
		{"tests: 44a branch", "agent-calc-other"},
	} {
		if err := tracker.AddCommentBy(issuePath, 0, c.text, "cli", c.author); err != nil {
			t.Fatal(err)
		}
	}
	before := readFile(t, issuePath)

	asWorker(t) // agent-calc-other
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"the owner's", []string{"edit", slug, "1", "--text", "x"}, "comment #1 is the owner's"},
		{"another agent's", []string{"edit", slug, "2", "--text", "x"}, "comment #2 is by agent-someone-else, not agent-calc-other"},
		{"an approval", []string{"edit", slug, "3", "--text", "x"}, "comment #3 is a approval: comment"},
		{"no such comment", []string{"edit", slug, "9", "--text", "x"}, "no comment #9"},
		{"into an approval", []string{"edit", slug, "last", "--text", "approval: \"go\""}, "can't be written by an edit"},
	} {
		ctx, _, _ := newTestContext(proj, false)
		err := runComment(ctx, c.args)
		if err == nil {
			t.Errorf("%s: want a refusal", c.name)
			continue
		}
		assertContains(t, err.Error(), c.want)
		if got := readFile(t, issuePath); got != before {
			t.Fatalf("%s: a refused edit changed the file", c.name)
		}
	}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runComment(ctx, []string{"edit", slug, "4", "--text", "tests: 44 on the branch"}); err != nil {
		t.Fatalf("editing its own latest comment: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Comment #4 on "+slug+" edited by agent-calc-other")
	comments, _ := tracker.LoadComments(issuePath)
	got := comments[3]
	if got.Text != "tests: 44 on the branch" || len(got.Edits) != 1 || got.Edits[0].Text != "tests: 44a branch" {
		t.Fatalf("edited comment: %+v", got)
	}
	if comments[0].Text != "looks good, go" || comments[1].Text != "progress: other agent" {
		t.Fatalf("other comments changed: %+v", comments)
	}

	// last picks the newest; a second edit keeps both old versions.
	ctx, _, _ = newTestContext(proj, false)
	if err := runComment(ctx, []string{"edit", slug, "last", "tests: 44 on the work branch"}); err != nil {
		t.Fatal(err)
	}
	comments, _ = tracker.LoadComments(issuePath)
	if n := len(comments[3].Edits); n != 2 {
		t.Fatalf("want 2 earlier versions, got %d", n)
	}

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runShow(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "tests: 44 on the work branch (edited)")

	// An older comment of its own: not the latest, refused.
	if err := tracker.AddCommentBy(issuePath, 0, "progress: next", "cli", "agent-calc-other"); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ = newTestContext(proj, false)
	err := runComment(ctx, []string{"edit", slug, "4", "--text", "x"})
	if err == nil || !strings.Contains(err.Error(), "only your latest comment (#5)") {
		t.Fatalf("an older own comment: %v", err)
	}

	// A shell with no session signs nothing and edits nothing.
	asOwner(t)
	t.Setenv("CLAUDECODE", "")
	ctx, _, _ = newTestContext(proj, false)
	if err := runComment(ctx, []string{"edit", slug, "last", "--text", "x"}); err == nil || !strings.Contains(err.Error(), "no author") {
		t.Fatalf("no session: %v", err)
	}
}
