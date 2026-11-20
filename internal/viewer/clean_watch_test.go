package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clean"
	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// stubClean makes the health tick's clean see a live machine with vol,
// and records each start instead of running issue-cli.
func stubClean(t *testing.T, vol diskuse.Volume) *[]string {
	t.Helper()
	var spawned []string
	prevSpawn, prevLive, prevVol := cleanSpawn, cleanLive, cleanVolume
	cleanSpawn = func(config string) error { spawned = append(spawned, config); return nil }
	cleanLive = func() bool { return true }
	cleanVolume = func() diskuse.Volume { return vol }
	t.Cleanup(func() { cleanSpawn, cleanLive, cleanVolume = prevSpawn, prevLive, prevVol })
	return &spawned
}

// The health tick starts the clean once it is due: after tonight's slot
// with no clean since, early while the volume is over 85% used, never with
// clean.off, and not again once tonight's clean is recorded.
func TestHealthTickStartsTheCleanWhenDue(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "projects.yaml")
	os.WriteFile(config, []byte("clean:\n  at: \"04:00\"\nprojects: []\n"), 0o644)
	projects := []tracker.Project{{Slug: "p", ConfigPath: config}}
	state := filepath.Join(dir, "state", "machine", "clean.json")
	spawned := stubClean(t, diskuse.Volume{UsedPct: 50, OK: true})
	lastErr := map[string]string{}

	now := time.Date(2026, 11, 18, 4, 10, 0, 0, time.Local)
	if why := cleanTick(projects, now, state, lastErr); why != "nightly" || len(*spawned) != 1 || (*spawned)[0] != config {
		t.Fatalf("no clean yet: why %q, spawned %v", why, *spawned)
	}
	clean.Record(state, clean.Result{At: now})
	if why := cleanTick(projects, now.Add(10*time.Minute), state, lastErr); why != "" {
		t.Errorf("started again after tonight's clean: %q", why)
	}

	// The volume over 85% starts it early, 6 hours after the last.
	cleanVolume = func() diskuse.Volume { return diskuse.Volume{UsedPct: 93, OK: true} }
	if why := cleanTick(projects, now.Add(2*time.Hour), state, lastErr); why != "" {
		t.Errorf("a full volume restarted it within 6 hours: %q", why)
	}
	if why := cleanTick(projects, now.Add(7*time.Hour), state, lastErr); !strings.Contains(why, "93%") {
		t.Errorf("a full volume 7 hours on didn't start it: %q", why)
	}

	// clean.off: never.
	os.WriteFile(config, []byte("clean:\n  off: true\nprojects: []\n"), 0o644)
	n := len(*spawned)
	if why := cleanTick(projects, now.AddDate(0, 0, 2), state, lastErr); why != "" || len(*spawned) != n {
		t.Errorf("clean.off still started it: %q", why)
	}
}

// In a test binary the tick starts nothing (diskuse.Live).
func TestHealthTickNeverCleansFromATest(t *testing.T) {
	spawned := stubClean(t, diskuse.Volume{UsedPct: 99, OK: true})
	cleanLive = func() bool { return diskuse.Live }
	if why := cleanTick([]tracker.Project{{Slug: "p"}}, time.Now(), filepath.Join(t.TempDir(), "clean.json"), map[string]string{}); why != "" || len(*spawned) != 0 {
		t.Errorf("a test binary started a clean: %q", why)
	}
}

// The Needs you disk row says how the last clean went, or why it waits.
func TestNeedsYouDiskRowNamesTheLastClean(t *testing.T) {
	now := time.Now()
	stubDisk(t, bigGoCache(now))
	prev := cleanStatus
	t.Cleanup(func() { cleanStatus = prev })
	cleanStatus = func() string { return "the clean waits since 18 Nov 04:00: 2 workers live" }
	rows := diskNeedsYou(&tracker.Project{Name: "P", Slug: "p"}, "", "/p/p", now)
	if len(rows) != 1 || !strings.HasSuffix(rows[0].Note, " The clean waits since 18 Nov 04:00: 2 workers live.") {
		t.Errorf("rows = %+v", rows)
	}
}
