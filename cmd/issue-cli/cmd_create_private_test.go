package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// leakScanCheckout makes proj's folder look like a dark-factory checkout:
// the workflow catalog has an entry file, as where make leak-check runs.
func leakScanCheckout(t *testing.T, proj *tracker.Project) {
	t.Helper()
	dir := filepath.Join(proj.Root(), filepath.FromSlash(workflowupgrade.CatalogPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "x.yaml"), "id: x\n")
}

// A sweep named after a private project gets a slug without the name, so
// its branch and every Refs: line stay clean; the title keeps it, and the
// warning names the list's line, never the name (report 7ab1 item 8).
func TestCreateKeepsPrivateNamesOutOfTheSlug(t *testing.T) {
	privateNamesList(t, "Another Game", fakePrivateName)
	proj := newTestProject(t)
	leakScanCheckout(t, proj)

	ctx, out, _ := testContext(proj)
	if err := runCreate(ctx, []string{"--title", "Sweep " + fakePrivateName + " retros for engine fixes", "--system", "Factory"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(proj.IssueDir, "Factory", "sweep-retros-for-engine-fixes.md")
	got := readIssueFile(t, file)
	for _, want := range []string{`slug: "sweep-retros-for-engine-fixes"`, "title: \"Sweep " + fakePrivateName + " retros"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in the issue file:\n%s", want, got)
		}
	}
	issues, err := proj.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	if tracker.FindIssue(issues, "factory/sweep-retros-for-engine-fixes") == nil {
		t.Errorf("the board doesn't find the issue by its clean slug")
	}
	s := out.String()
	for _, want := range []string{"Slug: factory/sweep-retros-for-engine-fixes", "line 2 of the private-names list", "branch", "Refs:"} {
		if !strings.Contains(s, want) {
			t.Errorf("want %q in create's output:\n%s", want, s)
		}
	}
	if strings.Contains(strings.ToLower(s), strings.ToLower(fakePrivateName)+"-") || strings.Contains(s, "sweep-"+strings.ToLower(fakePrivateName)) {
		t.Errorf("the name is still in a slug:\n%s", s)
	}

	// A name of two words is found in the title and leaves the slug too.
	ctx, _, _ = testContext(proj)
	if err := runCreate(ctx, []string{"--title", "Another Game: owner boxes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj.IssueDir, "owner-boxes.md")); err != nil {
		t.Errorf("two-word name not dropped from the slug: %v", err)
	}

	// A title that is only the name still gets a slug.
	ctx, _, _ = testContext(proj)
	if err := runCreate(ctx, []string{"--title", fakePrivateName}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj.IssueDir, "issue.md")); err != nil {
		t.Errorf("a title of only the name: want issue.md: %v", err)
	}
}

// Outside a checkout where the leak scan runs (a private project's own
// board names itself all the time), create leaves the slug alone.
func TestCreateKeepsTheSlugWhereNoLeakScanRuns(t *testing.T) {
	privateNamesList(t, fakePrivateName)
	proj := newTestProject(t)
	ctx, out, _ := testContext(proj)
	if err := runCreate(ctx, []string{"--title", fakePrivateName + " level two"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj.IssueDir, strings.ToLower(fakePrivateName)+"-level-two.md")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "private-names") {
		t.Errorf("warned where no leak scan runs:\n%s", out)
	}
}
