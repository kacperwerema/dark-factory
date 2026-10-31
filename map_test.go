package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/goals"
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

func TestMapRobotsWaitOnDependsOn(t *testing.T) {
	// depends_on waits on an agent just as blocked_on does: a live target
	// counts once even when both fields name it; a target with no worker
	// leaves the robot working.
	dep := &tracker.Issue{Slug: "ui/b", ExtraFields: []tracker.ExtraField{{Key: "depends_on", IsList: true, Values: []string{"ui/z", "UI/A"}}}}
	both := &tracker.Issue{Slug: "ui/c", ExtraFields: []tracker.ExtraField{{Key: "blocked_on", Value: "ui/a"}, {Key: "depends_on", Value: "ui/a"}}}
	free := &tracker.Issue{Slug: "ui/d", ExtraFields: []tracker.ExtraField{{Key: "depends_on", Value: "ui/z"}}}
	issues := []*tracker.Issue{{Slug: "ui/a"}, dep, both, free}
	agents := &AgentsModel{Workers: []AgentNode{
		{Name: "agent-ui-a", State: "working", Issue: &AgentIssue{Slug: "ui/a"}},
		{Name: "agent-ui-b", State: "working", Issue: &AgentIssue{Slug: "ui/b"}},
		{Name: "agent-ui-c", State: "working", Issue: &AgentIssue{Slug: "ui/c"}},
		{Name: "agent-ui-d", State: "working", Issue: &AgentIssue{Slug: "ui/d"}},
	}}
	got := mapRobotsOf(agents, issues)
	want := []string{"working", "agent", "agent", "working"}
	labels := []string{"ui/a", "ui/b → ui/a", "ui/c → ui/a", "ui/d"}
	if len(got) != len(want) {
		t.Fatalf("robots = %+v", got)
	}
	for i, r := range got {
		if r.State != want[i] || r.Label != labels[i] {
			t.Errorf("robot %d = %s %q, want %s %q", i, r.State, r.Label, want[i], labels[i])
		}
	}
}

func TestMapBuildingsAndProgress(t *testing.T) {
	// The lot shows the project's departments: a small HQ and Product,
	// taller with more open work.
	five := make([]*tracker.Issue, 5)
	for i := range five {
		five[i] = &tracker.Issue{Slug: fmt.Sprint(i), Status: "backlog"}
	}
	thirty := make([]*tracker.Issue, 30)
	for i := range thirty {
		thirty[i] = &tracker.Issue{Slug: fmt.Sprint(i), Status: "backlog"}
	}
	proj := &tracker.Project{}
	b := mapDepartmentsOf(proj, five)
	if len(b) != 2 || b[0].Name != "HQ · CEO" || b[0].Color != companyHQColor || b[1].Name != "Product" || b[1].Open != 5 || b[1].Color != companyProductColor {
		t.Fatalf("buildings = %+v", b)
	}
	if big := mapDepartmentsOf(proj, thirty)[1]; big.Height <= b[1].Height || big.Width <= b[1].Width {
		t.Errorf("more open work should stand taller and wider: %+v vs %+v", big, b[1])
	}
	if b[0].Width >= b[1].Width {
		t.Error("HQ is the small building")
	}

	epic := &tracker.Issue{Slug: "e", Title: "Hall", Status: "in progress"}
	issues := []*tracker.Issue{epic,
		{Slug: "e1", Status: "done", ExtraFields: []tracker.ExtraField{{Key: "parent", Value: "e"}}},
		{Slug: "e2", Status: "backlog", ExtraFields: []tracker.ExtraField{{Key: "parent", Value: "e"}}},
	}
	p := mapProgressOf(&goals.Report{}, issues, "#13A386")
	if p == nil || p.Label != "Epic: Hall" || p.Done != 1 || p.Total != 2 || p.Pct() != 50 || p.Text != "" {
		t.Errorf("progress = %+v", p)
	}
}

func TestMapProgressPrefersGoalsOverTheEpic(t *testing.T) {
	epic := &tracker.Issue{Slug: "e", Title: "Hall", Status: "in progress"}
	issues := []*tracker.Issue{epic, {Slug: "e1", Status: "done", ExtraFields: []tracker.ExtraField{{Key: "parent", Value: "e"}}}}
	num := func(f float64) *float64 { return &f }
	pct := func(n int) *int { return &n }
	users := &goals.Goal{ID: "users", Name: "Active users", Value: num(30), Target: num(40), Progress: pct(75)}
	leads := &goals.Goal{ID: "leads", Name: "Leads", Target: num(10), Source: "manual", Key: "dept:Marketing/leads"}

	hq := &goals.Report{HQ: &goals.Owner{Key: goals.HQKey, Name: "HQ", Goals: []*goals.Goal{users}},
		Departments: []*goals.Owner{{Key: "dept:Marketing", Name: "Marketing", Goals: []*goals.Goal{leads}}}}
	if p := mapProgressOf(hq, issues, "#13A386"); p == nil || p.Label != "Goal: Active users" || p.Text != "30 / 40" || p.Pct() != 75 {
		t.Errorf("HQ goal first: %+v", p)
	}

	dept := &goals.Report{HQ: &goals.Owner{Key: goals.HQKey, Name: "HQ", Goals: []*goals.Goal{}},
		Departments: []*goals.Owner{{Key: "dept:Marketing", Name: "Marketing", Goals: []*goals.Goal{leads}}}}
	p := mapProgressOf(dept, issues, "#13A386")
	if p == nil || p.Label != "Goal: Marketing · Leads" || p.Text != "no value yet" || p.Pct() != 0 {
		t.Fatalf("department goal without a value: %+v", p)
	}
	if !strings.Contains(p.Title, "issue-cli goal set dept:Marketing/leads") {
		t.Errorf("title should give the set command: %q", p.Title)
	}
}

func TestMapNewProjectTileListsTheBundledTemplates(t *testing.T) {
	tile := mapNewProjectTile()
	for _, name := range []string{"development", "content", "review", "writing"} {
		if !strings.Contains(tile.Sub, name) {
			t.Errorf("Sub %q lacks template %s", tile.Sub, name)
		}
	}
	if strings.Contains(tile.Sub, "game") || !strings.Contains(tile.Note, "issue-cli workflow init --template") {
		t.Errorf("tile = %+v", tile)
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
		"wb/room-dept":    WbRoomDept{Head: WbRoomHead{Title: "Marketing", Color: "#E2559B"}, Role: "Posts", Crew: wbCrew(nil)},
		"wb/room-factory": WbRoomFactory{Head: WbRoomHead{Title: "Product"}, HallURL: "/p/meals/board"},
		"wb/new-tile":     WbNewTile{Title: "New department", TestID: "new_department"},
		"wb/radio":        []WbMessage{{From: "lead-meals", To: "you", Initials: "CEO", Tone: "ceo", Text: "Hi"}},
		"wb/message-box":  WbMessageBox{Session: "lead-meals", SendURL: "/p/meals/lead/send"},
		"wb/site":         MapLot{Name: "Cost Estimator", Slug: "cost-estimator"},
		"wb/plan-card":    WbPlanCard{Label: "MISSING STAGE", Title: "Retro and feedback", TestID: "retro"},
		"wb/crate":        WbCrate{Slug: "ui/a", Robot: &WbRobot{State: "you"}, Question: true, CI: true},
		"wb/gate-card":    WbGateCard{Name: "Gate", Status: "backlog", Waiting: 2},
		"wb/stamp-icon":   nil,
		"wb/hall-station": WbHallStation{Name: "In progress", Status: "in progress", Crates: []WbCrate{{Slug: "ui/a"}}},
		"wb/hall-stop":    WbHallStop{Gate: &WbGateCard{Name: "Gate"}},
		"wb/done-station": WbDoneStation{Count: 3, Stack: make([]int, 3)},
		"wb/ci-scanner":   WbCIScanner{Red: true, Command: "make validate", Branch: "main"},
		"wb/parking":      WbParking{Count: 1, Items: []WbParked{{Slug: "ui/x", Reason: "after beta"}}},
		"wb/agent-panel":  WbAgentPanel{Slug: "ui/a", Robot: WbRobot{State: "you"}, Path: []WbPathStep{{Status: "idea", Tone: "done"}}},
		"wb/numbers":      WbNumbers{Title: "The hall in numbers", Rows: []WbFact{{Label: "Done", Value: "3"}}},
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
		`id="lot-cost-estimator" data-slug="cost-estimator" data-site="1"`, `data-testid="plan.site_cost_estimator"`, `AI cost estimates for building projects.`,
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
		`href="/p/agents/hall" data-testid="company.enter_hall"`,
		`data-testid="tile.new_department"`, `From a template: Marketing`, `This project has no projects.yaml entry`,
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

func TestHallPage(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "⏺ Working on it.\n")
	withMockTmuxSessions(t, []AgentSession{{Name: "lead-agents"}, {Name: "agent-ui-child"}, {Name: "agent-ui-asker"}})
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/agents/hall")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`href="/p/agents/company"`, `aria-current="page"><i class="wb-crumb-sq" style="background:#3D7BF7"></i>Product hall</span>`,
		`data-testid="hall.line"`, `<span class="wb-hst-name">In progress</span>`, `<span class="wb-hst-name">Backlog</span>`,
		`data-testid="hall.gate"`, `<span class="wb-mono">testing</span>`,
		// The asker carries its crate with a question for the owner.
		`<span class="wb-crate-slug">ui/asker</span><span class="wb-crate-q"`,
		`data-testid="hall.done"`, `data-testid="hall.parking"`, `data-testid="plan.retro"`, `Retro and feedback`,
		// The default selection waits on the owner; the box stays empty.
		`data-testid="hall.agent"`, `id="wb-msg-input" type="text" name="text" autocomplete="off" value=""`, `data-testid="hall.numbers"`,
	)
	if strings.Contains(html, "approved:") {
		t.Error("the hall prefills approval words for the default selection")
	}
	_, html = getBody(t, ts.URL+"/p/agents/hall?agent=ui/idle")
	// The owner's pick addresses the box, without approval words.
	assertContains(t, html, `href="/p/agents/issue/ui/idle">ui/idle</a>`, `No worker`, `value="ui/idle: "`)
	if strings.Contains(html, "approved:") {
		t.Error("the hall prefills approval words for the owner's pick")
	}
	if !strings.Contains(html, `wb-crate-line wb-crate-line--on" href="/p/agents/hall?agent=ui/idle#agent"`) {
		t.Error("the selected crate is ringed")
	}
}

func TestDepartmentsOnlyWhenConfigured(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)

	// By default: HQ and Product, nothing else.
	ts := newTestServer(t, []tracker.Project{a})
	_, home := getBody(t, ts.URL+"/")
	_, company := getBody(t, ts.URL+"/p/agents/company")
	ts.Close()
	if n := strings.Count(home, `class="wb-bld"`); n != 2 {
		t.Errorf("buildings by default = %d, want HQ and Product", n)
	}
	if strings.Contains(company, `data-testid="company.room.dept"`) {
		t.Error("no department rooms unless projects.yaml adds them")
	}
	assertContains(t, company, `2 departments`)

	// A department in projects.yaml: a building, a room, and its issues
	// leave Product's line.
	a.Departments = []tracker.Department{{Name: "Marketing", Color: "#E2559B", Role: "Social posts and release notes", Systems: []string{"UI"}}}
	ts = newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, home = getBody(t, ts.URL+"/")
	_, company = getBody(t, ts.URL+"/p/agents/company")
	assertContains(t, home, `title="Marketing: 4 open"`, `background-color:#E2559B`)
	assertContains(t, company, `3 departments`, `data-testid="company.room.dept"`, `>Marketing</span>`, `Social posts and release notes`, `systems: UI`)
	if n := strings.Count(home, `class="wb-bld"`); n != 3 {
		t.Errorf("buildings = %d, want HQ, Product and Marketing", n)
	}
}

func TestDepartmentOf(t *testing.T) {
	p := &tracker.Project{Departments: []tracker.Department{{Name: "Marketing", Systems: []string{"Social"}}}}
	byField := &tracker.Issue{System: "UI", ExtraFields: []tracker.ExtraField{{Key: "department", Value: "marketing"}}}
	bySystem := &tracker.Issue{System: "social"}
	product := &tracker.Issue{System: "UI"}
	if p.DepartmentOf(byField) != "Marketing" || p.DepartmentOf(bySystem) != "Marketing" || p.DepartmentOf(product) != "" {
		t.Errorf("departments: %q %q %q", p.DepartmentOf(byField), p.DepartmentOf(bySystem), p.DepartmentOf(product))
	}
}
