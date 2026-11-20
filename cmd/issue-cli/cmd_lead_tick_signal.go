package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A lead's background tick can be ended by a signal from outside (the
// background shell's owner, a stray kill): the process dies where it
// sleeps. Under --exit-on-news it holds its starts and plain status moves
// in memory for the next news, and their keys are already in the tick
// state, so nobody says them again. So the watch says a stop on its own
// stdout, with what it held and the command to start it again, before it
// exits, and it keeps its held lines in the lead's store on each tick: a
// tick killed without a word (SIGKILL) hands them to the lead's next tick.

// leadTickSignals are the signals a --watch tick says it was stopped by;
// a var so tests deliver one.
var leadTickSignals = func() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	return ch, func() { signal.Stop(ch) }
}

// leadTickExit ends the process after a stop; a var for tests.
var leadTickExit = os.Exit

// leadTickGoneWait is how long a new tick waits for an older tick it
// stopped to go, so the older one's last write to the store comes before
// the new one's.
var leadTickGoneWait = 3 * time.Second

// leadShell is this process's entry in its lead's store: a background
// shell the lead waits on. lead is "" when nothing is recorded (not a
// lead's own shell, or no state dir).
type leadShell struct {
	ctx  *Context
	lead string
	pid  int
	cmd  string

	mu     sync.Mutex
	closed bool
}

// openLeadWatch records this process as a background shell its lead waits
// on, so the state file lists it and a rotate hands its command over. A
// lead runs one tick: a new tick first stops any older tick of the same
// lead that still runs (one a closed session left behind, or one started
// twice) and waits for it to go.
func openLeadWatch(ctx *Context, me Identity) *leadShell {
	w := &leadShell{ctx: ctx, pid: os.Getpid()}
	if me.Role != tracker.RoleProjectLead && me.Role != tracker.RoleDepartmentLead || me.Name == "" || leadstate.Dir() == "" {
		return w
	}
	w.lead, w.cmd = me.Name, "issue-cli "+shellJoin(leadWatchArgs())
	var older []int
	if leadstate.IsTick(w.cmd) {
		if s, err := leadstate.Load(w.lead); err == nil {
			for _, o := range s.Watches {
				if o.PID == w.pid || !leadstate.IsTick(o.Command) || !leadTickProcess(o.PID) {
					continue
				}
				if err := stopLeadTick(o.PID); err == nil {
					fmt.Fprintf(ctx.stderr(), "lead tick: stopped the older tick of %s (pid %d)\n", w.lead, o.PID)
					older = append(older, o.PID)
				}
			}
		}
		for _, pid := range older {
			for until := time.Now().Add(leadTickGoneWait); leadTickProcess(pid) && time.Now().Before(until); {
				time.Sleep(50 * time.Millisecond)
			}
		}
	}
	w.update(func(s *leadstate.Store) {
		for _, pid := range older {
			s.Unregister(pid)
		}
		s.Register(leadstate.Watch{PID: w.pid, Command: w.cmd, Started: ctx.now()})
	})
	return w
}

func (w *leadShell) update(fn func(*leadstate.Store)) {
	if w.lead == "" {
		return
	}
	s, err := leadstate.Load(w.lead)
	if err != nil {
		return
	}
	fn(&s)
	_ = s.Save(w.lead)
}

// done drops the watch as one that exited on its own; what it held went
// out with it.
func (w *leadShell) done() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	w.update(func(s *leadstate.Store) {
		s.Ended(w.pid, w.ctx.now())
		if s.Held != nil && s.Held.PID == w.pid {
			s.Held = nil
		}
	})
}

// stopped drops the watch as one sig stopped; what it held went out with
// it. It reports false when the watch was closed already.
func (w *leadShell) stopped(sig string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	w.closed = true
	w.update(func(s *leadstate.Store) {
		s.Stopped(w.pid, sig, w.ctx.now())
		if s.Held != nil && s.Held.PID == w.pid {
			s.Held = nil
		}
	})
	return true
}

// hold keeps what the tick holds for its next news in the store.
func (w *leadShell) hold(held string) {
	lines := splitLines(held)
	w.update(func(s *leadstate.Store) {
		switch {
		case len(lines) == 0 && (s.Held == nil || s.Held.PID != w.pid):
		case len(lines) == 0:
			s.Held = nil
		default:
			s.Held = &leadstate.HeldLines{PID: w.pid, At: w.ctx.now(), Lines: lines}
		}
	})
}

// takeHeld returns what an earlier tick of the lead held and never said
// (it died without a word), with a line that says so, and drops it from
// the store; "" when there is none.
func (w *leadShell) takeHeld() string {
	var out string
	w.update(func(s *leadstate.Store) {
		h := s.Held
		if h == nil || h.PID == w.pid || len(h.Lines) == 0 {
			return
		}
		if h.PID > 0 && h.PID != w.pid && pidAlive(h.PID) && leadTickProcess(h.PID) {
			return // still its own: it says them itself
		}
		s.Held = nil
		out = fmt.Sprintf("lead tick: the tick before this one (pid %d) ended without a word at or after %s; it held:\n%s\n",
			h.PID, h.At.Local().Format("01-02 15:04"), strings.Join(h.Lines, "\n"))
	})
	return out
}

// splitLines is s's non-empty lines.
func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// signalName is a stop signal's name as the line says it.
func signalName(sig os.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGHUP:
		return "SIGHUP"
	}
	return strings.ToUpper(sig.String())
}

// signalExitCode is the exit code a shell gives a process sig ended.
func signalExitCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 1
}

// leadTickStopLine is the line a stopped tick ends on.
func leadTickStopLine(sig string, pid int, cmd string) string {
	if cmd == "" {
		cmd = "issue-cli " + shellJoin(leadWatchArgs())
	}
	return fmt.Sprintf("lead tick: stopped by %s (pid %d); start it again with Bash run_in_background:\n  %s\n", sig, pid, cmd)
}
