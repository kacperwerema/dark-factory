package clean

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/procs"
)

func writeFile(t *testing.T, path string, size int, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// A fake GOCACHE over the cap loses its oldest entries until it fits; the
// newest stay, and the files outside the entry folders stay.
func TestGoCacheOverTheCapLosesTheOldest(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 17, 4, 0, 0, 0, time.Local)
	for i := range 10 {
		writeFile(t, filepath.Join(dir, "0"+string(rune('a'+i%6)), "entry"+string(rune('0'+i))+"-d"), 1000, now.Add(-time.Duration(10-i)*2*time.Hour))
	}
	writeFile(t, filepath.Join(dir, "README"), 500, now.Add(-100*time.Hour))
	writeFile(t, filepath.Join(dir, "trim.txt"), 10, now.Add(-100*time.Hour))
	a := GoCache(dir, 4500, false)
	if a.Files != 6 || a.FreedBytes != 6000 {
		t.Fatalf("want the 6 oldest entries (6000 bytes) gone, got %d files, %d bytes: %s", a.Files, a.FreedBytes, a.Line())
	}
	for i := range 10 {
		p := filepath.Join(dir, "0"+string(rune('a'+i%6)), "entry"+string(rune('0'+i))+"-d")
		if want := i >= 6; exists(p) != want {
			t.Errorf("entry %d: exists %v, want %v", i, exists(p), want)
		}
	}
	if !exists(filepath.Join(dir, "README")) || !exists(filepath.Join(dir, "trim.txt")) {
		t.Error("the cache's own files went")
	}
}

// A fake GOCACHE under the cap is left alone.
func TestGoCacheUnderTheCapIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().AddDate(0, -1, 0)
	writeFile(t, filepath.Join(dir, "ab", "x-a"), 1000, old)
	writeFile(t, filepath.Join(dir, "cd", "y-d"), 1000, old)
	a := GoCache(dir, 4096, false)
	if a.Files != 0 || a.FreedBytes != 0 || !exists(filepath.Join(dir, "ab", "x-a")) || !exists(filepath.Join(dir, "cd", "y-d")) {
		t.Fatalf("a cache under the cap lost files: %s", a.Line())
	}
	if !strings.Contains(a.Note, "under the cap") {
		t.Errorf("note %q doesn't say it is under the cap", a.Note)
	}
}

// A dry run counts what would go and deletes nothing.
func TestGoCacheDryRunDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(dir, "ab", "old-a"), 1000, now.Add(-48*time.Hour))
	writeFile(t, filepath.Join(dir, "ab", "new-a"), 1000, now)
	a := GoCache(dir, 1500, true)
	if a.Files != 1 || !exists(filepath.Join(dir, "ab", "old-a")) {
		t.Fatalf("dry run: %s; old entry exists %v", a.Line(), exists(filepath.Join(dir, "ab", "old-a")))
	}
}

// Transcripts older than N days go; newer ones, other files and anything
// under memory/ stay.
func TestTranscriptsOlderThanNDaysGo(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, "claude", "projects")
	codex := filepath.Join(root, "codex", "sessions")
	now := time.Date(2026, 11, 17, 4, 0, 0, 0, time.Local)
	old := now.AddDate(0, 0, -20)
	edge := time.Date(2026, 11, 3, 0, 30, 0, 0, time.Local) // 14 days back: inside the window
	writeFile(t, filepath.Join(claude, "proj-a", "old.jsonl"), 100, old)
	writeFile(t, filepath.Join(claude, "proj-a", "edge.jsonl"), 100, edge)
	writeFile(t, filepath.Join(claude, "proj-a", "new.jsonl"), 100, now.Add(-time.Hour))
	writeFile(t, filepath.Join(claude, "proj-a", "notes.md"), 100, old)
	writeFile(t, filepath.Join(claude, "proj-a", "memory", "MEMORY.md"), 100, old)
	writeFile(t, filepath.Join(claude, "proj-a", "memory", "log.jsonl"), 100, old)
	writeFile(t, filepath.Join(codex, "2026", "10", "28", "rollout-1.jsonl"), 300, old)
	a := Transcripts([]string{claude, codex, filepath.Join(root, "missing")}, 14, now, false)
	if a.Files != 2 || a.FreedBytes != 400 {
		t.Fatalf("want 2 transcripts (400 bytes) gone, got %s", a.Line())
	}
	for _, p := range []string{"proj-a/edge.jsonl", "proj-a/new.jsonl", "proj-a/notes.md", "proj-a/memory/MEMORY.md", "proj-a/memory/log.jsonl"} {
		if !exists(filepath.Join(claude, p)) {
			t.Errorf("%s went", p)
		}
	}
	if exists(filepath.Join(claude, "proj-a", "old.jsonl")) {
		t.Error("the old transcript stayed")
	}
	if exists(filepath.Join(codex, "2026")) {
		t.Error("codex's emptied date folders stayed")
	}
	if !exists(codex) {
		t.Error("the sessions root went")
	}
}

func snapName(at time.Time) string { return at.Format(SnapshotLayout) + ".json" }

// Snapshot thinning keeps the last 7 days in full and the newest of each
// older day; a name that doesn't parse stays.
func TestSnapshotThinningKeepsAWeekAndOnePerOlderDay(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 17, 4, 0, 0, 0, time.Local)
	var all []string
	for day := 0; day < 10; day++ {
		for _, hour := range []int{1, 9, 17} {
			at := time.Date(2026, 11, 17-day, hour, 5, 0, 0, time.Local)
			if at.After(now) {
				continue
			}
			writeFile(t, filepath.Join(dir, snapName(at)), 100, at)
			all = append(all, snapName(at))
		}
	}
	writeFile(t, filepath.Join(dir, "notes.json"), 10, now.AddDate(0, 0, -30))
	a := Snapshots("snapshots x", dir, 7, now, false)
	// Days 11-11 to 11-17 (7 days) keep all; 11-08, 11-09, 11-10 keep one each.
	if a.Files != 6 {
		t.Fatalf("want 6 snapshots gone (2 of each of 3 older days), got %s", a.Line())
	}
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	for _, n := range all {
		at, _ := time.ParseInLocation(SnapshotLayout, strings.TrimSuffix(n, ".json"), time.Local)
		inWeek := !at.Before(time.Date(2026, 11, 11, 0, 0, 0, 0, time.Local))
		newestOfDay := at.Hour() == 17
		if want := inWeek || newestOfDay; exists(filepath.Join(dir, n)) != want {
			t.Errorf("%s: exists %v, want %v (left: %v)", n, !want, want, left)
		}
	}
	if !exists(filepath.Join(dir, "notes.json")) {
		t.Error("a file whose name doesn't parse went")
	}
}

// GoBusy names a go build, a go test, a test binary or a compiler, and
// nothing else.
func TestGoBusyNamesBuildsAndTests(t *testing.T) {
	cases := map[string]bool{
		"/usr/local/go/bin/go test ./...":                            true,
		"go build -o issue-cli ./cmd/issue-cli":                      true,
		"/private/var/folders/x/T/go-build1/b001/clean.test -test.v": true,
		"/usr/local/go/pkg/tool/darwin_arm64/compile -o /tmp/x.a":    true,
		"/Users/me/go/bin/gopls serve":                               false,
		"go env GOCACHE":                                             false,
		"/bin/zsh -c make validate":                                  false,
		"/opt/homebrew/bin/link --help":                              false,
	}
	for cmd, busy := range cases {
		got := GoBusy([]procs.Proc{{PID: 42, Command: cmd}})
		if (got != "") != busy {
			t.Errorf("%q: GoBusy %q, want busy %v", cmd, got, busy)
		}
	}
}

// Due: nightly once per slot, early while the volume is over 85% and the
// last clean is 6 hours old.
func TestDue(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 30, 0, 0, time.Local)
	low := diskuse.Volume{UsedPct: 50, OK: true}
	full := diskuse.Volume{UsedPct: 93, OK: true}
	due := func(f File, vol diskuse.Volume, at time.Time) (bool, string) {
		p, ok := Due(f, 4, 0, vol, Cache{}, at)
		if p.CacheOnly {
			t.Errorf("a full clean planned cache-only: %+v", p)
		}
		return ok, p.Why
	}
	if ok, why := due(File{}, low, now); !ok || why != "nightly" {
		t.Errorf("no clean yet: %v %q", ok, why)
	}
	done := File{Results: []Result{{At: time.Date(2026, 11, 17, 4, 1, 0, 0, time.Local)}}}
	if ok, _ := due(done, low, now); ok {
		t.Error("due again after tonight's clean")
	}
	if ok, why := due(done, full, now); !ok || !strings.Contains(why, "93%") {
		t.Errorf("a full volume 6 h after the clean: %v %q", ok, why)
	}
	if ok, _ := due(done, full, time.Date(2026, 11, 17, 8, 0, 0, 0, time.Local)); ok {
		t.Error("a full volume restarted the clean within 6 hours")
	}
}

// After tonight's clean the gocache area alone is due: while it waits, and
// in the day once a reading taken after it last ran has it over its cap by
// more than 25% (34 GB → 59.7 GB in a day over a 40 GB cap, 2026-11-21).
func TestDueCacheOnly(t *testing.T) {
	low := diskuse.Volume{UsedPct: 50, OK: true}
	night := time.Date(2026, 11, 21, 4, 1, 0, 0, time.Local)
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.Local)
	gb := int64(1 << 30)
	over := Cache{At: now.Add(-30 * time.Minute), Bytes: 51 * gb, CapBytes: 40 * gb, OK: true}
	done := File{Results: []Result{{At: night}}, CacheAt: night}
	if p, ok := Due(done, 4, 0, low, over, now); !ok || !p.CacheOnly || !strings.Contains(p.Why, "over its cap of 40.0 GB by more than 25%") {
		t.Errorf("51 GB over a 40 GB cap: %v %+v", ok, p)
	}
	if _, ok := Due(done, 4, 0, low, Cache{At: over.At, Bytes: 49 * gb, CapBytes: 40 * gb, OK: true}, now); ok {
		t.Error("49 GB (under 25% over a 40 GB cap) is due in the day")
	}
	trimmed := done
	trimmed.CacheAt = now.Add(-10 * time.Minute)
	if _, ok := Due(trimmed, 4, 0, low, over, now); ok {
		t.Error("a reading from before the last trim made the cache due again")
	}
	waits := done
	waits.CacheWaits = &Hold{At: now, Since: night, Why: "go test is running (pid 9)"}
	if p, ok := Due(waits, 4, 0, low, Cache{}, now); !ok || !p.CacheOnly || !strings.Contains(p.Why, "waits since 21 Nov 04:01") {
		t.Errorf("a waiting cache: %v %+v", ok, p)
	}
	if p, _ := Due(File{CacheWaits: waits.CacheWaits}, 4, 0, low, over, now); p.CacheOnly {
		t.Error("the nightly clean lost to a cache-only one")
	}
}

// The gocache area's wait: Record of a result whose cache waited starts
// CacheWaits and keeps the rest's result; a cache-only wait keeps Since;
// the cache running clears it. A whole-clean hold keeps its Since too.
func TestStateFileCacheWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine", "clean.json")
	t0 := time.Date(2026, 11, 21, 4, 0, 0, 0, time.Local)
	if err := Record(path, Result{At: t0, Areas: []Area{Waits(CacheArea, "go test is running (pid 9)"), {Name: "transcripts"}}}); err != nil {
		t.Fatal(err)
	}
	f := Load(path)
	if f.CacheWaits == nil || f.CacheWaits.Why != "go test is running (pid 9)" || len(f.Results) != 1 {
		t.Fatalf("after a nightly whose cache waited: %+v", f)
	}
	_ = RecordCacheWait(path, Hold{At: t0.Add(4 * time.Hour), Why: "go build is running (pid 3)"})
	f = Load(path)
	if w, ok := Waiting(f); !ok || !w.From().Equal(t0) || w.Why != "go build is running (pid 3)" {
		t.Errorf("Waiting after a second try: %+v %v", w, ok)
	}
	if s := Status(f); !strings.Contains(s, "the Go cache waits since 21 Nov 04:00: go build") {
		t.Errorf("Status: %q", s)
	}
	_ = Record(path, Result{At: t0.Add(5 * time.Hour), Areas: []Area{{Name: CacheArea}}})
	f = Load(path)
	if _, ok := Waiting(f); ok || !f.CacheAt.Equal(t0.Add(5*time.Hour)) {
		t.Errorf("the cache ran but still waits: %+v", f)
	}
	t1 := t0.AddDate(0, 0, 1)
	_ = RecordHold(path, Hold{At: t1, Why: "mem-guard says no"})
	_ = RecordHold(path, Hold{At: t1.Add(time.Hour), Why: "mem-guard says no"})
	if w, ok := Waiting(Load(path)); !ok || !w.From().Equal(t1) {
		t.Errorf("a standing hold lost its Since: %+v", w)
	}
}

// The state file keeps the hold until a result, and Status says which.
func TestStateFileHoldThenResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine", "clean.json")
	at := time.Date(2026, 11, 17, 4, 0, 0, 0, time.Local)
	if err := RecordHold(path, Hold{At: at, Why: "2 workers live"}); err != nil {
		t.Fatal(err)
	}
	if s := Status(Load(path)); !strings.Contains(s, "waits") || !strings.Contains(s, "2 workers live") {
		t.Errorf("status after a hold: %q", s)
	}
	r := Result{At: at.Add(time.Hour), Areas: []Area{{Name: "gocache", FreedBytes: 3 << 30}}, Before: diskuse.Volume{UsedPct: 93, OK: true}, After: diskuse.Volume{UsedPct: 71, OK: true}}
	if err := Record(path, r); err != nil {
		t.Fatal(err)
	}
	if s := Status(Load(path)); s != "the last clean (17 Nov 05:00) freed 3.0 GB, the volume 93% → 71% used" {
		t.Errorf("status after a result: %q", s)
	}
	release, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path); err == nil {
		t.Error("a second lock was taken while the first is held")
	}
	release()
	if r2, err := Lock(path); err != nil {
		t.Errorf("the lock wasn't free after release: %v", err)
	} else {
		r2()
	}
}
