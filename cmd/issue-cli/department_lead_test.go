package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// makeDeptLeadProject is makeLeadProject plus a Marketing department (system
// SEO, role prompt departments/marketing.md) with one issue, seo/page.
func makeDeptLeadProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeLeadProject(t)
	for rel, content := range map[string]string{
		"issues/SEO/page.md":       "---\ntitle: \"Page\"\nstatus: \"backlog\"\nsystem: \"SEO\"\n---\n\nBody.\n",
		"departments/marketing.md": "You write local SEO pages.\n",
	} {
		path := filepath.Join(proj.WorkDir, rel)
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	proj.Departments = []tracker.Department{{Name: "Marketing", Systems: []string{"SEO"}, Mission: "Refresh every site", Prompt: "departments/marketing.md"}}
	return proj
}

func TestLeadSpawn_Department(t *testing.T) {
	proj := makeDeptLeadProject(t)
	spec := captureStart(t, "dispatched")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runLead(ctx, []string{"spawn", "--department", "marketing"}); err != nil {
		t.Fatal(err)
	}
	if spec.Session != "lead-proj-marketing" || spec.LeadScope != "department:marketing" || spec.WorkDir != proj.WorkDir || spec.Worktree.Path != "" {
		t.Fatalf("spec = %+v", *spec)
	}
	for _, want := range []string{"You are the Marketing lead of Proj", "report to the CEO lead-proj", "Mission: Refresh every site", "You write local SEO pages.", "Scope: only Marketing's issues"} {
		if !strings.Contains(spec.Prompt, want) {
			t.Errorf("brief missing %q:\n%s", want, spec.Prompt)
		}
	}
	if !strings.Contains(stdout.String(), "✓ Spawned Marketing lead lead-proj-marketing") {
		t.Errorf("stdout = %q", stdout.String())
	}

	// The CEO's spawn points at the department leads.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runLead(ctx, []string{"spawn"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "tip: department leads: issue-cli --project proj lead spawn --department <name> (marketing)") {
		t.Errorf("CEO spawn stdout = %q", stdout.String())
	}

	// Without departments it points at the templates.
	proj.Departments = nil
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runLead(ctx, []string{"spawn"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "tip: add a department (Marketing, …) from a template: issue-cli --project proj department list") {
		t.Errorf("CEO spawn without departments stdout = %q", stdout.String())
	}
}

func TestLeadSpawn_DepartmentRefusals(t *testing.T) {
	proj := makeDeptLeadProject(t)
	captureStart(t, "dispatched")
	for name, want := range map[string]string{
		"product": "product is the CEO's: issue-cli --project proj lead spawn",
		"HQ":      "HQ is the CEO's",
		"sales":   `no department "sales" (departments: marketing)`,
	} {
		ctx, _, _ := newTestContext(proj, false)
		if err := runLead(ctx, []string{"spawn", "--department", name}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", name, err, want)
		}
	}
}

func TestLeadWhoami_DepartmentLead(t *testing.T) {
	proj := makeDeptLeadProject(t)
	withLive(t, "lead-proj")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:marketing")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runLead(ctx, []string{"whoami"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Role: department lead (lead-proj-marketing)", "Scope: department:marketing", "Reports to: lead-proj (live)", "May tick: Marketing's gates"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("whoami missing %q:\n%s", want, stdout.String())
		}
	}

	// The tmux name alone is enough.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	orig := currentTmuxNames
	currentTmuxNames = func() (string, string) { return "work", "lead-proj-marketing" }
	t.Cleanup(func() { currentTmuxNames = orig })
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	if me := whoAmI(proj, issues); me.Role != tracker.RoleDepartmentLead || me.Department != "Marketing" {
		t.Errorf("tmux name: %+v", me)
	}
}

// A department lead approves and dispatches only its own department's
// issues; the refusal names the CEO.
func TestDepartmentLead_Scope(t *testing.T) {
	proj := makeDeptLeadProject(t)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:marketing")

	ctx, _, _ := newTestContext(proj, false)
	err := runApprove(ctx, []string{"loner", "--to", "in progress", "--quote", "go"})
	if err == nil || !strings.Contains(err.Error(), "loner is Product's, not Marketing's: a department lead approves only its own department's issues. Ask the CEO lead-proj") {
		t.Fatalf("approve Product issue: err = %v", err)
	}
	ctx, _, _ = newTestContext(proj, false)
	err = runDispatch(ctx, []string{"loner", "--quote", "go"})
	if err == nil || !strings.Contains(err.Error(), "dispatches only its own department's issues") {
		t.Fatalf("dispatch Product issue: err = %v", err)
	}

	ctx, _, _ = newTestContext(proj, false)
	if err := runApprove(ctx, []string{"seo/page", "--to", "in progress", "--quote", "go"}); err != nil {
		t.Fatalf("approve own issue: %v", err)
	}
	comments, _ := tracker.LoadComments(loadLeadIssue(t, proj, "seo/page").FilePath)
	if len(comments) != 1 || !strings.Contains(comments[0].Text, "(in the Marketing lead chat, ") || !strings.HasSuffix(comments[0].Text, "recorded by lead-proj-marketing") {
		t.Fatalf("comments = %+v", comments)
	}
}

func TestLeadList_DepartmentLeads(t *testing.T) {
	proj := makeDeptLeadProject(t)
	withLive(t, "lead-proj-marketing")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runLead(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "== Department leads ==\n  lead-proj-marketing                      Marketing  live · 1 open\n") {
		t.Errorf("list:\n%s", stdout.String())
	}
	ctx, stdout, _ = newTestContext(proj, true)
	if err := runLead(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	var got leadListJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Departments) != 1 || got.Departments[0] != (deptLeadRow{Department: "Marketing", Session: "lead-proj-marketing", Live: true, Open: 1}) {
		t.Errorf("json departments = %+v", got.Departments)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	if got := resolveMessageTarget(proj, issues, "department:marketing"); got != "lead-proj-marketing" {
		t.Errorf("message target = %q", got)
	}
}

func TestShow_DepartmentLeadLine(t *testing.T) {
	proj := makeDeptLeadProject(t)
	withLive(t, "lead-proj")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runShow(ctx, []string{"seo/page"}); err != nil {
		t.Fatal(err)
	}
	want := "Lead: lead-proj-marketing (not running) · CEO: lead-proj (live) — the CEO covers until the Marketing lead starts: issue-cli --project proj lead spawn --department marketing\n"
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("show:\n%s", stdout.String())
	}

	withLive(t, "lead-proj", "lead-proj-marketing")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runShow(ctx, []string{"seo/page"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Lead: lead-proj-marketing (live) · CEO: lead-proj (live)\n") {
		t.Errorf("show (live):\n%s", stdout.String())
	}
}
