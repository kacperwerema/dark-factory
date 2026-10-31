package projectinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

func tempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func states(p *Plan) map[string]string {
	m := map[string]string{}
	for _, a := range p.Files {
		m[filepath.Base(a.Path)] = a.State
	}
	return m
}

func TestNewPlanChangesNothingAndListsEverything(t *testing.T) {
	root := tempDir(t)
	dir := filepath.Join(root, "shop")
	config := filepath.Join(root, "projects.yaml")
	p, err := NewPlan(Options{Dir: dir, Name: "Acme Shop", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if p.Slug != "acme-shop" || p.Template != DefaultTemplate {
		t.Errorf("slug %q template %q", p.Slug, p.Template)
	}
	want := map[string]string{"shop": StateCreate, "issues": StateCreate, "docs": StateCreate, "workflow.yaml": StateWrite}
	if got := states(p); len(got) != len(want) {
		t.Errorf("files = %v", got)
	} else {
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s, want %s", k, got[k], v)
			}
		}
	}
	if p.Config.State != ConfigCreate || p.Config.Path != config {
		t.Errorf("config = %+v", p.Config)
	}
	for _, line := range []string{"- name: Acme Shop", "slug: acme-shop", "issues: " + filepath.Join(dir, "issues"), "workflow: " + filepath.Join(dir, "workflow.yaml"), "workdir: " + dir} {
		if !strings.Contains(p.Config.Entry, line) {
			t.Errorf("entry missing %q:\n%s", line, p.Config.Entry)
		}
	}
	for _, f := range []string{dir, config} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s exists after a plan only", f)
		}
	}
}

func TestApplyThenASecondRunFindsEverythingThere(t *testing.T) {
	root := tempDir(t)
	dir := filepath.Join(root, "shop")
	config := filepath.Join(root, "cfg", "projects.yaml")
	p, err := NewPlan(Options{Dir: dir, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "workflow.yaml"))
	want, _ := workflowupgrade.ReadTemplate(DefaultTemplate)
	if string(got) != string(want) {
		t.Error("workflow.yaml is not the bundled template")
	}
	if name, _, ok := workflowupgrade.ParseStamp(workflowupgrade.ReadStamp(got)); !ok || name != DefaultTemplate {
		t.Errorf("stamp = %q", workflowupgrade.ReadStamp(got))
	}
	projects, err := tracker.LoadProjects(config)
	if err != nil || len(projects) != 1 || projects[0].Slug != "shop" || projects[0].IssueDir != filepath.Join(dir, "issues") {
		t.Fatalf("config: %+v %v", projects, err)
	}
	cfgBefore, _ := os.ReadFile(config)

	again, err := NewPlan(Options{Dir: dir, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range states(again) {
		if v != StateThere && v != StateSame {
			t.Errorf("second run: %s is %s", k, v)
		}
	}
	if again.Config.State != ConfigThere {
		t.Errorf("second run config: %s", again.Config.State)
	}
	if err := again.Apply(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(config); string(after) != string(cfgBefore) {
		t.Error("second run changed the config")
	}
}

func TestAnEditedWorkflowIsKept(t *testing.T) {
	dir := tempDir(t)
	mine := "statuses: [todo, done]\n"
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlan(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if s := states(p)["workflow.yaml"]; s != StateKeep {
		t.Fatalf("workflow.yaml: %s, want keep", s)
	}
	if p.Config.State != ConfigPrint || p.Config.Path != "" {
		t.Errorf("no --config should print only: %+v", p.Config)
	}
	if err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "workflow.yaml")); string(got) != mine {
		t.Errorf("workflow.yaml overwritten: %q", got)
	}
}

func TestAFileThatAppearsAfterThePlanIsNotOverwritten(t *testing.T) {
	dir := tempDir(t)
	p, err := NewPlan(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte("late\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(); err == nil || !strings.Contains(err.Error(), "appeared") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "workflow.yaml")); string(got) != "late\n" {
		t.Errorf("overwritten: %q", got)
	}
}

func TestConfigAddKeepsTheRestAndRefusesATakenSlug(t *testing.T) {
	root := tempDir(t)
	config := filepath.Join(root, "projects.yaml")
	orig := "# my machine\nviewer_url: http://localhost:8080\nprojects:\n  # the old one\n  - name: Old\n    issues: /x/issues   # keep me\n\nterminal: none\n"
	if err := os.WriteFile(config, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := NewPlan(Options{Dir: filepath.Join(root, "new"), Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if p.Config.State != ConfigAdd {
		t.Fatalf("state %s", p.Config.State)
	}
	if err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(config)
	if !strings.HasPrefix(string(got), "# my machine\nviewer_url: http://localhost:8080\nprojects:\n  # the old one\n  - name: Old\n    issues: /x/issues   # keep me\n  - name: new\n") {
		t.Errorf("config:\n%s", got)
	}
	if !strings.HasSuffix(string(got), "\nterminal: none\n") {
		t.Errorf("tail lost:\n%s", got)
	}
	if info, _ := os.Stat(config); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	if _, err := NewPlan(Options{Dir: filepath.Join(root, "elsewhere"), Slug: "old", Config: config}); err == nil || !strings.Contains(err.Error(), "already has a project old") {
		t.Errorf("taken slug: err = %v", err)
	}
}

func TestBadInputsAreRefused(t *testing.T) {
	root := tempDir(t)
	file := filepath.Join(root, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, o := range map[string]Options{
		"no dir":          {},
		"dir is a file":   {Dir: file},
		"bad template":    {Dir: filepath.Join(root, "a"), Template: "nope"},
		"bad slug":        {Dir: filepath.Join(root, "a"), Slug: "Not A Slug"},
		"config a folder": {Dir: filepath.Join(root, "a"), Config: root},
	} {
		if _, err := NewPlan(o); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
