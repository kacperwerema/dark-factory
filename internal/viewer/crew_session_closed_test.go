package viewer

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A stopped worker whose session the lead tick closed while its report waits
// on the owner says why on the Agents tab (agents.json and the Crew card),
// until a dispatch: line brings the worker back.
func TestStoppedWorkerSaysWhyItsSessionClosed(t *testing.T) {
	proj := setupAgentsProject(t)
	proj.TmuxSession = "work"
	stubAgentsGit(t)
	withMockTmuxSessions(t, nil)
	idle := filepath.Join(proj.IssueDir, "UI", "idle.md")
	closed := tracker.SessionClosedComment("agent-ui-idle", "in progress", `o-1 "Report"`)
	if err := tracker.AddCommentBy(idle, 0, closed, "cli", "lead-agents"); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	stopped := func() AgentNode {
		_, body := getResp(t, ts.URL+"/p/agents/agents.json")
		var m AgentsModel
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		for _, n := range m.Unfinished {
			if n.Name == "agent-ui-idle" {
				return n
			}
		}
		t.Fatalf("no stopped agent-ui-idle: %+v", m.Unfinished)
		return AgentNode{}
	}
	if n := stopped(); n.Closed != closed {
		t.Errorf("closed = %q, want %q", n.Closed, closed)
	}
	_, html := getBody(t, ts.URL+"/p/agents/agents")
	assertContains(t, html, `data-testid="crew.card.agent-ui-idle"`, crewSessionClosed)

	if err := tracker.AddCommentBy(idle, 0, tracker.DispatchComment("go on", "redispatch", "2026-11-17"), "cli", "lead-agents"); err != nil {
		t.Fatal(err)
	}
	if n := stopped(); n.Closed != "" {
		t.Errorf("after a dispatch the card still says %q", n.Closed)
	}
	if _, html := getBody(t, ts.URL+"/p/agents/agents"); strings.Contains(html, crewSessionClosed) {
		t.Error("after a dispatch the Crew card still says the session closed")
	}
}
