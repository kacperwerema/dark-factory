package health

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The storage family: how many MB the project takes on disk, by area, so
// the Stats page shows the growth of worktrees and playtest shots. One walk
// of the main checkout by a bounded pool of readers, sizes from Lstat
// (symlinks are not followed). It only knows the present: backfill leaves
// it out. See docs/workbench-files.md,
// "The storage family".
func init() {
	Register("storage", storageFamily)
	Describe("storage", "card: /stats (Disk use); tidy shots: issue-cli playtest prune")
}

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
	{"storage.attachments_mb", "attachments"},
}

type storageSizes struct {
	total, git, worktrees, playtest, agentLogs, ci, attachments int64
	worktreeCount                                               int
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
	return []Metric{
		{Name: "storage.total_mb", Value: mb(s.total), Unit: UnitMB, Help: "The project checkout on disk, everything included"},
		{Name: "storage.git_mb", Value: mb(s.git), Unit: UnitMB, Help: "The .git folder"},
		{Name: "storage.worktrees_mb", Value: mb(s.worktrees), Unit: UnitMB, Help: "Worktrees under .worktrees/, without their playtest runs"},
		{Name: "storage.worktrees", Value: float64(s.worktreeCount), Unit: UnitCount, Help: "Worktrees under .worktrees/"},
		{Name: "storage.playtest_mb", Value: mb(s.playtest), Unit: UnitMB, Help: "Playtest runs and shots: .playtest/ in the checkout and in every worktree"},
		{Name: "storage.agent_logs_mb", Value: mb(s.agentLogs), Unit: UnitMB, Help: "The .agent-logs folder (clilogs, dispatch prompts)"},
		{Name: "storage.ci_mb", Value: mb(s.ci), Unit: UnitMB, Help: "Local CI's .ci folder: run records, logs, kept red logs, test results"},
		{Name: "storage.attachments_mb", Value: mb(s.attachments), Unit: UnitMB, Help: "Issue attachments (mockups and their screenshots)"},
	}, nil
}

// storageWorkers bounds the walk's pool. The walk waits on lstat and
// readdir; measured on a 13 GB checkout (800k entries, APFS), 8 readers
// took it from 23 s to about 4 s, and more were slower (the kernel's
// locks), so 8 it is.
var storageWorkers = 8

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
		if strings.HasPrefix(rel, ".worktrees/") && name == ".git" {
			s.worktreeCount++ // a worktree's .git file
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
	s.attachments += o.attachments
	s.worktreeCount += o.worktreeCount
}

func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
