package ci

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Kept red logs: a red run's log is copied to .ci/red/ when the run ends, so
// it outlives the next green run (a re-run of the same head writes
// .ci/runs/<sha>.log again) and the 50-run prune of .ci/runs. They go by
// count and by age; the newest first red after a green stays past the count.

const (
	// keepRedLogs is how many kept red logs .ci/red holds.
	keepRedLogs = 10
	// maxRedLogAge is how long a kept red log stays at most.
	maxRedLogAge = 30 * 24 * time.Hour
)

// redStamp is the time in a kept log's name: UTC, so names sort by time.
const redStamp = "20060102T150405Z"

// redName is <stamp>-<sha>[.<tier>][.first].log.
var redName = regexp.MustCompile(`^(\d{8}T\d{6}Z)-([0-9a-f]{4,64})((?:\.[A-Za-z0-9_-]+)*)\.log$`)

// RedDir holds the kept red logs.
func RedDir(root string) string { return filepath.Join(Dir(root), "red") }

// RedLog is one kept red log.
type RedLog struct {
	// Name is the file name in RedDir; Path is it relative to the root.
	Name string
	Path string
	Sha  string
	Tier string
	At   time.Time
	// First marks the first red run after a green one.
	First bool
}

// keepRedLog copies run's log into .ci/red and returns the copy's path
// relative to root.
func keepRedLog(root string, run *Run, first bool) (string, error) {
	if err := os.MkdirAll(RedDir(root), 0o755); err != nil {
		return "", err
	}
	name := run.Finished.UTC().Format(redStamp) + "-" + runName(run.Sha, run.Tier)
	if first {
		name += ".first"
	}
	name += ".log"
	src, err := os.Open(filepath.Join(root, run.Log))
	if err != nil {
		return "", err
	}
	defer src.Close()
	dst := filepath.Join(RedDir(root), name)
	tmp, err := os.CreateTemp(RedDir(root), name+".tmp*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return filepath.Join(".ci", "red", name), nil
}

// ParseRedLogName reads a kept log's file name; ok is false for any other
// name (a temp file, a stray file, a path).
func ParseRedLogName(name string) (RedLog, bool) {
	m := redName.FindStringSubmatch(name)
	if m == nil {
		return RedLog{}, false
	}
	at, err := time.Parse(redStamp, m[1])
	if err != nil {
		return RedLog{}, false
	}
	rest, first := strings.CutSuffix(m[3], ".first")
	tier := strings.TrimPrefix(rest, ".")
	if tier == "" {
		tier = defaultTier
	}
	if strings.Contains(tier, ".") {
		return RedLog{}, false
	}
	return RedLog{Name: name, Path: filepath.Join(".ci", "red", name), Sha: m[2], Tier: tier, At: at, First: first}, true
}

// RedLogs lists the kept red logs, newest first.
func RedLogs(root string) []RedLog {
	entries, _ := os.ReadDir(RedDir(root))
	var logs []RedLog
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if l, ok := ParseRedLogName(e.Name()); ok {
			logs = append(logs, l)
		}
	}
	sort.Slice(logs, func(i, j int) bool {
		if !logs[i].At.Equal(logs[j].At) {
			return logs[i].At.After(logs[j].At)
		}
		return logs[i].Name > logs[j].Name
	})
	return logs
}

// pruneRedLogs drops kept logs older than maxRedLogAge, and past the newest
// keepRedLogs all but the newest first red. Leftover temp files go too.
func pruneRedLogs(root string, now time.Time) {
	firstKept := false
	n := 0
	for _, l := range RedLogs(root) {
		keep := now.Sub(l.At) < maxRedLogAge
		if keep {
			n++
			keep = n <= keepRedLogs || (l.First && !firstKept)
		}
		if l.First && keep {
			firstKept = true
		}
		if !keep {
			os.Remove(filepath.Join(RedDir(root), l.Name))
		}
	}
	entries, _ := os.ReadDir(RedDir(root))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > time.Hour {
				os.Remove(filepath.Join(RedDir(root), e.Name()))
			}
		}
	}
}

// LastRedLogPath is the newest kept red log's path relative to root, "" when
// there is none or it was pruned.
func (s *State) LastRedLogPath(root string) string {
	if s == nil || s.LastRedLog == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(root, s.LastRedLog)); err != nil {
		return ""
	}
	return s.LastRedLog
}

// LastRedLine is "Last red log: <path> (<sha>[ tier <t>], <ago> ago)" for
// ci status; "" without a kept red log.
func (s *State) LastRedLine(root string, now time.Time) string {
	rel := s.LastRedLogPath(root)
	if rel == "" {
		return ""
	}
	what := Short(s.LastRedSha)
	if s.LastRedTier != "" && s.LastRedTier != defaultTier {
		what += " tier " + s.LastRedTier
	}
	return "Last red log: " + filepath.Join(root, rel) + " (" + what + ", " + Ago(now.Sub(s.LastRedAt)) + " ago)"
}
