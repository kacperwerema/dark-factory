package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var stateNow = time.Date(2026, 11, 15, 14, 0, 0, 0, time.Local)

func leadCmd(t *testing.T, proj *tracker.Project, now time.Time, args ...string) (string, error) {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	err := runLead(ctx, args)
	return stdout.String(), err
}

// The state file holds the live worker with its last progress, the queue
// head with the owner's words, the notes, asks and tomorrow items, and
// lives under the state dir by the lead's name.
func TestLeadStateRendersBoardAndNotes(t *testing.T) {
	proj := makeAutostartProject(t)
	agentrun.ListSessions = func() []agentrun.Session {
		return []agentrun.Session{{Name: "agent-base"}, {Name: tracker.LeadSessionName(proj)}}
	}
	if err := queue.Update(proj.IssueDir, func(q *queue.Queue) error {
		_, err := q.Add(queue.Item{Slug: "high", Quote: "do high next", AddedBy: "lead"}, queue.Where{}, stateNow)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"state", "note", "landed", "low"},
		{"state", "note", "--tomorrow", "check the nightly"},
		{"state", "ask", "--quote", "make the board faster"},
	} {
		if _, err := leadCmd(t, proj, stateNow, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	out, err := leadCmd(t, proj, stateNow, "state")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agent-base · in progress", `high (owner: "do high next")`, "landed low", "check the nightly", `"make the board faster"`, "issue-cli --project proj lead history <words>"} {
		if !strings.Contains(out, want) {
			t.Errorf("state misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "lead-proj ·") {
		t.Error("the lead itself is listed as a worker")
	}
	path := leadstate.Path(tracker.LeadSessionName(proj))
	if !strings.HasPrefix(path, os.Getenv("XDG_STATE_HOME")) || !strings.HasSuffix(path, "/ai-workflow-engine/lead/lead-proj.md") {
		t.Fatalf("path %s", path)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != out {
		t.Fatalf("file differs from the printed state (%v)", err)
	}
	if p, _ := leadCmd(t, proj, stateNow, "state", "--path"); strings.TrimSpace(p) != path {
		t.Fatalf("--path = %q", p)
	}
}

// Yesterday's notes fold into yesterday's summary on the next render.
func TestLeadStateFoldsYesterdaysNotes(t *testing.T) {
	proj := makeAutostartProject(t)
	if _, err := leadCmd(t, proj, stateNow.Add(-24*time.Hour), "state", "note", "from yesterday"); err != nil {
		t.Fatal(err)
	}
	out, err := leadCmd(t, proj, stateNow, "state")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "from yesterday") {
		t.Fatalf("yesterday's note still in the state:\n%s", out)
	}
	sum, err := os.ReadFile(filepath.Join(proj.WorkDir, "sessions", "2026-11-14-lead-proj.summary.md"))
	if err != nil || !strings.Contains(string(sum), "## Lead notes\n- 14:00 from yesterday") {
		t.Fatalf("summary = %q, %v", sum, err)
	}
}

func TestLeadStateRefusesSecretsAndWorkers(t *testing.T) {
	proj := makeAutostartProject(t)
	if _, err := leadCmd(t, proj, stateNow, "state", "note", "token ghp_"+"abcdefghijklmnopqrstuvwxyz0123456789AB"); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("a secret note: %v", err)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "low")
	for _, args := range [][]string{{"state"}, {"state", "note", "x"}, {"rotate"}} {
		if _, err := leadCmd(t, proj, stateNow, args...); err == nil || !strings.Contains(err.Error(), "workers") {
			t.Errorf("%v as a worker: %v", args, err)
		}
	}
}

// A watch registers itself while it runs and drops itself when it exits.
func TestLeadWatchRegistersInTheState(t *testing.T) {
	proj := makeAutostartProject(t)
	lead := tracker.LeadSessionName(proj)
	var during leadstate.Store
	now, _ := watchStub(t, []string{"a", "b"}, 10)
	origArgs := leadWatchArgs
	leadWatchArgs = func() []string { return []string{"--project", "proj", "lead", "watch", "--run", "probe it"} }
	t.Cleanup(func() { leadWatchArgs = origArgs })
	origRun := leadWatchRun
	leadWatchRun = func(dir, cmd string) string {
		during, _ = leadstate.Load(lead)
		return origRun(dir, cmd)
	}
	ctx, _, _ := newTestContext(proj, false)
	ctx.Now = now
	if err := runLead(ctx, []string{"watch", "--run", "probe it", "--every", "10s"}); err != nil {
		t.Fatal(err)
	}
	if len(during.Watches) != 1 || during.Watches[0].PID != os.Getpid() || during.Watches[0].Command != "issue-cli --project proj lead watch --run 'probe it'" {
		t.Fatalf("while running: %+v", during.Watches)
	}
	after, _ := leadstate.Load(lead)
	if len(after.Watches) != 0 {
		t.Fatalf("after exit: %+v", after.Watches)
	}
}

// rotate writes the note (the flush's sections) and the digest, records
// the handover with the watches, starts the finisher, and a second rotate
// the same day doesn't overwrite the first note.
func TestLeadRotateWritesNoteDigestAndStartsFinisher(t *testing.T) {
	proj := makeAutostartProject(t)
	lead := tracker.LeadSessionName(proj)
	agentrun.HasSession = func(target string) bool { return target == agentrun.Target(proj, lead) }
	var started []string
	orig := leadRotateStartFinisher
	leadRotateStartFinisher = func(_ *tracker.Project, l string) error { started = append(started, l); return nil }
	t.Cleanup(func() { leadRotateStartFinisher = orig })
	origAlive := pidAlive
	pidAlive = func(int) bool { return true }
	t.Cleanup(func() { pidAlive = origAlive })

	s, _ := leadstate.Load(lead)
	s.Register(leadstate.Watch{PID: 77, Command: "issue-cli lead tick --watch --exit-on-news", Started: stateNow})
	s.Add(leadstate.KindTomorrow, "re-run the bench", stateNow)
	if err := s.Save(lead); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(proj.WorkDir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	flushNote := filepath.Join(sessions, "2026-11-15-lead-proj.md")
	if err := os.WriteFile(flushNote, []byte("the flush's note"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := leadCmd(t, proj, stateNow, "rotate")
	if err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(sessions, "2026-11-15-lead-proj.1400.md")
	if !strings.Contains(out, "Wrote "+note) || !strings.Contains(out, "end your turn now") || len(started) != 1 || started[0] != lead {
		t.Fatalf("out:\n%s\nstarted %v", out, started)
	}
	if b, _ := os.ReadFile(flushNote); string(b) != "the flush's note" {
		t.Fatal("the flush's note was overwritten")
	}
	text, _ := os.ReadFile(note)
	if err := tracker.CheckHandoffNote(string(text)); err != nil || !strings.Contains(string(text), "1. re-run the bench") {
		t.Fatalf("note: %v\n%s", err, text)
	}
	if _, err := os.Stat(filepath.Join(sessions, "2026-11-15-lead-proj.1400.digest.md")); err != nil {
		t.Fatal(err)
	}
	if got := tracker.NewestHandoffNote(sessions, lead); got != note {
		// the rotate's note was written after the flush's
		t.Fatalf("newest note = %s", got)
	}
	after, _ := leadstate.Load(lead)
	if h := after.Handover; h == nil || h.Note != note || len(h.Watches) != 1 {
		t.Fatalf("handover = %+v", h)
	}
	sec := leadstate.HandoverSection(lead, "issue-cli --project proj lead history <words>", stateNow.Add(10*time.Minute))
	for _, want := range []string{"## Where the last lead left off", "# Lead state: lead-proj", "## Restart these watches", "issue-cli lead tick --watch --exit-on-news", "lead history <words>"} {
		if !strings.Contains(sec, want) {
			t.Errorf("handover section misses %q:\n%s", want, sec)
		}
	}
	if leadstate.HandoverSection(lead, "x", stateNow.Add(3*time.Hour)) != "" {
		t.Error("a stale handover still shows")
	}
	if again, _ := leadCmd(t, proj, stateNow, "rotate", "--no-start"); !strings.Contains(again, "1400-1.md") {
		t.Fatalf("second rotate in the same minute:\n%s", again)
	}
}

// The finisher waits while the pane is in a turn, closes the session and
// starts the lead again.
func TestLeadRotateFinishWaitsThenRestarts(t *testing.T) {
	proj := makeAutostartProject(t)
	lead := tracker.LeadSessionName(proj)
	live := true
	agentrun.HasSession = func(string) bool { return live }
	busy := 3
	origBusy, origSleep, origSpawn, origKill := leadRotateBusy, leadRotateSleep, leadRotateSpawn, closeLeadSession
	leadRotateBusy = func(*tracker.Project, string) bool { busy--; return busy >= 0 }
	slept := 0
	leadRotateSleep = func(time.Duration) { slept++ }
	var order []string
	closeLeadSession = func(_ *tracker.Project, name string) error {
		order = append(order, "close "+name)
		live = false
		return nil
	}
	leadRotateSpawn = func(_ *Context, args []string) error { order = append(order, "spawn"); return nil }
	t.Cleanup(func() {
		leadRotateBusy, leadRotateSleep, leadRotateSpawn, closeLeadSession = origBusy, origSleep, origSpawn, origKill
	})
	if _, err := leadCmd(t, proj, stateNow, "rotate", "--finish"); err != nil {
		t.Fatal(err)
	}
	if slept != 3 || strings.Join(order, ",") != "close "+lead+",spawn" {
		t.Fatalf("slept %d, order %v", slept, order)
	}
}

func TestLeadHistoryFindsDigestLines(t *testing.T) {
	proj := makeAutostartProject(t)
	sessions := filepath.Join(proj.WorkDir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := "# Digest\n\n- 2026-11-14 10:00 owner: ship the board faster\n- 2026-11-14 10:05 command: issue-cli dispatch board\n- 2026-11-14 11:00 owner: unrelated\n"
	if err := os.WriteFile(filepath.Join(sessions, "2026-11-14-lead-proj.digest.md"), []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	out, err := leadCmd(t, proj, stateNow, "history", "board")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "\n") != 2 || !strings.HasPrefix(out, "2026-11-14 10:05 command: issue-cli dispatch board") || strings.Contains(out, "unrelated") {
		t.Fatalf("history:\n%s", out)
	}
	if none, _ := leadCmd(t, proj, stateNow, "history", "nothing-like-it"); !strings.HasPrefix(none, "Nothing in lead-proj's history matches") {
		t.Fatalf("no hit: %q", none)
	}
}
