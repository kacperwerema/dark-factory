package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clean"
	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// cleanFixture points the clean at temp dirs: a Go cache with one old
// entry over a 1 KB cap and one new one, a transcripts folder with an old
// transcript, a state file, a projects.yaml with the cap. It returns the
// paths a test checks.
type cleanPaths struct {
	oldEntry, newEntry, oldTranscript, state, config string
}

func cleanFixture(t *testing.T) cleanPaths {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()
	p := cleanPaths{
		oldEntry:      filepath.Join(dir, "gocache", "ab", "old-a"),
		newEntry:      filepath.Join(dir, "gocache", "cd", "new-a"),
		oldTranscript: filepath.Join(dir, "claude", "projects", "p", "old.jsonl"),
		state:         filepath.Join(dir, "state", "machine", "clean.json"),
		config:        filepath.Join(dir, "projects.yaml"),
	}
	for path, mod := range map[string]time.Time{p.oldEntry: now.Add(-72 * time.Hour), p.newEntry: now, p.oldTranscript: now.AddDate(0, 0, -30)} {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, make([]byte, 1024), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(path, mod, mod)
	}
	os.WriteFile(p.config, []byte("clean:\n  gocache_cap_gb: 0.0000015\nprojects: []\n"), 0o644)
	saved := []any{cleanMachine, cleanGoCacheDir, cleanTranscriptDirs, cleanVolume, cleanHolds, cleanGoBusy, cleanStatePath}
	t.Cleanup(func() {
		cleanMachine = saved[0].(bool)
		cleanGoCacheDir = saved[1].(func() string)
		cleanTranscriptDirs = saved[2].(func() []string)
		cleanVolume = saved[3].(func() diskuse.Volume)
		cleanHolds = saved[4].(func(*Context) []string)
		cleanGoBusy = saved[5].(func() string)
		cleanStatePath = saved[6].(func() string)
	})
	cleanMachine = true
	cleanGoCacheDir = func() string { return filepath.Join(dir, "gocache") }
	cleanTranscriptDirs = func() []string { return []string{filepath.Join(dir, "claude", "projects")} }
	cleanVolume = func() diskuse.Volume { return diskuse.Volume{UsedPct: 90, FreeGB: 50, OK: true} }
	cleanHolds = func(*Context) []string { return nil }
	cleanGoBusy = func() string { return "" }
	cleanStatePath = func() string { return p.state }
	return p
}

func cleanCtx(p cleanPaths, proj *tracker.Project) (*Context, *strings.Builder) {
	ctx, out, _ := newTestContext(proj, false)
	ctx.ConfigPath = p.config
	if proj != nil {
		ctx.AllProjects = []tracker.Project{*proj}
	}
	var b strings.Builder
	ctx.Stdout = &b
	_ = out
	return ctx, &b
}

// While a local CI run or a go build or test is live, or mem-guard says
// no, the clean deletes nothing and records why.
func TestCleanHeldDeletesNothing(t *testing.T) {
	for _, hold := range []string{"mem-guard says no", "a local CI run is live in test (pid 7)", "go test is running (pid 9)"} {
		t.Run(hold, func(t *testing.T) {
			p := cleanFixture(t)
			cleanHolds = func(*Context) []string { return []string{hold} }
			ctx, out := cleanCtx(p, nil)
			if err := runClean(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "clean waits: "+hold) {
				t.Errorf("output doesn't name the hold:\n%s", out)
			}
			for _, f := range []string{p.oldEntry, p.newEntry, p.oldTranscript} {
				if _, err := os.Stat(f); err != nil {
					t.Errorf("%s went while held", f)
				}
			}
			if f := clean.Load(p.state); f.Held == nil || f.Held.Why != hold || len(f.Results) != 0 {
				t.Errorf("state file: %+v", f)
			}
		})
	}
}

// A go build that starts after the holds were read still keeps the cache:
// the clean looks again right before it.
func TestCleanLeavesTheCacheWhileGoBuilds(t *testing.T) {
	p := cleanFixture(t)
	cleanGoBusy = func() string { return "go build is running (pid 5)" }
	ctx, out := cleanCtx(p, nil)
	if err := runClean(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.oldEntry); err != nil {
		t.Error("the cache lost an entry while go build ran")
	}
	if !strings.Contains(out.String(), "left alone: go build is running") {
		t.Errorf("output:\n%s", out)
	}
}

// The real holds see a live local CI run in a project's checkout.
func TestCleanHoldsSeeALiveCIRun(t *testing.T) {
	p := cleanFixture(t)
	proj, root := makeCIFixture(t, "")
	os.MkdirAll(filepath.Join(root, ".ci"), 0o755)
	os.WriteFile(filepath.Join(root, ".ci", "lock"), []byte(fmt.Sprintf(`{"pid":%d,"tier":"default"}`, os.Getpid())), 0o644)
	ctx, _ := cleanCtx(p, proj)
	holds := strings.Join(cleanHoldsNow(ctx), "; ")
	if !strings.Contains(holds, "a local CI run is live in test") {
		t.Errorf("holds %q don't name the CI run", holds)
	}
	cleanGoBusy = func() string { return "go test is running (pid 3)" }
	if holds := strings.Join(cleanHoldsNow(ctx), "; "); !strings.Contains(holds, "go test is running") {
		t.Errorf("holds %q don't name the go test", holds)
	}
}

// With nothing live the clean trims the cache to its cap, drops the old
// transcript, thins a project's snapshots, records the result and prints
// one line per area with the volume before and after.
func TestCleanRunsEveryArea(t *testing.T) {
	p := cleanFixture(t)
	proj, root := makeCIFixture(t, "")
	snaps := health.SnapshotsDir(root)
	os.MkdirAll(snaps, 0o755)
	old := time.Now().AddDate(0, 0, -20)
	for _, h := range []int{1, 2} {
		at := time.Date(old.Year(), old.Month(), old.Day(), h, 0, 0, 0, time.Local)
		os.WriteFile(filepath.Join(snaps, at.Format(clean.SnapshotLayout)+".json"), []byte("{}"), 0o644)
	}
	ctx, out := cleanCtx(p, proj)
	if err := runClean(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.oldEntry); err == nil {
		t.Error("the old cache entry stayed over the cap")
	}
	if _, err := os.Stat(p.newEntry); err != nil {
		t.Error("the new cache entry went")
	}
	if _, err := os.Stat(p.oldTranscript); err == nil {
		t.Error("the old transcript stayed")
	}
	if n, _ := os.ReadDir(snaps); len(n) != 1 {
		t.Errorf("snapshots left: %d, want 1", len(n))
	}
	for _, want := range []string{"== Clean: by hand ==", "gocache", "transcripts", "snapshots test", "worktrees test", "playtest test", "the volume 90% → 90% used"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	f := clean.Load(p.state)
	if len(f.Results) != 1 || f.Results[0].Freed() < 2048 {
		t.Errorf("state file: %+v", f)
	}
	// Just cleaned: --if-due does nothing until the next slot.
	ctx, out = cleanCtx(p, proj)
	cleanVolume = func() diskuse.Volume { return diskuse.Volume{UsedPct: 50, OK: true} }
	if err := runClean(ctx, []string{"--if-due"}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("--if-due right after a clean printed:\n%s", out)
	}
}

// A dry run deletes nothing and records nothing; clean.off stops a clean.
func TestCleanDryRunAndOff(t *testing.T) {
	p := cleanFixture(t)
	ctx, out := cleanCtx(p, nil)
	if err := runClean(ctx, []string{"--dry-run", "--json"}); err != nil {
		t.Fatal(err)
	}
	var res clean.Result
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || res.Freed() < 2048 {
		t.Errorf("dry run JSON: %v %+v", err, res)
	}
	if _, err := os.Stat(p.oldEntry); err != nil {
		t.Error("a dry run deleted the old entry")
	}
	if _, err := os.Stat(p.state); err == nil {
		t.Error("a dry run recorded a result")
	}
	os.WriteFile(p.config, []byte("clean:\n  off: true\nprojects: []\n"), 0o644)
	ctx, out = cleanCtx(p, nil)
	if err := runClean(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "clean: off") {
		t.Errorf("clean.off: %s", out)
	}
	if _, err := os.Stat(p.oldEntry); err != nil {
		t.Error("clean.off still deleted")
	}
}

// In a test binary the clean refuses unless a test points it at temp dirs.
func TestCleanRefusesInATestBinary(t *testing.T) {
	p := cleanFixture(t)
	cleanMachine = false
	ctx, _ := cleanCtx(p, nil)
	if err := runClean(ctx, nil); err == nil || !strings.Contains(err.Error(), "test binary") {
		t.Errorf("err = %v", err)
	}
}

// The docs name the job, its schedule, its limits and its off switch.
func TestCleanDocsNameTheJob(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "workbench-files.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	start := strings.Index(text, "### The nightly clean")
	if start < 0 {
		t.Fatal(`docs/workbench-files.md has no "### The nightly clean" section`)
	}
	section := text[start:]
	if end := strings.Index(section[4:], "\n## "); end >= 0 {
		section = section[:end+4]
	}
	for _, want := range []string{"issue-cli clean", "04:00", "85%", "40 GB", "14 days", "7 days", "local CI", "go build", "mem-guard", "clean.off", "clean_off", "memory/"} {
		if !strings.Contains(section, want) {
			t.Errorf("the nightly clean's docs don't say %q", want)
		}
	}
}
