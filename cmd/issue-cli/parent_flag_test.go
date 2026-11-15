package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// parentBoard is newTestProject plus an epic typed epic ("big-epic"), an
// issue that is an epic because another names it ("old-epic", parent of
// "kid"), and "plain", which is no epic.
func parentBoard(t *testing.T) *tracker.Project {
	t.Helper()
	proj := newTestProject(t)
	mustWrite(t, filepath.Join(proj.IssueDir, "big-epic.md"), "---\ntitle: \"Big epic\"\nstatus: \"idea\"\ntype: \"epic\"\n---\n")
	mustWrite(t, filepath.Join(proj.IssueDir, "old-epic.md"), "---\ntitle: \"Old epic\"\nstatus: \"idea\"\n---\n")
	mustWrite(t, filepath.Join(proj.IssueDir, "kid.md"), "---\ntitle: \"Kid\"\nstatus: \"idea\"\nparent: \"old-epic\"\n---\n")
	return proj
}

func parentOf(t *testing.T, proj *tracker.Project, slug string) string {
	t.Helper()
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	it := tracker.FindIssue(issues, slug)
	if it == nil {
		t.Fatalf("no issue %s", slug)
	}
	return tracker.ParentSlug(it)
}

// TestCreateAndUpdateTakeParent: create --parent and update --parent write
// parent: for an epic on the board (typed epic, or already a parent) in
// the board's spelling, and refuse anything else without writing.
func TestCreateAndUpdateTakeParent(t *testing.T) {
	proj := parentBoard(t)

	ctx, _, _ := testContext(proj)
	if err := runCreate(ctx, []string{"--title", "Child one", "--parent", "BIG-EPIC"}); err != nil {
		t.Fatal(err)
	}
	if got := parentOf(t, proj, "child-one"); got != "big-epic" {
		t.Fatalf("create --parent wrote parent %q, want big-epic", got)
	}

	ctx, out, _ := testContext(proj)
	if err := runUpdate(ctx, []string{"plain", "--parent", "old-epic"}); err != nil {
		t.Fatal(err)
	}
	if got := parentOf(t, proj, "plain"); got != "old-epic" {
		t.Fatalf("update --parent wrote parent %q, want old-epic", got)
	}
	assertContains(t, out.String(), "parent: old-epic")
	if body := readIssueFile(t, filepath.Join(proj.IssueDir, "plain.md")); !strings.Contains(body, "Body.") {
		t.Fatalf("update --parent alone lost the body:\n%s", body)
	}

	for _, tc := range []struct{ name, value, want string }{
		{"missing", "nope", "no such issue"},
		{"not an epic", "kid", "is not an epic"},
		{"empty", "", "needs an epic"},
	} {
		ctx, _, _ := testContext(proj)
		err := runCreate(ctx, []string{"--title", "Refused " + tc.name, "--parent", tc.value})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("create --parent %q: want %q, got %v", tc.value, tc.want, err)
		}
		if _, statErr := os.Stat(filepath.Join(proj.IssueDir, tracker.Slugify("Refused "+tc.name)+".md")); statErr == nil {
			t.Fatalf("refused create --parent %q still wrote the issue", tc.value)
		}
		ctx, _, _ = testContext(proj)
		err = runUpdate(ctx, []string{"child-one", "--parent", tc.value})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("update --parent %q: want %q, got %v", tc.value, tc.want, err)
		}
	}
	ctx, _, _ = testContext(proj)
	if err := runUpdate(ctx, []string{"big-epic", "--parent", "big-epic"}); err == nil || !strings.Contains(err.Error(), "its own parent") {
		t.Fatalf("update --parent self: want a refusal, got %v", err)
	}
	if got := parentOf(t, proj, "child-one"); got != "big-epic" {
		t.Fatalf("refused update changed parent to %q", got)
	}
}

// TestUpdateBodyFile: update --body-file reads the new body raw from a file
// or stdin, as create's does, and refuses it beside --body.
func TestUpdateBodyFile(t *testing.T) {
	proj := newTestProject(t)
	path := filepath.Join(proj.IssueDir, "plain.md")

	file := filepath.Join(t.TempDir(), "body.md")
	mustWrite(t, file, "## Brief\n\nFrom a file, `quoted` $HOME \\n kept.\n")
	ctx, _, _ := testContext(proj)
	if err := runUpdate(ctx, []string{"plain", "--body-file", file}); err != nil {
		t.Fatal(err)
	}
	got := readIssueFile(t, path)
	if !strings.Contains(got, "From a file, `quoted` $HOME \\n kept.") || strings.Contains(got, "Body.") {
		t.Fatalf("--body-file didn't replace the body verbatim:\n%s", got)
	}

	ctx, _, _ = testContext(proj)
	ctx.Stdin = strings.NewReader("From stdin.\n")
	if err := runUpdate(ctx, []string{"plain", "--body-file", "-"}); err != nil {
		t.Fatal(err)
	}
	if got := readIssueFile(t, path); !strings.Contains(got, "From stdin.") {
		t.Fatalf("--body-file - text missing:\n%s", got)
	}

	ctx, _, _ = testContext(proj)
	err := runUpdate(ctx, []string{"plain", "--body", "x", "--body-file", file})
	if err == nil || !strings.Contains(err.Error(), "together with --body-file") {
		t.Fatalf("--body with --body-file: want a refusal, got %v", err)
	}
	if got := readIssueFile(t, path); !strings.Contains(got, "From stdin.") {
		t.Fatalf("refused update still wrote:\n%s", got)
	}
}
