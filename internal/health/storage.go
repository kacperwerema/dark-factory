package health

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kacperwerema/dark-factory/internal/diskuse"
)

// The storage family: how many MB the project takes on disk, by area, so
// the Stats page shows the growth of worktrees and playtest shots. One walk
// of the main checkout by a bounded pool of readers, sizes from Lstat
// (symlinks are not followed). It only knows the present: backfill leaves
// it out. Beside the checkout it counts what the factory fills elsewhere
// on the machine: Go's build cache and the agent transcripts
// (internal/diskuse; one walk an hour, shared by every project). See
// docs/workbench-files.md, "The storage family".
func init() {
	Register("storage", storageFamily)
	// The cache walk takes as long as the family timeout on a busy Mac: a
	// snapshot refreshes the reading first, at most hourly, without one.
	Prepare("storage", func(ctx context.Context, env Env) { _, _ = diskReading(ctx, env.Now) })
	Describe("storage", "card: /stats (Disk use), /health (Disk outside the checkout); tidy shots: issue-cli playtest prune; a big Go cache or a full volume is a Needs you row: "+diskuse.Command)
}

// diskReading is where the family gets the machine's reading: a var for
// tests.
var diskReading = diskuse.Recent

// UnitMB is a size in megabytes (2^20 bytes).
const UnitMB = "MB"

// StorageAreas are the storage family's MB metrics, in the order the Stats
// page lists them, with their labels.
var StorageAreas = []struct{ Metric, Label string }{
	{"storage.worktrees_mb", "worktrees"},
	{"storage.playtest_mb", "playtest shots"},
	{"storage.git_mb", ".git"},
	{"storage.agent_logs_mb", ".agent-logs"},
	{"storage.ci_mb", "local CI (.ci)"},
	{"storage.ci_worktree_mb", "local CI worktree (.worktrees/.ci)"},
	{"storage.attachments_mb", "attachments"},
}

type storageSizes struct {
	total, git, worktrees, playtest, agentLogs, ci, ciWorktree, attachments int64
	worktreeCount                                                           int
}

func storageFamily(ctx context.Context, env Env) ([]Metric, error) {
	if env.Backfill {
		return nil, ErrNoPast
	}
	attachments := ""
	if env.Project != nil && env.Project.IssueDir != "" {
		if abs, err := filepath.Abs(filepath.Join(env.Project.IssueDir, "attachments")); err == nil {
			attachments = abs
		}
	}
	s, err := measureStorage(ctx, env.Root, attachments)
	if err != nil {
		return nil, err
	}
	mb := func(b int64) float64 { return float64(int64(float64(b)/(1<<20)*10+0.5)) / 10 }
	out := []Metric{
		{Name: "storage.total_mb", Value: mb(s.total), Unit: UnitMB, Help: "The project checkout on disk, everything included"},
		{Name: "storage.git_mb", Value: mb(s.git), Unit: UnitMB, Help: "The .git folder"},
		{Name: "storage.worktrees_mb", Value: mb(s.worktrees), Unit: UnitMB, Help: "Worktrees under .worktrees/, without their playtest runs and without local CI's .worktrees/.ci"},
		{Name: "storage.worktrees", Value: float64(s.worktreeCount), Unit: UnitCount, Help: "Worktrees under .worktrees/, local CI's .ci left out"},
		{Name: "storage.ci_worktree_mb", Value: mb(s.ciWorktree), Unit: UnitMB, Help: "Local CI's worktree .worktrees/.ci (the checkout its runs test)"},
		{Name: "storage.playtest_mb", Value: mb(s.playtest), Unit: UnitMB, Help: "Playtest runs and shots: .playtest/ in the checkout and in every worktree"},
		{Name: "storage.agent_logs_mb", Value: mb(s.agentLogs), Unit: UnitMB, Help: "The .agent-logs folder (clilogs, dispatch prompts)"},
		{Name: "storage.ci_mb", Value: mb(s.ci), Unit: UnitMB, Help: "Local CI's .ci folder: run records, logs, kept red logs, test results"},
		{Name: "storage.attachments_mb", Value: mb(s.attachments), Unit: UnitMB, Help: "Issue attachments (mockups and their screenshots)"},
	}
	now := env.Now
	if now.IsZero() {
		now = time.Now()
	}
	if d, ok := machineDisk(ctx, now); ok {
		out = append(out, diskMetrics(d)...)
	}
	return out, nil
}

// machineDisk is the machine's reading for this snapshot: one under an
// hour old from the readings file (the snapshot's Prepare step and the
// viewer's hourly background walk write it), else a walk given half the
// family's time left (the cache is a million files: 20 s on a busy Mac),
// else the newest older reading, with a warning.
func machineDisk(ctx context.Context, now time.Time) (diskuse.Reading, bool) {
	if r, ok := diskuse.Newest(now); ok && now.Sub(r.At) < diskuse.FreshFor {
		return r, true
	}
	walkCtx := ctx
	if dl, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		walkCtx, cancel = context.WithTimeout(ctx, time.Until(dl)/2)
		defer cancel()
	}
	r, err := diskReading(walkCtx, now)
	if err == nil {
		return r, true
	}
	if errors.Is(err, diskuse.ErrOff) {
		return diskuse.Reading{}, false
	}
	if old, ok := diskuse.Newest(now); ok && now.Sub(old.At) < diskuse.StaleAfter {
		Warn(ctx, "outside the checkout: the walk of the Go build cache stopped (%v); the reading is from %s", err, old.At.Format("2 Jan 15:04"))
		return old, true
	}
	Warn(ctx, "outside the checkout: the walk of the Go build cache stopped (%v); the viewer's hourly walk records it", err)
	return diskuse.Reading{}, false
}

// diskMetrics are the machine's reading as metrics: what is missing on this
// machine is left out, never 0.
func diskMetrics(d diskuse.Reading) []Metric {
	var out []Metric
	if d.HasGoCache {
		out = append(out, Metric{Name: "storage.gocache_mb", Value: d.GoCacheMB, Unit: UnitMB,
			Help: "Go's build cache (" + d.GoCacheDir + "), outside the checkout and shared by every worktree; over 100 GB puts a row in Needs you: " + diskuse.Command + " with no worker running"})
		if d.HasNew {
			out = append(out, Metric{Name: "storage.gocache_new_mb", Value: d.GoCacheNewMB, Unit: UnitMB,
				Help: "Go build cache files created in the 24 hours before the reading (birth time; before Go's 5-day trim)"})
		}
	}
	if d.HasTranscripts {
		out = append(out, Metric{Name: "storage.transcripts_mb", Value: d.TranscriptsMB, Unit: UnitMB,
			Help: "Agent transcripts: Claude Code's projects and Codex's sessions folders"})
	}
	if d.Volume.OK {
		out = append(out, Metric{Name: "storage.volume_used_pct", Value: float64(int64(d.Volume.UsedPct*10+0.5)) / 10, Unit: UnitPercent,
			Help: "The data volume's use, as df counts it; over 85% puts a row in Needs you"})
	}
	return out
}

// storageWorkers bounds the walk's pool. The walk waits on lstat and
// readdir; measured on a 13 GB checkout (800k entries, APFS), 8 readers
// took it from 23 s to about 4 s, and more were slower (the kernel's
// locks), so 8 it is.
var storageWorkers = 8

// ciWorktreeRel is local CI's worktree, relative to the checkout: it has
// its own area (storage.ci_worktree_mb), so it doesn't pass for issue trees.
const ciWorktreeRel = ".worktrees/.ci"

// measureStorage walks root once and sorts every file's size into its area.
// attachments is the issues' attachments folder; when it is outside root it
// is walked on its own and left out of the total. Directories are read by a
// bounded pool (storageWorkers); a cancelled ctx stops it.
func measureStorage(ctx context.Context, root, attachments string) (storageSizes, error) {
	root, _ = filepath.Abs(root)
	attachRel := "" // attachments relative to root, when inside it
	if attachments != "" && within(root, attachments) {
		rel, _ := filepath.Rel(root, attachments)
		attachRel = filepath.ToSlash(rel)
	}
	s, err := walkSizes(ctx, root, storageWorkers, func(s *storageSizes, rel, name string, size int64) {
		ciTree := strings.HasPrefix(rel, ciWorktreeRel+"/")
		if strings.HasPrefix(rel, ".worktrees/") && name == ".git" && !ciTree {
			s.worktreeCount++ // a worktree's .git file
		}
		s.total += size
		switch first, _, _ := strings.Cut(rel, "/"); {
		case first == ".git":
			s.git += size
		case ciTree:
			s.ciWorktree += size
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
		if attachRel != "" && (attachRel == "." || rel == attachRel || strings.HasPrefix(rel, attachRel+"/")) {
			s.attachments += size
		}
	})
	if err != nil {
		return s, err
	}
	if attachments != "" && attachRel == "" {
		a, err := walkSizes(ctx, attachments, storageWorkers, func(s *storageSizes, _, _ string, size int64) {
			s.attachments += size
		})
		if err != nil {
			return s, err
		}
		s.attachments += a.attachments
	}
	return s, nil
}

// walkSizes calls file for every entry under root that is not a directory
// (symlinks are not followed; sizes come from Lstat), with its slash path
// relative to root, and returns the sum of what the calls added. Each
// worker adds into its own storageSizes; a directory found while every
// worker is busy is read by the one that found it, so the pool never
// blocks on itself. Unreadable entries are left out.
func walkSizes(ctx context.Context, root string, workers int, file func(s *storageSizes, rel, name string, size int64)) (storageSizes, error) {
	type dir struct{ abs, rel string }
	work := make(chan dir, workers)
	var pending, done sync.WaitGroup
	sums := make([]storageSizes, workers)
	var read func(s *storageSizes, d dir)
	read = func(s *storageSizes, d dir) {
		if ctx.Err() != nil {
			return
		}
		if storageDirHook != nil {
			storageDirHook(d.rel)
		}
		entries, _ := os.ReadDir(d.abs) // unreadable: what was read counts
		for _, e := range entries {
			name := e.Name()
			sub := dir{abs: filepath.Join(d.abs, name), rel: name}
			if d.rel != "" {
				sub.rel = d.rel + "/" + name
			}
			if e.IsDir() {
				pending.Add(1)
				select {
				case work <- sub:
				default:
					read(s, sub)
					pending.Done()
				}
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			file(s, sub.rel, name, info.Size())
		}
	}
	for i := range workers {
		done.Add(1)
		go func(s *storageSizes) {
			defer done.Done()
			for d := range work {
				read(s, d)
				pending.Done()
			}
		}(&sums[i])
	}
	pending.Add(1)
	work <- dir{abs: root}
	pending.Wait()
	close(work)
	done.Wait()
	var total storageSizes
	for _, s := range sums {
		total.add(s)
	}
	return total, ctx.Err()
}

// storageDirHook, when set (tests only), is called with each directory's
// relative path before it is read.
var storageDirHook func(rel string)

func (s *storageSizes) add(o storageSizes) {
	s.total += o.total
	s.git += o.git
	s.worktrees += o.worktrees
	s.playtest += o.playtest
	s.agentLogs += o.agentLogs
	s.ci += o.ci
	s.ciWorktree += o.ciWorktree
	s.attachments += o.attachments
	s.worktreeCount += o.worktreeCount
}

func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
