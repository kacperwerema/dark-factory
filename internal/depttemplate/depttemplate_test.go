package depttemplate

import (
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
	list := List()
	if len(list) == 0 || list[0].Key != "marketing" {
		t.Fatalf("templates = %v, want marketing first", Keys())
	}
	m := list[0]
	if m.Name != "Marketing" || m.Description == "" || m.Workflow != "content" || len(m.Goals) == 0 || len(m.RolePrompt) == 0 {
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
	plan, err := NewPlan(proj, tmpl, "")
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
	if other := loadProject(t, path, "other"); len(other.Departments) != 1 || other.Product == nil {
		t.Fatalf("other project changed: %+v", other.Departments)
	}
	wf, _ := os.ReadFile(filepath.Join(proj.Root(), "workflows", "marketing.yaml"))
	want, _ := workflowupgrade.ReadTemplate("content")
	if string(wf) != string(want) {
		t.Fatal("workflows/marketing.yaml is not the content template")
	}
	if b, _ := os.ReadFile(filepath.Join(proj.Root(), "departments", "marketing", "role.md")); string(b) != string(tmpl.RolePrompt) {
		t.Fatal("role.md not written")
	}

	// A second add of the same name is refused.
	if _, err := NewPlan(got, tmpl, ""); err == nil || !strings.Contains(err.Error(), "already in projects.yaml") {
		t.Fatalf("second add: %v", err)
	}
	// Under another name, it goes after the first entry.
	plan, err = NewPlan(got, tmpl, "SEO")
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(got); err != nil {
		t.Fatal(err)
	}
	got = loadProject(t, path, "acme")
	if len(got.Departments) != 2 || got.Departments[1].Name != "SEO" || got.Departments[1].Workflow != "workflows/seo.yaml" {
		t.Fatalf("departments = %+v", got.Departments)
	}

	// Removing both gives back the file as it was.
	for _, n := range []string{"SEO", "Marketing"} {
		if err := RemoveEntry(path, "acme", n); err != nil {
			t.Fatal(err)
		}
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
	plan, err := NewPlan(other, tmpl, "")
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
	plan, err := NewPlan(proj, tmpl, "")
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
		if _, err := NewPlan(proj, tmpl, name); err == nil {
			t.Errorf("NewPlan(%q) should fail", name)
		}
	}
	noConfig := *proj
	noConfig.ConfigPath = ""
	if _, err := NewPlan(&noConfig, tmpl, ""); err == nil {
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
	plan, err := NewPlan(proj, tmpl, "")
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
