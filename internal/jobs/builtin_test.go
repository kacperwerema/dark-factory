package jobs

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// brief: builtin:memory-consolidate reads the bundled brief: no file in
// the project, the placeholders filled like a file brief's.
func TestBuiltinBriefMemoryConsolidate(t *testing.T) {
	p := project(t)
	p.Departments[0].Jobs = append(p.Departments[0].Jobs, tracker.Job{ID: "memory-consolidate", Every: "week", On: "sun", Brief: "builtin:memory-consolidate"})
	r, err := Find(p, "marketing/memory-consolidate")
	if err != nil {
		t.Fatal(err)
	}
	n, err := PlanRun(p, r, nil, "2026-W44", false)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := Brief(p, r, n, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Memory consolidation for marketing, run 2026-W44",
		"issue-cli memory consolidate",
		"--supersedes <id>,<id>",
		"never delete",
		"never edit CLAUDE.md",
		"Promote memories to CLAUDE.md (2026-W44)",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief misses %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "{{") {
		t.Errorf("unfilled placeholder:\n%s", brief)
	}
	if got := Builtins(); len(got) == 0 || got[0] != "memory-consolidate" {
		t.Errorf("Builtins() = %v", got)
	}
	for _, bad := range []string{"builtin:", "builtin:nope", "builtin:../workflow/content", "builtin:a/b"} {
		if _, err := BriefText(p, tracker.Job{Brief: bad}); err == nil || !strings.Contains(err.Error(), "bundled: builtin:memory-consolidate") {
			t.Errorf("%s: err %v", bad, err)
		}
	}
}

// A job on the Product entry is listed as product/<id>; its run is a
// Product issue (no department: field) under Product/, and {{slug}} names
// the run itself (factory/nightly-self-improvement-loop-…#Q1).
func TestProductJobNightlyImprove(t *testing.T) {
	p := project(t)
	p.Product = &tracker.Department{Name: tracker.ProductName, Systems: []string{"Ignored"}, Jobs: []tracker.Job{{ID: "nightly-improve", Every: "day", At: "02:00", Brief: "builtin:nightly-improve"}}}
	r, err := Find(p, "product/nightly-improve")
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsProduct() || r.Department != tracker.ProductName {
		t.Fatalf("ref %+v", r)
	}
	n, err := PlanRun(p, r, nil, "2026-11-01", false)
	if err != nil {
		t.Fatal(err)
	}
	if n.System != "Product" || n.Slug != "nightly-improve-2026-11-01" {
		t.Fatalf("run %+v", n)
	}
	brief, err := Brief(p, r, n, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"issue-cli bench nightly --run product/nightly-improve-2026-11-01", "never adopt, park, revert or queue an experiment", "background command"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief misses %q:\n%s", want, brief)
		}
	}
	if err := WriteRun(p, r, n, brief, now); err != nil {
		t.Fatal(err)
	}
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	var run *tracker.Issue
	for _, is := range issues {
		if is.Field("job") == "product/nightly-improve" {
			run = is
		}
	}
	if run == nil || run.Slug != "product/nightly-improve-2026-11-01" || run.Field("department") != "" || p.DepartmentOf(run) != "" {
		t.Fatalf("run issue %+v", run)
	}
}
