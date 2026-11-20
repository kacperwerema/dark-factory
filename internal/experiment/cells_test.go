package experiment

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// cellsWorkflow is a base with three types: feature walks every status on
// the base prompts, tweak skips in design and has its own in progress
// prompt, bugfix appends to the base in progress prompt. The Factory system
// appends to testing.
const cellsWorkflow = `
statuses:
  - name: idea
    prompt: "check main"
  - name: in design
    prompt: "design it"
  - name: in progress
    prompt: "build it"
  - name: testing
    prompt: "test it"
transitions:
  - from: idea
    to: in design
  - from: in design
    to: in progress
  - from: idea
    to: in progress
  - from: in progress
    to: testing
default_type: feature
types:
  feature:
    statuses: []
  tweak:
    path: [idea, in progress, testing]
    statuses:
      - name: in progress
        prompt: "tweak: test first"
  bugfix:
    path: [idea, in progress, testing]
    statuses:
      - name: in progress
        prompt_append: "repro first"
systems:
  Factory:
    statuses:
      - name: testing
        prompt_append: "factory rules"
`

func parseCells(t *testing.T, src string) *tracker.WorkflowConfig {
	t.Helper()
	wf, err := ParseWorkflow([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func labels(cs []Cell) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Label()+" ("+c.Owner+")")
	}
	return strings.Join(out, "; ")
}

// An edit to the base in progress prompt reaches feature and bugfix (whose
// prompt_append keeps the base text) but not tweak, which has its own.
func TestEditedCellsBasePromptSkipsATypeWithItsOwn(t *testing.T) {
	base := parseCells(t, cellsWorkflow)
	ref := parseCells(t, strings.Replace(cellsWorkflow, `prompt: "build it"`, `prompt: "build it; mind the guard tests"`, 1))
	got := labels(EditedCells(base, ref))
	if got != "feature · in progress (base); bugfix · in progress (base)" {
		t.Fatalf("cells: %s", got)
	}
	if s := strings.Join(SharedBy(ref, "in progress"), ","); s != "feature,bugfix" {
		t.Fatalf("shared by %s", s)
	}
}

func TestEditedCellsTypeAndSystemAndEdge(t *testing.T) {
	base := parseCells(t, cellsWorkflow)
	ref := parseCells(t, strings.NewReplacer(
		`prompt: "tweak: test first"`, `prompt: "tweak: test first, then build"`,
		`prompt_append: "factory rules"`, `prompt_append: "factory rules, cite D-n"`,
	).Replace(cellsWorkflow))
	got := labels(EditedCells(base, ref))
	want := "feature · testing · Factory (base); bugfix · testing · Factory (base); tweak · in progress (tweak); tweak · testing · Factory (base)"
	if got != want {
		t.Fatalf("cells:\n got %s\nwant %s", got, want)
	}
	// An inject_prompt on an edge into a status is part of its cell.
	edge := parseCells(t, strings.Replace(cellsWorkflow, "  - from: in design\n    to: in progress\n",
		"  - from: in design\n    to: in progress\n    actions:\n      - type: inject_prompt\n        prompt: \"start from the design\"\n", 1))
	if got := labels(EditedCells(base, edge)); got != "feature · in progress (base)" {
		t.Fatalf("edge cells: %s", got)
	}
	if cs := EditedCells(base, base); len(cs) != 0 {
		t.Fatalf("same workflow: %s", labels(cs))
	}
}

func TestNamedCells(t *testing.T) {
	wf := parseCells(t, cellsWorkflow)
	cs, err := NamedCells(wf, []string{"in progress", "tweak:in progress", "feature:in design"})
	if err != nil {
		t.Fatal(err)
	}
	if got := labels(cs); got != "feature · in progress (base); bugfix · in progress (base); tweak · in progress (tweak); feature · in design (base)" {
		t.Fatalf("named: %s", got)
	}
	for spec, want := range map[string]string{
		"shipping":        `no status "shipping"`,
		"chore:idea":      `no type "chore"`,
		"tweak:in design": "tweak's path skips in design",
	} {
		if _, err := NamedCells(wf, []string{spec}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", spec, err, want)
		}
	}
}

func TestReachSaysWhyATaskIsMissed(t *testing.T) {
	wf := parseCells(t, cellsWorkflow)
	cells, err := NamedCells(wf, []string{"in progress", "in design"})
	if err != nil {
		t.Fatal(err)
	}
	rs := Reach(wf, cells, []TaskRef{
		{Slug: "f1", Type: "feature"},
		{Slug: "t1", Type: "tweak"},
		{Slug: "f2", Type: "feature", Start: "testing"},
		{Slug: "u1", Type: "nonsense"}, // falls back to default_type feature
		{Slug: "d1", Type: "feature", Unread: "runs on the marketing department's workflow, not read"},
	})
	byslug := map[string]TaskReach{}
	for _, r := range rs {
		byslug[r.Task.Slug] = r
	}
	if r := byslug["f1"]; !r.Reached() || labels(r.Cells) != "feature · in progress (base); feature · in design (base)" {
		t.Fatalf("f1: %+v", r)
	}
	if r := byslug["t1"]; r.Reached() || strings.Join(r.Why, "; ") != "tweak has its own in progress prompt; tweak's path skips in design" {
		t.Fatalf("t1: %+v", r)
	}
	if r := byslug["f2"]; r.Reached() || strings.Join(r.Why, "; ") != "starts at testing, after in progress; starts at testing, after in design" {
		t.Fatalf("f2: %+v", r)
	}
	if r := byslug["u1"]; !r.Reached() || r.Type != "feature" {
		t.Fatalf("u1: %+v", r)
	}
	if r := byslug["d1"]; r.Checked || r.Reached() || r.Why[0] != "runs on the marketing department's workflow, not read" {
		t.Fatalf("d1: %+v", r)
	}
	if n, of := ReachCount(rs); n != 2 || of != 4 {
		t.Fatalf("reach %d of %d", n, of)
	}
}

// A change in one system's overlay reaches only tasks of that system.
func TestReachSystemOnlyCell(t *testing.T) {
	base := parseCells(t, cellsWorkflow)
	ref := parseCells(t, strings.Replace(cellsWorkflow, `prompt_append: "factory rules"`, `prompt_append: "factory rules!"`, 1))
	rs := Reach(ref, EditedCells(base, ref), []TaskRef{{Slug: "a", Type: "feature", System: "Factory"}, {Slug: "b", Type: "feature", System: "UI"}})
	if !rs[0].Reached() || rs[1].Reached() {
		t.Fatalf("reach %+v", rs)
	}
	if !strings.Contains(rs[1].Why[0], "is for system Factory only, not UI") {
		t.Fatalf("why %v", rs[1].Why)
	}
}
