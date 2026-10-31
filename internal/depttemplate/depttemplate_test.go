package depttemplate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/jobs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

const commentedConfig = `# The owner's projects.
viewer_url: "http://127.0.0.1:8090"   # keep this comment

projects:
  # first project
  - name: Acme
    issues: ./acme/issues     # trailing comment
    workdir: ROOT
    company:
      mission: "Grow"
  - name: Other
    issues: ./other/issues
    departments:
      - name: Sales
        role: "Calls"     # sales comment
      - name: Product
        prompt: departments/product/role.md
`

// sandbox writes the config and returns its path and the Acme project.
func sandbox(t *testing.T, config string) (string, *tracker.Project) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "acme")
	if err := os.MkdirAll(filepath.Join(root, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}
	config = strings.ReplaceAll(config, "ROOT", root)
	path := filepath.Join(dir, "projects.yaml")
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, loadProject(t, path, "acme")
}

func loadProject(t *testing.T, path, slug string) *tracker.Project {
	t.Helper()
	projects, err := tracker.LoadProjects(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range projects {
		if projects[i].Slug == slug {
			return &projects[i]
		}
	}
	t.Fatalf("no project %s", slug)
	return nil
}

func TestListHasMarketing(t *testing.T) {
	if keys := strings.Join(Keys(), ","); keys != "marketing,gtm" {
		t.Fatalf("templates = %s, want marketing,gtm (departments, then groups)", keys)
	}
	m, err := Load("marketing")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Marketing" || m.Description == "" || m.Workflow != "marketing" || len(m.Goals) == 0 || len(m.RolePrompt) == 0 {
		t.Fatalf("marketing template = %+v", m)
	}
	if _, err := Load("nope"); err == nil {
		t.Fatal("Load(nope) should fail")
	}
	if _, err := Load("../workflow"); err == nil {
		t.Fatal("Load(../workflow) should fail")
	}
}

func TestAddWritesFilesAndKeepsTheRestOfTheConfig(t *testing.T) {
	path, proj := sandbox(t, commentedConfig)
	before, _ := os.ReadFile(path)
	tmpl, _ := Load("marketing")
	plan, err := NewPlan(proj, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range plan.Files {
		if f.State != FileWrite {
			t.Fatalf("%s: state %s, want write", f.Path, f.State)
		}
	}
	if err := plan.Apply(proj); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	// Every line of the old file is still there, in order; the new ones
	// are the entry.
	added := diffAdded(t, string(before), string(after))
	if !strings.HasPrefix(added, "    departments:\n      - name: Marketing\n") {
		t.Fatalf("added:\n%s", added)
	}
	got := loadProject(t, path, "acme")
	d := got.DepartmentByKey("marketing")
	if d == nil || d.Workflow != "workflows/marketing.yaml" || d.Prompt != "departments/marketing/role.md" || len(d.Goals) != 2 || d.Color != "#E2559B" || len(d.Systems) != 1 {
		t.Fatalf("department = %+v", d)
	}
	if len(d.Jobs) != 1 || d.Jobs[0].Type != "seo-pass" {
		t.Fatalf("jobs = %+v, want weekly-seo-pass with type seo-pass", d.Jobs)
	}
	if other := loadProject(t, path, "other"); len(other.Departments) != 1 || other.Product == nil {
		t.Fatalf("other project changed: %+v", other.Departments)
	}
	wf, _ := os.ReadFile(filepath.Join(proj.Root(), "workflows", "marketing.yaml"))
	want, _ := workflowupgrade.ReadTemplate("marketing")
	if string(wf) != string(want) {
		t.Fatal("workflows/marketing.yaml is not the marketing template")
	}
	if b, _ := os.ReadFile(filepath.Join(proj.Root(), "departments", "marketing", "role.md")); string(b) != string(tmpl.RolePrompt) {
		t.Fatal("role.md not written")
	}

	// A second add is refused: a template installs once.
	_, err = NewPlan(got, tmpl)
	var inst *InstalledError
	if !errors.As(err, &inst) || inst.Dept.Name != "Marketing" {
		t.Fatalf("second add: %v", err)
	}
	if want := "Marketing is already a department of Acme (" + path + "): a template installs once. To add it again, remove it first: issue-cli --project acme department remove marketing"; err.Error() != want {
		t.Fatalf("second add:\n got %s\nwant %s", err, want)
	}

	// Removing it gives back the file as it was.
	if err := RemoveEntry(path, "acme", "Marketing"); err != nil {
		t.Fatal(err)
	}
	if final, _ := os.ReadFile(path); string(final) != string(before) {
		t.Fatalf("after remove:\n%s\nwant:\n%s", final, before)
	}
}

func TestAddToAnExistingList(t *testing.T) {
	path, _ := sandbox(t, commentedConfig)
	other := loadProject(t, path, "other")
	other.WorkDir = filepath.Dir(path)
	before, _ := os.ReadFile(path)
	tmpl, _ := Load("marketing")
	plan, err := NewPlan(other, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(other); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if added := diffAdded(t, string(before), string(after)); !strings.HasPrefix(added, "      - name: Marketing\n        color:") {
		t.Fatalf("added:\n%s", added)
	}
	got := loadProject(t, path, "other")
	if len(got.Departments) != 2 || got.Product == nil {
		t.Fatalf("departments = %+v product = %v", got.Departments, got.Product)
	}
	if err := RemoveEntry(path, "other", "marketing"); err != nil {
		t.Fatal(err)
	}
	if final, _ := os.ReadFile(path); string(final) != string(before) {
		t.Fatalf("after remove:\n%s", final)
	}
}

func TestExistingFilesAreNeverOverwritten(t *testing.T) {
	path, proj := sandbox(t, commentedConfig)
	tmpl, _ := Load("marketing")
	wfPath := filepath.Join(proj.Root(), "workflows", "marketing.yaml")
	rolePath := filepath.Join(proj.Root(), "departments", "marketing", "role.md")
	os.MkdirAll(filepath.Dir(wfPath), 0o755)
	os.MkdirAll(filepath.Dir(rolePath), 0o755)
	os.WriteFile(wfPath, []byte("# my edited workflow\n"), 0o644)
	os.WriteFile(rolePath, tmpl.RolePrompt, 0o644)
	plan, err := NewPlan(proj, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, f := range plan.Files {
		states[f.Path] = f.State
	}
	if states["workflows/marketing.yaml"] != FileKeep || states["departments/marketing/role.md"] != FileSame {
		t.Fatalf("states = %v", states)
	}
	if err := plan.Apply(proj); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(wfPath); string(b) != "# my edited workflow\n" {
		t.Fatalf("edited workflow overwritten: %q", b)
	}
	if loadProject(t, path, "acme").DepartmentByKey("marketing") == nil {
		t.Fatal("entry not added")
	}
}

func TestRefusals(t *testing.T) {
	_, proj := sandbox(t, commentedConfig)
	tmpl, _ := Load("marketing")
	for _, name := range []string{"Product", "hq", "HQ", "!!"} {
		named := *tmpl
		named.Name = name
		if _, err := NewPlan(proj, &named); err == nil {
			t.Errorf("NewPlan(template named %q) should fail", name)
		}
	}
	noConfig := *proj
	noConfig.ConfigPath = ""
	if _, err := NewPlan(&noConfig, tmpl); err == nil {
		t.Error("NewPlan without a config should fail")
	}
}

func TestBrokenResultIsNotWritten(t *testing.T) {
	path, _ := sandbox(t, commentedConfig)
	before, _ := os.ReadFile(path)
	if err := AddEntry(path, "acme", "Marketing", "- name: [unclosed\n"); err == nil || !strings.Contains(err.Error(), "left unchanged") {
		t.Fatalf("err = %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("config changed")
	}
	if err := RemoveEntry(path, "acme", "Marketing"); err == nil {
		t.Fatal("remove from a project without departments should fail")
	}
}

func TestAddFollowsASymlink(t *testing.T) {
	path, proj := sandbox(t, commentedConfig)
	link := filepath.Join(filepath.Dir(path), "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Skip(err)
	}
	if err := AddEntry(link, "acme", "Marketing", "- name: Marketing\n"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a file")
	}
	if loadProject(t, path, proj.Slug).DepartmentByKey("marketing") == nil {
		t.Fatal("target not edited")
	}
}

// diffAdded checks after is before with one block of lines inserted, and
// returns that block.
func diffAdded(t *testing.T, before, after string) string {
	t.Helper()
	b, a := splitLines([]byte(before)), splitLines([]byte(after))
	i := 0
	for i < len(b) && b[i] == a[i] {
		i++
	}
	j := 0
	for j < len(b)-i && b[len(b)-1-j] == a[len(a)-1-j] {
		j++
	}
	if i+j != len(b) {
		t.Fatalf("old lines changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	return strings.Join(a[i:len(a)-j], "")
}

func TestAddInstallsTheTemplatesJobs(t *testing.T) {
	path, proj := sandbox(t, commentedConfig)
	tmpl, err := Load("marketing")
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl.Jobs) != 1 || tmpl.Jobs[0].ID != "weekly-seo-pass" {
		t.Fatalf("template jobs = %+v", tmpl.Jobs)
	}
	plan, err := NewPlan(proj, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(proj); err != nil {
		t.Fatal(err)
	}
	got := loadProject(t, path, "acme")
	d := got.DepartmentByKey("marketing")
	if d == nil || len(d.Jobs) != 1 {
		t.Fatalf("department = %+v", d)
	}
	j := d.Jobs[0]
	if j.Brief != "departments/marketing/jobs/weekly-seo-pass.md" || j.Every != "week" || j.On != "mon" || j.At != "07:00" || j.Name != "Weekly SEO pass" {
		t.Fatalf("job = %+v", j)
	}
	brief, err := os.ReadFile(filepath.Join(proj.Root(), filepath.FromSlash(j.Brief)))
	if err != nil || !strings.Contains(string(brief), "{{period}}") {
		t.Fatalf("brief: %v\n%s", err, brief)
	}
	if r, err := jobs.Find(got, "marketing/weekly-seo-pass"); err != nil || r.Err != nil {
		t.Fatalf("installed job: %v %+v", err, r)
	}
}

// department add gtm: the group, then Marketing under it; a second run on
// a project that has Marketing already moves it under the group and keeps
// its edited files.
func TestAddGroupInstallsItsDepartments(t *testing.T) {
	path, proj := sandbox(t, commentedConfig)
	gtm, err := Load("gtm")
	if err != nil {
		t.Fatal(err)
	}
	// GTM's own issues (its weekly plan's runs) walk the planning workflow.
	if !gtm.Group || len(gtm.Children) != 1 || gtm.Workflow != "planning" || len(gtm.Jobs) != 1 || gtm.Jobs[0].ID != "weekly-plan" {
		t.Fatalf("gtm template = %+v", gtm)
	}
	plan, err := NewPlan(proj, gtm)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range plan.Files {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, ",") != "workflows/gtm.yaml,departments/gtm/role.md,departments/gtm/jobs/weekly-plan.md" || len(plan.Children) != 1 || plan.Children[0].Parent != "gtm" {
		t.Fatalf("plan files %v children %+v", paths, plan.Children)
	}
	if err := plan.Apply(proj); err != nil {
		t.Fatal(err)
	}
	got := loadProject(t, path, "acme")
	if g := got.GroupOf("marketing"); g == nil || g.Name != "GTM" || g.Workflow != "workflows/gtm.yaml" || len(g.Jobs) != 1 || g.Jobs[0].Brief != "departments/gtm/jobs/weekly-plan.md" {
		t.Fatalf("marketing's group = %+v, departments %+v", g, got.Departments)
	}
	if _, err := os.Stat(filepath.Join(proj.Root(), "workflows", "marketing.yaml")); err != nil {
		t.Fatal("marketing's workflow not written")
	}

	// Marketing installed on its own first, its role edited: adding gtm
	// keeps the file and gives the entry parent: gtm.
	path, proj = sandbox(t, commentedConfig)
	mkt, _ := Load("marketing")
	p, _ := NewPlan(proj, mkt)
	if err := p.Apply(proj); err != nil {
		t.Fatal(err)
	}
	role := filepath.Join(proj.Root(), "departments", "marketing", "role.md")
	if err := os.WriteFile(role, []byte("my edits"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj = loadProject(t, path, "acme")
	plan, err = NewPlan(proj, gtm)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Children) != 0 || strings.Join(plan.Reparent, ",") != "Marketing" {
		t.Fatalf("plan children %v reparent %v", plan.Children, plan.Reparent)
	}
	if err := plan.Apply(proj); err != nil {
		t.Fatal(err)
	}
	got = loadProject(t, path, "acme")
	if g := got.GroupOf("marketing"); g == nil || g.Name != "GTM" {
		t.Fatalf("marketing not moved under GTM: %+v", got.Departments)
	}
	if b, _ := os.ReadFile(role); string(b) != "my edits" {
		t.Fatalf("edited role.md overwritten: %q", b)
	}
	// A third run: GTM is there, refused like any template.
	if _, err := NewPlan(got, gtm); err == nil || !strings.Contains(err.Error(), "GTM is already a department of Acme (") || !strings.Contains(err.Error(), "a template installs once") {
		t.Fatalf("second gtm add: %v", err)
	}
}

func TestNewPlanUnder(t *testing.T) {
	path, proj := sandbox(t, commentedConfig)
	gtm, _ := Load("gtm")
	mkt, _ := Load("marketing")
	if _, err := NewPlanUnder(proj, mkt, "gtm"); err == nil || !strings.Contains(err.Error(), `no department "gtm" to put it under; groups: none yet`) {
		t.Fatalf("no group: %v", err)
	}
	if _, err := NewPlanUnder(proj, mkt, "product"); err == nil || !strings.Contains(err.Error(), "built in and never a group") {
		t.Fatalf("product: %v", err)
	}
	if _, err := NewPlanUnder(proj, gtm, "marketing"); err == nil || !strings.Contains(err.Error(), `no department "marketing"`) {
		t.Fatalf("group under a department: %v", err)
	}
	p, _ := NewPlan(proj, gtm)
	if err := p.Apply(proj); err != nil {
		t.Fatal(err)
	}
	proj = loadProject(t, path, "acme")
	// Marketing is in now: another template (Outreach, on the content
	// workflow) stands in for the next department under GTM.
	outreach := *mkt
	outreach.Name, outreach.Workflow = "Outreach", "content"
	// Under a child is a nested group now (factory/nested-groups-…#D-1).
	if under, err := NewPlanUnder(proj, &outreach, "marketing"); err != nil || under.Parent != "marketing" || !strings.Contains(under.Entry, "parent: marketing") {
		t.Fatalf("under a child: %v %+v", err, under)
	}
	p, err := NewPlanUnder(proj, &outreach, "GTM")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(proj); err != nil {
		t.Fatal(err)
	}
	if kids := loadProject(t, path, "acme").Children("gtm"); len(kids) != 2 || kids[1].Name != "Outreach" {
		t.Fatalf("children = %+v", kids)
	}
}

// A department installed under another name (the old --name) is still the
// template's install: its workflow file carries the template's stamp.
func TestInstalledUnderAnotherNameByItsWorkflowStamp(t *testing.T) {
	_, proj := sandbox(t, commentedConfig)
	tmpl, _ := Load("marketing")
	wf, _ := workflowupgrade.ReadTemplate("marketing")
	if err := os.MkdirAll(filepath.Join(proj.Root(), "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(proj.Root(), "workflows", "growth.yaml"), wf, 0o644)
	os.WriteFile(filepath.Join(proj.Root(), "workflows", "sales.yaml"), []byte("template: content@v0.1.0\n"), 0o644)
	proj.Departments = []tracker.Department{{Name: "Sales", Workflow: "workflows/sales.yaml"}, {Name: "Plain"}}
	if tmpl.Installed(proj) {
		t.Fatal("Sales runs another template's workflow: Marketing is not installed")
	}
	proj.Departments = append(proj.Departments, tracker.Department{Name: "Growth", Workflow: "workflows/growth.yaml"})
	if d := tmpl.InstalledAs(proj); d == nil || d.Name != "Growth" {
		t.Fatalf("InstalledAs = %+v, want Growth", d)
	}
	_, err := NewPlan(proj, tmpl)
	if err == nil || !strings.Contains(err.Error(), "Marketing is already a department of Acme as Growth (") || !strings.Contains(err.Error(), "department remove growth") {
		t.Fatalf("NewPlan = %v", err)
	}
}

// TestNewPlanUnderNestedDepth: a department may go under a nested group
// while it stays within tracker.MaxGroupDepth; a group template counts its
// departments one deeper
// (factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it#D-1).
func TestNewPlanUnderNestedDepth(t *testing.T) {
	proj := &tracker.Project{Slug: "acme", Departments: []tracker.Department{
		{Name: "Z"}, {Name: "Y", Parent: "z"}, {Name: "X", Parent: "y"}, {Name: "W", Parent: "x"},
		{Name: "LoopA", Parent: "loopb"}, {Name: "LoopB", Parent: "loopa"},
	}}
	gtm, _ := Load("gtm")
	mkt, _ := Load("marketing")
	if _, err := NewPlanUnder(proj, mkt, "w"); err == nil || !strings.Contains(err.Error(), "the department would sit 4 groups deep: groups nest at most 3 deep") || !strings.Contains(err.Error(), "W → X → Y → Z") {
		t.Errorf("marketing under w: %v", err)
	}
	if _, err := NewPlanUnder(proj, gtm, "x"); err == nil || !strings.Contains(err.Error(), "GTM's departments would sit 4 groups deep") {
		t.Errorf("gtm under x: %v", err)
	}
	if _, err := NewPlanUnder(proj, mkt, "loopa"); err == nil || !strings.Contains(err.Error(), "parent: chain is broken") {
		t.Errorf("under a loop: %v", err)
	}
	for _, c := range []struct {
		t      *Template
		parent string
	}{{mkt, "x"}, {gtm, "y"}} {
		if _, err := NewPlanUnder(proj, c.t, c.parent); err != nil && strings.Contains(err.Error(), "deep") {
			t.Errorf("%s under %s fits: %v", c.t.Key, c.parent, err)
		}
	}
}
