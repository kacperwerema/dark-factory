package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The fixture is setupAgentsProject: ui/child at the testing gate, ui/asker
// with a question, ui/idle and the epic's own job stopped, a session not
// bound to an issue, the CEO not running.

func TestCrewPage(t *testing.T) {
	proj := setupAgentsProject(t)
	proj.TmuxSession = "work"
	stubAgentsGit(t)
	withMockTmuxSessions(t, []AgentSession{
		{Name: "work:agent-ui-child"},
		{Name: "work:agent-ui-asker"},
		{Name: "work:agent-agents-ux-review"},
	})
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/agents/agents")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`<link rel="stylesheet" href="/static/workshop.css">`,
		`<a class="wb-crumb wb-crumb--link" href="/p/agents/company">Agents</a>`,
		`<span class="wb-crumb wb-crumb--on" aria-current="page">Crew</span>`,
		`<h1 class="wb-h1 wb-h1--big">Crew</h1>`,
		`href="/p/agents/agents?classic=1" data-testid="crew.classic">Classic view</a>`,
		// Three live sessions; the CEO is off.
		`<div class="wb-statbig-num">3</div><div class="wb-statbig-label">live agents</div>`,
		`data-testid="summary.crew">`, `1 at a gate · 1 with a question · 2 stopped jobs`,
		// The CEO, not running, with its start button.
		`data-testid="crew.card.lead-agents"`, `wb-chip wb-chip--idle">not running</span>`,
		`data-spawn-lead="/p/agents/lead/spawn" data-spawn-what="the CEO" data-testid="crew.card.lead-agents.spawn">Start the CEO</button>`,
		// Product column (no departments): run by the CEO, both workers.
		`data-testid="crew.column.product"`, `Run by the CEO`,
		`data-testid="crew.card.agent-ui-child"`, `waiting on you at the testing gate`,
		`data-testid="crew.card.agent-ui-asker"`, `asked a question`,
		`href="/p/agents/issue/ui/child" data-testid="crew.card.agent-ui-child.issue">Child</a>`,
		`<span class="wb-tag-pill wb-tag-pill--epic" title="Epic">Epic</span>`,
		// Stopped jobs and the unbound session.
		`data-testid="crew.stopped"`, `data-testid="crew.card.agent-ui-idle"`, `retro not swept`,
		`<code>issue-cli dispatch ui/idle</code>`,
		`data-testid="crew.others"`, `data-testid="crew.card.agent-agents-ux-review"`, `live · not bound to an issue`,
		// Nothing selected.
		`data-testid="crew.panel.empty"`,
		`<script src="/static/crew.js"></script>`,
	)
	if strings.Contains(html, `data-testid="crew.no_workers"`) {
		t.Error("workers are live, but the page says none run")
	}
	// The org chart comes before the rows below it.
	if strings.Index(html, `crew.card.agent-ui-asker"`) > strings.Index(html, `crew.card.agent-ui-idle"`) {
		t.Error("live workers should come before the stopped jobs")
	}
}

func TestCrewPanel(t *testing.T) {
	proj := setupAgentsProject(t)
	proj.TmuxSession = "work"
	proj.Terminal = "alacritty -e tmux attach -t {{session}}" // opens a window; unset is headless without i3
	stubAgentsGit(t)
	withMockTmuxSessions(t, []AgentSession{{Name: "work:agent-ui-child"}})
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/agents/agents?agent=agent-ui-child")
	assertContains(t, html,
		`wb-crew-card--on`, `aria-current="true" data-testid="crew.card.agent-ui-child.pick"`,
		`data-testid="crew.panel.name">agent-ui-child</h2>`,
		`data-open-session="/p/agents/agents/open" data-session="agent-ui-child" data-testid="crew.panel.open"`,
		`data-testid="crew.panel.command"`, `tmux attach`,
		`href="/p/agents/issue/ui/child#approve-testing" data-testid="crew.panel.approve">Approve the testing gate at the station →</a>`,
		`data-testid="crew.panel.diff">main...`,
	)

	// A stopped job: no terminal, the dispatch command instead.
	_, html = getBody(t, ts.URL+"/p/agents/agents?agent=agent-ui-idle")
	assertContains(t, html, `data-testid="crew.panel.name">agent-ui-idle</h2>`, `No agent is running on this job.`)
	if strings.Contains(html, `data-testid="crew.panel.open"`) {
		t.Error("a stopped job offers Open in a terminal")
	}

	// Unknown and malformed names: the empty hint, never a 500.
	for _, q := range []string{"agent-nobody", "zsh", "agent-x;rm"} {
		code, html := getBody(t, ts.URL+"/p/agents/agents?agent="+q)
		if code != 200 || !strings.Contains(html, `data-testid="crew.panel.empty"`) {
			t.Errorf("?agent=%s: status %d, empty hint missing", q, code)
		}
	}
}

func TestCrewNoWorkers(t *testing.T) {
	proj := setupAgentsProject(t)
	stubAgentsGit(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/agents")
	assertContains(t, html, `data-testid="crew.no_workers"`, `No workers running`, `<code>issue-cli dispatch &lt;slug&gt;</code>`,
		`data-testid="crew.column.product.empty"`, `<div class="wb-statbig-num">0</div><div class="wb-statbig-label">live agents</div>`)
	if strings.Contains(html, `data-testid="crew.others"`) {
		t.Error("an empty row should be hidden")
	}
}

func TestBuildCrewDepartments(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	m := &AgentsModel{
		Project:     "P",
		ProjectLead: AgentNode{Name: "lead-p", Live: true},
		DepartmentLeads: []AgentNode{
			{Name: "lead-p-marketing", Department: "Marketing", Live: false},
		},
		Workers: []AgentNode{
			{Name: "agent-a", State: "idle", LastCall: now.Add(-25 * time.Minute).Format(time.RFC3339), Live: true},
			{Name: "agent-b", State: "working", Live: true},
			{Name: "agent-m", State: "working", Department: "marketing", Live: true},
		},
	}
	d := buildCrew(m, []tracker.Department{{Name: "Marketing"}}, "/p/p", "lead-p-marketing", now)
	if len(d.Columns) != 2 || d.Columns[0].Name != "Product" || d.Columns[1].Name != "Marketing" {
		t.Fatalf("columns = %+v", d.Columns)
	}
	if w := d.Columns[0].Workers; len(w) != 2 || w[0].Name != "agent-b" || w[1].State != "idle · issue-cli call 25 m ago" {
		t.Errorf("product workers (working before idle) = %+v", w)
	}
	mk := d.Columns[1]
	if mk.Lead == nil || mk.Lead.State != "not running" || mk.Lead.SpawnURL != "/p/p/lead/spawn?dept=Marketing" || len(mk.Workers) != 1 {
		t.Errorf("marketing = %+v lead %+v", mk, mk.Lead)
	}
	if d.Panel == nil || d.Panel.Card.Name != "lead-p-marketing" || d.Panel.OpenURL != "" {
		t.Errorf("a lead that is not running: panel = %+v", d.Panel)
	}
	if d.Count.Value != "4" || !d.Summary.OK || d.Summary.Text != "Nobody here waits on you" {
		t.Errorf("count %+v summary %+v", d.Count, d.Summary)
	}
}

func TestHeaderCrewURL(t *testing.T) {
	for in, want := range map[string]string{
		"/p/x/needs-you": "/p/x/agents",
		"/needs-you":     "",
		"":               "",
	} {
		if got := (WbHeader{WaitingURL: in}).CrewURL(); got != want {
			t.Errorf("CrewURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCrewPageWorkshopBoardOnly: every element on the page carries Workshop
// Board classes (wb-*: they come from the wb/* components), and the text is
// English (no Polish letters from the design mockups).
func TestCrewPageWorkshopBoardOnly(t *testing.T) {
	proj := setupAgentsProject(t)
	proj.TmuxSession = "work"
	stubAgentsGit(t)
	withMockTmuxSessions(t, []AgentSession{{Name: "work:agent-ui-child"}, {Name: "work:agent-agents-ux-review"}})
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, html := getBody(t, ts.URL+"/p/agents/agents?agent=agent-ui-child")
	for _, attr := range regexp.MustCompile(`class="([^"]*)"`).FindAllStringSubmatch(html, -1) {
		for _, c := range strings.Fields(attr[1]) {
			if c != "wb" && !strings.HasPrefix(c, "wb-") {
				t.Errorf("class %q is not a Workshop Board class", c)
			}
		}
	}
	if strings.ContainsAny(html, "ąćęłńóśźżĄĆĘŁŃÓŚŹŻ") {
		t.Error("the page carries Polish text")
	}
}

// Proves: AC3 — the Crew panel carries what the classic canvas panel did:
// the tmux display name, the attach command even when the agent is not
// running, and the time tmux was read (classic-page-gaps G47, G49).
func TestCrewPanelDisplayAttachAndChecked(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	m := &AgentsModel{
		Project:     "P",
		ProjectLead: AgentNode{Name: "lead-p", Display: "shared:lead-p", AttachCmd: "tmux attach -t shared:lead-p", Live: false},
	}
	d := buildCrew(m, nil, "/p/p", "lead-p", now)
	if d.Checked != now.UnixMilli() {
		t.Errorf("checked = %d", d.Checked)
	}
	p := d.Panel
	if p == nil || p.Display != "shared:lead-p" || p.Command != "tmux attach -t shared:lead-p" || p.CommandLabel != "From a shell, once it runs again" {
		t.Errorf("panel = %+v", p)
	}
}
