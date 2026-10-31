package main

import (
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/critique"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The station shows the newest critic run: verdict, blocks and should
// findings with path:line, malformed ones, notes folded, Coverage.
func TestStationShowsCritiqueFindings(t *testing.T) {
	p := setupStationProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	run := &critique.Run{Slug: "ui/station", From: "in progress", To: "testing", At: time.Now().Add(-2 * time.Hour), Agent: "codex",
		Report: critique.Parse("blocks internal/x.go:42 — nil on an empty list\nshould docs/a.md:3 — cite D-2\nshould docs/a.md:7 — two cases:\n- the empty list\n- a nil map\nblocks vague words\nnote templates/b.html:9 — fine\nCoverage: read 3 of 4 files\nNot read: static/c.js\n")}
	if err := critique.Record(p.IssueDir, run); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/issue/ui/station")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	assertContains(t, html,
		`data-testid="station.critique" data-verdict="holds"`, `Critique · holds`, `in progress → testing · codex · 2 h ago`,
		`Holds the gate`, `2 blocks · 2 should · 1 note`,
		"<code>docs/a.md:7</code> two cases:\n- the empty list\n- a nil map</li>",
		`data-severity="blocks"><b>blocks</b> <code>internal/x.go:42</code> nil on an empty list`,
		`data-severity="should"><b>should</b> <code>docs/a.md:3</code> cite D-2`,
		`<b>blocks</b> (malformed) blocks vague words`,
		`<summary>1 note</summary>`, `Coverage: read 3 of 4 files`, `<code>static/c.js</code>`)
}

// No run and no critique gate: no block. A gate but no run: the empty
// state names the command.
func TestStationCritiqueEmptyState(t *testing.T) {
	p := setupStationProject(t)
	wf := &tracker.WorkflowConfig{Statuses: []tracker.WorkflowStatus{{Name: "in progress"}, {Name: "testing"}},
		Transitions: []tracker.WorkflowTransition{{From: "in progress", To: "testing", Actions: []tracker.WorkflowAction{{Type: "validate", Rule: "critique"}}}}}
	issue := &tracker.Issue{Slug: "ui/station", Status: "in progress"}
	c := stationCritique(&p, wf, issue, time.Now())
	if c == nil || !c.Empty || strings.Join(c.Gates, ",") != "in progress → testing" {
		t.Fatalf("empty state = %+v", c)
	}
	if stationCritique(&p, &tracker.WorkflowConfig{}, issue, time.Now()) != nil {
		t.Error("a block without a gate or a run")
	}
}
