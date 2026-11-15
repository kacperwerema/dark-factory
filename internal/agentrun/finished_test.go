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

// sweepStubs stands in for tmux and ps: sessions are the live sessions,
// pids the pane pid of each, bare the panes that are only a shell, working
// the panes in a turn, stopped the pids in state T, attached the sessions a
// person has open.
func sweepStubs(t *testing.T, sessions []string, bare, working, attached map[string]bool, stopped map[int]bool) *Snapshot {
	t.Helper()
	origList, origCapture, origAttached, origArgs := ListSessions, CapturePane, SessionAttached, PaneArgs
	t.Cleanup(func() {
		ListSessions, CapturePane, SessionAttached, PaneArgs = origList, origCapture, origAttached, origArgs
	})
	var listed []Session
	snap := &Snapshot{children: map[int][]int{1: {2}}, stopped: stopped}
	pidOf := map[int]string{}
	for i, name := range sessions {
		listed = append(listed, Session{Name: name})
		pid := 100 + i
		pidOf[pid] = name
		snap.panes = append(snap.panes, Pane{Session: name, Window: "0", PID: pid, Command: "zsh"})
		if !bare[name] {
			snap.children[pid] = []int{1000 + i}
		}
	}
	ListSessions = func() []Session { return listed }
	CapturePane = func(target string) (string, error) {
		for name := range working {
			if target == "="+name+":" {
				return "✻ Working… (esc to interrupt)\n", nil
			}
		}
		return "> \n", nil
	}
	SessionAttached = func(name string) bool { return attached[name] }
	PaneArgs = func(int) (string, error) { return "-zsh", nil }
	return snap
}

func sweepIssue(slug, status string) *tracker.Issue {
	return &tracker.Issue{Slug: slug, Status: status}
}

// The sweep closes worker sessions whose issue is closed and dead panes; it
// leaves leads, post-mortem agents, open workers, paused, working and
// attached ones, and names another project's open issue holds.
func TestFinishedSessions(t *testing.T) {
	done, open, parked := sweepIssue("cli/done-one", "done"), sweepIssue("cli/open-one", "in progress"), sweepIssue("cli/parked-one", "parked")
	deadOpen := sweepIssue("cli/dead-one", "in progress")
	paused, busy, watched, kept := sweepIssue("cli/paused", "done"), sweepIssue("cli/busy", "done"), sweepIssue("cli/watched", "done"), sweepIssue("cli/kept", "done")
	issues := []*tracker.Issue{done, open, parked, deadOpen, paused, busy, watched, kept}
	name := tracker.AgentSessionName
	sessions := []string{
		name(done.Slug), tracker.LimitedSessionName(done.Slug), name(open.Slug), name(parked.Slug), name(deadOpen.Slug),
		name(paused.Slug), name(busy.Slug), name(watched.Slug), name(kept.Slug),
		"agent-orphan", "agent-orphan-dead", "lead-proj", "postmortem-cli-done-one",
	}
	pausedIdx := slices.Index(sessions, name(paused.Slug))
	snap := sweepStubs(t, sessions,
		map[string]bool{name(deadOpen.Slug): true, "agent-orphan-dead": true, "lead-proj": true, "postmortem-cli-done-one": true},
		map[string]bool{name(busy.Slug): true},
		map[string]bool{name(watched.Slug): true},
		map[int]bool{100 + pausedIdx: true})
	proj := &tracker.Project{Slug: "proj"}
	closed := func(i *tracker.Issue) bool { return tracker.IsClosedStatus(nil, i.Status) }

	got := FinishedSessions(proj, "", SweepInput{Issues: issues, Closed: closed, Snap: snap, KeepNames: map[string]bool{name(kept.Slug): true}})
	var names []string
	for _, f := range got {
		names = append(names, f.Name+" — "+f.Why)
	}
	want := []string{
		name(done.Slug) + " — cli/done-one is done",
		tracker.LimitedSessionName(done.Slug) + " — cli/done-one is done",
		name(parked.Slug) + " — cli/parked-one is parked",
		name(deadOpen.Slug) + " — only a shell: its agent exited",
		"agent-orphan-dead — only a shell: its agent exited, and no issue names it",
	}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		t.Errorf("finished:\n%s\nwant:\n%s", strings.Join(names, "\n"), strings.Join(want, "\n"))
	}
}

// In shared mode the sweep reads the project's session's windows; an
// attached shared session (the owner's own) holds nothing back.
func TestFinishedSessionsSharedMode(t *testing.T) {
	done := sweepIssue("cli/done-one", "done")
	agent := tracker.AgentSessionName(done.Slug)
	snap := sweepStubs(t, nil, nil, nil, map[string]bool{"work": true}, nil)
	ListSessions = func() []Session { return []Session{{Name: "work:" + agent}, {Name: "other:" + agent}} }
	snap.panes = []Pane{{Session: "work", Window: agent, PID: 100, Command: "node"}}
	proj := &tracker.Project{Slug: "proj", TmuxSession: "work"}
	got := FinishedSessions(proj, "", SweepInput{Issues: []*tracker.Issue{done}, Closed: func(*tracker.Issue) bool { return true }, Snap: snap})
	if len(got) != 1 || got[0].Name != agent {
		t.Fatalf("finished = %+v", got)
	}
}

// CloseWhenIdle waits while the pane shows the working line and closes the
// session once it is gone, on a private tmux server.
func TestCloseWhenIdleWaitsForTheTurn(t *testing.T) {
	isolateTmux(t)
	orig := idleClose
	idleClose.First, idleClose.Poll, idleClose.Polls = "0.1", "0.1", 600
	t.Cleanup(func() { idleClose = orig })
	proj := &tracker.Project{Slug: "p"}
	flag := filepath.Join(t.TempDir(), "turn-ended")
	script := "echo 'Working (esc to interrupt)'; while [ ! -e " + flag + " ]; do sleep 0.1; done; printf '\\033[2J\\033[H> '; sleep 600"
	tmuxRun(t, "new-session", "-d", "-s", "agent-x", "sh", "-c", script)

	if err := CloseWhenIdle(proj, "agent-x"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if testTmux(t, "has-session", "-t", "=agent-x:").Run() != nil {
		t.Fatal("closed while the agent was in a turn")
	}
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if testTmux(t, "has-session", "-t", "=agent-x:").Run() != nil {
			return
		}
	}
	t.Fatal("agent-x still open after its turn ended")
}
