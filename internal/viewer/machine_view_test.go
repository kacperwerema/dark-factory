package viewer

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/fseventsd"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// stubMachine makes machineVerdict answer v.
func stubMachine(t *testing.T, v fseventsd.Verdict) {
	t.Helper()
	prev := machineVerdict
	machineVerdict = func(time.Time) fseventsd.Verdict { return v }
	t.Cleanup(func() { machineVerdict = prev })
}

// runawayFseventsd is the 2026-11-15 reading: 13.2 GB of 36 GB at 99% CPU.
func runawayFseventsd(now time.Time) fseventsd.Verdict {
	r := fseventsd.Reading{At: now.Add(-3 * time.Minute), PID: 312, CPU: 99, FootprintMB: 13.2 * 1024, MemMB: 36 * 1024}
	return fseventsd.Check([]fseventsd.Reading{r}, now)
}

func TestMachineRowNamesTheCostAndTheOwnersCommand(t *testing.T) {
	now := time.Now()
	v := runawayFseventsd(now)
	if !v.Over {
		t.Fatalf("fixture not over: %+v", v)
	}
	stubMachine(t, v)
	p := &tracker.Project{Name: "P", Slug: "p"}
	rows := machineNeedsYou(p, "", "/p/p", now)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	want := "fseventsd (the macOS file-event daemon) uses 13.2 GB, 37% of memory, at 99% CPU: its footprint is over 2.0 GB. " +
		"It counts toward mem-guard's reading and holds worker starts. " +
		"Restart it in a terminal: sudo killall fseventsd (launchd starts it again at once; nothing here runs it for you)."
	if r.Kind != "machine" || r.Note != want || r.URL != "/p/p/status" {
		t.Errorf("row = %+v\nnote %q\nwant %q", r, r.Note, want)
	}
	if line := machineStatusLine(now); line != "Machine: fseventsd (the macOS file-event daemon) uses 13.2 GB, 37% of memory, at 99% CPU (its footprint is over 2.0 GB). Restart it: sudo killall fseventsd" {
		t.Errorf("status line = %q", line)
	}

	// Under the lines: no row, no line.
	stubMachine(t, fseventsd.Verdict{})
	if rows := machineNeedsYou(p, "", "/p/p", now); len(rows) != 0 || machineStatusLine(now) != "" {
		t.Errorf("quiet daemon: rows %+v, line %q", rows, machineStatusLine(now))
	}
}

// status.md (issue-cli status) names it under Agents, and Needs you lists it
// once across projects.
func TestMachineOnStatusAndOnceAcrossProjects(t *testing.T) {
	a := setupStatusProject(t, time.Now())
	b := setupStatusProject(t, time.Now())
	b.Name, b.Slug = "Other", "other"
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	stubMachine(t, runawayFseventsd(time.Now()))
	ts := newTestServer(t, []tracker.Project{a, b})
	defer ts.Close()

	_, md := getResp(t, ts.URL+"/p/status/status.md")
	if !strings.Contains(md, "\nMachine: fseventsd (the macOS file-event daemon) uses 13.2 GB, 37% of memory") || !strings.Contains(md, "Restart it: sudo killall fseventsd\n") {
		t.Errorf("status.md has no Machine: line:\n%s", md)
	}
	var one NeedsYouJSON
	_, js := getResp(t, ts.URL+"/p/status/needs-you.json")
	if err := json.Unmarshal([]byte(js), &one); err != nil {
		t.Fatal(err)
	}
	_, body := getResp(t, ts.URL+"/needs-you")
	if n := strings.Count(body, `data-kind="machine"`); n != 1 {
		t.Errorf("machine rows across projects = %d, want 1", n)
	}
	// 3 rows each (the Status fixture) and the machine's row once.
	if want := `data-testid="needs_you.count">7</span>`; one.Count != 4 || !strings.Contains(body, want) {
		t.Errorf("one project counts %d (want 4); across projects missing %q", one.Count, want)
	}
}
