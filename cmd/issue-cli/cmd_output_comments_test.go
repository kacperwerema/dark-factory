package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// AC5: agents read the owner's comments, answer them and add the version
// that answers them, all through issue-cli.
func TestOutputCommentsReplyRevise(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	v1 := filepath.Join(t.TempDir(), "brand.md")
	mustWrite(t, v1, "# Brand\n\n## Lines\n\nB. A dark factory with one light on: yours.\n\nC. Run the company.\n")
	o := addOutput(t, proj, "--issue", "seo-pass", "--title", "Brand", "--kind", "report", "--file", v1)
	id := shortID(o.ID)
	run := func(args ...string) (string, error) {
		ctx, out := goalsCtx(t, proj, false)
		err := runOutput(ctx, args)
		return out.String(), err
	}

	s, err := run("comments", id)
	if err != nil {
		t.Fatal(err)
	}
	assertAll(t, s, "0 open, 0 resolved", "No comments yet. The owner comments on the reader page: ", "/p/acme/shelf/read/"+o.ID)

	now := time.Now()
	whole, _ := outputs.AddComment(proj, o.ID, outputs.Anchor{}, "Lead with what I settle.", tracker.OwnerAuthor, 1, now)
	gone, _ := outputs.AddComment(proj, o.ID, outputs.Anchor{Heading: "lines", HeadingText: "Lines", Quote: "Run the company."}, "Drop C.", tracker.OwnerAuthor, 1, now)
	kept, _ := outputs.AddComment(proj, o.ID, outputs.Anchor{Heading: "lines", HeadingText: "Lines", Quote: "one light on: yours."}, "B is the one.", tracker.OwnerAuthor, 1, now)
	if _, err := outputs.Resolve(proj, o.ID, whole.ID, tracker.OwnerAuthor, false, now); err != nil {
		t.Fatal(err)
	}

	s, _ = run("show", id)
	assertAll(t, s, "Comments: 2 open, 1 resolved · issue-cli output comments "+id)
	s, _ = run("comments", id)
	assertAll(t, s, "== Comments on "+id+" v1: Brand · 2 open, 1 resolved ==",
		gone.ID+" [open] v1 · under Lines", "  > Run the company.", "  owner, ", ": Drop C.",
		"issue-cli output reply "+id+" <thread>", "issue-cli output revise "+id+" --file")
	if strings.Contains(s, whole.ID) {
		t.Error("resolved thread listed without --all")
	}
	s, _ = run("comments", id, "--all")
	assertAll(t, s, whole.ID+" [resolved] v1 · whole document")

	// The worker answers and revises: the passage it dropped falls back to
	// its heading; the kept one stays.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo-pass")
	s, err = run("reply", id, gone.ID, "--text", "v2 drops C.")
	if err != nil {
		t.Fatal(err)
	}
	assertAll(t, s, "✓ Replied on "+gone.ID, "as "+tracker.AgentSessionName("seo-pass"), "#thread-"+gone.ID)
	if _, err := run("reply", id, "c-zzzz", "--text", "x"); err == nil || !strings.Contains(err.Error(), "no thread") {
		t.Errorf("unknown thread: %v", err)
	}
	v2 := filepath.Join(t.TempDir(), "brand.md")
	mustWrite(t, v2, "# Brand\n\n## Lines\n\nB. A dark factory with one light on: yours.\n")
	s, err = run("revise", id, "--file", v2, "--note", "drops C")
	if err != nil {
		t.Fatal(err)
	}
	assertAll(t, s, "✓ "+id+" is at v2, back in review: Brand", " v2 [review] report · Brand", "💬 2 open", "?view=changes")
	s, _ = run("comments", id)
	assertAll(t, s, kept.ID+" [open] v1 · under Lines\n", gone.ID+" [open] v1 · under Lines; the passage is not in v2", "seo-pass")
	if _, err := run("revise", id); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("revise without --file: %v", err)
	}
}

func assertAll(t *testing.T, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		assertContains(t, s, w)
	}
}
