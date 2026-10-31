package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The Station shows "red N of M" for each check red on the issue.
func TestStationShowsRedRounds(t *testing.T) {
	p := setupStationProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	path := filepath.Join(p.IssueDir, "UI", "station.md")
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	var rs []tracker.RoundResult
	for i, head := range []string{"h1", "h2", "h3"} {
		rs = append(rs, tracker.RoundResult{Check: "gate:criteria_proven", Red: true, At: at.Add(time.Duration(i) * time.Minute), Head: head, Why: "AC2 has no proof"})
	}
	if err := tracker.AppendRoundResults(path, rs...); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/issue/ui/station")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html, `data-testid="station.tests.red_rounds"`, `red 3 of 4`, `gate:criteria_proven`, `AC2 has no proof`)
}
