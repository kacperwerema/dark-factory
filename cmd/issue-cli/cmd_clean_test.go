package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
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
	// oldTmp is an old pt-bins-* folder in the fake $TMPDIR, heldTmp an old
	// one a live process holds (the fake lsof names it).
	oldTmp, heldTmp string
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
		oldTmp:        filepath.Join(dir, "tmp", "pt-bins-111"),
		heldTmp:       filepath.Join(dir, "tmp", "pt-bins-222"),
	}
	for _, d := range []string{p.oldTmp, p.heldTmp} {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "issue-cli"), make([]byte, 2048), 0o644)
		os.Chtimes(filepath.Join(d, "issue-cli"), now.Add(-48*time.Hour), now.Add(-48*time.Hour))
		os.Chtimes(d, now.Add(-48*time.Hour), now.Add(-48*time.Hour))
	}
	for path, mod := range map[string]time.Time{p.oldEntry: now.Add(-72 * time.Hour), p.newEntry: now, p.oldTranscript: now.AddDate(0, 0, -30)} {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, make([]byte, 1024), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(path, mod, mod)
	}
	os.WriteFile(p.config, []byte("clean:\n  gocache_cap_gb: 0.0000015\nprojects: []\n"), 0o644)
	saved := []any{cleanMachine, cleanGoCacheDir, cleanTranscriptDirs, cleanVolume, cleanHolds, cleanGoBusy, cleanStatePath, cleanTmpDir, cleanTmpInUse}
	savedAreaHolds, savedCache := cleanAreaHolds, cleanCache
	t.Cleanup(func() {
		cleanAreaHolds, cleanCache = savedAreaHolds, savedCache
		cleanMachine = saved[0].(bool)
		cleanGoCacheDir = saved[1].(func() string)
		cleanTranscriptDirs = saved[2].(func() []string)
		cleanVolume = saved[3].(func() diskuse.Volume)
		cleanHolds = saved[4].(func(*Context) []string)
		cleanGoBusy = saved[5].(func() string)
		cleanStatePath = saved[6].(func() string)
		cleanTmpDir = saved[7].(func() string)
		cleanTmpInUse = saved[8].(func(string) (map[string]bool, error))
	})
	cleanMachine = true
	cleanGoCacheDir = func() string { return filepath.Join(dir, "gocache") }
	cleanTranscriptDirs = func() []string { return []string{filepath.Join(dir, "claude", "projects")} }
	cleanVolume = func() diskuse.Volume { return diskuse.Volume{UsedPct: 90, FreeGB: 50, OK: true} }
	cleanHolds = func(*Context) []string { return nil }
	cleanAreaHolds = cleanAreaHoldsNow
	cleanCache = func(time.Time, int64) clean.Cache { return clean.Cache{} }
	cleanGoBusy = func() string { return "" }
	cleanStatePath = func() string { return p.state }
	cleanTmpDir = func() string { return filepath.Join(dir, "tmp") }
	cleanTmpInUse = func(string) (map[string]bool, error) { return map[string]bool{"pt-bins-222": true}, nil }
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

// While mem-guard says no the clean deletes nothing and records why.
func TestCleanHeldDeletesNothing(t *testing.T) {
	for _, hold := range []string{"mem-guard says no (memory 80% used)"} {
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

// A go test that runs holds the gocache area only: the cache keeps every
// entry, the old transcript and temp folder still go, the report says
// which area waited and why, and the next --if-due tick runs the cache
// alone once the test is gone (factory/clean-holds-per-area-…).
func TestCleanOnlyTheCacheWaitsWhileGoTests(t *testing.T) {
	p := cleanFixture(t)
	cleanGoBusy = func() string { return "go test is running (pid 5)" }
	ctx, out := cleanCtx(p, nil)
	if err := runClean(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.oldEntry); err != nil {
		t.Error("the cache lost an entry while go test ran")
	}
	if _, err := os.Stat(p.oldTranscript); err == nil {
		t.Error("the old transcript waited on the go test")
	}
	if _, err := os.Stat(p.oldTmp); err == nil {
		t.Error("the old temp folder waited on the go test")
	}
	for _, want := range []string{"gocache                      freed    0.0 MB  waits: go test is running (pid 5)", "waited                       gocache (the viewer tries again on its next tick)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if f := clean.Load(p.state); len(f.Results) != 1 || f.CacheWaits == nil || f.Held != nil {
		t.Fatalf("state file: %+v", f)
	}

	// Still testing: the cache-only try records the wait and prints one line.
	cleanVolume = func() diskuse.Volume { return diskuse.Volume{UsedPct: 50, OK: true} }
	ctx, out = cleanCtx(p, nil)
	if err := runClean(ctx, []string{"--if-due"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "clean waits: gocache: go test is running (pid 5)\n" {
		t.Errorf("cache-only try while go test runs printed:\n%s", got)
	}
	if f := clean.Load(p.state); len(f.Results) != 1 || f.CacheWaits == nil {
		t.Fatalf("state file after the held try: %+v", f)
	}

	// The test is gone: the cache alone runs and the wait clears.
	cleanGoBusy = func() string { return "" }
	ctx, out = cleanCtx(p, nil)
	if err := runClean(ctx, []string{"--if-due"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.oldEntry); err == nil {
		t.Error("the old cache entry stayed once the test was gone")
	}
	if strings.Contains(out.String(), "transcripts") || !strings.Contains(out.String(), "== Clean: the Go cache waits since") {
		t.Errorf("the cache-only clean ran other areas or lost its why:\n%s", out)
	}
	if f := clean.Load(p.state); f.CacheWaits != nil || f.CacheAt.IsZero() || len(f.Results) != 2 {
		t.Errorf("state file after the cache ran: %+v", f)
	}
}

// In the day, --if-due trims the cache alone once the newest reading has
// it over its cap by more than 25%, and not again on the same reading.
func TestCleanTrimsAnOverCapCacheInTheDay(t *testing.T) {
	p := cleanFixture(t)
	proj, _ := makeCIFixture(t, "")
	cleanVolume = func() diskuse.Volume { return diskuse.Volume{UsedPct: 50, OK: true} }
	// Tonight's slot 12 h away, so the clean a minute ago is after the last.
	at := time.Now().Add(12 * time.Hour).Format("15:04")
	os.WriteFile(p.config, []byte("clean:\n  at: \""+at+"\"\n  gocache_cap_gb: 0.0000015\nprojects: []\n"), 0o644)
	_ = clean.Record(p.state, clean.Result{At: time.Now().Add(-time.Minute), Areas: []clean.Area{{Name: clean.CacheArea}}})
	reading := time.Now()
	cleanCache = func(_ time.Time, capBytes int64) clean.Cache {
		return clean.Cache{At: reading, Bytes: 2 * capBytes, CapBytes: capBytes, OK: true}
	}
	ctx, out := cleanCtx(p, proj)
	if err := runClean(ctx, []string{"--if-due"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.oldEntry); err == nil {
		t.Errorf("the over-cap cache wasn't trimmed:\n%s", out)
	}
	if !strings.Contains(out.String(), "over its cap of") || strings.Contains(out.String(), "worktrees") {
		t.Errorf("output:\n%s", out)
	}
	ctx, out = cleanCtx(p, proj)
	if err := runClean(ctx, []string{"--if-due"}); err != nil || out.Len() != 0 {
		t.Errorf("the same reading trimmed again: %v\n%s", err, out)
	}
}

// The real area holds: a live local CI run holds gocache and its
// project's worktrees, a go test the cache; the whole-clean holds name
// neither.
func TestCleanHoldsSeeALiveCIRun(t *testing.T) {
	p := cleanFixture(t)
	proj, root := makeCIFixture(t, "")
	os.MkdirAll(filepath.Join(root, ".ci"), 0o755)
	os.WriteFile(filepath.Join(root, ".ci", "lock"), []byte(fmt.Sprintf(`{"pid":%d,"tier":"default"}`, os.Getpid())), 0o644)
	ctx, _ := cleanCtx(p, proj)
	holds := cleanAreaHoldsNow(ctx)
	if !strings.Contains(holds[clean.CacheArea], "a local CI run is live in test") || !strings.Contains(holds["worktrees test"], "a local CI run is live in test") {
		t.Errorf("area holds %q don't name the CI run", holds)
	}
	if _, ok := holds["transcripts"]; ok {
		t.Errorf("a CI run holds transcripts: %q", holds)
	}
	cleanGoBusy = func() string { return "go test is running (pid 3)" }
	if holds := cleanAreaHoldsNow(ctx); !strings.Contains(holds[clean.CacheArea], "go test is running") {
		t.Errorf("area holds %q don't name the go test", holds)
	}
	for _, h := range cleanHoldsNow(ctx) {
		if strings.Contains(h, "CI") || strings.Contains(h, "go test") {
			t.Errorf("the whole clean waits on %q", h)
		}
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
	if _, err := os.Stat(p.oldTmp); err == nil {
		t.Error("the old pt-bins folder stayed in $TMPDIR")
	}
	if _, err := os.Stat(p.heldTmp); err != nil {
		t.Error("the pt-bins folder a live process holds went")
	}
	for _, want := range []string{"== Clean: by hand ==", "gocache", "transcripts", "tmp                          freed    0.0 MB  1 entries removed, 1 kept (in use or younger than 24 h)", "snapshots test", "worktrees test", "playtest test", "the volume 90% → 90% used"} {
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
	if _, err := os.Stat(p.oldTmp); err != nil {
		t.Error("a dry run deleted the old temp folder")
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

// lsof failing leaves $TMPDIR as it is and says so on the tmp line.
func TestCleanTmpLeftAloneWithoutLsof(t *testing.T) {
	p := cleanFixture(t)
	cleanTmpInUse = func(string) (map[string]bool, error) { return nil, fmt.Errorf("lsof: not found") }
	ctx, out := cleanCtx(p, nil)
	if err := runClean(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.oldTmp); err != nil {
		t.Error("the tmp area took an entry without knowing what is open")
	}
	if !strings.Contains(out.String(), "left alone: lsof: not found") {
		t.Errorf("output:\n%s", out)
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

// Every clean area is a row of docs/fs-churn.md's table of what grows and
// what bounds it: a new area can't land without saying what it bounds.
func TestEveryCleanAreaIsInTheGrowthTable(t *testing.T) {
	p := cleanFixture(t)
	proj, _ := makeCIFixture(t, "")
	ctx, out := cleanCtx(p, proj)
	if err := runClean(ctx, []string{"--dry-run", "--json"}); err != nil {
		t.Fatal(err)
	}
	var res clean.Result
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "fs-churn.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	start := strings.Index(text, "## What grows over time and what bounds it")
	if start < 0 {
		t.Fatal(`docs/fs-churn.md has no "## What grows over time and what bounds it" section`)
	}
	section := text[start:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	if len(res.Areas) < 6 {
		t.Fatalf("the dry run returned %d areas: %+v", len(res.Areas), res.Areas)
	}
	for _, a := range res.Areas {
		name, _, _ := strings.Cut(a.Name, " ") // "snapshots <project>" is the snapshots area
		if !strings.Contains(section, "clean `"+name+"`") {
			t.Errorf("clean area %q has no row in docs/fs-churn.md#what-grows-over-time-and-what-bounds-it: add one with what it bounds (clean `%s`)", a.Name, name)
		}
	}
}

// AC7, 24/7: a live worker doesn't hold the clean (the real holds read a
// live CI run, a go build and mem-guard, never a worker), the tmp area
// keeps what a live process holds, and a clean in progress (its lock held)
// doesn't hold the queue: the next item still starts.
func TestCleanBesideLiveWorkers(t *testing.T) {
	p := cleanFixture(t)
	proj := makeAutostartProject(t)
	started := stubStart(t)
	agentrun.HasSession = func(target string) bool { return strings.Contains(target, "agent-base") }
	agentrun.ListSessions = func() []agentrun.Session {
		return []agentrun.Session{{Name: "agent-base", StartTime: time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05")}}
	}
	cleanHolds = cleanHoldsNow
	ctx, out := cleanCtx(p, proj)
	if err := runClean(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "clean waits") || !strings.Contains(out.String(), "1 entries removed, 1 kept") {
		t.Fatalf("the clean with a live worker:\n%s", out)
	}
	if _, err := os.Stat(p.heldTmp); err != nil {
		t.Error("the temp folder a live process holds went")
	}

	release, err := clean.Lock(p.state)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	mustQ(t, proj, "add", "high", "--quote", queueWords, "--how", "in the CEO chat")
	if out := mustQ(t, proj, "next", "--start"); len(*started) != 1 || (*started)[0] != "high" {
		t.Fatalf("with a clean running the queue started %v:\n%s", *started, out)
	}
}
