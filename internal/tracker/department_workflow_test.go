package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// deptProject is Product on workflow.yaml (with ci: and worktree: true) and
// Marketing (system SEO) on workflows/marketing.yaml.
func deptProject(t *testing.T, marketing string) *Project {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "workflow.yaml"), `
worktree: true
ci:
  run:
    - make test
statuses:
  - name: idea
  - name: done
transitions:
  - from: idea
    to: done
`)
	if marketing != "" {
		writeTestFile(t, filepath.Join(dir, "workflows", "marketing.yaml"), marketing)
	}
	return &Project{
		Name: "p", Slug: "p", IssueDir: filepath.Join(dir, "issues"), WorkDir: dir,
		WorkflowFile: filepath.Join(dir, "workflow.yaml"),
		Departments: []Department{
			{Name: "Marketing", Systems: []string{"SEO"}, Workflow: "workflows/marketing.yaml"},
			{Name: "Sales"},
		},
	}
}

const marketingYAML = `
statuses:
  - name: brief
  - name: published
transitions:
  - from: brief
    to: published
`

func TestWorkflowForPicksTheDepartmentsFile(t *testing.T) {
	p := deptProject(t, marketingYAML)
	product, err := p.WorkflowFor("")
	if err != nil || product.GetStatus("idea") == nil || product.File != "" {
		t.Fatalf("Product: %v, file %q", err, product.File)
	}
	sales, err := p.WorkflowFor("Sales")
	if err != nil || sales.GetStatus("idea") == nil {
		t.Fatalf("a department without workflow: should run on Product's: %v", err)
	}
	mk, err := p.WorkflowFor("marketing")
	if err != nil {
		t.Fatal(err)
	}
	if mk.GetStatus("brief") == nil || mk.GetStatus("idea") != nil {
		t.Fatalf("Marketing statuses = %v", mk.GetStatusOrder())
	}
	if mk.Department != "Marketing" || !strings.HasSuffix(mk.File, filepath.Join("workflows", "marketing.yaml")) {
		t.Fatalf("Marketing department %q file %q", mk.Department, mk.File)
	}
	if !mk.CI.Enabled() || !mk.WorktreeEnabled() {
		t.Fatal("project-wide ci: and worktree: should come from Product's file")
	}
}

func TestWorkflowForIssueUsesDepartmentOf(t *testing.T) {
	p := deptProject(t, marketingYAML)
	seo := &Issue{Slug: "seo/a", System: "SEO", Status: "brief"}
	wf := p.LoadWorkflowForIssue(seo)
	if wf.LoadErr != nil || wf.GetStatus("brief") == nil {
		t.Fatalf("SEO issue: err %v, statuses %v", wf.LoadErr, wf.GetStatusOrder())
	}
	if got := p.LoadWorkflowForIssue(&Issue{System: "CLI", Status: "idea"}); got.GetStatus("idea") == nil {
		t.Fatal("Product issue lost Product's workflow")
	}
	if p.WorkflowDepartment(seo) != "Marketing" || p.WorkflowDepartment(&Issue{ExtraFields: []ExtraField{{Key: "department", Value: "Sales"}}}) != "" {
		t.Fatal("WorkflowDepartment: Marketing has a file, Sales runs on Product's")
	}
}

func TestBrokenDepartmentFileIsAnErrorNotAFallback(t *testing.T) {
	for name, content := range map[string]string{"missing": "", "unparsable": "statuses: [: x\n", "empty": "board: {}\n"} {
		t.Run(name, func(t *testing.T) {
			p := deptProject(t, content)
			_, err := p.WorkflowFor("Marketing")
			if err == nil || !strings.Contains(err.Error(), "department Marketing") {
				t.Fatalf("err = %v", err)
			}
			wf := p.LoadWorkflowForIssue(&Issue{System: "SEO", Status: "brief"})
			if wf.LoadErr == nil || len(wf.Statuses) != 0 {
				t.Fatalf("broken file: LoadErr %v, statuses %v (must not fall back)", wf.LoadErr, wf.GetStatusOrder())
			}
		})
	}
}

func TestCheckDepartmentMove(t *testing.T) {
	p := deptProject(t, marketingYAML)
	issue := &Issue{System: "CLI", Status: "idea"}
	err := p.CheckDepartmentMove(issue, "Marketing")
	if err == nil || !strings.Contains(err.Error(), "brief, published") || !strings.Contains(err.Error(), "--status") {
		t.Fatalf("idea → Marketing: %v", err)
	}
	if err := p.CheckDepartmentMove(issue, "Sales"); err != nil {
		t.Fatalf("Sales runs on Product's workflow: %v", err)
	}
	if moved := issue.WithField("system", "SEO"); p.DepartmentOf(moved) != "Marketing" || issue.System != "CLI" {
		t.Fatal("WithField must copy, not change the issue")
	}
}

func TestIssueDoneAtTheEndOfItsOwnWorkflow(t *testing.T) {
	p := deptProject(t, marketingYAML)
	if !p.IssueDone(&Issue{System: "SEO", Status: "published"}) {
		t.Fatal("published ends Marketing's workflow")
	}
	if p.IssueDone(&Issue{System: "SEO", Status: "brief"}) || p.IssueDone(&Issue{System: "CLI", Status: "published"}) {
		t.Fatal("brief is open; published means nothing on Product")
	}
	if !p.IssueDone(&Issue{System: "CLI", Status: "done"}) {
		t.Fatal("done is done on Product")
	}
}

func TestDepartmentFileCacheSeesEdits(t *testing.T) {
	p := deptProject(t, marketingYAML)
	if wf, _ := p.WorkflowFor("Marketing"); wf.GetStatus("draft") != nil {
		t.Fatal("draft before the edit")
	}
	writeTestFile(t, p.DepartmentWorkflowPath("Marketing"), strings.Replace(marketingYAML, "  - name: brief\n", "  - name: brief\n  - name: draft\n", 1))
	if wf, _ := p.WorkflowFor("Marketing"); wf.GetStatus("draft") == nil {
		t.Fatal("the edit didn't show: the cache must key on size and mtime")
	}
}
