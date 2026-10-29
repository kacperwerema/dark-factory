package main

import (
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
		`data-testid="map.lot.agents"`, `href="/p/agents/status"`, `aplikacja PWA`,
		`data-testid="map.lot.test-project"`,
		// child waits at its gate, asker asks a question: both wait on the owner.
		`class="pl-bot pl-bot--you" title="ui/child`, `class="pl-bot pl-bot--you" title="ui/asker`,
		// The lead's newest paragraph is its quote.
		`„Do you want the epic closed?”`, `lead-agents</div>`,
		`Epic: Epic</span><span>0/3</span>`,
		`>Pytanie</span>`, `>Bramka: testing</span>`, `href="/p/agents/issue/ui/child#approve-testing"`,
		`data-show="lot-agents"`, `data-testid="map.lot.waiting"`,
	)
	// Four ready testing gates and asker's question in agents, plus
	// test-project's ready backlog gate.
	if !strings.Contains(html, `>6 czeka na Ciebie</a>`) {
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
	assertContains(t, html, `>Bramka: in progress</span>`, `href="/p/test-project/issue/add-dark-mode#approve-in-progress"`, `>1 czeka na Ciebie</a>`)
}

func TestLastLeadParagraph(t *testing.T) {
	pane := "⏺ I sent **two** workers to `ui/a`.\n" +
		"⏺ Bash(issue-cli status)\n" +
		"  ⎿  ok\n" +
		"> what now?\n"
	if got := lastLeadParagraph(pane); got != "I sent two workers to ui/a." {
		t.Errorf("quote = %q, want the newest paragraph that is not a tool call", got)
	}
	long := "⏺ " + strings.Repeat("słowo ", 60)
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
	if last := b[len(b)-1]; last.Name != "Pozostałe (3)" || last.Open != 3 {
		t.Errorf("last building = %+v, want the rest counted together", last)
	}
	if b[0].Height <= b[1].Height {
		t.Errorf("more open work should stand taller: %d vs %d", b[0].Height, b[1].Height)
	}

	epic := &tracker.Issue{Slug: "e", Title: "Hala", Status: "in progress"}
	issues := []*tracker.Issue{epic,
		{Slug: "e1", Status: "done", ExtraFields: []tracker.ExtraField{{Key: "parent", Value: "e"}}},
		{Slug: "e2", Status: "backlog", ExtraFields: []tracker.ExtraField{{Key: "parent", Value: "e"}}},
	}
	p := mapProgressOf(issues)
	if p == nil || p.Label != "Epic: Hala" || p.Done != 1 || p.Total != 2 || p.Pct() != 50 {
		t.Errorf("progress = %+v", p)
	}
}

func TestSortMapQueueBlockingFirstThenOldest(t *testing.T) {
	now := time.Now()
	q := []PlQueueCard{
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

func TestPolishAge(t *testing.T) {
	for d, want := range map[time.Duration]string{30 * time.Second: "teraz", 12 * time.Minute: "12 min", 3 * time.Hour: "3 h", 50 * time.Hour: "2 d"} {
		if got := polishAge(d); got != want {
			t.Errorf("polishAge(%v) = %q, want %q", d, got, want)
		}
	}
}

// TestPlanszaComponentsStandAlone renders every component on its own: a page
// composes them, so none may lean on the page around it.
func TestPlanszaComponentsStandAlone(t *testing.T) {
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	progress := &PlProgress{Label: "Epic: Hala", Done: 2, Total: 7}
	card := PlQueueCard{Label: "Pytanie", Tone: "you", Project: "Jemik", ProjectSlug: "meals", Slug: "zakupy/x", CTA: "Odpowiedz", URL: "/p/meals/issue/zakupy/x"}
	for name, data := range map[string]any{
		"pl/head":       "Mapa",
		"pl/header":     PlHeader{Crumbs: plCrumbs(1, "Jemik", "/p/meals/status"), Working: 2, Waiting: 3},
		"pl/robot":      PlRobot{State: "agent", Label: "ui/b → ui/a"},
		"pl/robots":     plCrew([]PlRobot{{State: "working"}}),
		"pl/chip":       *plCIChip("red", "make validate"),
		"pl/chips":      plCountChips(1, 1, 1, 2),
		"pl/ceo":        PlCEO{Session: "lead-meals", Live: true, Quote: "Mam 3 sprawy."},
		"pl/progress":   progress,
		"pl/bang":       3,
		"pl/skyline":    []PlBuilding{{Name: "Plan", Open: 3, Color: "#3D7BF7", Width: 48, Height: 25}},
		"pl/lot":        MapLot{Name: "Jemik", Slug: "meals", Color: "#13A386", Progress: progress, Waiting: 1},
		"pl/queue-card": card,
		"pl/side":       []PlQueueCard{card},
	} {
		var b strings.Builder
		if err := srv.tmpl.ExecuteTemplate(&b, name, data); err != nil || strings.TrimSpace(b.String()) == "" {
			t.Errorf("%s: %v (output %q)", name, err, b.String())
		}
	}
	var b strings.Builder
	srv.tmpl.ExecuteTemplate(&b, "pl/header", PlHeader{Crumbs: plCrumbs(1, "Jemik", "/p/meals/status")})
	assertContains(t, b.String(), `<a class="pl-crumb pl-crumb--link" href="/">Mapa</a>`, `aria-current="page">Jemik</span>`, `title="Wejdź w projekt, potem w halę">Hala</span>`)
}
