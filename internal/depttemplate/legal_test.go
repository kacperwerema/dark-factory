package depttemplate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// department add legal on a sandbox project plans and writes Legal as a
// top-level department: no parent, its own workflow file, the role prompt
// and both job briefs, and nothing else in projects.yaml moves.
func TestAddLegalPlansATopLevelDepartment(t *testing.T) {
	path, proj := sandbox(t, commentedConfig)
	tmpl, err := Load("legal")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Group || tmpl.Workflow != "legal" {
		t.Fatalf("legal template = %+v, want a department with workflow legal", tmpl)
	}
	plan, err := NewPlan(proj, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range plan.Files {
		if f.State != FileWrite {
			t.Fatalf("%s: state %s, want write", f.Path, f.State)
		}
		paths = append(paths, f.Path)
	}
	for _, want := range []string{
		"workflows/legal.yaml",
		"departments/legal/role.md",
		"departments/legal/jobs/monthly-baseline.md",
		"departments/legal/jobs/law-watch.md",
	} {
		if !planHas(paths, want) {
			t.Fatalf("plan files = %v, want %s", paths, want)
		}
	}
	if err := plan.Apply(proj); err != nil {
		t.Fatal(err)
	}

	got := loadProject(t, path, "acme")
	d := got.DepartmentByKey("legal")
	if d == nil {
		t.Fatal("no Legal department after add")
	}
	if d.Parent != "" || got.GroupOf(d.Name) != nil {
		t.Fatalf("Legal parent = %q, want top-level", d.Parent)
	}
	if d.Workflow != "workflows/legal.yaml" || d.Prompt != "departments/legal/role.md" || len(d.Goals) != 2 {
		t.Fatalf("department = %+v", d)
	}
	if len(d.Jobs) != 2 {
		t.Fatalf("jobs = %+v, want monthly-baseline and law-watch", d.Jobs)
	}
	for _, j := range d.Jobs {
		if j.Type != "compliance-check" {
			t.Fatalf("job %s type = %q, want compliance-check", j.ID, j.Type)
		}
		if r, err := jobs.Find(got, "legal/"+j.ID); err != nil || r.Err != nil {
			t.Fatalf("installed job %s: %v %+v", j.ID, err, r)
		}
	}
	wf, _ := os.ReadFile(filepath.Join(proj.Root(), "workflows", "legal.yaml"))
	want, _ := workflowupgrade.ReadTemplate("legal")
	if len(want) == 0 || string(wf) != string(want) {
		t.Fatal("workflows/legal.yaml is not the legal template")
	}

	// The installed workflow loads, with the four types and the owner's
	// gates where the design puts them.
	lw, err := got.WorkflowFor("Legal")
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"compliance-check", "document", "legal-check", "due-diligence"} {
		if _, ok := lw.Types[typ]; !ok {
			t.Fatalf("legal workflow has no type %s", typ)
		}
	}
	if !ownerOnlyInto(lw, "publish") {
		t.Fatal("owner review → publish is not owner_only")
	}
}

// The role prompt, the job briefs and the workflow's prompts say agents
// never give a legal sign-off, and every finding cites its source.
func TestLegalTemplateNeverSignsOff(t *testing.T) {
	tmpl, err := Load("legal")
	if err != nil {
		t.Fatal(err)
	}
	role := string(tmpl.RolePrompt)
	for _, want := range []string{"never give a legal sign-off", "EUR-Lex", "ISAP", "the date you read"} {
		if !strings.Contains(role, want) {
			t.Fatalf("role.md lacks %q", want)
		}
	}
	if len(tmpl.JobBriefs) != 2 {
		t.Fatalf("job briefs = %d, want 2", len(tmpl.JobBriefs))
	}
	for _, j := range tmpl.Jobs {
		if !strings.Contains(string(tmpl.JobBriefs[j.ID]), "never sign off") {
			t.Fatalf("job brief %s lacks \"never sign off\"", j.Brief)
		}
	}
	wf, _ := workflowupgrade.ReadTemplate("legal")
	if !strings.Contains(string(wf), "never sign off") {
		t.Fatal("legal workflow lacks \"never sign off\"")
	}
}

func planHas(paths []string, want string) bool {
	for _, p := range paths {
		if filepath.ToSlash(p) == want || strings.HasSuffix(filepath.ToSlash(p), "/"+want) {
			return true
		}
	}
	return false
}

func ownerOnlyInto(w *tracker.WorkflowConfig, to string) bool {
	for _, t := range w.Transitions {
		if t.To != to {
			continue
		}
		for _, a := range t.Actions {
			if a.Type == "require_human_approval" && a.OwnerOnly {
				return true
			}
		}
	}
	return false
}
