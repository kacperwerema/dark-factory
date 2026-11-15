package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// parentProject: epic e with children a (parent:) and b (legacy umbrella:,
// another version), one unrelated issue c, each with its own D-n lines.
func parentProject(t *testing.T) *tracker.Project {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "issues")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "epic-e.md"), "---\ntitle: \"epic-e\"\nstatus: \"in progress\"\nversion: \"0.2\"\n---\n\nThe epic.\n")
	mustWrite(t, filepath.Join(dir, "child-a.md"), "---\ntitle: \"child-a\"\nstatus: \"idea\"\nversion: \"0.2\"\nparent: \"epic-e\"\n---\n\n## Decisions\n\n- **D-1** · 2026-10-01 · chat: Child A's call, \"ok\"\n")
	mustWrite(t, filepath.Join(dir, "child-b.md"), "---\ntitle: \"child-b\"\nstatus: \"done\"\nversion: \"0.1\"\numbrella: \"epic-e\"\n---\n\nBody.\n")
	mustWrite(t, filepath.Join(dir, "other-c.md"), "---\ntitle: \"other-c\"\nstatus: \"idea\"\nversion: \"0.2\"\n---\n\n## Decisions\n\n- **D-1** · 2026-10-02 · chat: Other C's call, \"ok\"\n- **D-2** · 2026-10-03 · chat: Other C's second, \"ok\"\n")
	return &tracker.Project{Name: "test", Slug: "test", IssueDir: dir, Version: "0.2"}
}

// listedSlugs reads the slugs off list's rows ("  [status] slug ...").
func listedSlugs(out string) []string {
	var slugs []string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "] "); strings.HasPrefix(line, "  [") && i > 0 {
			slugs = append(slugs, strings.Fields(line[i+2:])[0])
		}
	}
	return slugs
}

// TestListParentPrintsExactlyTheChildren: two children (parent: and the
// legacy umbrella:, one outside the project version) and one other issue;
// list --parent prints exactly the two.
func TestListParentPrintsExactlyTheChildren(t *testing.T) {
	proj := parentProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runList(ctx, []string{"--parent", "epic-e"}); err != nil {
		t.Fatal(err)
	}
	slugs := listedSlugs(stdout.String())
	sort.Strings(slugs)
	if got := strings.Join(slugs, " "); got != "child-a child-b" {
		t.Fatalf("list --parent epic-e = %q, want child-a child-b:\n%s", got, stdout.String())
	}
	assertContains(t, stdout.String(), "\n2 issues\n")

	// Other filters still apply.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runList(ctx, []string{"--parent", "epic-e", "--status", "open"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(listedSlugs(stdout.String()), " "); got != "child-a" {
		t.Errorf("--parent --status open = %q", got)
	}

	// A slug that is neither an issue nor anyone's parent is a typo.
	ctx, _, _ = newTestContext(proj, false)
	if err := runList(ctx, []string{"--parent", "no-such-epic"}); err == nil {
		t.Error("list --parent no-such-epic: want an error")
	}

	// An issue with no children lists none.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runList(ctx, []string{"--parent", "other-c"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "\n0 issues\n")
}

// TestShowListsAnEpicsChildren: show names the epic's children; a child
// shows no Children line.
func TestShowListsAnEpicsChildren(t *testing.T) {
	proj := parentProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runShow(ctx, []string{"epic-e"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Children (2): ")
	assertContains(t, stdout.String(), "child-a (idea)")
	assertContains(t, stdout.String(), "child-b (done)")

	ctx, stdout, _ = newTestContext(proj, false)
	if err := runShow(ctx, []string{"other-c"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "Children (") {
		t.Errorf("an issue with no children shows a Children line:\n%s", stdout.String())
	}
}

// TestDecisionsSlugPrintsOnlyThatIssue: decisions <slug> prints only that
// issue's D-n lines, the same as --issue.
func TestDecisionsSlugPrintsOnlyThatIssue(t *testing.T) {
	proj := parentProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"other-c"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	assertContains(t, out, "== Decisions (2) ==")
	assertContains(t, out, "other-c#D-1")
	assertContains(t, out, "other-c#D-2")
	if strings.Contains(out, "child-a") {
		t.Errorf("decisions other-c lists another issue's decision:\n%s", out)
	}

	ctx, flagOut, _ := newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"--issue", "other-c"}); err != nil {
		t.Fatal(err)
	}
	if flagOut.String() != out {
		t.Errorf("decisions <slug> and --issue <slug> differ:\n%s\n---\n%s", out, flagOut.String())
	}

	ctx, _, _ = newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"other-c", "--issue", "child-a"}); err == nil {
		t.Error("two different issues: want an error")
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"other-c", "child-a"}); err == nil {
		t.Error("two positionals: want an error")
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runDecisions(ctx, []string{"no-such-issue"}); err == nil {
		t.Error("decisions no-such-issue: want an error")
	}
}
