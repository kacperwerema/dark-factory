package viewer

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// AC3: the lead's status shows open asks between sibling workers; answered
// asks and asks on closed issues drop off.
func TestStatusShowsOpenSiblingAsks(t *testing.T) {
	proj := setupStatusProject(t, time.Now())
	ready := filepath.Join(proj.IssueDir, "UI", "ready.md")
	tested := filepath.Join(proj.IssueDir, "UI", "tested.md")
	shipped := filepath.Join(proj.IssueDir, "UI", "shipped.md")
	for _, c := range []struct{ file, text string }{
		{ready, tracker.AskComment("ui/tested", "Which field holds the base branch?")},
		{tested, tracker.AskComment("ui/ready", "Answered already")},
		{shipped, tracker.AskComment("ui/ready", "On a done issue")},
	} {
		if err := tracker.AddCommentBy(c.file, 0, c.text, "cli", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := tracker.MarkCommentsDone(tested, []int{1}); err != nil {
		t.Fatal(err)
	}

	issues, _ := tracker.LoadIssues(proj.IssueDir)
	m := buildStatus(&proj, issues, nil, time.Now())
	if len(m.SiblingAsks) != 1 || m.SiblingAsks[0].From != "ui/tested" || m.SiblingAsks[0].On != "ui/ready" {
		t.Fatalf("sibling asks = %+v", m.SiblingAsks)
	}

	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, body := getResp(t, ts.URL+"/p/status/status.md")
	if !strings.Contains(body, "## Open asks between siblings (1)\n- ui/tested → ui/ready (#1 ") || !strings.Contains(body, "): Which field holds the base branch?\n") {
		t.Errorf("status.md misses the open ask:\n%s", body)
	}
	for _, gone := range []string{"Answered already", "On a done issue"} {
		if strings.Contains(body, gone) {
			t.Errorf("status.md shows %q:\n%s", gone, body)
		}
	}
	if strings.Contains(body, "## Waiting on you (4)") {
		t.Errorf("an ask between workers counts as waiting on the owner:\n%s", body)
	}
}
