package health

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func writeSized(t *testing.T, p string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStorageFamilyMeasuresEachArea(t *testing.T) {
	root := t.TempDir()
	const mb = 1 << 20
	writeSized(t, filepath.Join(root, ".git", "objects", "pack"), 2*mb)
	writeSized(t, filepath.Join(root, ".worktrees", "ui", "a", ".git"), 100)
	writeSized(t, filepath.Join(root, ".worktrees", "ui", "a", "main.go"), 3*mb)
	writeSized(t, filepath.Join(root, ".worktrees", "ui", "a", ".playtest", "runs", "s", "r", "shots", "TP1.png"), 4*mb)
	writeSized(t, filepath.Join(root, ".worktrees", "cli", "b", ".git"), 100)
	writeSized(t, filepath.Join(root, ".playtest", "runs", "s", "r", "shots", "TP1.png"), 1*mb)
	writeSized(t, filepath.Join(root, ".agent-logs", "clilog.jsonl"), mb/2)
	writeSized(t, filepath.Join(root, ".ci", "red", "20261031T110000Z-abc1234.first.log"), mb/2)
	writeSized(t, filepath.Join(root, "issues", "attachments", "x", "Main.png"), mb)
	writeSized(t, filepath.Join(root, "main.go"), mb)
	if err := os.Symlink(filepath.Join(root, ".git"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	env := Env{Root: root, Project: &tracker.Project{IssueDir: filepath.Join(root, "issues")}}
	metrics, err := storageFamily(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, m := range metrics {
		got[m.Name] = m.Value
		if m.Help == "" {
			t.Errorf("%s has no help", m.Name)
		}
	}
	want := map[string]float64{
		"storage.total_mb":       13,
		"storage.git_mb":         2,
		"storage.worktrees_mb":   3,
		"storage.worktrees":      2,
		"storage.playtest_mb":    5,
		"storage.agent_logs_mb":  0.5,
		"storage.ci_mb":          0.5,
		"storage.attachments_mb": 1,
	}
	for name, v := range want {
		if got[name] != v {
			t.Errorf("%s = %v, want %v", name, got[name], v)
		}
	}
	for _, a := range StorageAreas {
		if _, ok := got[a.Metric]; !ok {
			t.Errorf("StorageAreas names %s, which the family doesn't record", a.Metric)
		}
	}
}

func TestStorageFamilyHasNoPast(t *testing.T) {
	if _, err := storageFamily(context.Background(), Env{Root: t.TempDir(), Backfill: true}); !errors.Is(err, ErrNoPast) {
		t.Errorf("backfill err = %v, want ErrNoPast", err)
	}
}

func TestStorageAttachmentsOutsideTheRoot(t *testing.T) {
	root, issues := t.TempDir(), t.TempDir()
	writeSized(t, filepath.Join(issues, "attachments", "a.png"), 1<<20)
	s, err := measureStorage(context.Background(), root, filepath.Join(issues, "attachments"))
	if err != nil {
		t.Fatal(err)
	}
	if s.attachments != 1<<20 || s.total != 0 {
		t.Errorf("sizes = %+v", s)
	}
}

// measureStorageSerial is the family's walk before the pool: one WalkDir,
// kept as the reference the parallel walk must match.
func measureStorageSerial(root, attachments string) storageSizes {
	var s storageSizes
	root, _ = filepath.Abs(root)
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		size := info.Size()
		if strings.HasPrefix(rel, ".worktrees/") && d.Name() == ".git" {
			s.worktreeCount++
		}
		s.total += size
		switch first, _, _ := strings.Cut(rel, "/"); {
		case first == ".git":
			s.git += size
		case first == ".worktrees":
			if strings.Contains(rel, "/.playtest/") {
				s.playtest += size
			} else {
				s.worktrees += size
			}
		case first == ".playtest":
			s.playtest += size
		case first == ".agent-logs":
			s.agentLogs += size
		case first == ".ci":
			s.ci += size
		}
		if attachments != "" && within(attachments, p) {
			s.attachments += size
		}
		return nil
	})
	if attachments != "" && !within(root, attachments) {
		filepath.WalkDir(attachments, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if info, err := d.Info(); err == nil {
					s.attachments += info.Size()
				}
			}
			return nil
		})
	}
	return s
}

// storageFixture builds a checkout with every area, many worktrees and deep
// folders, so the pool hands work around and finds work while busy.
func storageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSized(t, filepath.Join(root, ".git", "objects", "pack", "p.pack"), 5000)
	writeSized(t, filepath.Join(root, ".git", "HEAD"), 21)
	for i := range 30 {
		wt := filepath.Join(root, ".worktrees", "sys", fmt.Sprintf("w%02d", i))
		writeSized(t, filepath.Join(wt, ".git"), 90+i)
		writeSized(t, filepath.Join(wt, "main.go"), 1000+i)
		deep := wt
		for d := range i % 7 {
			deep = filepath.Join(deep, fmt.Sprintf("d%d", d))
			writeSized(t, filepath.Join(deep, "f.go"), 10*d+i)
		}
		writeSized(t, filepath.Join(wt, ".playtest", "runs", "s", "r", "shots", "TP1.png"), 3000+i)
		if i%5 == 0 { // a nested repo's .git folder: walked, not counted as a worktree
			writeSized(t, filepath.Join(wt, "vendor", "x", ".git", "HEAD"), 17)
		}
	}
	writeSized(t, filepath.Join(root, ".playtest", "runs", "s", "r", "shots", "TP1.png"), 7000)
	writeSized(t, filepath.Join(root, ".agent-logs", "clilog.jsonl"), 600)
	writeSized(t, filepath.Join(root, ".ci", "red", "a.log"), 800)
	writeSized(t, filepath.Join(root, "issues", "attachments", "x", "Main.png"), 900)
	writeSized(t, filepath.Join(root, "issues", "attachments-old", "y.png"), 50) // not attachments/
	writeSized(t, filepath.Join(root, "main.go"), 400)
	if err := os.MkdirAll(filepath.Join(root, "empty", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".git"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestStorageParallelMatchesSerial(t *testing.T) {
	root := storageFixture(t)
	outside := filepath.Join(t.TempDir(), "attachments")
	writeSized(t, filepath.Join(outside, "a", "b.png"), 1234)
	for _, attach := range []string{filepath.Join(root, "issues", "attachments"), outside, ""} {
		want := measureStorageSerial(root, attach)
		if want.worktreeCount != 30 || want.total == 0 {
			t.Fatalf("fixture: serial walk = %+v", want)
		}
		for _, workers := range []int{1, 2, storageWorkers} {
			got, err := walkSizesWith(t, root, attach, workers)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("attachments %q, %d workers:\n got %+v\nwant %+v", attach, workers, got, want)
			}
		}
	}
}

// walkSizesWith runs measureStorage with the pool at the given size.
func walkSizesWith(t *testing.T, root, attach string, workers int) (storageSizes, error) {
	t.Helper()
	saved := storageWorkers
	storageWorkers = workers
	defer func() { storageWorkers = saved }()
	return measureStorage(context.Background(), root, attach)
}

func TestStorageHonoursCancel(t *testing.T) {
	root := storageFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s, err := measureStorage(ctx, root, ""); !errors.Is(err, context.Canceled) || s.total != 0 {
		t.Errorf("cancelled before the walk: err = %v, sizes = %+v; want context.Canceled and nothing read", err, s)
	}

	// Cancelled during the walk: no folder is read after the cancel but
	// the ones already being read, at most one per reader.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	read, afterCancel := 0, 0
	storageDirHook = func(string) {
		mu.Lock()
		defer mu.Unlock()
		if read++; read == 5 {
			cancel()
		} else if read > 5 {
			afterCancel++
		}
	}
	defer func() { storageDirHook = nil }()
	_, err := measureStorage(ctx, root, "")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	full := measureStorageSerial(root, "")
	if afterCancel > storageWorkers {
		t.Errorf("%d folders read after the cancel, want at most %d (the readers already busy)", afterCancel, storageWorkers)
	}
	if read >= 30 || full.worktreeCount != 30 {
		t.Errorf("the walk read %d folders after a cancel at the 5th; it should have stopped", read)
	}
}

// AC1: the family counts Go's build cache and the agent transcripts, which
// live outside the checkout, from one walk an hour shared by every
// snapshot, and keeps them out of the checkout's total.
func TestStorageFamilyCountsTheGoCacheAndTranscripts(t *testing.T) {
	const mb = 1 << 20
	root, cache, claude, codex := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("GOCACHE", cache)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	prevLive, prevVol := diskuse.Live, diskuse.VolumeOf
	diskuse.Live = true
	diskuse.VolumeOf = func(string) diskuse.Volume { return diskuse.Volume{UsedPct: 87.25, FreeGB: 120, OK: true} }
	t.Cleanup(func() { diskuse.Live, diskuse.VolumeOf = prevLive, prevVol })

	writeSized(t, filepath.Join(root, "main.go"), mb)
	writeSized(t, filepath.Join(cache, "3f", "3f0a-d"), 3*mb)
	writeSized(t, filepath.Join(cache, "a1", "a1b2-a"), mb)
	writeSized(t, filepath.Join(claude, "projects", "-Users-x-repo", "s.jsonl"), 2*mb)
	writeSized(t, filepath.Join(codex, "sessions", "2026", "11", "16", "r.jsonl"), mb/2)
	writeSized(t, filepath.Join(claude, "settings.json"), mb) // not a transcript

	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.Local)
	env := Env{Root: root, Now: now}
	got := func() map[string]float64 {
		t.Helper()
		metrics, err := storageFamily(context.Background(), env)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]float64{}
		for _, x := range metrics {
			m[x.Name] = x.Value
			if x.Help == "" {
				t.Errorf("%s has no help", x.Name)
			}
		}
		return m
	}
	first := got()
	want := map[string]float64{"storage.gocache_mb": 4, "storage.transcripts_mb": 2.5, "storage.volume_used_pct": 87.3, "storage.total_mb": 1}
	for name, v := range want {
		if first[name] != v {
			t.Errorf("%s = %v, want %v", name, first[name], v)
		}
	}
	if _, ok := first["storage.gocache_new_mb"]; ok != (runtime.GOOS == "darwin") {
		t.Errorf("storage.gocache_new_mb written = %v on %s: only macOS keeps a birth time", ok, runtime.GOOS)
	}

	// A snapshot within the hour reuses the reading: no second walk.
	writeSized(t, filepath.Join(cache, "ff", "ffff-d"), 10*mb)
	env.Now = now.Add(30 * time.Minute)
	if again := got(); again["storage.gocache_mb"] != 4 {
		t.Errorf("a snapshot 30 min later walked again: gocache_mb = %v, want the reading's 4", again["storage.gocache_mb"])
	}
	env.Now = now.Add(2 * time.Hour)
	if later := got(); later["storage.gocache_mb"] != 14 {
		t.Errorf("two hours later gocache_mb = %v, want a new walk's 14", later["storage.gocache_mb"])
	}
	if n := len(diskuse.Load(diskuse.Path())); n != 2 {
		t.Errorf("readings file holds %d readings, want 2 (one per walk)", n)
	}

	// No cache folder (GOCACHE=off): no cache metric, not a 0.
	t.Setenv("GOCACHE", "off")
	env.Now = now.Add(4 * time.Hour)
	if off := got(); func() bool { _, ok := off["storage.gocache_mb"]; return ok }() {
		t.Errorf("GOCACHE=off still writes storage.gocache_mb = %v", off["storage.gocache_mb"])
	}
}

// A walk that runs out of the family's time falls back to the newest
// reading under three days old, with a warning, and the checkout's areas
// are still written.
func TestStorageFamilyFallsBackWhenTheCacheWalkIsSlow(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	prevLive, prevRead := diskuse.Live, diskReading
	diskuse.Live = true
	t.Cleanup(func() { diskuse.Live, diskReading = prevLive, prevRead })
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.Local)
	if err := diskuse.Append(diskuse.Path(), diskuse.Reading{At: now.Add(-5 * time.Hour), HasGoCache: true, GoCacheMB: 159 * 1024}); err != nil {
		t.Fatal(err)
	}
	var budget time.Duration
	diskReading = func(ctx context.Context, _ time.Time) (diskuse.Reading, error) {
		dl, _ := ctx.Deadline()
		budget = time.Until(dl)
		<-ctx.Done()
		return diskuse.Reading{}, ctx.Err()
	}
	env := Env{Root: t.TempDir(), Now: now, Config: &tracker.HealthConfig{Timeout: "400ms"}}
	warned := &warnings{}
	metrics, err := runFamily(context.WithValue(context.Background(), warningsKey{}, warned), env, registered{name: "storage", fn: storageFamily})
	if err != nil {
		t.Fatalf("family failed: %v", err)
	}
	if budget <= 0 || budget > 250*time.Millisecond {
		t.Errorf("the walk got %s of a 400ms family, want about half", budget)
	}
	got := map[string]float64{}
	for _, m := range metrics {
		got[m.Name] = m.Value
	}
	if got["storage.gocache_mb"] != 159*1024 {
		t.Errorf("gocache_mb = %v, want the older reading's", got["storage.gocache_mb"])
	}
	if _, ok := got["storage.total_mb"]; !ok {
		t.Error("the checkout's total is missing")
	}
	if len(warned.list) != 1 || !strings.Contains(warned.list[0], "the reading is from 16 Nov 05:00") {
		t.Errorf("warnings = %q", warned.list)
	}
}

// A family's Prepare step runs before the families, outside the family
// timeout, and not on backfill.
func TestPrepareRunsBeforeTheFamiliesWithoutTheirTimeout(t *testing.T) {
	var order []string
	Prepare("zz-prep", func(ctx context.Context, env Env) {
		if _, ok := ctx.Deadline(); ok {
			t.Error("the prepare step got the family's deadline")
		}
		order = append(order, "prepare")
	})
	t.Cleanup(func() {
		registryMu.Lock()
		delete(prepares, "zz-prep")
		registryMu.Unlock()
	})
	fam := registered{name: "zz-prep", fn: func(context.Context, Env) ([]Metric, error) {
		order = append(order, "family")
		return nil, nil
	}}
	env := Env{Root: t.TempDir(), Now: time.Date(2026, 11, 16, 10, 0, 0, 0, time.Local)}
	if _, _, err := take(context.Background(), env, TriggerManual, []registered{fam}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "prepare,family" {
		t.Errorf("order = %v", order)
	}
	order = nil
	env.Backfill = true
	_, _, _ = take(context.Background(), env, TriggerManual, []registered{fam})
	if len(order) > 0 && order[0] == "prepare" {
		t.Errorf("backfill ran the prepare step: %v", order)
	}
}

func TestStorageFamilyCountsTheCIWorktree(t *testing.T) {
	root := t.TempDir()
	const mb = 1 << 20
	writeSized(t, filepath.Join(root, ".worktrees", ".ci", ".git"), 100)
	writeSized(t, filepath.Join(root, ".worktrees", ".ci", "main.go"), 5*mb)
	writeSized(t, filepath.Join(root, ".worktrees", ".ci", ".playtest", "runs", "x.png"), mb)
	writeSized(t, filepath.Join(root, ".worktrees", "ui", "a", ".git"), 100)
	writeSized(t, filepath.Join(root, ".worktrees", "ui", "a", "main.go"), 2*mb)
	writeSized(t, filepath.Join(root, ".worktrees", ".cix", "a.go"), mb) // not the CI tree
	metrics, err := storageFamily(context.Background(), Env{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, m := range metrics {
		got[m.Name] = m.Value
	}
	want := map[string]float64{
		"storage.ci_worktree_mb": 6,
		"storage.worktrees_mb":   3,
		"storage.worktrees":      1,
		"storage.playtest_mb":    0,
		"storage.total_mb":       9,
	}
	for name, v := range want {
		if got[name] != v {
			t.Errorf("%s = %v, want %v", name, got[name], v)
		}
	}
	listed := false
	for _, a := range StorageAreas {
		listed = listed || a.Metric == "storage.ci_worktree_mb"
	}
	if !listed {
		t.Error("StorageAreas leaves out storage.ci_worktree_mb: the Stats page's Disk use wouldn't show it")
	}
}
