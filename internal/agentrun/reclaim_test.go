package agentrun

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// On a private tmux server, Reclaimable names the idle sessions of a done
// issue and of one waiting on the owner, and leaves a done issue's busy
// pane, an in-progress issue's idle one, a lead and a kept name.
func TestReclaimableOnAPrivateTmuxServer(t *testing.T) {
	isolateTmux(t)
	done, busy := sweepIssue("cli/done-one", "done"), sweepIssue("cli/busy-one", "done")
	open, gate := sweepIssue("cli/open-one", "in progress"), sweepIssue("cli/gate-one", "human-testing")
	kept := sweepIssue("cli/kept-one", "done")
	name := tracker.AgentSessionName
	idle := "printf '> '; sleep 600"
	for _, s := range []struct{ session, script string }{
		{name(done.Slug), idle},
		{name(busy.Slug), "echo '✻ Working… (esc to interrupt)'; sleep 600"},
		{name(open.Slug), idle},
		{name(gate.Slug), idle},
		{name(kept.Slug), idle},
		{"lead-proj", idle},
	} {
		tmuxRun(t, "new-session", "-d", "-s", s.session, "-x", "120", "-y", "30", "sh", "-c", s.script)
	}
	// The busy pane shows its working line before the read.
	for deadline := time.Now().Add(10 * time.Second); !AgentWorking(&tracker.Project{Slug: "proj"}, name(busy.Slug)); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("busy pane never showed its working line")
		}
	}
	proj := &tracker.Project{Slug: "proj"}
	in := ReclaimInput{
		Issues: []*tracker.Issue{done, busy, open, gate, kept},
		Closed: func(i *tracker.Issue) bool { return i.Status == "done" },
		Waiting: func(i *tracker.Issue) string {
			if i.Slug == gate.Slug {
				return "waits on the owner's approval for documentation"
			}
			return ""
		},
		KeepNames: map[string]bool{name(kept.Slug): true},
		Snap:      TakeSnapshot,
		Now:       time.Now().Add(time.Hour),
		Idle:      15 * time.Minute,
	}
	var got []string
	for _, r := range Reclaimable(proj, "", in) {
		got = append(got, r.Name+" — "+r.Why)
		if r.IdleSince.IsZero() {
			t.Errorf("%s: no idle since", r.Name)
		}
	}
	slices.Sort(got)
	want := []string{
		name(done.Slug) + " — done",
		name(gate.Slug) + " — waits on the owner's approval for documentation",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("reclaimable =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Within Idle of its launch nothing counts yet.
	in.Now = time.Now()
	if r := Reclaimable(proj, "", in); len(r) != 0 {
		t.Fatalf("fresh sessions counted: %+v", r)
	}
}

// IdleSince is the end of the agent's newest issue-cli call, not its launch.
func TestReclaimIdleSinceIsTheLastCall(t *testing.T) {
	done := sweepIssue("cli/done-one", "done")
	agent := tracker.AgentSessionName(done.Slug)
	snap := sweepStubs(t, []string{agent}, nil, nil, nil, nil)
	ListSessions = func() []Session { return []Session{{Name: agent, StartTime: "2026-11-15 08:00:00"}} }
	root := t.TempDir()
	log := filepath.Join(root, ".agent-logs", agent, agent+".clilog")
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, []byte(`{"ts":"2026-11-15T09:30:00Z","project":"proj","dur_ms":60000}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := &tracker.Project{Slug: "proj"}
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	got := Reclaimable(proj, root, ReclaimInput{Issues: []*tracker.Issue{done}, Closed: func(*tracker.Issue) bool { return true }, Snap: func() *Snapshot { return snap }, Now: now, Idle: 15 * time.Minute})
	if len(got) != 1 || !got[0].IdleSince.Equal(time.Date(2026, 11, 15, 9, 31, 0, 0, time.UTC)) {
		t.Fatalf("reclaim = %+v", got)
	}
	if line := got[0].Line(); !strings.HasPrefix(line, agent+" (done, idle since ") {
		t.Errorf("line = %q", line)
	}
}
