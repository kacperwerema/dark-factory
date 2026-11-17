package agentrun

import (
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/recent"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// commandReadingKeep is how long a reading stands inside KeepReadings: past
// one short command's run, so it reads each thing once.
const commandReadingKeep = time.Minute

var keeping struct {
	sync.Mutex
	on    bool
	prev  time.Duration
	panes recent.Readings[string, paneRead]
}

type paneRead struct {
	text string
	err  error
}

// KeepReadings keeps the machine's readings for the rest of one short
// command: tmux's sessions, each pane's screen and has-session answer,
// mem-guard, the load and the main checkout are read once, not once per part
// that asks (queue next asked tmux 25 times a run). A decision taken under
// the start lock calls recent.Forget first and reads afresh; Start ends the
// keeping, since a dispatch waits for its own pane to change. end ends it too
// and is safe to call twice.
func KeepReadings() (end func()) {
	keeping.Lock()
	defer keeping.Unlock()
	if keeping.on {
		return func() {}
	}
	keeping.on, keeping.prev = true, recent.Window()
	recent.Keep(commandReadingKeep)
	return EndReadings
}

// EndReadings ends KeepReadings: from here on every reading is afresh again.
func EndReadings() {
	keeping.Lock()
	defer keeping.Unlock()
	if !keeping.on {
		return
	}
	keeping.on = false
	recent.Keep(keeping.prev)
	recent.Forget()
}

func readingsKept() bool {
	keeping.Lock()
	defer keeping.Unlock()
	return keeping.on
}

// keptHasSession answers has-session from the kept session list when the
// target names a session ("=name:") or a shared session's window
// ("=shared:=window"); ok is false for any other target.
func keptHasSession(target string) (live, ok bool) {
	name, ok := strings.CutPrefix(target, "=")
	if !ok {
		return false, false
	}
	if session, window, shared := strings.Cut(name, ":="); shared {
		if hasManagedPrefix(session) || !hasManagedPrefix(window) { // ListSharedWindows lists only these
			return false, false
		}
		name = session + ":" + window
	} else if name, ok = strings.CutSuffix(name, ":"); !ok || strings.Contains(name, ":") {
		return false, false
	}
	if name == "" || IsLeadView(name) {
		return false, false
	}
	for _, s := range lastSessions.Get(readSessions) {
		if s.Name == name {
			return true, true
		}
	}
	return false, true
}

// liveNow is IsLive for a verdict that only reads (why a queue item or a
// backlog candidate is held): while machine readings are kept, in the
// viewer as well as under KeepReadings, it answers from the one kept session
// list instead of a tmux has-session per item (the Status page asked once
// per started item). A target the list can't answer, and every reading
// outside a keep, is IsLive. Start, dispatch and the other action paths
// keep calling IsLive and HasSession, which read tmux afresh in the viewer.
func liveNow(proj *tracker.Project, name string) bool {
	if recent.Window() > 0 && !hasSessionStubbed() {
		if live, ok := keptHasSession(Target(proj, name)); ok {
			return live
		}
	}
	return IsLive(proj, name)
}

// hasSessionStubbed reports a test's own HasSession: its answer wins over
// the kept list, as it does inside IsLive.
func hasSessionStubbed() bool {
	return reflect.ValueOf(HasSession).Pointer() != reflect.ValueOf(defaultHasSession).Pointer()
}
