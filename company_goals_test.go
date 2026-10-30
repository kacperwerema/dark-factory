package main

import (
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func goalTarget(v float64) *float64 { return &v }

// TestCompanyGoals: HQ's configured goals replace the epics in the header,
// HQ lists the department goals rolling into each, and the rooms and the
// Product line show their mission and goals.
func TestCompanyGoals(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	a.Company = &tracker.Company{Mission: "Grow the client roster", Goals: []tracker.Goal{
		{ID: "clients", Name: "Paying clients", Target: goalTarget(20)},
		{ID: "revenue", Name: "Revenue", Target: goalTarget(1000), Unit: "EUR", Period: "month"},
	}}
	a.Product = &tracker.Department{Name: "Product", Mission: "Ship the engine", Goals: []tracker.Goal{
		{ID: "bugs", Name: "Open bugs", Target: goalTarget(5), Direction: "down"},
	}}
	a.Departments = []tracker.Department{{Name: "Marketing", Color: "#E2559B", Mission: "Every client site weekly", Systems: []string{"UI"}, Goals: []tracker.Goal{
		{ID: "pages", Name: "Pages refreshed", Target: goalTarget(40), RollsUp: "clients"},
		{ID: "posts", Name: "Posts", Target: goalTarget(10), RollsUp: "clients"},
	}}}
	today := time.Now().Format("2006-01-02")
	for _, e := range []goals.Entry{
		{Goal: "marketing/pages", Value: 30, Date: today},
		{Goal: "marketing/posts", Value: 5, Date: today},
		{Goal: "product/bugs", Value: 10, Date: today},
		{Goal: "hq/revenue", Value: 400, Date: "2020-01-15"},
	} {
		if err := goals.Append(&a, e); err != nil {
			t.Fatal(err)
		}
	}
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/company")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		// Header: HQ's goals, rolled-up progress for clients (mean of 75 and 50).
		`<span class="wb-goal-name">Paying clients</span>`, `<span class="wb-goal-val">63%</span>`, `title="mean progress of 2 department goals"`,
		// A stale value is muted, with its date in the tooltip.
		`wb-goal-chip--off" data-testid="company.goal" title="stale: from 2020-01-15"`, `<span class="wb-goal-val">400 EUR / 1000 EUR</span>`,
		// HQ: mission and the roll-up.
		`data-testid="company.room.mission">Grow the client roster</div>`, `data-testid="company.hq.rollup"`,
		`data-testid="company.hq.rollup.child"`, `<span class="wb-goal-name">Marketing · Pages refreshed</span>`,
		`<i style="width:75%;background:#E2559B"></i>`,
		// Rooms and the line.
		`data-testid="company.room.goals"`, `Every client site weekly`, `<span class="wb-goal-val">30 / 40</span>`,
		`Ship the engine`, `<span class="wb-goal-name">Open bugs</span>`, `<span class="wb-goal-val">10 / 5</span>`, `<i style="width:50%;background:#3D7BF7"></i>`,
	)
	if strings.Contains(html, `<span class="wb-goal-name">Epic</span>`) {
		t.Error("configured HQ goals replace the open-epics stand-in")
	}
}
