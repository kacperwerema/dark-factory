package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/projectinit"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// A project issue-cli project init made, from an empty folder and a new
// sandbox config, is a board the viewer shows, and its company page has
// the New department tile as a form over the templates.
func TestProjectInitGivesABoardWithTheNewDepartmentTile(t *testing.T) {
	withMockTmuxSessions(t, nil)
	dir := t.TempDir()
	config := filepath.Join(dir, "projects.yaml")
	plan, err := projectinit.NewPlan(projectinit.Options{Dir: filepath.Join(dir, "shop"), Name: "Shop", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	projects, err := tracker.LoadProjects(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Slug != "shop" {
		t.Fatalf("projects = %+v", projects)
	}
	ts := newTestServer(t, projects)
	defer ts.Close()
	for _, page := range []string{"/p/shop/", "/p/shop/workboard"} {
		if code, _ := getBody(t, ts.URL+page); code != http.StatusOK {
			t.Errorf("%s: status %d", page, code)
		}
	}
	code, body := getBody(t, ts.URL+"/p/shop/company")
	if code != http.StatusOK {
		t.Fatalf("company: status %d", code)
	}
	for _, want := range []string{
		`data-testid="tile.new_department"`,
		`action="/p/shop/departments/new" data-testid="company.new_dept.form"`,
		`data-testid="company.new_dept.template.marketing"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("company page missing %q", want)
		}
	}
}
