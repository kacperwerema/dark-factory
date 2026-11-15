package tracker

import (
	"fmt"
	"path/filepath"
	"testing"
)

func decisionIssueText(title, line string) string {
	return fmt.Sprintf("---\ntitle: %q\nstatus: \"idea\"\n---\n\n## Decisions\n\n%s\n\nSee other#D-1.\n", title, line)
}

func decisionParses(fn func()) int64 {
	before := ReadLoadCounts().DecisionParses
	fn()
	return ReadLoadCounts().DecisionParses - before
}

// A board indexed again parses no unchanged issue's decisions; a changed
// file is parsed again, and only it.
func TestDecisionIndexParsesEachFileVersionOnce(t *testing.T) {
	dir := t.TempDir()
	writeSettled(t, filepath.Join(dir, "a.md"), decisionIssueText("A", "- **D-1** · 2026-11-01 · Keep it small"))
	writeSettled(t, filepath.Join(dir, "other.md"), decisionIssueText("Other", "- **D-1** · 2026-11-02 · Ship it"))

	build := func() *DecisionIndex { return BuildDecisionIndex(mustLoad(t, dir), nil) }
	var x *DecisionIndex
	if n := decisionParses(func() { x = build() }); n != 2 {
		t.Fatalf("first build parsed %d bodies, want 2", n)
	}
	if len(x.Entries) != 2 || len(x.Lookup("other#D-1").CitedOpen) != 1 {
		t.Fatalf("index = %+v", x.Entries)
	}
	if n := decisionParses(func() { x = build(); build() }); n != 0 {
		t.Fatalf("unchanged board parsed %d bodies, want 0", n)
	}
	if len(x.Entries) != 2 || len(x.Lookup("other#D-1").CitedOpen) != 1 {
		t.Fatalf("warm index = %+v", x.Entries)
	}

	writeSettled(t, filepath.Join(dir, "a.md"), decisionIssueText("A", "- **D-1** · 2026-11-01 · Keep it smaller"))
	if n := decisionParses(func() { x = build() }); n != 1 {
		t.Fatalf("one changed file parsed %d bodies, want 1", n)
	}
	if got := x.Lookup("a#D-1").Text; got != "Keep it smaller" {
		t.Fatalf("a#D-1 = %q after the edit", got)
	}
}

// A caller that changes its copy's body or slug gets that body's decisions,
// not the cached file's.
func TestIssueDecisionsFollowTheCopy(t *testing.T) {
	dir := t.TempDir()
	writeSettled(t, filepath.Join(dir, "a.md"), decisionIssueText("A", "- **D-1** · 2026-11-01 · Keep it small"))
	issue := mustLoad(t, dir)[0]
	if d := issue.Decisions(); len(d) != 1 || d[0].Ref != "a#D-1" {
		t.Fatalf("decisions = %+v", d)
	}
	issue.Slug = "cli/a"
	if d := issue.Decisions(); len(d) != 1 || d[0].Ref != "cli/a#D-1" {
		t.Fatalf("after slug change = %+v", d)
	}
	issue.BodyRaw = "## Decisions\n\n- **D-2** · 2026-11-03 · Other\n"
	if d := issue.Decisions(); len(d) != 1 || d[0].ID != "D-2" {
		t.Fatalf("after body change = %+v", d)
	}
	if d := mustLoad(t, dir)[0].Decisions(); len(d) != 1 || d[0].Ref != "a#D-1" {
		t.Fatalf("a fresh load sees the copy's change: %+v", d)
	}
}
