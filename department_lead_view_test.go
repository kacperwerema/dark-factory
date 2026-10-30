package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// setupDeptLeadProject is setupAgentsProject plus a Marketing department
// (system SEO) with one issue in progress, seo/page.
func setupDeptLeadProject(t *testing.T) tracker.Project {
	t.Helper()
	proj := setupAgentsProject(t)
	path := filepath.Join(proj.IssueDir, "SEO", "page.md")
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte("---\ntitle: \"Page\"\nstatus: \"in progress\"\nsystem: \"SEO\"\n---\n\nBody.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	proj.Departments = []tracker.Department{{Name: "Marketing", Systems: []string{"SEO"}, Color: "#E2559B"}}
	return proj
}

// The canvas puts the department lead under the CEO, live or not, and names
// each worker's department; the lead is never an "other" session.
func TestAgentsModel_DepartmentLeads(t *testing.T) {
	proj := setupDeptLeadProject(t)
	stubAgentsGit(t)
	withMockTmuxSessions(t, []AgentSession{{Name: "lead-agents-marketing"}, {Name: "agent-seo-page"}, {Name: "agent-ui-child"}})
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, body := getResp(t, ts.URL+"/p/agents/agents.json")
	var m AgentsModel
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if len(m.DepartmentLeads) != 1 {
		t.Fatalf("department leads = %+v", m.DepartmentLeads)
	}
	if l := m.DepartmentLeads[0]; l.Name != "lead-agents-marketing" || l.Kind != "department-lead" || l.Department != "Marketing" || !l.Live || l.State != "live" {
		t.Errorf("department lead = %+v", l)
	}
	depts := map[string]string{}
	for _, w := range m.Workers {
		depts[w.Name] = w.Department
	}
	if depts["agent-seo-page"] != "Marketing" || depts["agent-ui-child"] != "" {
		t.Errorf("worker departments = %v", depts)
	}
	for _, o := range m.Others {
		if o.Name == "lead-agents-marketing" {
			t.Errorf("the department lead shows as other: %+v", m.Others)
		}
	}
}

func TestLeadSpawnEndpoint_Department(t *testing.T) {
	proj := setupDeptLeadProject(t)
	var got agentrun.Spec
	orig := startLead
	startLead = func(_ *tracker.Project, spec agentrun.Spec) agentrun.Response {
		got = spec
		return agentrun.Response{Status: "dispatched", Session: spec.Session}
	}
	t.Cleanup(func() { startLead = orig })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, out := postJSON(t, ts.URL+"/p/agents/lead/spawn?dept=marketing", `{}`)
	if code != 200 || out["session"] != "lead-agents-marketing" || got.LeadScope != "department:marketing" || !strings.Contains(got.Prompt, "You are the Marketing lead of Agents") {
		t.Fatalf("department lead: %d %v %+v", code, out, got)
	}
	got = agentrun.Spec{}
	if code, _ := postJSON(t, ts.URL+"/p/agents/lead/spawn?dept=sales", `{}`); code != 404 || got.Session != "" {
		t.Fatalf("unknown department: %d %+v", code, got)
	}
}

// /lead?dept=<name> is the department lead's chat: every URL on it carries
// the department, and an unknown one is 404 on the page and the endpoints.
func TestLeadChat_Department(t *testing.T) {
	proj := setupDeptLeadProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/agents/lead?dept=marketing")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	for _, want := range []string{
		`<h1 class="wb-h1">Marketing lead</h1>`,
		`data-live-url="?part=live&amp;dept=marketing"`,
		`data-ceo-send="/p/agents/lead/send?dept=marketing"`,
		`data-key-url="/p/agents/lead/key?dept=marketing"`,
		`Marketing · lead</span>`,
		`lead-agents-marketing`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}

	code, _ = leadChatPost(t, ts.URL+"/p/agents/lead/send?dept=marketing", "application/json", `{"text":"hello"}`)
	if code != 200 || st.session != "lead-agents-marketing" {
		t.Fatalf("send: %d to %q", code, st.session)
	}

	for _, u := range []string{"/p/agents/lead?dept=sales", "/p/agents/lead.json?dept=sales"} {
		if code, _ := getBody(t, ts.URL+u); code != 404 {
			t.Errorf("%s: status %d, want 404", u, code)
		}
	}
	for _, u := range []string{"/p/agents/lead/send?dept=sales", "/p/agents/lead/key?dept=sales", "/p/agents/lead/answer?dept=sales"} {
		if code, _ := leadChatPost(t, ts.URL+u, "application/json", `{"text":"x","key":"Escape"}`); code != 404 {
			t.Errorf("%s: status %d, want 404", u, code)
		}
	}
}

// A department's room shows its lead: Start when it is not running, Talk
// (its chat) when it is.
func TestCompanyRoom_DepartmentLead(t *testing.T) {
	proj := setupDeptLeadProject(t)
	st := stubLeadChat(t, false)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/agents/company")
	for _, want := range []string{`data-testid="company.room.dept.lead"`, `>lead-agents-marketing</span>`, `data-spawn-lead="/p/agents/lead/spawn?dept=marketing" data-testid="company.room.dept.lead.start"`} {
		if !strings.Contains(html, want) {
			t.Errorf("room (not running) lacks %q", want)
		}
	}
	st.mu.Lock()
	st.live = true
	st.mu.Unlock()
	_, html = getBody(t, ts.URL+"/p/agents/company")
	if !strings.Contains(html, `href="/p/agents/lead?dept=marketing" data-testid="company.room.dept.lead.talk">Talk to the Marketing lead</a>`) {
		t.Errorf("room (live) lacks the Talk link")
	}
}
