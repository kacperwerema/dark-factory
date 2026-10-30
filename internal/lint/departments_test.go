package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func TestCheckDepartments(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	root := env.Project.Root()
	if err := os.MkdirAll(filepath.Join(root, "departments"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "departments", "hq.md"), []byte("CEO"), 0644); err != nil {
		t.Fatal(err)
	}
	env.Project.Company = &tracker.Company{Prompt: "departments/hq.md", Goals: []tracker.Goal{
		{ID: "clients"}, {ID: "top", RollsUp: "clients"},
	}}
	env.Project.Product = &tracker.Department{Name: "Product", Systems: []string{"Engine"}}
	env.Project.Departments = []tracker.Department{{Name: "Marketing", Prompt: "departments/marketing.md", Goals: []tracker.Goal{
		{ID: "pages", RollsUp: "clients", Source: "file:docs/run.json#pages"},
		{ID: "pages"},
		{ID: "posts", Period: "daily", Direction: "sideways", RollsUp: "revenue"},
		{ID: "leads", Source: "quickbooks"},
		{ID: "calls", Source: "file:run.json"},
		{},
	}}}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"projects.yaml (company): goal top: rolls_up: on an HQ goal",
		"projects.yaml (Product): systems: on the Product entry is ignored",
		"projects.yaml (Marketing): prompt: departments/marketing.md does not exist",
		"goal pages: source file " + filepath.Join(root, "docs/run.json") + " does not exist",
		`goal id "pages" appears twice`,
		`goal posts: period "daily" is not week, month, quarter or none`,
		`goal posts: direction "sideways" is not up or down`,
		`goal posts: rolls_up: "revenue" names no HQ goal`,
		`goal leads: source "quickbooks" is not manual or file:<path>#<metric id>`,
		"goal calls: source file:run.json: want file:<path>#<metric id>",
		"a goal has no id:",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing finding %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "hq.md") {
		t.Errorf("an existing prompt file was flagged:\n%s", all)
	}
	if len(got) != 11 {
		t.Errorf("got %d findings, want 11:\n%s", len(got), all)
	}
}

func TestCheckDepartmentsNothingConfigured(t *testing.T) {
	if f := checkDepartments(lintProject(t, minimalWorkflow, nil)); len(f) != 0 {
		t.Errorf("findings without departments: %v", f)
	}
}

func TestCheckOutputs(t *testing.T) {
	env := lintProject(t, minimalWorkflow, nil)
	p := env.Project
	p.Departments = []tracker.Department{{Name: "Marketing"}}
	old := env.Now.Add(-20 * 24 * time.Hour).UTC().Format(time.RFC3339)
	fresh := env.Now.Add(-time.Hour).UTC().Format(time.RFC3339)
	log := strings.Join([]string{
		`{"op":"add","id":"o-1","date":"` + old + `","department":"marketing","title":"Old post","kind":"post","url":"https://a.example"}`,
		`{"op":"add","id":"o-2","date":"` + fresh + `","department":"sales","title":"Lead list","kind":"report","url":"https://a.example/l"}`,
		`{"op":"add","id":"o-3","date":"` + fresh + `","department":"marketing","title":"Gone","kind":"page","path":"docs/gone.md"}`,
		`broken`,
	}, "\n") + "\n"
	path := outputs.LogPath(p)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(log), 0644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range checkDepartments(env) {
		got = append(got, f.Where+": "+f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"outputs.jsonl:4: line skipped:",
		"output o-1 (Old post) has waited for review since",
		"output o-3 (Gone): docs/gone.md is gone",
		`1 output(s) of department "sales", which projects.yaml no longer has`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "o-2 (Lead list) has waited") || len(got) != 4 {
		t.Errorf("findings = %d:\n%s", len(got), all)
	}
}
