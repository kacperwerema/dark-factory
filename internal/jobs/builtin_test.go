package jobs

import (
	"os"
	"path/filepath"
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
	if got := strings.Join(Builtins(), " "); !strings.Contains(got, "memory-consolidate") {
		t.Errorf("Builtins() = %v", got)
	}
	for _, bad := range []string{"builtin:", "builtin:nope", "builtin:../workflow/content", "builtin:a/b"} {
		if _, err := BriefText(p, tracker.Job{Brief: bad}); err == nil || !strings.Contains(err.Error(), "builtin:memory-consolidate") {
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

// brief: builtin:growth-scout on a Marketing department (the bundled
// template) with no type: plans a report run, never the default piece that
// walks to publish, and its brief names every input and the rule that
// nothing is executed. On a workflow without a report type (the content
// template) it is refused with the fix (factory/growth-scout-…, AC1).
func TestBuiltinBriefGrowthScout(t *testing.T) {
	p := project(t)
	marketing, err := os.ReadFile("../../cmd/issue-cli/templates/workflow/marketing.yaml")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(p.Root(), "workflows/marketing.yaml"), string(marketing))
	scout := tracker.Job{ID: "growth-scout", Every: "month", Brief: "builtin:growth-scout"}
	p.Departments[0].Jobs = append(p.Departments[0].Jobs, scout)
	r, err := Find(p, "marketing/growth-scout")
	if err != nil {
		t.Fatal(err)
	}
	n, err := PlanRun(p, r, nil, "2026-11", false)
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != "report" || n.Status != "research" {
		t.Fatalf("run type %q status %q, want report at research", n.Type, n.Status)
	}
	brief, err := Brief(p, r, n, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Growth scout for marketing, run 2026-11",
		"previous run: none (first run)",
		"departments/marketing/brand.md`, `icp.md`, `competitors.md` and `market.md`",
		"issue-cli goals marketing",
		"issue-cli outputs marketing",
		"mem-guard check",
		"**First cheap test**: under a day of agent work and $0-20",
		"**ask Legal**",
		"**Needs**: none, an outside account, or money",
		"**ICE**",
		"Not proposed again",
		"issue-cli output add --issue seo/" + n.Slug + " --kind report",
		"execute nothing",
		"never file the ideas as issues",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief misses %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "{{") {
		t.Errorf("unfilled placeholder:\n%s", brief)
	}
	if err := WriteRun(p, r, n, brief, now); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(n.File)
	if err != nil || !strings.Contains(string(data), `type: "report"`) || !strings.Contains(string(data), `job: "marketing/growth-scout"`) {
		t.Fatalf("run file (%v):\n%s", err, data)
	}

	// An explicit type: still wins.
	wf, err := p.WorkflowFor("Marketing")
	if err != nil {
		t.Fatal(err)
	}
	if typ, err := RunType(wf, tracker.Job{ID: "g", Every: "month", Brief: scout.Brief, Type: "piece"}); err != nil || typ != "piece" {
		t.Errorf("explicit type: %q, %v", typ, err)
	}

	// The content template has no report type: refused, with the fix.
	p2 := project(t)
	p2.Departments[0].Jobs = append(p2.Departments[0].Jobs, scout)
	r2, err := Find(p2, "marketing/growth-scout")
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanRun(p2, r2, nil, "2026-11", false)
	if err == nil || !strings.Contains(err.Error(), "job marketing/growth-scout: builtin:growth-scout runs are type report: the workflow has no report type (add it: issue-cli workflow upgrade --department") {
		t.Errorf("err = %v", err)
	}
	if BuiltinType("builtin:memory-consolidate") != "" || BuiltinType("departments/x/growth-scout.md") != "" {
		t.Error("BuiltinType names a type for a brief that needs none")
	}
}
