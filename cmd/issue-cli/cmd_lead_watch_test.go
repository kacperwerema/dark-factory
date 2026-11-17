package main

import (
	"strings"
	"testing"
	"time"
)

// watchStub feeds lead watch the outputs in order (the last repeats), with
// a pinned clock that each sleep moves on by its duration: no real sleep.
// maxRuns stops a watch that would never exit.
func watchStub(t *testing.T, outputs []string, maxRuns int) (ctxNow func() time.Time, runs *int) {
	t.Helper()
	clock := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	n := 0
	origRun, origSleep := leadWatchRun, leadWatchSleep
	leadWatchRun = func(_, _ string) string {
		i := min(n, len(outputs)-1)
		n++
		return outputs[i]
	}
	leadWatchSleep = func(d time.Duration) bool {
		clock = clock.Add(d)
		return n < maxRuns
	}
	t.Cleanup(func() { leadWatchRun, leadWatchSleep = origRun, origSleep })
	return func() time.Time { return clock }, &n
}

func watch(t *testing.T, now func() time.Time, args ...string) string {
	t.Helper()
	ctx, stdout, _ := newTestContext(nil, false)
	ctx.Now = now
	if err := runLead(ctx, append([]string{"watch"}, args...)); err != nil {
		t.Fatalf("lead watch %v: %v", args, err)
	}
	return stdout.String()
}

func TestLeadWatch(t *testing.T) {
	t.Run("exits on a change", func(t *testing.T) {
		now, runs := watchStub(t, []string{"3 images", "3 images", "3 images", "4 images"}, 100)
		out := watch(t, now, "--run", "ls | wc -l", "--every", "10m")
		want := "watch: output changed after 30m (4 runs): ls | wc -l\n4 images\n"
		if out != want || *runs != 4 {
			t.Fatalf("out = %q (runs %d), want %q", out, *runs, want)
		}
	})
	t.Run("exits on a match", func(t *testing.T) {
		now, runs := watchStub(t, []string{"pending", "pending", "ready: 5"}, 100)
		out := watch(t, now, "--run", "probe", "--every", "1m", "--until", `ready: \d+`)
		if !strings.HasPrefix(out, "watch: output matched /ready: \\d+/ after 2m (3 runs): probe\nready: 5") || *runs != 3 {
			t.Fatalf("out = %q (runs %d)", out, *runs)
		}
	})
	t.Run("a first run that matches exits at once", func(t *testing.T) {
		now, _ := watchStub(t, []string{"ready"}, 100)
		if out := watch(t, now, "--run", "probe", "--until", "ready"); !strings.HasPrefix(out, "watch: output matched /ready/ after 0s (1 run)") {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("stays quiet on equal output", func(t *testing.T) {
		now, runs := watchStub(t, []string{"same"}, 50)
		if out := watch(t, now, "--run", "probe", "--every", "10s"); out != "" {
			t.Fatalf("quiet watch printed %q", out)
		}
		if *runs != 50 {
			t.Fatalf("runs = %d, want 50", *runs)
		}
	})
	t.Run("quiet while the regex doesn't match", func(t *testing.T) {
		now, _ := watchStub(t, []string{"a", "b", "c"}, 20)
		if out := watch(t, now, "--run", "probe", "--until", "zzz"); out != "" {
			t.Fatalf("printed %q", out)
		}
	})
}

func TestLeadWatchUsage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--run", "x", "--until-change", "--until", "y"},
		{"--run", "x", "--every", "1s"},
		{"--run", "x", "--until", "("},
	} {
		ctx, _, _ := newTestContext(nil, false)
		if err := runLead(ctx, append([]string{"watch"}, args...)); err == nil {
			t.Errorf("lead watch %v: no error", args)
		}
	}
}

// The real runner keeps stderr and appends a failure's exit code.
func TestLeadWatchRunKeepsExitCode(t *testing.T) {
	got := leadWatchRun(t.TempDir(), "echo out; echo err >&2; exit 3")
	if got != "out\nerr\nexit 3" {
		t.Fatalf("got %q", got)
	}
	if got := leadWatchRun(t.TempDir(), "echo fine"); got != "fine" {
		t.Fatalf("got %q", got)
	}
}
