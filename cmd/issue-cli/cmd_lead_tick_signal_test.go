package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

const signalTick = "issue-cli --project proj lead tick --watch --exit-on-news"

// stubTickSignals records the exit code a stop asks for instead of
// exiting; with fake, the tick's signals come from the returned channel
// instead of the process's own.
func stubTickSignals(t *testing.T, fake bool) (chan os.Signal, chan int) {
	t.Helper()
	sigs, exits := make(chan os.Signal, 1), make(chan int, 1)
	origSigs, origExit, origArgs := leadTickSignals, leadTickExit, leadWatchArgs
	if fake {
		leadTickSignals = func() (<-chan os.Signal, func()) { return sigs, func() {} }
	}
	leadTickExit = func(code int) { exits <- code }
	leadWatchArgs = func() []string { return strings.Fields(signalTick)[1:] }
	t.Cleanup(func() { leadTickSignals, leadTickExit, leadWatchArgs = origSigs, origExit, origArgs })
	return sigs, exits
}

// A --watch --exit-on-news tick holds a start for its next news. Stopped
// by a signal while it sleeps, it says the start and the stop with the
// command to start it again, asks for exit 128+15, and the lead's store
// records the stop (the next call says it at once). Before, the process
// died where it slept and said nothing.
func TestLeadTickWatchStoppedBySignalSaysWhatItHeld(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	stubOldTicks(t)
	lead := tracker.LeadSessionName(proj)
	for i := 0; i < 7; i++ {
		// Enough open items that the refill line stays quiet.
		name := fmt.Sprintf("item-%d", i)
		writeIssue(t, proj, name, "backlog")
		mustQ(t, proj, "add", name, "--quote", queueWords)
	}
	// A real SIGTERM to this process, which the tick catches.
	_, exits := stubTickSignals(t, false)
	code := -1
	orig := leadTickSleep
	leadTickSleep = func(time.Duration) bool {
		s, _ := leadstate.Load(lead)
		if s.Held == nil || s.Held.PID != os.Getpid() || len(s.Held.Lines) != 1 || !strings.Contains(s.Held.Lines[0], "Started queue item 1: item-0") {
			t.Errorf("the store doesn't keep what the tick holds: %+v", s.Held)
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case code = <-exits:
		case <-time.After(5 * time.Second):
			t.Error("no exit after the signal")
		}
		return false
	}
	t.Cleanup(func() { leadTickSleep = orig })
	clearGap()
	out := tick(t, proj, "--watch", "--exit-on-news", "--every", "1m")

	if code != 128+15 {
		t.Errorf("exit code %d, want 143", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("want the held start, the stop and its command, got:\n%s", out)
	}
	assertContains(t, lines[0], "start: ✓ Started queue item 1: item-0")
	assertContains(t, lines[1], fmt.Sprintf("lead tick: stopped by SIGTERM (pid %d); start it again with Bash run_in_background:", os.Getpid()))
	if lines[2] != "  "+signalTick {
		t.Errorf("command line %q", lines[2])
	}
	s, _ := leadstate.Load(lead)
	if len(s.Watches) != 0 || s.Held != nil || s.Tick == nil || s.Tick.Signal != "SIGTERM" || s.Tick.PID != os.Getpid() {
		t.Fatalf("store after the stop: watches %+v held %+v tick %+v", s.Watches, s.Held, s.Tick)
	}
	st := s.TickStatus(pidAlive, time.Now())
	if !st.Missing || st.Signal != "SIGTERM" {
		t.Fatalf("a stopped tick waits out the grace: %+v", st)
	}
}

// A tick killed without a word (SIGKILL) leaves what it held in the
// store; the lead's next tick says it first, and once.
func TestLeadTickWatchCarriesHeldOfAKilledTick(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	stubOldTicks(t)
	stubTickSignals(t, true)
	lead := tracker.LeadSessionName(proj)
	at := time.Date(2026, 11, 18, 7, 40, 0, 0, time.Local)
	s, _ := leadstate.Load(lead)
	s.Register(leadstate.Watch{PID: 4242, Command: signalTick, Started: at.Add(-time.Hour)})
	s.Held = &leadstate.HeldLines{PID: 4242, At: at, Lines: []string{"status: alpha moved to testing", "start: ✓ Started queue item 9: gone"}}
	if err := s.Save(lead); err != nil {
		t.Fatal(err)
	}
	orig := leadTickSleep
	leadTickSleep = func(time.Duration) bool { return false }
	t.Cleanup(func() { leadTickSleep = orig })

	out := tick(t, proj, "--watch", "--exit-on-news", "--every", "1m")
	for _, want := range []string{
		"lead tick: the tick before this one (pid 4242) ended without a word at or after 11-18 07:40; it held:\nstatus: alpha moved to testing\nstart: ✓ Started queue item 9: gone\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if s, _ := leadstate.Load(lead); s.Held != nil {
		t.Fatalf("the carried lines stay in the store: %+v", s.Held)
	}
	if out := tick(t, proj, "--watch", "--exit-on-news", "--every", "1m"); strings.Contains(out, "pid 4242") {
		t.Fatalf("said twice:\n%s", out)
	}
}

// The lead's next call names a stop as such, and a dead pid as killed or
// a session gone, not only the session.
func TestLeadTickMissingNamesAStop(t *testing.T) {
	proj := makeAutostartProject(t)
	lead := tracker.LeadSessionName(proj)
	now := time.Date(2026, 11, 20, 0, 20, 0, 0, time.Local)
	s, _ := leadstate.Load(lead)
	s.Register(leadstate.Watch{PID: 5151, Command: signalTick, Started: now.Add(-time.Hour)})
	s.Stopped(5151, "SIGTERM", now.Add(-time.Minute))
	if err := s.Save(lead); err != nil {
		t.Fatal(err)
	}
	ctx, _, stderr := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	printLeadTickMissing(ctx, "status", nil)
	if want := "no background tick is running (the last one (pid 5151) was stopped by SIGTERM at 00:19)"; !strings.Contains(stderr.String(), want) {
		t.Fatalf("missing %q:\n%s", want, stderr)
	}
}

// tickProcess starts this test binary as issue-cli (through main, as the
// shipped binary runs) on a read-only --observe tick of proj, from its
// root, with a private tmux folder and no home project of the session
// that runs the test.
func tickProcess(t *testing.T, proj *tracker.Project) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "lead", "tick", "--watch", "--exit-on-news", "--observe", "--every", "1m")
	cmd.Dir = proj.WorkDir
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TMUX_TMPDIR=") && !strings.HasPrefix(e, "ISSUE_VIEWER_PROJECT=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "ISSUE_CLI_TEST_AS_MAIN=1", "ISSUE_CLI_TEST_LEAD_SCOPE=project", "TMUX_TMPDIR="+t.TempDir())
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd, &out
}

// waitTick polls cond until it holds, failing after a minute (a loaded
// machine).
func waitTick(t *testing.T, what string, out *bytes.Buffer, cond func() bool) {
	t.Helper()
	for until := time.Now().Add(time.Minute); !cond(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(until) {
			t.Fatalf("timed out waiting for %s; its output:\n%s", what, out)
		}
	}
}

// tickedOnce: pid's watch is in some lead's store and its first observe
// tick has written its state; it returns that lead.
func tickedOnce(pid int) (string, bool) {
	files, _ := filepath.Glob(filepath.Join(leadstate.Dir(), "*.json"))
	ticked, _ := filepath.Glob(filepath.Join(viewerstate.Dir(), "lead-tick", "*observe*.json"))
	for _, f := range files {
		lead := strings.TrimSuffix(filepath.Base(f), ".json")
		s, err := leadstate.Load(lead)
		if err != nil {
			continue
		}
		for _, w := range s.Watches {
			if w.PID == pid && len(ticked) > 0 {
				return lead, true
			}
		}
	}
	return "", false
}

// The real process: SIGTERM makes the tick say its stop and exit 143; a
// tick killed with SIGKILL leaves what it held, and the next tick's stop
// says it.
func TestLeadTickProcessSaysItsStopAndCarriesAKilledTicksLines(t *testing.T) {
	if testing.Short() {
		t.Skip("starts issue-cli processes")
	}
	proj := makeAutostartProject(t)

	a, outA := tickProcess(t, proj)
	var lead string
	waitTick(t, "the first tick to register and tick", outA, func() bool { l, ok := tickedOnce(a.Process.Pid); lead = l; return ok })
	if err := a.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	a.Wait()
	if code := a.ProcessState.ExitCode(); code != 143 {
		t.Errorf("exit code %d, want 143; output:\n%s", code, outA)
	}
	if want := fmt.Sprintf("lead tick: stopped by SIGTERM (pid %d); start it again with Bash run_in_background:", a.Process.Pid); !strings.Contains(outA.String(), want) {
		t.Fatalf("missing %q in:\n%s", want, outA)
	}
	if s, _ := leadstate.Load(lead); s.Tick == nil || s.Tick.Signal != "SIGTERM" || len(s.Watches) != 0 {
		t.Fatalf("store after the stop: %+v %+v", s.Tick, s.Watches)
	}

	b, outB := tickProcess(t, proj)
	waitTick(t, "the second tick to register", outB, func() bool { _, ok := tickedOnce(b.Process.Pid); return ok })
	s, _ := leadstate.Load(lead)
	s.Held = &leadstate.HeldLines{PID: b.Process.Pid, At: time.Now(), Lines: []string{"status: base in progress → done"}}
	if err := s.Save(lead); err != nil {
		t.Fatal(err)
	}
	b.Process.Signal(syscall.SIGKILL)
	b.Wait()

	c, outC := tickProcess(t, proj)
	waitTick(t, "the third tick to register", outC, func() bool { _, ok := tickedOnce(c.Process.Pid); return ok })
	c.Process.Signal(syscall.SIGTERM)
	c.Wait()
	want := fmt.Sprintf("lead tick: the tick before this one (pid %d) ended without a word", b.Process.Pid)
	if out := outC.String(); !strings.Contains(out, want) || !strings.Contains(out, "\nstatus: base in progress → done\nlead tick: stopped by SIGTERM") {
		t.Fatalf("the killed tick's lines didn't come out:\n%s", out)
	}
}
