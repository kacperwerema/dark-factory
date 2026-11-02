package ci

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func nightlyCfg(at string) *tracker.CIConfig {
	c := cfg(cmd("true"), tracker.CICommand{Cmd: "true", Tier: "slow"})
	c.Nightly = &tracker.CINightly{Tier: "slow", At: at}
	return c
}

func TestCheckNightly(t *testing.T) {
	root := newRepo(t)
	c := nightlyCfg("03:00")
	now := time.Date(2026, 11, 2, 2, 0, 0, 0, time.Local)
	var spawned []string
	allow, why := true, ""
	w := &Watcher{
		SpawnTier: func(_, tier string) error { spawned = append(spawned, tier); return nil },
		Memory:    func() (bool, string) { return allow, why },
		Now:       func() time.Time { return now },
	}

	// 02:00: today's 03:00 has not come, but yesterday's slot was never
	// run: it starts now.
	if n, err := w.CheckNightly(root, c); err != nil || !n.Spawned || n.Tier != "slow" || n.Head == "" {
		t.Fatalf("missed slot must start: %+v %v", n, err)
	}
	if n, _ := w.CheckNightly(root, c); n.Spawned {
		t.Error("one start per slot, even when the run never writes its state")
	}

	// The run lands, finishing after yesterday's slot: nothing until 03:00.
	if _, _, err := Execute(Options{Root: root, Config: c, Tier: "slow", Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Minute)
	if n, _ := w.CheckNightly(root, c); n.Spawned {
		t.Error("tier ran since the slot: must not start again")
	}

	// 03:10, mem-guard says no: held with its reason, then started once it
	// allows.
	now = time.Date(2026, 11, 2, 3, 10, 0, 0, time.Local)
	allow, why = false, "mem-guard says no: 80% used"
	if n, _ := w.CheckNightly(root, c); n.Spawned || n.Held != why {
		t.Errorf("mem-guard hold: %+v", n)
	}
	allow, why = true, ""
	if n, _ := w.CheckNightly(root, c); !n.Spawned {
		t.Errorf("due after 03:00 with memory free must start: %+v", n)
	}
	if len(spawned) != 2 {
		t.Errorf("spawned %v, want two nightly starts", spawned)
	}

	// No nightly: block, no SpawnTier, or a live run: never starts.
	w.nightly = nil
	if n, _ := w.CheckNightly(root, cfg(cmd("true"), tracker.CICommand{Cmd: "true", Tier: "slow"})); n.Spawned {
		t.Error("no ci.nightly must never start")
	}
	orig := pidAlive
	defer func() { pidAlive = orig }()
	writeFile(t, LockPath(root), `{"pid": 4242}`)
	pidAlive = func(int) bool { return true }
	if n, _ := w.CheckNightly(root, c); n.Spawned {
		t.Error("must not start while a run is live")
	}
	pidAlive = func(int) bool { return false }
	w.SpawnTier = func(string, string) error { return errors.New("no tmux") }
	if _, err := w.CheckNightly(root, c); err == nil {
		t.Error("a spawn error must be returned")
	}
}

func TestNightlySlot(t *testing.T) {
	c := nightlyCfg("03:30")
	at := func(h, m int) time.Time { return time.Date(2026, 11, 2, h, m, 0, 0, time.UTC) }
	if got := NightlySlot(c, at(3, 29)); !got.Equal(time.Date(2026, 11, 1, 3, 30, 0, 0, time.UTC)) {
		t.Errorf("before at: %v, want yesterday's slot", got)
	}
	if got := NightlySlot(c, at(3, 30)); !got.Equal(at(3, 30)) {
		t.Errorf("at at: %v", got)
	}
	if got := NightlySlot(nightlyCfg(""), at(12, 0)); !got.Equal(at(3, 0)) {
		t.Errorf("default at must be 03:00: %v", got)
	}
}

// TestNightlyRunNamesTheRedSessionAndStep runs this repo's own nightly tier
// (workflow.yaml ci.nightly) with its failures: regex, the ux-test command
// replaced by a stand-in printing what `make ux-test ALL=1` prints for a
// failed step: the tier's run and state record the failure as
// <session>/<step>, and its log is kept like a red CI run's.
func TestNightlyRunNamesTheRedSessionAndStep(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	wf, err := tracker.LoadWorkflow(filepath.Join(filepath.Dir(file), "..", "..", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tier := wf.CI.NightlyTier()
	if tier == "" {
		t.Fatal("workflow.yaml has no ci.nightly tier")
	}
	c := cfg()
	c.Nightly = wf.CI.Nightly
	var uxTest string
	for _, rc := range wf.CI.Run {
		switch rc.TierName() {
		case tracker.CIDefaultTier:
			c.Run = append(c.Run, cmd("true"))
		case tier:
			uxTest = rc.Cmd
			rc.Results = nil
			rc.Cmd = `echo '== ux-test updates (ui/updates) ==';` +
				` echo '✗ TP8 the Install button opens the agent';` +
				` echo 'FAIL updates/TP8 the Install button opens the agent' >&2;` +
				` echo 'FAIL workflow-time/TP4 the bars add up' >&2;` +
				` echo '✗ ux-test failed' >&2; exit 1`
			c.Run = append(c.Run, rc)
		}
	}
	if !strings.Contains(uxTest, "ALL=1") {
		t.Errorf("the nightly tier must run every session (make ux-test ALL=1), got %q", uxTest)
	}
	root := newRepo(t)
	runs, _, err := Execute(Options{Root: root, Config: c, Tier: tier})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %+v, %v", runs, err)
	}
	want := "updates/TP8,workflow-time/TP4"
	if r := runs[0]; r.State != Red || strings.Join(r.Failures, ",") != want || r.KeptLog == "" {
		t.Errorf("run = state %s failures %v kept %q, want red %s with a kept log", r.State, r.Failures, r.KeptLog, want)
	}
	st := mustState(t, root)
	if ts, ok := st.Tier(tier); !ok || ts.State != Red || strings.Join(ts.Failures, ",") != want {
		t.Errorf("tier state = %+v", ts)
	}
	if st.State == Red {
		t.Error("the nightly tier must not turn main red")
	}
	if st.LastRedTier != tier {
		t.Errorf("last red log tier = %q, want %s (the Status card's Last red line)", st.LastRedTier, tier)
	}
}

func TestTierTimeout(t *testing.T) {
	c := nightlyCfg("03:00")
	c.Timeout = "20m"
	c.Nightly.Timeout = "3h"
	if got := c.TierTimeout("slow"); got != 3*time.Hour {
		t.Errorf("nightly tier timeout = %v", got)
	}
	if got := c.TierTimeout(tracker.CIDefaultTier); got != 20*time.Minute {
		t.Errorf("default tier timeout = %v", got)
	}
}
