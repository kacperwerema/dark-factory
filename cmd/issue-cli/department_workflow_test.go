package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

// makeDepartmentProject is a project with Product on a small workflow.yaml
// and a Marketing department (system SEO) on the bundled content template,
// with one issue in each.
func makeDepartmentProject(t *testing.T) *tracker.Project {
	t.Helper()
	dir := realTempDir(t)
	writeDeptFile(t, filepath.Join(dir, "workflow.yaml"), strings.TrimSpace(`
statuses:
  - name: "idea"
  - name: "backlog"
  - name: "done"
transitions:
  - from: "idea"
    to: "backlog"
  - from: "backlog"
    to: "done"
`)+"\n")
	content, err := workflowupgrade.ReadTemplate("content")
	if err != nil {
		t.Fatal(err)
	}
	writeDeptFile(t, filepath.Join(dir, "workflows", "marketing.yaml"), string(content))
	issues := filepath.Join(dir, "issues")
	writeDeptFile(t, filepath.Join(issues, "CLI", "tool.md"), "---\ntitle: \"tool\"\nstatus: \"idea\"\nsystem: \"CLI\"\n---\n\nA tool.\n")
	writeDeptFile(t, filepath.Join(issues, "SEO", "faq-page.md"), "---\ntitle: \"faq page\"\nstatus: \"brief\"\nsystem: \"SEO\"\n---\n\nAn FAQ page for the plumbing client.\n")
	return &tracker.Project{
		Name: "test", Slug: "test", IssueDir: issues, WorkDir: dir,
		WorkflowFile: filepath.Join(dir, "workflow.yaml"),
		Departments:  []tracker.Department{{Name: "Marketing", Systems: []string{"SEO"}, Workflow: "workflows/marketing.yaml"}},
	}
}

func TestDepartmentIssueWalksItsOwnWorkflow(t *testing.T) {
	proj := makeDepartmentProject(t)
	ctx, out, _ := newTestContext(proj, false)

	if err := runTransition(ctx, []string{"seo/faq-page", "--to", "draft"}); err != nil {
		t.Fatalf("brief → draft: %v", err)
	}
	issue := loadIssueByPath(t, proj.IssueDir, filepath.Join(proj.IssueDir, "SEO", "faq-page.md"))
	if issue.Status != "draft" || !strings.Contains(issue.BodyRaw, "## Draft") {
		t.Fatalf("after brief → draft: status %q, body:\n%s", issue.Status, issue.BodyRaw)
	}
	// Product's statuses are not the department's, and the refusal lists
	// the department's own.
	err := runTransition(ctx, []string{"seo/faq-page", "--to", "backlog"})
	if err == nil || !strings.Contains(err.Error(), "owner review") {
		t.Fatalf("a Marketing issue to Product's backlog: err = %v, want the content statuses listed", err)
	}
	// Product's issue still follows workflow.yaml.
	if err := runTransition(ctx, []string{"cli/tool", "--to", "backlog"}); err != nil {
		t.Fatalf("Product idea → backlog: %v\n%s", err, out)
	}
}

func TestDepartmentPublishNeedsTheOwnersStamp(t *testing.T) {
	proj := makeDepartmentProject(t)
	path := filepath.Join(proj.IssueDir, "SEO", "faq-page.md")
	writeDeptFile(t, path, "---\ntitle: \"faq page\"\nstatus: \"owner review\"\nsystem: \"SEO\"\n---\n\nAn FAQ page.\n")
	ctx, _, _ := newTestContext(proj, false)
	err := runTransition(ctx, []string{"seo/faq-page", "--to", "publish"})
	if err == nil || !strings.Contains(err.Error(), "approv") {
		t.Fatalf("owner review → publish without approval: err = %v, want an approval refusal", err)
	}
	writeDeptFile(t, path, "---\ntitle: \"faq page\"\nstatus: \"owner review\"\nsystem: \"SEO\"\nhuman_approval: \"publish\"\n---\n\nAn FAQ page.\n")
	if err := runTransition(ctx, []string{"seo/faq-page", "--to", "publish"}); err != nil {
		t.Fatalf("owner review → publish with approval: %v", err)
	}
	issue := loadIssueByPath(t, proj.IssueDir, path)
	if !strings.Contains(issue.BodyRaw, "Published: link to what went out") {
		t.Fatalf("publish should add the Published box, body:\n%s", issue.BodyRaw)
	}
}

func TestShowNamesTheDepartmentAndItsFile(t *testing.T) {
	proj := makeDepartmentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runShow(ctx, []string{"seo/faq-page"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	assertContains(t, got, "Department: Marketing")
	assertContains(t, got, filepath.Join("workflows", "marketing.yaml"))

	out.Reset()
	if err := runStart(ctx, []string{"seo/faq-page"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Department: Marketing")
	assertContains(t, out.String(), "Workflow file: ")

	out.Reset()
	if err := runShow(ctx, []string{"cli/tool"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Department: Product")
}

func TestShowNamesADepartmentWithGoalsOnce(t *testing.T) {
	proj := makeDepartmentProject(t)
	proj.Departments[0].Mission = "Every client site refreshed every week"
	proj.Departments[0].Goals = []tracker.Goal{{ID: "pages", Name: "Client pages refreshed"}}
	ctx, out, _ := newTestContext(proj, false)
	for _, run := range []func(*Context, []string) error{runShow, runStart} {
		out.Reset()
		if err := run(ctx, []string{"seo/faq-page"}); err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(out.String(), "Department: Marketing"); n != 1 {
			t.Fatalf("Department: Marketing printed %d times, want once:\n%s", n, out)
		}
		assertContains(t, out.String(), "Department: Marketing (Every client site refreshed every week) — goals: issue-cli goals marketing")
	}
}

func TestBrokenDepartmentFileStopsOnlyItsIssues(t *testing.T) {
	proj := makeDepartmentProject(t)
	writeDeptFile(t, proj.DepartmentWorkflowPath("Marketing"), "statuses: [: broken\n")
	ctx, _, _ := newTestContext(proj, false)
	err := runTransition(ctx, []string{"seo/faq-page", "--to", "draft"})
	if err == nil || !strings.Contains(err.Error(), "marketing.yaml") || !strings.Contains(err.Error(), "department Marketing") {
		t.Fatalf("transition on a broken department file: err = %v, want it to name the department and file", err)
	}
	if err := runTransition(ctx, []string{"cli/tool", "--to", "backlog"}); err != nil {
		t.Fatalf("Product issue with a broken department file: %v", err)
	}
}

func TestSetMetaRefusesAMoveToAWorkflowWithoutTheStatus(t *testing.T) {
	proj := makeDepartmentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	err := runSetMeta(ctx, []string{"cli/tool", "--key", "department", "--value", "Marketing"})
	if err == nil || !strings.Contains(err.Error(), "brief, draft, owner review, publish, done") || !strings.Contains(err.Error(), "--status") {
		t.Fatalf("move idea → Marketing: err = %v, want the target statuses and the --status retry", err)
	}
	if err := runSetMeta(ctx, []string{"cli/tool", "--key", "department", "--value", "Marketing", "--status", "brief"}); err != nil {
		t.Fatalf("move with --status: %v", err)
	}
	issue := loadIssueByPath(t, proj.IssueDir, filepath.Join(proj.IssueDir, "CLI", "tool.md"))
	if issue.Status != "brief" || proj.DepartmentOf(issue) != "Marketing" {
		t.Fatalf("after the move: status %q, department %q\n%s", issue.Status, proj.DepartmentOf(issue), out)
	}
	if err := runSetMeta(ctx, []string{"cli/tool", "--key", "waiting", "--value", "x", "--status", "brief"}); err == nil {
		t.Fatal("--status with an unrelated key was accepted")
	}
}

func TestCreateAndListByDepartment(t *testing.T) {
	proj := makeDepartmentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runCreate(ctx, []string{"--title", "New post", "--department", "Marketing", "--status", "brief"}); err != nil {
		t.Fatalf("create in Marketing: %v", err)
	}
	assertContains(t, out.String(), "Department: Marketing")
	if err := runCreate(ctx, []string{"--title", "Wrong one", "--department", "Marketing", "--status", "idea"}); err == nil {
		t.Fatal("create with Product's status in Marketing was accepted")
	}
	if err := runCreate(ctx, []string{"--title", "x", "--department", "Nope"}); err == nil || !strings.Contains(err.Error(), "Marketing") {
		t.Fatalf("unknown department: err = %v, want the department list", err)
	}

	out.Reset()
	if err := runList(ctx, []string{"--department", "Marketing", "--all"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "faq-page") || !strings.Contains(got, "new-post") || strings.Contains(got, "cli/tool") {
		t.Fatalf("list --department Marketing:\n%s", got)
	}
}

func TestProcessWorkflowPerDepartment(t *testing.T) {
	proj := makeDepartmentProject(t)
	ctx, out, _ := newTestContext(proj, false)
	if err := runProcessWorkflow(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Marketing: workflows/marketing.yaml")
	assertContains(t, out.String(), "--department Marketing")

	out.Reset()
	if err := runProcessWorkflow(ctx, []string{"--department", "Marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "owner review")

	out.Reset()
	if err := runProcessTransitions(ctx, []string{"seo/faq-page"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "owner review")
}

func TestNextListsDepartmentWorkApart(t *testing.T) {
	proj := makeDepartmentProject(t)
	proj.Version = "0.1"
	p := filepath.Join(proj.IssueDir, "SEO", "faq-page.md")
	writeDeptFile(t, p, "---\ntitle: \"faq page\"\nstatus: \"brief\"\nsystem: \"SEO\"\nversion: \"0.1\"\n---\n\nAn FAQ page.\n")
	ctx, out, _ := newTestContext(proj, false)
	if err := runNext(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Marketing — open on workflows/marketing.yaml (1)")
	assertContains(t, out.String(), "seo/faq-page")
}

func TestWorkflowInitOutWritesADepartmentFile(t *testing.T) {
	dir := realTempDir(t)
	target := filepath.Join(dir, "workflows", "seo.yaml")
	var out strings.Builder
	if err := doWorkflowInitOut("content", target, false, strings.NewReader(""), &out, false); err != nil {
		t.Fatal(err)
	}
	wf, err := tracker.LoadWorkflow(target)
	if err != nil || wf.Template == "" || !strings.HasPrefix(wf.Template, "content@") {
		t.Fatalf("written file: template %q, err %v", wf.Template, err)
	}
	assertContains(t, out.String(), "workflow: "+filepath.ToSlash(target))
	assertContains(t, out.String(), "from a template: issue-cli department list")
	if _, err := os.Stat(filepath.Join(dir, "issues")); err == nil {
		t.Fatal("--out scaffolded issues/: a department file is not a project")
	}
	if err := doWorkflowInitOut("content", target, false, strings.NewReader(""), &out, false); err == nil {
		t.Fatal("--out overwrote without --force")
	}
}

func TestWorkflowFileForDepartment(t *testing.T) {
	proj := makeDepartmentProject(t)
	got, err := workflowFileFor(proj, "marketing")
	if err != nil || got != proj.DepartmentWorkflowPath("Marketing") {
		t.Fatalf("workflowFileFor(marketing) = %q, %v", got, err)
	}
	proj.Departments = append(proj.Departments, tracker.Department{Name: "Sales"})
	if _, err := workflowFileFor(proj, "Sales"); err == nil || !strings.Contains(err.Error(), "--out workflows/sales.yaml") {
		t.Fatalf("department without a file: err = %v", err)
	}
}

// writeDeptFile writes content to path, making its folders.
func writeDeptFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
