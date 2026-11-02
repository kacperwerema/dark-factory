package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
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

// makeGroupLeadProject is makeDeptLeadProject with Marketing under a GTM
// group, and a GTM-wide issue, gtm/plan.
func makeGroupLeadProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeDeptLeadProject(t)
	path := filepath.Join(proj.WorkDir, "issues/GTM/plan.md")
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte("---\ntitle: \"Plan\"\nstatus: \"backlog\"\nsystem: \"GTM\"\n---\n\nBody.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	proj.Departments[0].Parent = "gtm"
	proj.Departments = append([]tracker.Department{{Name: "GTM", Systems: []string{"GTM"}, Mission: "Bring in clients"}}, proj.Departments...)
	return proj
}

// A group's lead approves its own and its departments' issues; Product's
// stay the CEO's.
func TestGroupLead_ScopeAndWhoami(t *testing.T) {
	proj := makeGroupLeadProject(t)
	withLive(t, "lead-proj")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:gtm")

	for _, slug := range []string{"seo/page", "gtm/plan"} {
		ctx, _, _ := newTestContext(proj, false)
		if err := runApprove(ctx, []string{slug, "--to", "in progress", "--quote", "go"}); err != nil {
			t.Fatalf("approve %s: %v", slug, err)
		}
	}
	ctx, _, _ := newTestContext(proj, false)
	err := runApprove(ctx, []string{"loner", "--to", "in progress", "--quote", "go"})
	if err == nil || !strings.Contains(err.Error(), "loner is Product's, not in GTM: a group lead approves only its group's and its departments' issues. Ask the CEO lead-proj") {
		t.Fatalf("approve Product issue: err = %v", err)
	}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runLead(ctx, []string{"whoami"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Role: department lead (lead-proj-gtm)", "Your departments: Marketing", "Reports to: lead-proj (live)", "May tick: GTM's and its departments' (Marketing) gates"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("group whoami missing %q:\n%s", want, stdout.String())
		}
	}
}

// A department lead under a group reports to the group's lead while it is
// live, else to the CEO, and can't approve the group's own issues.
func TestGroupChildLead_ReportsToGroupLead(t *testing.T) {
	proj := makeGroupLeadProject(t)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:marketing")
	withLive(t, "lead-proj")
	whoami := func() string {
		ctx, stdout, _ := newTestContext(proj, false)
		if err := runLead(ctx, []string{"whoami"}); err != nil {
			t.Fatal(err)
		}
		return stdout.String()
	}
	if got := whoami(); !strings.Contains(got, "Group: GTM") || !strings.Contains(got, "Reports to: lead-proj (live)") {
		t.Errorf("group lead down:\n%s", got)
	}
	withLive(t, "lead-proj", "lead-proj-gtm")
	if got := whoami(); !strings.Contains(got, "Reports to: lead-proj-gtm (live)") {
		t.Errorf("group lead live:\n%s", got)
	}

	ctx, _, _ := newTestContext(proj, false)
	err := runApprove(ctx, []string{"gtm/plan", "--to", "in progress", "--quote", "go"})
	if err == nil || !strings.Contains(err.Error(), "gtm/plan is GTM's, not Marketing's") {
		t.Fatalf("approve the group's issue: err = %v", err)
	}
}

func TestLeadList_Group(t *testing.T) {
	proj := makeGroupLeadProject(t)
	withLive(t, "lead-proj-gtm")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runLead(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	got := stdout.String()
	g, m := strings.Index(got, "lead-proj-gtm"), strings.Index(got, "└ lead-proj-marketing")
	if g < 0 || m < g || !strings.Contains(got, "GTM (group)  live") || !strings.Contains(got, "(reports to GTM)") {
		t.Errorf("lead list:\n%s", got)
	}

	// The worker's header names the chain.
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	line := leadHeaderLine(proj, tracker.FindIssue(issues, "seo/page"), issues)
	if !strings.Contains(line, "Lead: lead-proj-marketing (not running) · GTM lead: lead-proj-gtm (live)") || !strings.Contains(line, "the GTM lead covers") {
		t.Errorf("header = %q", line)
	}
}

// makeNestedGroupLeadProject is makeGroupLeadProject with an Outbound
// group between GTM and Marketing: Marketing → Outbound → GTM
// (factory/nested-groups-a-group-inside-a-group-with-goals-rolling-up-and-the-map-drawing-it).
func makeNestedGroupLeadProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeGroupLeadProject(t)
	for i := range proj.Departments {
		if proj.Departments[i].Name == "Marketing" {
			proj.Departments[i].Parent = "outbound"
		}
	}
	proj.Departments = append(proj.Departments, tracker.Department{Name: "Outbound", Parent: "gtm"})
	return proj
}

// A department lead two groups down reports to the nearest live lead up
// the chain; the top group's lead reaches every department under it.
func TestNestedGroupLeads_ReportUpTheChain(t *testing.T) {
	proj := makeNestedGroupLeadProject(t)
	whoami := func(scope string) string {
		t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", scope)
		ctx, stdout, _ := newTestContext(proj, false)
		if err := runLead(ctx, []string{"whoami"}); err != nil {
			t.Fatal(err)
		}
		return stdout.String()
	}
	withLive(t, "lead-proj", "lead-proj-gtm")
	got := whoami("department:marketing")
	for _, want := range []string{"Group: Outbound in GTM (their leads lead-proj-outbound, then lead-proj-gtm come first while live", "Reports to: lead-proj-gtm (live)"} {
		if !strings.Contains(got, want) {
			t.Errorf("marketing whoami missing %q:\n%s", want, got)
		}
	}
	withLive(t, "lead-proj", "lead-proj-gtm", "lead-proj-outbound")
	if got := whoami("department:marketing"); !strings.Contains(got, "Reports to: lead-proj-outbound (live)") {
		t.Errorf("outbound live:\n%s", got)
	}
	if got := whoami("department:gtm"); !strings.Contains(got, "May tick: GTM's and its departments' (Outbound, Marketing) gates") {
		t.Errorf("gtm whoami:\n%s", got)
	}

	// GTM's lead approves Marketing's issue two levels down.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:gtm")
	ctx, _, _ := newTestContext(proj, false)
	if err := runApprove(ctx, []string{"seo/page", "--to", "in progress", "--quote", "go"}); err != nil {
		t.Fatalf("gtm approves marketing's: %v", err)
	}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runLead(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	list := stdout.String()
	o, m := strings.Index(list, "  └ lead-proj-outbound"), strings.Index(list, "    └ lead-proj-marketing")
	if o < 0 || m < o || !strings.Contains(list, "Outbound (group)") || !strings.Contains(list, "(reports to Outbound)") {
		t.Errorf("lead list:\n%s", list)
	}
}
