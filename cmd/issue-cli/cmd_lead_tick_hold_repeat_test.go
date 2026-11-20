package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// watchTickNews is one --watch tick at now: its lines and how many are news.
func watchTickNews(t *testing.T, proj *tracker.Project, now time.Time) (string, int) {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return now }
	n, err := leadTickOnce(ctx, true)
	if err != nil {
		t.Fatalf("tick: %v\n%s", err, stdout.String())
	}
	return stdout.String(), n.news
}

func setLoad(avg float64) {
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: avg, Avg5: avg, Cores: 14} }
}

// The hold said last, back after a tick where a self-clearing hold (the
// load) won quietly, is no news; a hold with another first item is.
func TestLeadTickSameHoldAfterLoadHoldIsNoNews(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	mustQ(t, proj, "add", "low")
	t0 := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	assertContains(t, watchTick(t, proj, t0), "hold: nothing ready to start (first: low")

	setLoad(32)
	if out := watchTick(t, proj, t0.Add(5*time.Minute)); out != "" {
		t.Fatalf("load hold under --watch printed:\n%s", out)
	}
	setLoad(2)
	if out, news := watchTickNews(t, proj, t0.Add(10*time.Minute)); news != 0 {
		t.Fatalf("same hold is news again after a load tick:\n%s", out)
	}
	if out, _ := watchTickNews(t, proj, t0.Add(12*time.Minute)); out != "" {
		t.Fatalf("same hold printed on the next tick too:\n%s", out)
	}

	// Another first item: news once, also across a load tick.
	writeIssue(t, proj, "other", "backlog")
	mustQ(t, proj, "add", "other")
	mustQ(t, proj, "drop", "1")
	assertContains(t, watchTick(t, proj, t0.Add(15*time.Minute)), "hold: nothing ready to start (first: other")
	setLoad(32)
	watchTick(t, proj, t0.Add(20*time.Minute))
	setLoad(2)
	if out, news := watchTickNews(t, proj, t0.Add(25*time.Minute)); news != 0 {
		t.Fatalf("other's hold is news again after a load tick:\n%s", out)
	}
}

// A by-hand tick says a hold whose reason changes, memory to load, once.
func TestLeadTickChangedHoldReasonIsNews(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 60, CanStart: true} }
	assertContains(t, tick(t, proj), "hold: memory 60% ≥ 50%")
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 20, CanStart: true} }
	setLoad(32)
	assertContains(t, tick(t, proj), "hold: load 32.0/32.0 on 14 cores (max 14)")
	if out := tick(t, proj); strings.Contains(out, "hold:") {
		t.Fatalf("load hold twice:\n%s", out)
	}
}

// Three ticks with an unchanged hold, load ticks between: --exit-on-news
// keeps waiting, then exits once on real news with the hold above it once.
func TestLeadTickExitOnNewsWaitsThroughSameHold(t *testing.T) {
	proj, _, _ := tickProject(t)
	stubStart(t)
	mustQ(t, proj, "add", "low")
	assertContains(t, watchTick(t, proj, time.Now()), "hold: nothing ready to start (first: low")

	root := filepath.Dir(proj.IssueDir)
	ticks := 0
	orig := leadTickSleep
	leadTickSleep = func(time.Duration) bool {
		ticks++
		switch ticks {
		case 1, 3:
			setLoad(2)
		case 2, 4:
			setLoad(32)
		case 5:
			setLoad(2)
			wf := "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\nci:\n  run:\n    - \"true\"\n"
			os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644)
			os.MkdirAll(filepath.Join(root, ".ci"), 0o755)
			os.WriteFile(filepath.Join(root, ".ci", "state.json"), []byte(`{"branch":"main","last_sha":"deadbeefcafe","red_since":"deadbeefcafe","failed":"make validate","state":"red"}`), 0o644)
		default:
			t.Errorf("tick %d: should have exited on the red CI", ticks)
			return false
		}
		return true
	}
	t.Cleanup(func() { leadTickSleep = orig })
	setLoad(32)
	out := tick(t, proj, "--watch", "--exit-on-news", "--every", "1m")
	if ticks != 5 {
		t.Fatalf("exited after %d sleeps, want 5 (the same hold is no news):\n%s", ticks, out)
	}
	if n := strings.Count(out, "hold: nothing ready to start"); n != 1 {
		t.Fatalf("hold line %d times, want once:\n%s", n, out)
	}
	if strings.Index(out, "hold: nothing ready") > strings.Index(out, "ci: main red since deadbee") {
		t.Fatalf("hold line not above the news:\n%s", out)
	}
}
