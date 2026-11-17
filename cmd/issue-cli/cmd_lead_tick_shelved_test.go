package main

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The tick closes the idle session of a report that waits on the owner with
// its output on the shelf, prints a line naming the issue and the output,
// and posts the session: closed comment the Agents tab and a re-dispatch
// read. A plain finished session stays quiet.
func TestLeadTickClosesAShelvedReport(t *testing.T) {
	proj, finished, closed := tickProject(t)
	writeIssue(t, proj, "waiting", "human-testing")
	writeIssue(t, proj, "shipped", "done")
	*finished = []agentrun.Finished{
		{Name: "agent-waiting", Slug: "waiting", Why: "waits", Shelved: `o-1 "Report"`},
		{Name: "agent-shipped", Slug: "shipped", Why: "shipped is done"},
	}
	out := tick(t, proj)
	assertContains(t, out, `closed: agent-waiting (waiting waits on the owner's review at human-testing; its output o-1 "Report" is on the shelf, so it holds no worker slot; re-dispatch resumes it)`)
	assertNotContains(t, out, "agent-shipped")
	if strings.Join(*closed, ",") != "agent-waiting,agent-shipped" {
		t.Fatalf("closed %v", *closed)
	}
	issue := loadLeadIssue(t, proj, "waiting")
	c := tracker.LastSessionClosed(issue.Comments())
	if c == nil {
		t.Fatalf("no session: closed comment on waiting: %+v", issue.Comments())
	}
	for _, want := range []string{"session: closed agent-waiting", "at human-testing", `o-1 "Report" on the shelf`, "Re-dispatch resumes it"} {
		if !strings.Contains(c.Text, want) {
			t.Errorf("comment %q lacks %q", c.Text, want)
		}
	}
}
