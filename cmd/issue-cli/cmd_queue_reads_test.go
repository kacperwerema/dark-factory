package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// standInTmuxBody logs each call's arguments to $TMUX_STANDIN_LOG, sleeps
// $TMUX_STANDIN_SLEEP (a loaded machine, where each spawn is what costs),
// lists $TMUX_STANDIN_SESSIONS and shows a quiet prompt in every pane.
const standInTmuxBody = `echo "$*" >> "$TMUX_STANDIN_LOG"
[ -n "$TMUX_STANDIN_SLEEP" ] && sleep "$TMUX_STANDIN_SLEEP"
case "$1" in
list-sessions) printf '%s\n' "$TMUX_STANDIN_SESSIONS" | tr ',' '\n' | sed 's/$/|:|1700000000|:|/' ;;
list-windows) ;;
capture-pane) echo '>' ;;
has-session) exit 1 ;;
esac
exit 0
`

// standInTmux puts the logging tmux first on PATH, gives the queue three live
// workers and a lead, and points the tmux reads the test init stubs back at
// it. It returns the log and the stand-in's path.
func standInTmux(t *testing.T, proj *tracker.Project, sleep string) (log, bin string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "tmux")
	standin.Write(t, bin, standInTmuxBody)
	log = filepath.Join(dir, "tmux.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_STANDIN_LOG", log)
	t.Setenv("TMUX_STANDIN_SLEEP", sleep)
	t.Setenv("TMUX_STANDIN_SESSIONS", "agent-base,agent-claimed,agent-high,lead-"+proj.Slug)
	has, list, capture := agentrun.HasSession, agentrun.ListSessions, agentrun.CapturePane
	agentrun.HasSession, agentrun.ListSessions, agentrun.CapturePane = realHasSession, realListSessions, realCapturePane
	t.Cleanup(func() { agentrun.HasSession, agentrun.ListSessions, agentrun.CapturePane = has, list, capture })
	return log, bin
}

// The exec-backed tmux reads, saved before any test stubs them.
var realHasSession, realListSessions, realCapturePane = agentrun.HasSession, agentrun.ListSessions, agentrun.CapturePane

func tmuxCalls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(data)), " ", "_"))
}

// One queue next reads tmux once per thing it asks: the session list once,
// each pane once, and has-session from that list. It asked 4 lists and 2
// captures per pane before, and every spawn is what a loaded machine pays.
func TestQueueNextReadsTmuxOncePerRun(t *testing.T) {
	proj := makeAutostartProject(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	log, _ := standInTmux(t, proj, "")
	mustQ(t, proj, "next")
	calls := tmuxCalls(t, log)
	seen := map[string]int{}
	for _, c := range calls {
		seen[c]++
		if strings.HasPrefix(c, "has-session") {
			t.Errorf("has-session spawned (%s): the kept session list answers it", c)
		}
	}
	for c, n := range seen {
		if n > 1 {
			t.Errorf("tmux %s ran %d times in one queue next, want once", c, n)
		}
	}
	if seen["list-sessions_-F_"+strings.ReplaceAll(agentrun.Format("session_name", "session_created", "session_path"), " ", "_")] != 1 {
		t.Errorf("no session list read; calls:\n%s", strings.Join(calls, "\n"))
	}
	// After the run, readings are afresh again: the next read spawns.
	before := len(tmuxCalls(t, log))
	agentrun.ListSessions()
	if len(tmuxCalls(t, log)) == before {
		t.Error("the session list stayed kept after queue next returned")
	}
}

// queueNextVsOneSpawn: a queue next may take this many tmux spawns plus a
// queue list. It reads tmux 6 times on this fixture (one list, one window
// list, four panes): 0.38 s against a 0.81 s limit here; it took 17 spawns
// and 1.05 s before the readings were kept.
const queueNextVsOneSpawn = 10

// queue next stays within queueNextVsOneSpawn of what a single tmux spawn
// and a queue list cost in the same run, on a quiet machine or a loaded one (TestStatsPageFastOnABigProject's pattern). The
// stand-in sleeps 50 ms a call: the spawns, not the Go, are what a 5-minute
// lead tick paid (p95 6 s on the live board).
func TestQueueNextFastRelativeToOneSpawn(t *testing.T) {
	proj := makeAutostartProject(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	_, bin := standInTmux(t, proj, "0.05")
	timed := func(f func()) time.Duration {
		start := time.Now()
		f()
		return time.Since(start)
	}
	var tries []string
	for try := 0; try < 3; try++ {
		spawn := timed(func() { _ = exec.Command(bin, "list-windows").Run() })
		list := timed(func() { mustQ(t, proj, "list") })
		limit := queueNextVsOneSpawn*spawn + list
		d := timed(func() { mustQ(t, proj, "next") })
		if d <= limit {
			t.Logf("queue next %v (one spawn %v, queue list %v)", d, spawn, list)
			return
		}
		tries = append(tries, d.String()+" (one spawn "+spawn.String()+", queue list "+list.String()+")")
	}
	t.Fatalf("queue next: %s; want under %d tmux spawns plus a queue list", strings.Join(tries, ", "), queueNextVsOneSpawn)
}
