package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clean"
)

// A clean that has waited more than 3 h is said once on the lead tick,
// with the hold that kept it; under 3 h, or once it ran, nothing.
func TestLeadTickSaysALongCleanWaitOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine", "clean.json")
	prev := leadTickCleanPath
	leadTickCleanPath = func() string { return path }
	t.Cleanup(func() { leadTickCleanPath = prev })
	night := time.Date(2026, 11, 21, 4, 0, 0, 0, time.Local)
	_ = clean.Record(path, clean.Result{At: night, Areas: []clean.Area{clean.Waits(clean.CacheArea, "go test is running (pid 9)")}})

	st := leadTickState{Said: map[string]time.Time{}}
	tick := func(at time.Time) string {
		var b strings.Builder
		tk := &leadTicker{w: &b, prev: st, seen: map[string]time.Time{}, now: at}
		leadTickClean(tk)
		st = leadTickState{Said: tk.seen}
		return b.String()
	}
	if out := tick(night.Add(2 * time.Hour)); out != "" {
		t.Errorf("a 2 h wait was said: %q", out)
	}
	_ = clean.RecordCacheWait(path, clean.Hold{At: night.Add(3 * time.Hour), Why: "go build is running (pid 4)"})
	out := tick(night.Add(3*time.Hour + 10*time.Minute))
	if !strings.Contains(out, "clean: waits since 21 Nov 04:00 (3h10m): go build is running (pid 4)") {
		t.Errorf("a 3 h wait: %q", out)
	}
	if out := tick(night.Add(4 * time.Hour)); out != "" {
		t.Errorf("the same wait said twice: %q", out)
	}
	_ = clean.Record(path, clean.Result{At: night.Add(5 * time.Hour), Areas: []clean.Area{{Name: clean.CacheArea}}})
	if out := tick(night.Add(9 * time.Hour)); out != "" {
		t.Errorf("a clean that ran was said: %q", out)
	}
	// The next night's whole clean held by mem-guard is a new wait.
	_ = clean.RecordHold(path, clean.Hold{At: night.AddDate(0, 0, 1), Why: "mem-guard says no"})
	if out := tick(night.AddDate(0, 0, 1).Add(4 * time.Hour)); !strings.Contains(out, "mem-guard says no") {
		t.Errorf("a held nightly clean: %q", out)
	}
}
