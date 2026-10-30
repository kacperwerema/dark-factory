package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// TestDemoConfig guards `make demo`, the README's "just looking?" path: the
// config it loads must be committed, point at real directories and its own
// workflow, and serve the board.
func TestDemoConfig(t *testing.T) {
	const config = "demo/projects.yaml"

	// A bare `projects.yaml` rule in .gitignore once hid this file, so a
	// fresh clone had no demo config.
	if _, err := exec.LookPath("git"); err == nil {
		if err := exec.Command("git", "check-ignore", "-q", "--no-index", config).Run(); err == nil {
			t.Fatalf("%s is gitignored; a fresh clone would not have it", config)
		}
	}

	projects, err := tracker.LoadProjects(config)
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(projects) == 0 {
		t.Fatalf("%s defines no projects", config)
	}

	for _, p := range projects {
		for _, dir := range []string{p.IssueDir, p.DocsDir, p.WorkDir} {
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				t.Errorf("project %s: %q is not a directory", p.Slug, dir)
			}
		}
		if p.WorkflowFile == "" {
			t.Errorf("project %s: no workflow set; the board would fall back to the repo's own workflow.yaml", p.Slug)
		} else if _, err := tracker.LoadWorkflow(p.WorkflowFile); err != nil {
			t.Errorf("project %s: workflow %s: %v", p.Slug, p.WorkflowFile, err)
		}
		if p.Terminal != "none" {
			t.Errorf("project %s: terminal is %q; the demo should not assume a desktop (use \"none\")", p.Slug, p.Terminal)
		}
		issues, err := tracker.LoadIssues(p.IssueDir)
		if err != nil {
			t.Errorf("project %s: LoadIssues: %v", p.Slug, err)
		} else if len(issues) == 0 {
			t.Errorf("project %s: no sample issues in %s", p.Slug, p.IssueDir)
		}
	}

	srv, err := NewServer(projects)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	for _, path := range []string{"/p/" + projects[0].Slug + "/", "/p/" + projects[0].Slug + "/board"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "localhost" // httptest defaults to example.com, which the Host guard refuses
		srv.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d", path, rec.Code)
		}
	}
}

// TestDemoSeed guards what the README's pictures show from the demo: HQ and
// a Marketing department on its own workflow, goals with values, outputs in
// review, and gates that only wait for the owner's stamp.
func TestDemoSeed(t *testing.T) {
	projects, err := tracker.LoadProjects("demo/projects.yaml")
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	p := &projects[0]
	if p.Company == nil || len(p.Company.Goals) == 0 {
		t.Fatal("demo: no company: with goals (HQ's room would be empty)")
	}
	var marketing *tracker.Department
	for i := range p.Departments {
		if p.Departments[i].Name == "Marketing" {
			marketing = &p.Departments[i]
		}
	}
	if marketing == nil || marketing.Workflow == "" || len(marketing.Goals) == 0 {
		t.Fatalf("demo: no Marketing department with its own workflow and goals: %+v", p.Departments)
	}
	if _, err := tracker.LoadWorkflow(filepath.Join(projectRoot(p), marketing.Workflow)); err != nil {
		t.Errorf("Marketing workflow: %v", err)
	}
	for _, prompt := range []string{p.Company.Prompt, marketing.Prompt} {
		if _, err := os.Stat(filepath.Join(projectRoot(p), prompt)); err != nil {
			t.Errorf("role prompt %s: %v", prompt, err)
		}
	}

	list, bad := outputs.Load(p)
	if len(bad) > 0 {
		t.Errorf("outputs log has bad lines: %+v", bad)
	}
	review := 0
	for _, o := range list {
		if o.State == "review" {
			review++
		}
	}
	if review < 2 {
		t.Errorf("demo shelf: %d outputs in review, want at least 2", review)
	}

	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	var open []*tracker.Issue
	for _, is := range issues {
		if is.Status != "done" {
			open = append(open, is)
		}
	}
	ready := 0
	for _, r := range needsYouRows(p, "", "/p/demo", open, time.Now()) {
		if r.Ready {
			ready++
		}
	}
	if ready < 3 {
		t.Errorf("demo Needs you: %d ready gates, want at least 3 for Approve selected", ready)
	}
}
