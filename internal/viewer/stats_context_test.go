package viewer

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/spend"
)

// AC2: the Context floor trend draws the context family's median floor of
// the worker sessions per day beside their median turn, and is pending
// until a snapshot has the family.
func TestStatsContextFloorTrend(t *testing.T) {
	proj := fixItProject(t)
	y, m, dd := time.Now().Date()
	now := time.Date(y, m, dd, 12, 0, 0, 0, time.Local)
	snap := func(daysAgo int, sessions, floor, turn float64) *health.Snapshot {
		rows := map[string]float64{"context.floor.sessions{role=worker}": sessions}
		if sessions > 0 {
			rows["context.floor.p50{role=worker}"] = floor
			rows["context.turn.p50{role=worker}"] = turn
		}
		s := healthSnap(now.AddDate(0, 0, -daysAgo), rows)
		s.Families[spend.ContextFamily] = health.FamilyResult{}
		return s
	}
	find := func(d *Dashboard) *ChartCard {
		for i := range d.Trends {
			if d.Trends[i].ID == "context" {
				return &d.Trends[i]
			}
		}
		return nil
	}

	d := dashboardFor(t, &proj, "range=7d", []*health.Snapshot{healthSnap(now.AddDate(0, 0, -1), nil)}, now)
	if find(d) != nil {
		t.Error("Context floor card drawn with no context family")
	}
	pending := false
	for _, p := range d.Pending {
		pending = pending || (p.Family == spend.ContextFamily && p.Title == "Context floor")
	}
	if !pending {
		t.Errorf("pending = %+v, want Context floor waiting for the context family", d.Pending)
	}

	// A briefing change on day 2 drops the floor from 82k to 60k; day 3
	// had no worker session (no point, no failure).
	d = dashboardFor(t, &proj, "range=7d", []*health.Snapshot{
		snap(4, 6, 82000, 118000),
		snap(3, 0, 0, 0),
		snap(2, 3, 60000, 90000),
		snap(1, 1, 61000, 95000),
	}, now)
	c := find(d)
	if c == nil {
		t.Fatalf("no Context floor card in %d trends", len(d.Trends))
	}
	if c.Title != "Context floor" || c.Points != 6 || c.Empty != "" || c.Error != "" {
		t.Errorf("card = %q, %d points, empty %q, error %q", c.Title, c.Points, c.Empty, c.Error)
	}
	if !strings.Contains(c.Sub, "issue-cli spend --context") {
		t.Errorf("sub = %q", c.Sub)
	}
	if len(c.Legend) != 2 || c.Legend[0].Name != "floor p50" || c.Legend[1].Name != "turn p50" {
		t.Errorf("legend = %+v", c.Legend)
	}
	table := ""
	for _, r := range c.Table.Rows {
		table += strings.Join(r, " ") + "\n"
	}
	for _, want := range []string{"82k", "60k", "118k"} {
		if !strings.Contains(table, want) {
			t.Errorf("table lacks %s:\n%s", want, table)
		}
	}
	for _, p := range d.Pending {
		if p.Family == spend.ContextFamily {
			t.Error("Context floor still pending with the family present")
		}
	}
}
