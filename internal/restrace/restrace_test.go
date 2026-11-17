package restrace_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/restrace"
)

func stateDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("XDG_STATE_HOME", d)
	return d
}

// TestTraceOffOpensNoFile: with DF_TRACE unset a start or stop writes
// nothing, makes no folder and allocates nothing.
func TestTraceOffOpensNoFile(t *testing.T) {
	d := stateDir(t)
	t.Setenv(restrace.EnvVar, "")
	restrace.Start(restrace.KindChrome, 42, "/tmp/p", "headless")
	restrace.Stop(restrace.KindChrome, 42, "/tmp/p")
	restrace.Sample(1, 100, 3)
	if _, err := os.Stat(filepath.Join(d, "ai-workflow-engine", "trace")); !os.IsNotExist(err) {
		t.Fatalf("trace folder made with tracing off: %v", err)
	}
	if n := testing.AllocsPerRun(100, func() { restrace.Start(restrace.KindChrome, 42, "/tmp/p", "x") }); n != 0 {
		t.Errorf("Start with tracing off allocates %v times per call, want 0", n)
	}
}

// TestTraceOnWritesOneLinePerEvent: kind, pid, owner, cwd and time on
// every line, in the state dir's trace/resources.jsonl.
func TestTraceOnWritesOneLinePerEvent(t *testing.T) {
	d := stateDir(t)
	t.Setenv(restrace.EnvVar, "resources")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "factory/leaky")
	restrace.Start(restrace.KindChrome, 42, "/tmp/dark-factory-playtest-1-x", "headless")
	restrace.Stop(restrace.KindChrome, 42, "/tmp/dark-factory-playtest-1-x")
	restrace.Sample(77, 512, 1)
	path := filepath.Join(d, "ai-workflow-engine", "trace", "resources.jsonl")
	if restrace.Path() != path {
		t.Fatalf("Path = %s, want %s", restrace.Path(), path)
	}
	ev := restrace.Read(path)
	if len(ev) != 3 {
		t.Fatalf("got %d events, want 3", len(ev))
	}
	cwd, _ := os.Getwd()
	for _, e := range ev[:2] {
		if e.Kind != restrace.KindChrome || e.PID != 42 || e.Owner != "factory/leaky" || e.Cwd != cwd || e.At.IsZero() || e.By != os.Getpid() {
			t.Errorf("event = %+v", e)
		}
	}
	if s := ev[2]; s.Event != restrace.EventSample || s.FootprintMB != 512 || s.Live == nil || *s.Live != 1 {
		t.Errorf("sample = %+v", s)
	}
}

func TestTraceRotatesAtTenMB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.jsonl")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", restrace.RotateAt)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restrace.Append(path, restrace.Event{At: time.Now(), Event: "start", Kind: "chrome", PID: 1}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != restrace.RotateAt {
		t.Fatalf("no rotated file of the old size: %v", err)
	}
	if ev := restrace.Read(path); len(ev) != 1 {
		t.Fatalf("after rotation got %d events, want the new one", len(ev))
	}
}

func TestOpenIsStartsWithNoStop(t *testing.T) {
	ev := []restrace.Event{
		{Event: "start", Kind: "chrome", PID: 10, ID: "/p1"},
		{Event: "start", Kind: "chrome", PID: 11, ID: "/p2"},
		{Event: "stop", Kind: "chrome", PID: 10, ID: "/p1"},
		{Event: "start", Kind: "child", PID: 12},
		{Event: "start", Kind: "child", PID: 13},
		{Event: "stop", Kind: "child", PID: 13},
		{Event: "sample", Kind: "fseventsd", PID: 1},
	}
	open := restrace.Open(ev)
	if len(open) != 2 || open[0].PID != 11 || open[1].PID != 12 {
		t.Fatalf("open = %+v", open)
	}
}

func TestWants(t *testing.T) {
	for v, want := range map[string]bool{"": false, "resources": true, "cli,resources": true, "1": true, "all": true, "cli": false} {
		if restrace.Wants(v) != want {
			t.Errorf("Wants(%q) = %v", v, !want)
		}
	}
}

// TestSetDebugReachesChildren: --debug turns tracing on here and in every
// child, through DF_TRACE.
func TestSetDebugReachesChildren(t *testing.T) {
	t.Setenv(restrace.EnvVar, "")
	restrace.SetDebug(true)
	defer restrace.SetDebug(false)
	if !restrace.On() || !restrace.Wants(os.Getenv(restrace.EnvVar)) {
		t.Fatalf("SetDebug(true): On %v, DF_TRACE %q", restrace.On(), os.Getenv(restrace.EnvVar))
	}
}

// TestSampleNowCountsLiveResources: each fseventsd sample carries the
// number of traced resources still alive, so growth can be matched to
// what was running.
func TestSampleNowCountsLiveResources(t *testing.T) {
	stateDir(t)
	t.Setenv(restrace.EnvVar, "resources")
	restrace.Start(restrace.KindChrome, os.Getpid(), "/tmp/p-live", "headless")
	restrace.Start(restrace.KindChild, 1<<30, "", "gone")
	restrace.Start(restrace.KindWorktree, 0, "/wt", "branch")
	restrace.Start(restrace.KindChrome, os.Getpid()+0, "/tmp/p-stopped", "headless")
	restrace.Stop(restrace.KindChrome, os.Getpid(), "/tmp/p-stopped")
	restrace.SampleNow(99, 2048)
	ev := restrace.Read(restrace.Path())
	last := ev[len(ev)-1]
	if last.Event != restrace.EventSample || last.Kind != restrace.KindFseventsd || last.PID != 99 || last.FootprintMB != 2048 {
		t.Fatalf("last = %+v, want the sample", last)
	}
	if last.Live == nil || *last.Live != 1 {
		t.Fatalf("live = %v, want 1 (the one live, unstopped Chrome)", last.Live)
	}
}
