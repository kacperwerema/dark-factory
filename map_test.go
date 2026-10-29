package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// stubLeadPane makes every lead live with pane as its scrollback.
func stubLeadPane(t *testing.T, pane string) {
	t.Helper()
	origHas, origCap := agentrun.HasSession, agentrun.CaptureHistory
	agentrun.HasSession = func(string) bool { return true }
	agentrun.CaptureHistory = func(string, int) (string, error) { return pane, nil }
	t.Cleanup(func() { agentrun.HasSession, agentrun.CaptureHistory = origHas, origCap })
}

func TestMapPage(t *testing.T) {
	a := setupAgentsProject(t)
	a.Kind = "aplikacja PWA"
	b, _ := setupTestProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, leadChatPaneText)
	withMockTmuxSessions(t, []AgentSession{{Name: "lead-agents"}, {Name: "agent-ui-child"}, {Name: "agent-ui-asker"}})
	ts := newTestServer(t, []tracker.Project{a, b})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`data-testid="map.lot.agents"`, `href="/p/agents/company"`, `aplikacja PWA`,
		`data-testid="map.lot.test-project"`,
		// child waits at its gate, asker asks a question: both wait on the owner.
		`class="wb-bot wb-bot--you" title="ui/child`, `class="wb-bot wb-bot--you" title="ui/asker`,
		// The lead's newest paragraph is its quote.
		`“Do you want the epic closed?”`, `lead-agents</div>`,
		`Epic: Epic</span><span>0/3</span>`,
		`>Question</span>`, `>Gate: testing</span>`, `href="/p/agents/issue/ui/child#approve-testing"`,
		`data-show="lot-agents"`, `data-testid="map.lot.waiting"`,
	)
	// Four ready testing gates and asker's question in agents, plus
	// test-project's ready backlog gate.
	if !strings.Contains(html, `>6 waiting on you</a>`) {
		t.Error("the header should count everything waiting on the owner across projects")
	}
}

func TestMapSingleProjectShowsTheMap(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/")
	if code != 200 || !strings.Contains(html, `data-testid="map.lot.test-project"`) {
		t.Fatalf("status %d, want the map with the one project", code)
	}
	assertContains(t, html, `>Gate: in progress</span>`, `href="/p/test-project/issue/add-dark-mode#approve-in-progress"`, `>1 waiting on you</a>`)
}

func TestLastLeadParagraph(t *testing.T) {
	pane := "⏺ I sent **two** workers to `ui/a`.\n" +
		"⏺ Bash(issue-cli status)\n" +
		"  ⎿  ok\n" +
		"> what now?\n"
	if got := lastLeadParagraph(pane); got != "I sent two workers to ui/a." {
		t.Errorf("quote = %q, want the newest paragraph that is not a tool call", got)
	}
	long := "⏺ " + strings.Repeat("wörd ", 60)
	if got := lastLeadParagraph(long); len([]rune(got)) != mapQuoteMax || !strings.HasSuffix(got, "…") {
		t.Errorf("long quote = %q (%d runes), want it clipped to %d", got, len([]rune(got)), mapQuoteMax)
	}
	if got := lastLeadParagraph("> only the owner typed\n"); got != "" {
		t.Errorf("quote = %q, want none", got)
	}
}

func TestMapRobotsWaitOnAnotherAgent(t *testing.T) {
	blocked := &tracker.Issue{Slug: "ui/b", ExtraFields: []tracker.ExtraField{{Key: "blocked_on", Value: "ui/a"}}}
	free := &tracker.Issue{Slug: "ui/c", ExtraFields: []tracker.ExtraField{{Key: "blocked_on", Value: "ui/z"}}}
	issues := []*tracker.Issue{{Slug: "ui/a"}, blocked, free}
	agents := &AgentsModel{Workers: []AgentNode{
		{Name: "agent-ui-a", State: "working", Issue: &AgentIssue{Slug: "ui/a"}},
		{Name: "agent-ui-b", State: "idle", Issue: &AgentIssue{Slug: "ui/b"}},
		{Name: "agent-ui-c", State: "idle", Issue: &AgentIssue{Slug: "ui/c"}},
	}, Others: []AgentNode{{Name: "agent-review"}}}
	got := mapRobotsOf(agents, issues)
	want := []string{"working", "agent", "idle", "working"}
	if len(got) != len(want) {
		t.Fatalf("robots = %+v", got)
	}
	for i, r := range got {
		if r.State != want[i] {
			t.Errorf("robot %d (%s) = %s, want %s", i, r.Label, r.State, want[i])
		}
	}
	if got[1].Label != "ui/b → ui/a" {
		t.Errorf("label = %q, want the issue it waits on", got[1].Label)
	}
}

func TestMapBuildingsAndProgress(t *testing.T) {
	var open []*tracker.Issue
	for i, sys := range []string{"Plan", "Plan", "Plan", "Zakupy", "Zakupy", "Czat", "Makro", "Testy", ""} {
		open = append(open, &tracker.Issue{Slug: "x" + string(rune('a'+i)), Status: "backlog", System: sys})
	}
	wf := tracker.DefaultWorkflow()
	b := mapBuildingsOf(open, wf)
	if len(b) != mapBuildings || b[0].Name != "Plan" || b[0].Open != 3 || b[1].Name != "Zakupy" {
		t.Fatalf("buildings = %+v", b)
	}
	if last := b[len(b)-1]; last.Name != "Others (3)" || last.Open != 3 {
		t.Errorf("last building = %+v, want the rest counted together", last)
	}
	if b[0].Height <= b[1].Height {
		t.Errorf("more open work should stand taller: %d vs %d", b[0].Height, b[1].Height)
	}

	epic := &tracker.Issue{Slug: "e", Title: "Hall", Status: "in progress"}
	issues := []*tracker.Issue{epic,
		{Slug: "e1", Status: "done", ExtraFields: []tracker.ExtraField{{Key: "parent", Value: "e"}}},
		{Slug: "e2", Status: "backlog", ExtraFields: []tracker.ExtraField{{Key: "parent", Value: "e"}}},
	}
	p := mapProgressOf(issues)
	if p == nil || p.Label != "Epic: Hall" || p.Done != 1 || p.Total != 2 || p.Pct() != 50 {
		t.Errorf("progress = %+v", p)
	}
}

func TestSortMapQueueBlockingFirstThenOldest(t *testing.T) {
	now := time.Now()
	q := []WbQueueCard{
		{Slug: "new", Since: now.Add(-time.Minute)},
		{Slug: "ci", Blocking: true, Since: now},
		{Slug: "old", Since: now.Add(-time.Hour)},
	}
	sortMapQueue(q)
	if q[0].Slug != "ci" || q[1].Slug != "old" || q[2].Slug != "new" {
		t.Errorf("order = %s %s %s", q[0].Slug, q[1].Slug, q[2].Slug)
	}
}

func TestMapLotTakesKindAndColorFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "projects.yaml")
	os.WriteFile(cfg, []byte("projects:\n  - name: Jemik\n    slug: meals\n    kind: aplikacja PWA\n    color: \"#13A386\"\n    issues: ./issues\n"), 0644)
	pc, err := tracker.LoadProjectsConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p := pc.Projects[0]; p.Kind != "aplikacja PWA" || p.Color != "#13A386" {
		t.Errorf("project = %+v", p)
	}
}

// TestWorkshopComponentsStandAlone renders every component on its own: a page
// composes them, so none may lean on the page around it.
func TestWorkshopComponentsStandAlone(t *testing.T) {
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	progress := &WbProgress{Label: "Epic: Hall", Done: 2, Total: 7}
	card := WbQueueCard{Label: "Question", Tone: "you", Project: "Jemik", ProjectSlug: "meals", Slug: "zakupy/x", CTA: "Answer", URL: "/p/meals/issue/zakupy/x"}
	for name, data := range map[string]any{
		"wb/head":         "Map",
		"wb/header":       WbHeader{Crumbs: wbCrumbs(1, "Jemik", "/p/meals/status"), Working: 2, Waiting: 3},
		"wb/robot":        WbRobot{State: "agent", Label: "ui/b → ui/a"},
		"wb/robots":       wbCrew([]WbRobot{{State: "working"}}),
		"wb/chip":         *wbCIChip("red", "make validate"),
		"wb/chips":        wbCountChips(1, 1, 1, 2),
		"wb/ceo":          WbCEO{Session: "lead-meals", Live: true, Quote: "Three things for you."},
		"wb/progress":     progress,
		"wb/bang":         3,
		"wb/skyline":      []WbBuilding{{Name: "Plan", Open: 3, Color: "#3D7BF7", Width: 48, Height: 25}},
		"wb/lot":          MapLot{Name: "Jemik", Slug: "meals", Color: "#13A386", Progress: progress, Waiting: 1},
		"wb/queue-card":   card,
		"wb/queue-row":    card,
		"wb/goal":         WbGoal{Progress: *progress, Color: "#13A386"},
		"wb/room-head":    WbRoomHead{Title: "HQ · CEO", Color: "#F2B233", Meta: "1/1 desk"},
		"wb/room-foot":    []WbFootItem{{Text: "✓ 2 this week", Tone: "green"}},
		"wb/ceo-desk":     WbCEODesk{CEO: WbCEO{Session: "lead-meals"}, SpawnURL: "/p/meals/lead/spawn"},
		"wb/room-hq":      WbRoomHQ{Head: WbRoomHead{Title: "HQ · CEO"}},
		"wb/stations":     []WbStation{{Label: "gate", Gate: true}, {Label: "in progress", Count: 2, Crew: wbCrew([]WbRobot{{State: "working"}})}},
		"wb/room-factory": WbRoomFactory{Head: WbRoomHead{Title: "Product"}, HallURL: "/p/meals/board"},
		"wb/new-tile":     WbNewTile{Title: "New department", TestID: "new_department"},
		"wb/radio":        []WbMessage{{From: "lead-meals", To: "you", Initials: "CEO", Tone: "ceo", Text: "Hi"}},
		"wb/message-box":  WbMessageBox{Session: "lead-meals", SendURL: "/p/meals/lead/send"},
		"wb/site":         MapLot{Name: "Cost Estimator", Slug: "cost-estimator"},
		"wb/side":         []WbQueueCard{card},
	} {
		var b strings.Builder
		if err := srv.tmpl.ExecuteTemplate(&b, name, data); err != nil || strings.TrimSpace(b.String()) == "" {
			t.Errorf("%s: %v (output %q)", name, err, b.String())
		}
	}
	var b strings.Builder
	srv.tmpl.ExecuteTemplate(&b, "wb/header", WbHeader{Crumbs: wbCrumbs(1, "Jemik", "/p/meals/status")})
	assertContains(t, b.String(), `<a class="wb-crumb wb-crumb--link" href="/">Map</a>`, `aria-current="page">Jemik</span>`, `title="Pick a project, then its hall">Hall</span>`)
}

func TestMapEngineHubAndBuildingSite(t *testing.T) {
	engine, _ := setupTestProject(t)
	engine.Name, engine.Slug = "Dark Factory", "dark-factory"
	site := tracker.Project{Name: "Cost Estimator", Slug: "cost-estimator", Description: "AI cost estimates for building projects.", IssueDir: filepath.Join(t.TempDir(), "issues")}
	os.MkdirAll(site.IssueDir, 0755)
	app, _ := setupTestProject(t)
	srv, err := NewServer([]tracker.Project{site, engine, app})
	if err != nil {
		t.Fatal(err)
	}
	srv.engine = "dark-factory"
	data := srv.buildMap(time.Now())
	if data.Engine != "dark-factory" || data.Active != 2 || data.Sites != 1 {
		t.Fatalf("engine %q, active %d, sites %d", data.Engine, data.Active, data.Sites)
	}
	if last := data.Lots[len(data.Lots)-1]; !last.Site || last.Slug != "cost-estimator" {
		t.Errorf("building sites come after the projects at work, got last %+v", last.Slug)
	}

	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/")
	assertContains(t, html,
		`class="wb-lots wb-lots--hub" id="lots" data-engine="dark-factory"`,
		`class="wb-lot wb-lot--engine" id="lot-dark-factory"`, `>ENGINE</div>`,
		`data-site="1" data-testid="map.site.cost-estimator"`, `AI cost estimates for building projects.`,
		`data-spawn-lead="/p/cost-estimator/lead/spawn"`,
		`2 active · 1 building site · roads lead to the engine`,
	)
}

func TestCompanyPage(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "⏺ I sent two workers to Product.\n⏺ Bash(issue-cli status)\n⏺ Waiting on your call about the epic.\n")
	withMockTmuxSessions(t, []AgentSession{{Name: "lead-agents"}, {Name: "agent-ui-child"}, {Name: "agent-ui-asker"}})
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/agents/company")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`<a class="wb-crumb wb-crumb--link" href="/">Map</a>`, `aria-current="page"><i class="wb-crumb-sq"`, `>Agents</span>`,
		`data-testid="company.goal"`, `<span class="wb-goal-name">Epic</span>`,
		`data-testid="company.room.hq"`, `>HQ · CEO</span>`, `“Waiting on your call about the epic.”`, `href="/p/agents/lead" data-testid="company.ceo.talk"`,
		`data-testid="company.room.product"`, `>Product · software factory</span>`, `2 workers`,
		`data-testid="company.line"`, `<div class="wb-station-label">in progress</div>`, `<div class="wb-station-label">gate</div>`,
		`href="/p/agents/board" data-testid="company.enter_hall"`,
		`data-testid="tile.new_department"`, `Coming later: a new department is a new pillar`,
		`data-testid="company.queue.row"`, `>Question</span>`,
		// The Radio: the CEO's lines, newest first, without tool calls.
		`I sent two workers to Product.`, `data-send="/p/agents/lead/send"`,
	)
	if strings.Contains(html, "Bash(issue-cli status)") {
		t.Error("a tool call is not something the CEO said")
	}
	if strings.Index(html, "Waiting on your call") > strings.Index(html, "I sent two workers") {
		t.Error("the Radio lists the newest line first")
	}
}

func TestCompanyStations(t *testing.T) {
	wf := &tracker.WorkflowConfig{
		Statuses: []tracker.WorkflowStatus{{Name: "idea"}, {Name: "backlog"}, {Name: "in progress"}, {Name: "human-testing"}, {Name: "done"}, {Name: "parked"}},
		Transitions: []tracker.WorkflowTransition{
			{From: "backlog", To: "in progress", Actions: []tracker.WorkflowAction{{Type: "require_human_approval", Status: "in progress"}}},
		},
	}
	issues := []*tracker.Issue{{Slug: "a", Status: "backlog"}, {Slug: "b", Status: "in progress"}, {Slug: "c", Status: "in progress"}, {Slug: "d", Status: "done"}}
	workers := []AgentNode{{State: "working", Issue: &AgentIssue{Slug: "b", Status: "in progress"}}, {State: "at-gate", Issue: &AgentIssue{Slug: "c", Status: "in progress"}}}
	waiting := []WaitingItem{{Kind: "gate", Target: "in progress", Issue: issues[0]}}
	got := companyStations(wf, issues, workers, waiting)
	var labels []string
	for _, s := range got {
		labels = append(labels, s.Label)
	}
	if strings.Join(labels, ",") != "idea,backlog,gate,in progress,human test" {
		t.Fatalf("line = %v, want the statuses up to done with a gate before in progress", labels)
	}
	if g := got[2]; !g.Gate || g.Count != 1 {
		t.Errorf("gate = %+v, want 1 issue ready for approval", g)
	}
	if s := got[3]; s.Count != 2 || len(s.Crew.Robots) != 2 || s.Crew.Robots[0].State != "you" {
		t.Errorf("in progress = %+v", s)
	}
	if !got[4].Owner {
		t.Error("human testing is the owner's station")
	}
}
