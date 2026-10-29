package main

import (
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The fixture (setupDecisionsProject): sim/pivot#D-1 has the owner's words
// and replaces sim/phases#D-1 (no words, still cited by sim/creeps, so
// stale); shop#D-1 has words, shop#D-2 is deferred without words.

func TestDecisionsLogPage(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/wb/decisions")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`<link rel="stylesheet" href="/static/workshop.css">`,
		`href="/p/wb/company"`, `aria-current="page"><i class="wb-crumb-sq" style="background:#3D7BF7"></i>Product hall</span>`,
		`<h1 class="wb-h1 wb-h1--big">Decisions log</h1>`,
		`href="/p/wb/decisions?classic=1" data-testid="decisions.classic">Classic view</a>`,
		`<div class="wb-statbig-num">4</div><div class="wb-statbig-label">decisions · 2 active</div>`,
		`wb-summary wb-summary--warn" data-testid="summary.decisions">`, `1 superseded · 1 stale citations`,
		`data-testid="search.decisions"`, `data-testid="filter.who_all">All</a>`,
		`With your words`, `Without your words · to confirm`,
		`data-testid="decisions.day"><span>`,
		`data-testid="decisions.thread.sim/pivot"`, `href="/p/wb/issue/sim/pivot">pivot</a>`,
		`<a class="wb-tag-pill" href="/p/wb/decisions?system=Sim">Sim</a>`, `wb-tag-pill--mono">done</span>`,
		// Words and who recorded them.
		`data-testid="decisions.row.sim/pivot.d-1.quote"`, `<blockquote>“no readable tension”</blockquote>`,
		`The raid becomes an <strong>ET-style</strong> objective format &lt;6v6&gt;.`,
		`recorded by: CEO (lead chat)`, `recorded by: chat`,
		`refers to <a class="wb-mono-inline wb-drow-ref" href="/p/wb/issue/sim/phases">sim/phases#D-1</a>`,
		`superseded by <a class="wb-mono-inline wb-drow-ref" href="/p/wb/issue/sim/pivot">sim/pivot#D-1</a>`,
		`cited by: <a class="wb-mono-inline" href="/p/wb/issue/sim/creeps">sim/creeps</a>`, `cited by: nobody yet`,
		`also considered: A Reset`,
		// Chips: active, deferred, stale.
		`wb-chip wb-chip--green">active</span>`, `wb-chip wb-chip--idle">deferred</span>`, `wb-chip wb-chip--red">superseded · still cited</span>`,
		// The decision taken alone asks the owner; the CEO is off, so the
		// buttons say so instead of pretending.
		`data-testid="decisions.row.shop.d-2.confirm"`, `data-send="/p/wb/lead/send"`,
		`value="I confirm shop#D-2: &#34;Balance later.&#34;. Record it with my words (issue-cli decide shop --replaces shop#D-2)."`,
		`The CEO is not running`, `data-testid="decisions.start_ceo"`,
		// Right panel.
		`data-testid="bars.decisions_by_system"`, `<span class="wb-bar-label">Economy</span>`, `<span class="wb-bar-count">2</span>`,
		`<h2 class="wb-h2">Log hygiene</h2>`, `<span>Superseded</span><b>1</b>`, `<span>Stale citations</span><b class="wb-tone-red">1</b>`,
		`<span>Cited by other issues</span><b>2 decisions</b>`,
		`data-testid="command.decide"`, `issue-cli decide`,
		`data-testid="company.message"`,
	)
	// A replaced decision does not ask for words; one with words neither.
	for _, id := range []string{"sim/phases.d-1", "sim/pivot.d-1", "shop.d-1"} {
		if strings.Contains(html, `data-testid="decisions.row.`+id+`.confirm"`) {
			t.Errorf("%s should not ask for the owner's words", id)
		}
	}
	// Newest day first.
	if a, b := strings.Index(html, `data-ref="shop#D-2"`), strings.Index(html, `data-ref="sim/phases#D-1"`); !(a >= 0 && a < b) {
		t.Errorf("not newest first: %d %d", a, b)
	}
	if strings.Contains(html, "decisions-table") {
		t.Error("the new page renders the classic table")
	}
}

func TestDecisionsLogConfirmWithLiveCEO(t *testing.T) {
	proj := setupDecisionsProject(t)
	stubLeadPane(t, "⏺ Ready.\n")
	withMockTmuxSessions(t, []AgentSession{{Name: tracker.LeadSessionName(&proj)}})
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/wb/decisions")
	assertContains(t, html, `<button type="submit" class="wb-alone-btn" title="Sends`, `data-question="I question shop#D-2 (&#34;Balance later.&#34;): "`)
	if strings.Contains(html, "The CEO is not running") || strings.Contains(html, `data-testid="decisions.start_ceo"`) {
		t.Error("a live CEO still shows as off")
	}
}

func TestDecisionsLogFilters(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	refs := []string{"sim/pivot#D-1", "sim/phases#D-1", "shop#D-1", "shop#D-2"}
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"who=words", []string{"sim/pivot#D-1", "shop#D-1"}},
		{"who=alone", []string{"sim/phases#D-1", "shop#D-2"}},
		{"q=GOLD", []string{"shop#D-1"}},
		{"q=raid+tension", []string{"sim/pivot#D-1"}},
		{"system=sim", []string{"sim/pivot#D-1", "sim/phases#D-1"}},
		{"system=Economy&who=alone", []string{"shop#D-2"}},
		{"state=stale", []string{"sim/phases#D-1"}},
	} {
		_, html := getBody(t, ts.URL+"/p/wb/decisions?"+c.query)
		for _, ref := range refs {
			want := false
			for _, w := range c.want {
				want = want || w == ref
			}
			if got := strings.Contains(html, `data-ref="`+ref+`"`); got != want {
				t.Errorf("?%s: %s shown=%v, want %v", c.query, ref, got, want)
			}
		}
	}

	_, html := getBody(t, ts.URL+"/p/wb/decisions?who=alone&system=Sim")
	assertContains(t, html,
		`wb-pill wb-pill--on wb-pill--you" href="/p/wb/decisions?system=Sim&amp;who=alone" aria-current="true" data-testid="filter.who_alone"`,
		`href="/p/wb/decisions?who=alone" data-testid="filter.clear_system">System: Sim`,
		`<input type="hidden" name="who" value="alone">`, `<input type="hidden" name="system" value="Sim">`,
		// The counts and the bars stay over the whole log.
		`<div class="wb-statbig-num">4</div>`, `<span class="wb-bar-label">Economy</span>`,
	)
	_, html = getBody(t, ts.URL+"/p/wb/decisions?q=nothing-like-this")
	assertContains(t, html, `data-testid="decisions.nomatch"`, `href="/p/wb/decisions" data-testid="decisions.nomatch.clear"`)
}

func TestDecisionsLogClassicAndEmpty(t *testing.T) {
	proj := setupDecisionsProject(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/wb/decisions?classic=1")
	assertContains(t, html, `data-testid="decisions.table"`, `href="/p/wb/decisions" data-testid="decisions.new_view">New view</a>`)

	f := setupWorkbenchProject(t, time.Now())
	ts2 := newTestServer(t, []tracker.Project{f.proj})
	defer ts2.Close()
	_, html = getBody(t, ts2.URL+"/p/wb/decisions")
	assertContains(t, html, `data-testid="decisions.empty"`, "No decisions recorded yet", `<div class="wb-statbig-num">0</div>`, "No decision sits in an issue with a system yet.", `<span>Cited by other issues</span><b>none yet</b>`)
	if strings.Contains(html, `data-testid="search.decisions"`) || strings.Contains(html, `summary.decisions`) {
		t.Error("an empty log shows search or a summary")
	}
}

func TestDecisionsComponentsStandAlone(t *testing.T) {
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	row := WbDecisionRow{ID: "D-2", Ref: "ui/a#D-2", Slug: "ui/a", Text: "Limit 12.", By: "agent", ByTone: "agent", State: WbChip{Tone: "green", Text: "active"},
		Confirm: &WbConfirm{SendURL: "/p/x/lead/send", ConfirmText: "I confirm ui/a#D-2", QuestionText: "I question ui/a#D-2: ", Live: true}}
	for name, data := range map[string]any{
		"wb/stat-big":        WbStat{Value: "77", Label: "decisions"},
		"wb/summary-box":     WbSummaryBox{Text: "0 superseded", OK: true, Key: "decisions"},
		"wb/search-bar":      WbSearchBar{Action: "/p/x/decisions", Name: "q", Key: "decisions", Filters: []WbFilterPill{{Label: "All", URL: "/p/x/decisions", On: true, Key: "who_all"}}},
		"wb/filter-pill":     WbFilterPill{Label: "Without your words", Tone: "you", Key: "who_alone"},
		"wb/day-label":       "Thu 29 Oct",
		"wb/you-quote":       "Keep the key",
		"wb/decision-alone":  row,
		"wb/decision-row":    row,
		"wb/decision-thread": WbDecisionThread{Title: "A", Slug: "ui/a", URL: "/p/x/issue/ui/a", System: "UI", Rows: []WbDecisionRow{row}},
		"wb/bars":            WbBars{Title: "By system", Key: "x", Rows: []WbCountBar{{Label: "UI", Count: 3, Pct: 100}}},
		"wb/command-card":    WbCommandCard{Title: "Add a decision", Command: "issue-cli decide", Key: "decide"},
	} {
		var b strings.Builder
		if err := srv.tmpl.ExecuteTemplate(&b, name, data); err != nil || strings.TrimSpace(b.String()) == "" {
			t.Errorf("%s: %v (output %q)", name, err, b.String())
		}
	}
}

func TestDecisionsByAndDayLabel(t *testing.T) {
	for how, want := range map[string]string{
		"lead chat":                   "CEO (lead chat)",
		"answered in lead chat (Q-1)": "CEO (lead chat)",
		"recorded by agent":           "agent",
		"":                            "agent",
		"project rule":                "project rule",
	} {
		if got, _ := decisionsBy(how); got != want {
			t.Errorf("decisionsBy(%q) = %q, want %q", how, got, want)
		}
	}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	if got := decisionsDayLabel(time.Date(2026, 10, 29, 0, 0, 0, 0, time.Local), now); got != "Thu 29 Oct" {
		t.Errorf("day label = %q", got)
	}
	if got := decisionsDayLabel(time.Date(2025, 10, 29, 0, 0, 0, 0, time.Local), now); got != "Wed 29 Oct 2025" {
		t.Errorf("other year = %q", got)
	}
}
