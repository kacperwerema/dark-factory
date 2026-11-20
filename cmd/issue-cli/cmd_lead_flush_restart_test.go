package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// writeFlushFiles runs the note and summary steps with a recognisable note
// and summary.
func writeFlushFiles(t *testing.T, proj *tracker.Project) {
	t.Helper()
	for _, c := range [][]string{
		{"note", "--file", writeTemp(t, "FLUSH NOTE TODAY\n"+goodNote(10))},
		{"summary", "--file", writeTemp(t, "FLUSH SUMMARY TODAY")},
	} {
		if _, err := runFlush(t, proj, append(c, "--run", flushRunSlug)...); err != nil {
			t.Fatal(err)
		}
	}
}

// stubOldTicks makes the pids in live old ticks that still run, and records
// the ones close stops.
func stubOldTicks(t *testing.T, live ...int) *[]int {
	t.Helper()
	var stopped []int
	running := map[int]bool{}
	for _, pid := range live {
		running[pid] = true
	}
	origProc, origStop, origAlive := leadTickProcess, stopLeadTick, pidAlive
	leadTickProcess = func(pid int) bool { return running[pid] }
	pidAlive = func(pid int) bool { return running[pid] || pid == os.Getpid() }
	stopLeadTick = func(pid int) error { stopped = append(stopped, pid); delete(running, pid); return nil }
	t.Cleanup(func() { leadTickProcess, stopLeadTick, pidAlive = origProc, origStop, origAlive })
	return &stopped
}

// A flush close records a flush handover, closes the lead, stops its old
// tick and starts it again: the fresh lead's first prompt carries the note,
// the summary, the state file and the tick to start again, and its state
// lists no old tick.
func TestLeadFlushCloseStartsTheLeadAgainWithItsTick(t *testing.T) {
	proj, events := makeFlushProject(t, "> ")
	lead := tracker.LeadSessionName(proj)
	tick := "issue-cli --project proj lead tick --watch --exit-on-news"
	s, _ := leadstate.Load(lead)
	s.Register(leadstate.Watch{PID: 4242, Command: tick, Started: jobNow})
	if err := s.Save(lead); err != nil {
		t.Fatal(err)
	}
	stopped := stubOldTicks(t, 4242)
	writeFlushFiles(t, proj)

	out, err := runFlush(t, proj, "close", "--run", flushRunSlug)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Join(*events, ",") != lead+",spawn "+lead {
		t.Fatalf("events %v", *events)
	}
	if len(*stopped) != 1 || (*stopped)[0] != 4242 || !strings.Contains(out, "Stopped the old tick (pid 4242)") {
		t.Fatalf("stopped %v, out %q", *stopped, out)
	}
	after, _ := leadstate.Load(lead)
	if len(after.Watches) != 0 {
		t.Fatalf("old watches kept: %+v", after.Watches)
	}
	h := after.Handover
	if !h.FromFlush() || !strings.HasSuffix(h.Note, "2026-10-29-"+lead+".md") || !strings.HasSuffix(h.Digest, ".summary.md") {
		t.Fatalf("handover %+v", h)
	}

	// The spawn close makes reads it: the clock is the flush's.
	prompt := agentrun.LeadSpec(proj, proj.LoadWorkflow(), proj.WorkDir, time.Date(2026, 10, 30, 0, 6, 0, 0, time.Local)).Prompt
	for _, want := range []string{"FLUSH NOTE TODAY", "FLUSH SUMMARY TODAY", "## Your state file", "started by the nightly lead flush", "## Restart these watches", "- " + tick} {
		if !strings.Contains(prompt, want) {
			t.Errorf("fresh lead's prompt misses %q", want)
		}
	}
	if strings.Count(prompt, "## Where the last lead left off") != 1 {
		t.Errorf("the note's heading is not there once:\n%s", prompt)
	}
}

// A lead with no tick on record still gets one to start: the tick is what
// starts the queue and the jobs.
func TestLeadFlushCloseHandsOverATickEvenWithNoneRecorded(t *testing.T) {
	proj, _ := makeFlushProject(t, "> ")
	lead := tracker.LeadSessionName(proj)
	stopped := stubOldTicks(t)
	writeFlushFiles(t, proj)
	if out, err := runFlush(t, proj, "close", "--run", flushRunSlug); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	after, _ := leadstate.Load(lead)
	if after.Handover == nil || len(after.Handover.Watches) != 1 || after.Handover.Watches[0].Command != "issue-cli --project proj lead tick --watch --exit-on-news" {
		t.Fatalf("handover %+v", after.Handover)
	}
	if len(*stopped) != 0 {
		t.Fatalf("stopped %v", *stopped)
	}
}

// A start that fails after the close says so and names the owner's command.
func TestLeadFlushCloseSaysWhenTheLeadDidNotStart(t *testing.T) {
	proj, _ := makeFlushProject(t, "> ")
	stubOldTicks(t)
	writeFlushFiles(t, proj)
	orig := leadFlushSpawn
	leadFlushSpawn = func(*Context) error { return os.ErrPermission }
	t.Cleanup(func() { leadFlushSpawn = orig })
	_, err := runFlush(t, proj, "close", "--run", flushRunSlug)
	if err == nil || !strings.Contains(err.Error(), "is closed but did not start again") || !strings.Contains(err.Error(), "issue-cli --project proj lead spawn") {
		t.Fatalf("err %v", err)
	}
}

// A new tick ends an older tick of the same lead that still runs, so the
// lead never ticks twice; a watch that is not a tick stays.
func TestNewLeadTickStopsTheOlderOne(t *testing.T) {
	proj := makeAutostartProject(t)
	lead := tracker.LeadSessionName(proj)
	s, _ := leadstate.Load(lead)
	s.Register(leadstate.Watch{PID: 4242, Command: "issue-cli lead tick --watch --exit-on-news", Started: stateNow})
	s.Register(leadstate.Watch{PID: 4343, Command: "issue-cli lead watch --run 'make x'", Started: stateNow})
	if err := s.Save(lead); err != nil {
		t.Fatal(err)
	}
	stopped := stubOldTicks(t, 4242, 4343)
	origArgs := leadWatchArgs
	leadWatchArgs = func() []string { return []string{"--project", "proj", "lead", "tick", "--watch", "--exit-on-news"} }
	t.Cleanup(func() { leadWatchArgs = origArgs })

	ctx, _, stderr := newTestContext(proj, false)
	done := registerLeadWatch(ctx, Identity{Role: tracker.RoleProjectLead, Name: lead})
	defer done()
	if len(*stopped) != 1 || (*stopped)[0] != 4242 || !strings.Contains(stderr.String(), "stopped the older tick of "+lead+" (pid 4242)") {
		t.Fatalf("stopped %v, stderr %q", *stopped, stderr)
	}
	after, _ := leadstate.Load(lead)
	pids := map[int]bool{}
	for _, w := range after.Watches {
		pids[w.PID] = true
	}
	if pids[4242] || !pids[4343] || !pids[os.Getpid()] {
		t.Fatalf("watches %+v", after.Watches)
	}
}

// The whole thing on a private tmux server with a stand-in claude and
// terminal: none: a live lead and its tick (a process that looks like one),
// the flush's close, then the same lead name live again on a fresh claude
// whose first prompt carries the note, the summary and the tick, and the
// old tick gone.
func TestLeadFlushCloseRestartsTheLeadOnARealTmuxServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	realHas, realList, realPane := agentrun.HasSession, agentrun.ListSessions, agentrun.CapturePane
	proj, _ := makeFlushProject(t, "> ")
	agentrun.HasSession, agentrun.ListSessions, agentrun.CapturePane = realHas, realList, realPane
	origKill, origSpawn, origStart := closeLeadSession, leadFlushSpawn, startAgent
	closeLeadSession, startAgent = agentrun.KillTmux, agentrun.Start // the real close and launcher, on stand-ins
	leadFlushSpawn = func(ctx *Context) error { return spawnLead(ctx, "", "claude", false) }
	t.Cleanup(func() { closeLeadSession, leadFlushSpawn, startAgent = origKill, origSpawn, origStart })

	sockDir, err := os.MkdirTemp("/tmp", "flr-")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(sockDir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", sock, "kill-server")
		kill.Env = envWithout("TMUX")
		_ = kill.Run()
		_ = os.RemoveAll(sockDir)
	})
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", sockDir)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", t.TempDir())
	bin, rec := t.TempDir(), t.TempDir()
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("REC", rec)
	origTimeout, origSettle := agentrun.PromptDeliveryTimeout, agentrun.LaunchSettle
	agentrun.PromptDeliveryTimeout, agentrun.LaunchSettle = 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { agentrun.PromptDeliveryTimeout, agentrun.LaunchSettle = origTimeout, origSettle })
	standin.Write(t, filepath.Join(bin, "claude"), `printf '%s\n' "$@" > "$REC/claude.args.tmp"
mv "$REC/claude.args.tmp" "$REC/claude.args"
exec sleep 600
`)
	// The old tick: a shell whose command line is a lead tick --watch.
	standin.Write(t, filepath.Join(bin, "issue-cli"), "sleep 600\n")

	root, err := filepath.EvalSymlinks(proj.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	proj.WorkDir, proj.IssueDir, proj.WorkflowFile = root, filepath.Join(root, "issues"), filepath.Join(root, "workflow.yaml")
	proj.Terminal = "none"
	lead := tracker.LeadSessionName(proj)
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", lead, "sleep 600").CombinedOutput(); err != nil {
		t.Fatalf("old lead: %v %s", err, out)
	}
	oldTick := exec.Command(filepath.Join(bin, "issue-cli"), "--project", proj.Slug, "lead", "tick", "--watch", "--exit-on-news")
	if err := oldTick.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- oldTick.Wait() }()
	t.Cleanup(func() { _ = oldTick.Process.Kill() })
	s, _ := leadstate.Load(lead)
	s.Register(leadstate.Watch{PID: oldTick.Process.Pid, Command: "issue-cli --project proj lead tick --watch --exit-on-news", Started: jobNow})
	if err := s.Save(lead); err != nil {
		t.Fatal(err)
	}
	writeFlushFiles(t, proj)

	out, err := runFlush(t, proj, "close", "--run", flushRunSlug)
	if err != nil {
		t.Fatalf("close: %v\n%s", err, out)
	}
	waitForFile(t, filepath.Join(rec, "claude.args"))
	args, _ := os.ReadFile(filepath.Join(rec, "claude.args"))
	for _, want := range []string{"FLUSH NOTE TODAY", "FLUSH SUMMARY TODAY", "## Your state file", "- issue-cli --project proj lead tick --watch --exit-on-news"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("fresh lead's prompt misses %q", want)
		}
	}
	if !agentrun.HasSession(agentrun.Target(proj, lead)) {
		t.Fatalf("%s is not live after the flush:\n%s", lead, out)
	}
	if got := agentrun.SessionAgent(proj, lead); got != "claude" {
		t.Fatalf("%s runs %q", lead, got)
	}
	select {
	case <-exited:
	case <-time.After(30 * time.Second):
		t.Fatalf("the old tick still runs:\n%s", out)
	}
	assertContains(t, out, "✓ Closed "+lead)
	assertContains(t, out, "Spawned CEO "+lead)
}
