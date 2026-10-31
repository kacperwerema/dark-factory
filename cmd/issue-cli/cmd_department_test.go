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
	assertContains(t, s, "gtm        GTM (group: marketing) (not installed)")
	assertContains(t, s, "department add marketing")

	ctx, out = goalsCtx(t, proj, true)
	if err := runDepartment(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	var rows []departmentListRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) < 4 || rows[0].Key != "hq" || rows[2].Key != "marketing" || rows[2].Installed || rows[3].Key != "gtm" || strings.Join(rows[3].Group, ",") != "marketing" {
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
	assertContains(t, s, `create --department marketing --title "<first brief>"`)
	assertContains(t, s, "Edit departments/marketing/role.md to say what this department should know, and each job's brief (departments/marketing/jobs/weekly-seo-pass.md).")
	assertContains(t, s, "connectors marketing prints the config")
	assertContains(t, s, "Types of work: piece (default), foundation, market-size, seo-pass. Pick one with issue-cli")
	after, _ := os.ReadFile(cfg)
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatalf("projects.yaml lines changed:\n%s", after)
	}

	proj = reloadDeptProject(t, cfg)
	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"show", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Mission: Your site(s) refreshed every week")
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

	// Again: refused (TestDepartmentAddRefusesAnInstalledTemplate).
	ctx, _ = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err == nil || !strings.Contains(err.Error(), "a template installs once") {
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
	assertContains(t, out.String(), "workflow init --template marketing --out workflows/marketing.template.yaml")
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
		{[]string{"add", "nope"}, "templates: marketing, gtm"},
		{[]string{"add", "marketing", "--name", "SEO"}, "name"},
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

// A second add of a template the project has is refused with the
// library's message, the one the viewer's Add answers 409 with.
func TestDepartmentAddRefusesAnInstalledTemplate(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	proj = reloadDeptProject(t, cfg)
	before, _ := os.ReadFile(cfg)
	for _, args := range [][]string{{"add", "marketing"}, {"add", "marketing", "--dry-run"}} {
		ctx, _ = goalsCtx(t, proj, false)
		err := runDepartment(ctx, args)
		want := "Marketing is already a department of " + proj.Name + " (" + cfg + "): a template installs once. To add it again, remove it first: issue-cli --project " + proj.Slug + " department remove marketing"
		if err == nil || err.Error() != want {
			t.Fatalf("%v:\n got %v\nwant %s", args, err, want)
		}
	}
	if after, _ := os.ReadFile(cfg); string(after) != string(before) {
		t.Fatalf("a refused add changed projects.yaml:\n%s", after)
	}
}

func TestDepartmentRemove(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	before, _ := os.ReadFile(cfg)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	proj = reloadDeptProject(t, cfg)
	mustWrite(t, filepath.Join(proj.IssueDir, "faq.md"), "---\ntitle: \"FAQ\"\nstatus: \"draft\"\ndepartment: \"Marketing\"\n---\nBody.\n")

	ctx, _ = goalsCtx(t, proj, false)
	err := runDepartment(ctx, []string{"remove", "marketing"})
	if err == nil || !strings.Contains(err.Error(), "1 open issue") || !strings.Contains(err.Error(), "set-meta faq --key department") {
		t.Fatalf("remove with open issues: %v", err)
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"remove", "product"}); err == nil || !strings.Contains(err.Error(), "built in") {
		t.Fatalf("remove product: %v", err)
	}

	mustWrite(t, filepath.Join(proj.IssueDir, "faq.md"), "---\ntitle: \"FAQ\"\nstatus: \"done\"\ndepartment: \"Marketing\"\n---\nBody.\n")
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"remove", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Removed department Marketing")
	assertContains(t, out.String(), "Files left in place")
	assertContains(t, out.String(), "workflows/marketing.yaml")
	if after, _ := os.ReadFile(cfg); string(after) != string(before) {
		t.Fatalf("after remove:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(proj.Root(), "workflows", "marketing.yaml")); err != nil {
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

// department add gtm installs GTM and Marketing under it; list, remove and
// --parent know the group.
func TestDepartmentAddGroup(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "gtm", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "In group GTM: department Marketing (template marketing)")
	assertContains(t, out.String(), "parent: gtm")

	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "gtm"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "✓ Added department GTM (template gtm)")
	assertContains(t, s, "departments/gtm/role.md: wrote")
	assertContains(t, s, "workflows/marketing.yaml: wrote")
	assertContains(t, s, "GTM is a group: its lead is the lead of its departments' leads")
	assertContains(t, s, "Edit departments/gtm/role.md and departments/marketing/role.md")

	proj = reloadDeptProject(t, cfg)
	if g := proj.GroupOf("marketing"); g == nil || g.Name != "GTM" {
		t.Fatalf("marketing's group = %v", g)
	}
	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "marketing  Marketing ✓ installed · in group GTM")

	ctx, _ = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"remove", "gtm"}); err == nil || !strings.Contains(err.Error(), "GTM is a group with departments under it (marketing)") {
		t.Fatalf("remove group: %v", err)
	}
	// Marketing is in: a template installs once, under a group or not.
	ctx, _ = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing", "--parent", "gtm"}); err == nil || !strings.Contains(err.Error(), "a template installs once") {
		t.Fatalf("marketing again under gtm: %v", err)
	}
	// Taken out, it goes back in under the group with --parent.
	ctx, _ = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"remove", "marketing"}); err != nil {
		t.Fatal(err)
	}
	proj = reloadDeptProject(t, cfg)
	ctx, _ = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing", "--parent", "nope"}); err == nil || !strings.Contains(err.Error(), `no department "nope" to put it under`) {
		t.Fatalf("--parent nope: %v", err)
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing", "--parent", "gtm"}); err != nil {
		t.Fatal(err)
	}
	if kids := reloadDeptProject(t, cfg).Children("gtm"); len(kids) != 1 || kids[0].Name != "Marketing" {
		t.Fatalf("children = %+v", kids)
	}
}

// deptGitEnv keeps the machine's git config (hooks, identity) out of a
// sandbox repository the test commits in.
func deptGitEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
}

// TestDepartmentAddNamesUntrackedFiles: after add, the files git does not
// track are named with the commit to make; nothing is committed without
// --commit.
func TestDepartmentAddNamesUntrackedFiles(t *testing.T) {
	asLead(t)
	deptGitEnv(t)
	proj, _ := deptTemplateProject(t)
	root := proj.Root()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "init")
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "New files git does not track yet: departments/marketing/jobs/weekly-seo-pass.md, departments/marketing/role.md, workflows/marketing.yaml")
	assertContains(t, s, "add departments/marketing/jobs/weekly-seo-pass.md departments/marketing/role.md workflows/marketing.yaml && git -C")
	assertContains(t, s, "commit -m 'chore(departments): add the Marketing department' --")
	assertContains(t, s, "projects.yaml is in this repository and has uncommitted changes, the new department entry among them")
	if st := git(t, root, "status", "--porcelain"); !strings.Contains(st, "?? departments/") {
		t.Fatalf("add committed without --commit:\n%s", st)
	}
}

func TestDepartmentAddCommitFlag(t *testing.T) {
	asLead(t)
	deptGitEnv(t)
	proj, _ := deptTemplateProject(t)
	root := proj.Root()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "init")
	mustWrite(t, filepath.Join(root, "mine.txt"), "not the department's\n")
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "gtm", "--commit"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Committed ")
	st := git(t, root, "status", "--porcelain")
	if strings.Contains(st, "departments/") || strings.Contains(st, "workflows/") || !strings.Contains(st, "?? mine.txt") {
		t.Fatalf("status after --commit:\n%s", st)
	}
	files := git(t, root, "show", "--name-only", "--format=%s", "HEAD")
	for _, f := range []string{"chore(departments): add the GTM department", "workflows/gtm.yaml", "workflows/marketing.yaml", "departments/marketing/role.md", "departments/gtm/jobs/weekly-plan.md"} {
		assertContains(t, files, f)
	}
}

func TestDepartmentUpdateKeepsEditsAndReplacesOldText(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	// Make it the earlier template's install: old values in projects.yaml,
	// role.md edited by the owner.
	raw, _ := os.ReadFile(cfg)
	old := strings.NewReplacer("role: SEO and content for your site(s)", "role: SEO and content for the client sites",
		"name: Pages refreshed", "name: Client pages refreshed").Replace(string(raw))
	mustWrite(t, cfg, old)
	role := filepath.Join(proj.Root(), "departments", "marketing", "role.md")
	mustWrite(t, role, "# Ours\n")
	proj = reloadDeptProject(t, cfg)

	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"update", "marketing", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "Dry run: department update marketing (nothing changed)")
	assertContains(t, s, `role (`+cfg+`): would update: "SEO and content for the client sites" → "SEO and content for your site(s)"`)
	assertContains(t, s, "departments/marketing/role.md: kept: not an earlier template's text")
	if b, _ := os.ReadFile(cfg); string(b) != old {
		t.Fatal("dry run changed projects.yaml")
	}

	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"update", "marketing", "--side"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Updated Marketing to the marketing template's text")
	assertContains(t, out.String(), "Wrote the template's text next to your file: departments/marketing/role.template.md")
	if b, _ := os.ReadFile(role); string(b) != "# Ours\n" {
		t.Fatalf("edited role.md overwritten: %q", b)
	}
	b, _ := os.ReadFile(cfg)
	assertContains(t, string(b), "role: SEO and content for your site(s)")
	assertContains(t, string(b), "name: Pages refreshed")

	ctx, out = goalsCtx(t, reloadDeptProject(t, cfg), false)
	if err := runDepartment(ctx, []string{"update", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Nothing to update in Marketing")
}
