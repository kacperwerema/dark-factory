package agentrun

import (
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/recent"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestKeptHasSessionAnswersFromTheList(t *testing.T) {
	end := KeepReadings()
	defer end()
	if !readingsKept() {
		t.Fatal("KeepReadings didn't keep")
	}
	lastSessions.Get(func() []Session {
		return []Session{{Name: "agent-a"}, {Name: "lead-p"}, {Name: "shared:agent-b"}}
	})
	for _, c := range []struct {
		target   string
		live, ok bool
	}{
		{"=agent-a:", true, true},
		{"=lead-p:", true, true},
		{"=agent-gone:", false, true},
		{"=shared:=agent-b", true, true},
		{"=shared:=agent-c", false, true},
		{"=shared:=notes", false, false}, // not a window ListSharedWindows lists
		{"=view-lead-p:", false, false},  // lead views are left out of the list
		{"agent-a", false, false},        // a prefix match: ask tmux
		{"=agent-a:0.1", false, false},   // a pane: ask tmux
	} {
		live, ok := keptHasSession(c.target)
		if live != c.live || ok != c.ok {
			t.Errorf("keptHasSession(%q) = %v, %v; want %v, %v", c.target, live, ok, c.live, c.ok)
		}
	}
	end()
	end() // twice is fine
	if readingsKept() || recent.Window() != 0 {
		t.Fatalf("after end: kept %v, window %v", readingsKept(), recent.Window())
	}
}

// In the viewer readings are kept (recent.Keep) without KeepReadings: the
// queue and candidate verdicts answer from the kept list (liveNow), while
// HasSession, which Start, dispatch and every action path ask, still asks
// tmux, so a session that just started or ended is seen at once. The kept
// list here names agent-a; this test's tmux server has no sessions at all.
func TestStartReadsTmuxAfresh(t *testing.T) {
	recent.Keep(time.Hour)
	t.Cleanup(func() { recent.Keep(0); recent.Forget() })
	recent.Forget()
	lastSessions.Get(func() []Session { return []Session{{Name: "agent-a"}} })
	proj := &tracker.Project{}
	if !liveNow(proj, "agent-a") {
		t.Error("liveNow(agent-a) asked tmux: want the kept list's answer")
	}
	if liveNow(proj, "agent-gone") {
		t.Error("liveNow(agent-gone) = live; the kept list doesn't name it")
	}
	if HasSession(Target(proj, "agent-a")) {
		t.Error("HasSession(agent-a) answered from the kept list: Start must read tmux afresh")
	}
	if IsLive(proj, "agent-a") {
		t.Error("IsLive(agent-a) answered from the kept list: dispatch must read tmux afresh")
	}
}

// A test's own HasSession wins over the kept list, as it does inside IsLive.
func TestLiveNowKeepsAStubbedHasSession(t *testing.T) {
	recent.Keep(time.Hour)
	t.Cleanup(func() { recent.Keep(0); recent.Forget() })
	recent.Forget()
	lastSessions.Get(func() []Session { return nil })
	orig := HasSession
	HasSession = func(string) bool { return true }
	t.Cleanup(func() { HasSession = orig })
	if !liveNow(&tracker.Project{}, "agent-a") {
		t.Error("liveNow ignored the stubbed HasSession")
	}
}

// With readings off (a test binary, issue-cli outside a keep, a scenario
// viewer) liveNow is IsLive.
func TestLiveNowOffAsksTmux(t *testing.T) {
	recent.Keep(0)
	orig := HasSession
	var asked []string
	HasSession = func(target string) bool { asked = append(asked, target); return false }
	t.Cleanup(func() { HasSession = orig })
	liveNow(&tracker.Project{}, "agent-a")
	if len(asked) != 1 || asked[0] != "=agent-a:" {
		t.Errorf("asked %v, want one has-session for =agent-a:", asked)
	}
}
