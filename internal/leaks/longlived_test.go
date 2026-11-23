package leaks

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/procs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func startedAt(t time.Time) string { return t.Local().Format("2006-01-02 15:04:05") }

// TestLongSessionsFlagsLeadsPastTheirRotation: a CEO and a department lead
// older than 26 h are flagged with their rotate, a young lead and a worker
// are not, a lead no project owns is an orphan with its kill-session, and a
// project whose flush has no standing start says so.
func TestLongSessionsFlagsLeadsPastTheirRotation(t *testing.T) {
	now := time.Date(2026, 11, 21, 8, 30, 0, 0, time.Local)
	orchard := &tracker.Project{Slug: "orchard", Departments: []tracker.Department{{Name: "Marketing"}}}
	quince := &tracker.Project{Slug: "quince"}
	sessions := []agentrun.Session{
		{Name: "lead-orchard", StartTime: startedAt(now.Add(-2 * time.Hour))},
		{Name: "lead-orchard-marketing", StartTime: startedAt(now.AddDate(0, 0, -8))},
		{Name: "lead-quince", StartTime: startedAt(now.AddDate(0, 0, -6))},
		{Name: "lead-test-project", StartTime: startedAt(now.AddDate(0, 0, -19))},
		{Name: "agent-orchard-x", StartTime: startedAt(now.AddDate(0, 0, -9))},
		{Name: "shared:lead-orchard", StartTime: startedAt(now.AddDate(0, 0, -9))},
	}
	got := LongSessions(SessionsInput{Sessions: sessions, Projects: []*tracker.Project{orchard, quince}, Now: now,
		FlushOn: func(p *tracker.Project) bool { return p.Slug != "quince" }})
	var lines []string
	for _, f := range got {
		lines = append(lines, f.Kind+" "+f.Line())
	}
	all := strings.Join(lines, "\n")
	if len(got) != 3 {
		t.Fatalf("findings:\n%s", all)
	}
	for _, want := range []string{
		"lead-orphan lead-test-project has run 19d0h and no project in projects.yaml owns it: tmux kill-session -t lead-test-project",
		"lead-unrotated lead-orchard-marketing has run 8d0h without rotating: issue-cli --project orchard lead rotate --lead lead-orchard-marketing",
		"lead-unrotated lead-quince has run 6d0h without rotating (its nightly lead-flush has no standing start",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

// TestLongLivedReadsViewerLeadsAndCI: the viewer and a CI run by their own
// RSS, a lead by its pane's whole tree; other processes not at all.
func TestLongLivedReadsViewerLeadsAndCI(t *testing.T) {
	now := time.Date(2026, 11, 21, 8, 0, 0, 0, time.Local)
	start := now.Add(-50 * time.Hour)
	ms := []procs.Mem{
		{PID: 10, PPID: 1, Started: start, RSSKB: 400 * 1024, Command: "/Users/x/.local/bin/issue-viewer -config p.yaml"},
		{PID: 20, PPID: 1, Started: start, RSSKB: 1024, Command: "-zsh"},
		{PID: 21, PPID: 20, Started: start, RSSKB: 300 * 1024, Command: "claude --name lead-orchard"},
		{PID: 22, PPID: 21, Started: start, RSSKB: 100 * 1024, Command: "node mcp-server"},
		{PID: 30, PPID: 1, Started: start, RSSKB: 50 * 1024, Command: "issue-cli --project orchard ci run"},
		{PID: 40, PPID: 1, Started: start, RSSKB: 900 * 1024, Command: "Google Chrome"},
	}
	got := longLivedFrom(ms, map[int]string{20: "lead-orchard", 99: "lead-gone"}, now)
	var names []string
	for _, r := range got {
		names = append(names, fmt.Sprintf("%s=%.0f", r.Name, r.RSSMB))
	}
	if strings.Join(names, " ") != "issue-cli ci run (pid 30)=50 issue-viewer=400 lead-orchard=401" {
		t.Fatalf("readings: %v", names)
	}
}

// TestGrownNeedsADayOld: a process that more than doubled over 24 h and
// is over the floor is grown; one younger than the window, one that grew
// less, one under the floor and one with no fresh reading are not, and a
// restart is a new process.
func TestGrownNeedsADayOld(t *testing.T) {
	now := time.Date(2026, 11, 21, 8, 0, 0, 0, time.Local)
	r := func(key string, ago time.Duration, mb float64) MemReading {
		name, _, _ := strings.Cut(key, "@")
		return MemReading{At: now.Add(-ago), Key: key, Name: name, RSSMB: mb}
	}
	rs := []MemReading{
		r("issue-viewer@1", 25*time.Hour, 400), r("issue-viewer@1", 10*time.Minute, 1020),
		r("lead-young@1", 20*time.Hour, 200), r("lead-young@1", 5*time.Minute, 900),
		r("lead-slow@1", 25*time.Hour, 400), r("lead-slow@1", 5*time.Minute, 700),
		r("lead-small@1", 25*time.Hour, 50), r("lead-small@1", 5*time.Minute, 200),
		r("lead-stale@1", 25*time.Hour, 300), r("lead-stale@1", 2*time.Hour, 900),
		r("issue-viewer@2", 25*time.Hour, 100), // the viewer before a restart
	}
	got := Grown(rs, now)
	if len(got) != 1 || got[0].Name != "issue-viewer" || got[0].FromMB != 400 || got[0].ToMB != 1020 {
		t.Fatalf("grown = %+v", got)
	}
	if !strings.Contains(got[0].Line(), "issue-viewer grew 400 → 1020 MB since 11-20 07:00: restart it: issue-cli viewer restart") {
		t.Errorf("line %q", got[0].Line())
	}
}

// TestRecordMemKeepsOnlyADay: the file holds MemKeep of readings
// however long the viewer runs.
func TestRecordMemKeepsOnlyADay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine", "rss.json")
	origRead, origPanes := readMems, leadPanes
	t.Cleanup(func() { readMems, leadPanes = origRead, origPanes })
	leadPanes = func() map[int]string { return nil }
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local)
	readMems = func(now time.Time) ([]procs.Mem, error) {
		return []procs.Mem{{PID: 1, Started: start, RSSKB: 1024, Command: "issue-viewer"}}, nil
	}
	for i := 0; i < 6*24*3; i++ { // three days at one reading per 20 minutes
		if err := RecordMem(path, start.Add(time.Duration(i)*20*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(LoadMem(path)); n > int(MemKeep/(20*time.Minute))+1 {
		t.Fatalf("%d readings kept, want at most %d", n, int(MemKeep/(20*time.Minute))+1)
	}
}
