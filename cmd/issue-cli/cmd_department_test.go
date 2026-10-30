package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const deptTemplateConfig = `# owner's config
projects:
  - name: Acme   # the sandbox
    issues: ISSUES
    workdir: ROOT
`

// deptTemplateProject is a sandbox project loaded from a projects.yaml in
// its own temp dir, with one Product issue.
func deptTemplateProject(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(issues, "engine.md"), "---\ntitle: \"Engine\"\nstatus: \"idea\"\n---\nBody.\n")
	cfg := filepath.Join(root, "projects.yaml")
	mustWrite(t, cfg, strings.NewReplacer("ISSUES", issues, "ROOT", root).Replace(deptTemplateConfig))
	return reloadDeptProject(t, cfg), cfg
}

func reloadDeptProject(t *testing.T, cfg string) *tracker.Project {
	t.Helper()
	projects, err := tracker.LoadProjects(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &projects[0]
}

func TestDepartmentListShowsTemplates(t *testing.T) {
	proj, _ := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "== Built in ==")
	assertContains(t, s, "product    Product")
	assertContains(t, s, "workflow: development")
	assertContains(t, s, "== Templates ==")
	assertContains(t, s, "marketing  Marketing (not installed)")
	assertContains(t, s, "department add marketing")

	ctx, out = goalsCtx(t, proj, true)
	if err := runDepartment(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	var rows []departmentListRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) < 3 || rows[0].Key != "hq" || rows[2].Key != "marketing" || rows[2].Installed {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestDepartmentAddInstallsMarketing(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	before, _ := os.ReadFile(cfg)

	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Dry run: department add marketing (nothing changed)")
	assertContains(t, out.String(), "workflows/marketing.yaml: write")
	assertContains(t, out.String(), "- name: Marketing")
	if after, _ := os.ReadFile(cfg); string(after) != string(before) {
		t.Fatal("dry run changed projects.yaml")
	}
	if _, err := os.Stat(filepath.Join(proj.Root(), "workflows")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote files")
	}

	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "✓ Added department Marketing (template marketing)")
	assertContains(t, s, "workflows/marketing.yaml: wrote")
	assertContains(t, s, "departments/marketing/role.md: wrote")
	assertContains(t, s, "lead spawn --department marketing")
	after, _ := os.ReadFile(cfg)
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatalf("projects.yaml lines changed:\n%s", after)
	}

	proj = reloadDeptProject(t, cfg)
	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"show", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Mission: Every client site refreshed every week")
	assertContains(t, out.String(), "# Marketing: role")
	ctx, out = goalsCtx(t, proj, false)
	if err := runGoals(ctx, []string{"marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "no value yet: issue-cli goal set marketing/pages <value>")
	if _, err := proj.WorkflowFor("Marketing"); err != nil {
		t.Fatalf("department workflow: %v", err)
	}

	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "marketing  Marketing ✓ installed")

	// Again: refused. Under another name, with the existing role.md kept.
	ctx, _ = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err == nil || !strings.Contains(err.Error(), "already in projects.yaml") {
		t.Fatalf("second add: %v", err)
	}
}

func TestDepartmentAddKeepsEditedFiles(t *testing.T) {
	asLead(t)
	proj, _ := deptTemplateProject(t)
	if err := os.MkdirAll(filepath.Join(proj.Root(), "workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(proj.Root(), "workflows", "marketing.yaml"), "# mine\n")
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "workflows/marketing.yaml: kept your edited file")
	if b, _ := os.ReadFile(filepath.Join(proj.Root(), "workflows", "marketing.yaml")); string(b) != "# mine\n" {
		t.Fatalf("edited file overwritten: %q", b)
	}
}

func TestDepartmentAddRefusals(t *testing.T) {
	asLead(t)
	proj, _ := deptTemplateProject(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"add", "nope"}, "templates: marketing"},
		{[]string{"add", "marketing", "--name", "Product"}, "built in"},
		{[]string{"add", "marketing", "--name", "hq"}, "built in"},
		{[]string{"add"}, "usage: issue-cli department add"},
		{[]string{"frob"}, "usage: issue-cli department list|add"},
	} {
		ctx, _ := goalsCtx(t, proj, false)
		err := runDepartment(ctx, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}

	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "engine")
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err == nil || !strings.Contains(err.Error(), "workers never add departments") {
		t.Fatalf("worker add: %v", err)
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing", "--dry-run"}); err != nil {
		t.Fatalf("a worker may dry-run: %v", err)
	}
}

func TestDepartmentRemove(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	before, _ := os.ReadFile(cfg)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing", "--name", "SEO"}); err != nil {
		t.Fatal(err)
	}
	proj = reloadDeptProject(t, cfg)
	mustWrite(t, filepath.Join(proj.IssueDir, "faq.md"), "---\ntitle: \"FAQ\"\nstatus: \"draft\"\ndepartment: \"SEO\"\n---\nBody.\n")

	ctx, _ = goalsCtx(t, proj, false)
	err := runDepartment(ctx, []string{"remove", "seo"})
	if err == nil || !strings.Contains(err.Error(), "1 open issue") || !strings.Contains(err.Error(), "set-meta faq --key department") {
		t.Fatalf("remove with open issues: %v", err)
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"remove", "product"}); err == nil || !strings.Contains(err.Error(), "built in") {
		t.Fatalf("remove product: %v", err)
	}

	mustWrite(t, filepath.Join(proj.IssueDir, "faq.md"), "---\ntitle: \"FAQ\"\nstatus: \"done\"\ndepartment: \"SEO\"\n---\nBody.\n")
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"remove", "seo"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Removed department SEO")
	assertContains(t, out.String(), "Files left in place")
	assertContains(t, out.String(), "workflows/seo.yaml")
	if after, _ := os.ReadFile(cfg); string(after) != string(before) {
		t.Fatalf("after remove:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(proj.Root(), "workflows", "seo.yaml")); err != nil {
		t.Fatal("remove deleted the workflow file")
	}
}

func TestDepartmentShowUnknownNamesList(t *testing.T) {
	proj, _ := deptTemplateProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	err := runDepartment(ctx, []string{"show", "sales"})
	if err == nil || !strings.Contains(err.Error(), "department list") {
		t.Fatalf("err = %v", err)
	}
}
