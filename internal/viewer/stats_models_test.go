package viewer

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The By model table says how to fill it until a worker ran on a named
// model, then has a row per type and model; a row without a read
// transcript says unknown, never 0.
func TestStatsByModelTable(t *testing.T) {
	empty := modelTable(timeline.Report{Models: []timeline.ModelRow{{Type: "docs", Model: tracker.ModelUnnamed, N: 3}}})
	if empty.Empty == nil || !strings.Contains(empty.Empty.Text, "model:") || len(empty.Rows) != 0 {
		t.Fatalf("no named model: %+v", empty)
	}
	tab := modelTable(timeline.Report{Models: []timeline.ModelRow{
		{Type: "docs", Model: "claude-sonnet-5-5", N: 4, Reopened: 1, RedRounds: 3, RedP50: 1, Tokens: 2_500_000, TokensKnown: 3},
		{Type: "docs", Model: tracker.ModelUnnamed, N: 2},
	}})
	if tab.Empty != nil || len(tab.Rows) != 2 {
		t.Fatalf("table = %+v", tab)
	}
	var cells []string
	for _, c := range tab.Rows[0] {
		cells = append(cells, c.Text)
	}
	got := strings.Join(cells, " | ")
	if got != "docs | claude-sonnet-5-5 | 4 | 1 (25%) | 3 / 1 | 2.5M (3 of 4 read)" {
		t.Errorf("sonnet row = %s", got)
	}
	if last := tab.Rows[1][5].Text; last != "unknown" {
		t.Errorf("no transcript read: %q, want unknown", last)
	}
}
