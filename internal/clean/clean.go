// Package clean is the nightly clean's areas (issue-cli clean): the Go
// build cache down to a cap, agent transcripts older than N days, health
// snapshots thinned to one a day, each a function over directories and a
// clock, so a test runs it on a temp dir. What holds a clean (a worker, a
// local CI run, a go build or test running) is read by the caller; GoBusy
// reads the process list's share of it. The results are kept in a small
// state file (Path) the pages read.
//
// On 2026-11-17 the data volume reached 93%: the cache held 263 GB, and Go
// trims only what went unused for 5 days. The owner asked for regular
// cleaning (factory/a-nightly-clean-job-keeps-the-disk-down-go-cache-cap-
// transcripts-over-14-days-health-snapshots-merged-worktrees-and-playtest-runs).
package clean

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/period"
	"github.com/kacperwerema/dark-factory/internal/procs"
)

// Area is one part of a clean: what went and what it freed.
type Area struct {
	// Name is the area ("gocache", "transcripts", "snapshots <project>").
	Name string `json:"name"`
	// Files is how many files went (or would, on a dry run).
	Files      int   `json:"files"`
	FreedBytes int64 `json:"freed_bytes"`
	// Note is the rest of the line: sizes, the cap, why nothing went.
	Note string `json:"note,omitempty"`
	Err  string `json:"err,omitempty"`
}

// Line is the area as one report line.
func (a Area) Line() string {
	s := fmt.Sprintf("%-28s freed %9s", a.Name, MB(a.FreedBytes))
	if a.Files > 0 {
		s += fmt.Sprintf("  %d files", a.Files)
	}
	if a.Note != "" {
		s += "  " + a.Note
	}
	if a.Err != "" {
		s += "  error: " + a.Err
	}
	return s
}

// MB is a byte count in MB or GB, as the report prints it.
func MB(b int64) string {
	m := float64(b) / (1 << 20)
	if m >= 1024 {
		return fmt.Sprintf("%.1f GB", m/1024)
	}
	return fmt.Sprintf("%.1f MB", m)
}

// GoCache trims Go's build cache at dir to capBytes: when the entries sum
// to more, the oldest go (by mtime: Go refreshes an entry's mtime when a
// build uses it, and its own trim reads the same) until the rest is at or
// under the cap. Only files in the cache's two-hex-character entry folders
// count and go; README, trim.txt and the folders stay. Under the cap
// nothing goes. dry reports without deleting.
func GoCache(dir string, capBytes int64, dry bool) Area {
	a := Area{Name: "gocache"}
	if dir == "" {
		a.Note = "no cache (GOCACHE=off)"
		return a
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		a.Note = "no cache folder"
		return a
	}
	// Two passes keep memory flat over a million entries: sum the bytes
	// per hour of mtime, pick the hour from which on the newest fit under
	// the cap, then delete what is older.
	perHour := map[int64]int64{}
	var total int64
	entries := func(visit func(path string, size int64, hour int64)) {
		subs, _ := os.ReadDir(dir)
		for _, s := range subs {
			if !s.IsDir() || !isEntryDir(s.Name()) {
				continue
			}
			sub := filepath.Join(dir, s.Name())
			files, _ := os.ReadDir(sub)
			for _, f := range files {
				if f.IsDir() {
					continue
				}
				info, err := f.Info()
				if err != nil {
					continue
				}
				visit(filepath.Join(sub, f.Name()), info.Size(), info.ModTime().Unix()/3600)
			}
		}
	}
	entries(func(_ string, size, hour int64) {
		perHour[hour] += size
		total += size
	})
	if total <= capBytes {
		a.Note = fmt.Sprintf("%s, under the cap of %s", MB(total), MB(capBytes))
		return a
	}
	hours := make([]int64, 0, len(perHour))
	for h := range perHour {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] > hours[j] })
	// keepFrom: the oldest hour whose files still fit; everything before
	// it goes.
	var kept int64
	keepFrom := hours[0] + 1
	for _, h := range hours {
		if kept+perHour[h] > capBytes {
			break
		}
		kept += perHour[h]
		keepFrom = h
	}
	entries(func(path string, size, hour int64) {
		if hour >= keepFrom {
			return
		}
		if !dry {
			if err := os.Remove(path); err != nil {
				return
			}
		}
		a.Files++
		a.FreedBytes += size
	})
	a.Note = fmt.Sprintf("%s → %s, cap %s", MB(total), MB(total-a.FreedBytes), MB(capBytes))
	return a
}

// isEntryDir is a cache entry folder's name: two lowercase hex digits.
func isEntryDir(name string) bool {
	if len(name) != 2 {
		return false
	}
	for _, c := range name {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Transcripts deletes the *.jsonl files under dirs whose mtime is before
// the window of days calendar days ending now (period.DaysBack): the agent
// CLIs' transcripts. A folder named memory is never entered, other files
// never go, and a file written inside the window stays (a live session
// writes its file all the time). A folder a transcript went from is
// removed when that left it empty; the roots stay.
func Transcripts(dirs []string, days int, now time.Time, dry bool) Area {
	a := Area{Name: "transcripts"}
	cutoff := period.DaysBack(now, days)
	for _, root := range dirs {
		root = filepath.Clean(root)
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			continue
		}
		touched := map[string]bool{}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != root && d.Name() == "memory" {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			info, err := d.Info()
			if err != nil || !info.ModTime().Before(cutoff) {
				return nil
			}
			if !dry {
				if err := os.Remove(p); err != nil {
					return nil
				}
				for d := filepath.Dir(p); d != root && strings.HasPrefix(d, root); d = filepath.Dir(d) {
					touched[d] = true
				}
			}
			a.Files++
			a.FreedBytes += info.Size()
			return nil
		})
		if dry {
			continue
		}
		// The folders a transcript went from, deepest first, so a folder
		// whose only child was an emptied folder goes too; Remove fails on
		// one that isn't empty.
		emptied := make([]string, 0, len(touched))
		for d := range touched {
			emptied = append(emptied, d)
		}
		sort.Slice(emptied, func(i, j int) bool { return len(emptied[i]) > len(emptied[j]) })
		for _, d := range emptied {
			_ = os.Remove(d)
		}
	}
	a.Note = fmt.Sprintf("older than %d days", days)
	return a
}

// SnapshotLayout is a health snapshot file's name (internal/health).
const SnapshotLayout = "2006-01-02T1504"

// Snapshots thins the health snapshots in dir: every one from the last
// days calendar days (today counts) stays, and of each older day only the
// newest. A file whose name doesn't parse stays. name labels the area.
func Snapshots(name, dir string, days int, now time.Time, dry bool) Area {
	a := Area{Name: name}
	files, err := os.ReadDir(dir)
	if err != nil {
		a.Note = "no snapshots"
		return a
	}
	keepFrom := period.DaysBack(now, days-1)
	type snap struct {
		path string
		at   time.Time
		size int64
	}
	byDay := map[string][]snap{}
	total := 0
	for _, f := range files {
		base, ok := strings.CutSuffix(f.Name(), ".json")
		if f.IsDir() || !ok {
			continue
		}
		at, err := time.ParseInLocation(SnapshotLayout, base, now.Location())
		if err != nil {
			continue
		}
		total++
		if !at.Before(keepFrom) {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		day := at.Format("2006-01-02")
		byDay[day] = append(byDay[day], snap{filepath.Join(dir, f.Name()), at, info.Size()})
	}
	for _, snaps := range byDay {
		sort.Slice(snaps, func(i, j int) bool { return snaps[i].at.After(snaps[j].at) })
		for _, s := range snaps[1:] {
			if !dry {
				if err := os.Remove(s.path); err != nil {
					continue
				}
			}
			a.Files++
			a.FreedBytes += s.size
		}
	}
	a.Note = fmt.Sprintf("%d → %d snapshots", total, total-a.Files)
	return a
}

// goVerbs are the go subcommands that read or write the build cache.
var goVerbs = map[string]bool{"build": true, "test": true, "vet": true, "run": true, "install": true, "generate": true}

// goTools are the toolchain's programs under pkg/tool that a build runs.
var goTools = map[string]bool{"compile": true, "link": true, "asm": true, "cgo": true, "vet": true, "cover": true}

// GoBusy names the first process in ps that builds or tests Go: a go
// build, test, vet, run, install or generate, a compiler or linker under
// the toolchain's pkg/tool, or a *.test binary. "" when none.
func GoBusy(ps []procs.Proc) string {
	for _, p := range ps {
		f := strings.Fields(p.Command)
		if len(f) == 0 {
			continue
		}
		base := filepath.Base(f[0])
		switch {
		case base == "go" && len(f) > 1 && goVerbs[f[1]]:
			return fmt.Sprintf("go %s is running (pid %d)", f[1], p.PID)
		case strings.HasSuffix(base, ".test"):
			return fmt.Sprintf("a go test binary is running (%s, pid %d)", base, p.PID)
		case goTools[base] && strings.Contains(f[0], "/pkg/tool/"):
			return fmt.Sprintf("a go build is running (%s, pid %d)", base, p.PID)
		}
	}
	return ""
}
