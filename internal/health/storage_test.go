package health

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
