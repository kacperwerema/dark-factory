package goals

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// IssuesPrefix starts a file: source path that is read from the project's
// issues folder (the work files, the private tracker when there is one)
// instead of the project root: file:<issues>/.goals/sales/weekly-funnel.json#visits.
const IssuesPrefix = "<issues>/"

// JobFileDir is the folder, under the issues folder, holding the metrics
// files recurring jobs write: .goals/<dept key>/<job id>.json.
const JobFileDir = ".goals"

// ResolvePath is a file: source path on disk: a path starting with
// <issues>/ under the issues folder (refused when it climbs out of it), an
// absolute path as is, anything else under the project root.
func ResolvePath(p *tracker.Project, path string) (string, error) {
	if rest, ok := strings.CutPrefix(path, IssuesPrefix); ok {
		base := filepath.Clean(p.IssueDir)
		full := filepath.Join(base, filepath.FromSlash(rest))
		if full == base || !strings.HasPrefix(full, base+string(filepath.Separator)) {
			return "", fmt.Errorf("%s climbs out of the issues folder", path)
		}
		return full, nil
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Join(p.Root(), path), nil
}

// JobFileSource is the path a goal's file: source names for job dept/id's
// metrics file: <issues>/.goals/<dept key>/<job id>.json.
func JobFileSource(dept, id string) string {
	return IssuesPrefix + JobFileDir + "/" + tracker.DepartmentKey(dept) + "/" + strings.TrimSpace(id) + ".json"
}

// JobKeyOf is the job key (<dept key>/<job id>) a file: source path names
// when it has the job file's shape (<issues>/.goals/<dept>/<id>.json);
// false for any other path. Whether the job exists is JobOf's question.
func JobKeyOf(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, IssuesPrefix+JobFileDir+"/")
	if !ok {
		return "", false
	}
	dept, file, ok := strings.Cut(rest, "/")
	id, isJSON := strings.CutSuffix(file, ".json")
	if !ok || !isJSON || dept == "" || id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return dept + "/" + id, true
}

// JobOf is the job writing the metrics file at path (a file: source path),
// nil when the path doesn't have the job file's shape or no such job is
// configured.
func JobOf(p *tracker.Project, path string) *jobs.Ref {
	key, ok := JobKeyOf(path)
	if !ok {
		return nil
	}
	r, err := jobs.Find(p, key)
	if err != nil {
		return nil
	}
	return r
}

// Metric is one value a job's run read, with the day it is for.
type Metric struct {
	ID    string  `json:"id"`
	Value float64 `json:"value"`
	// Date is the day the value is for (YYYY-MM-DD): the write's day for
	// the metrics it set, a kept metric's own day on a merge. A file
	// written before metrics carried a date has none; the file's date
	// stands in.
	Date string `json:"date,omitempty"`
}

// jobFile is the metrics file a job writes, in the metric run format.
type jobFile struct {
	Date string `json:"date"`
	// Written is the day goal write wrote the file (the clock's day, not
	// --date): the run stamp a gate compares with today.
	Written string   `json:"written,omitempty"`
	Job     string   `json:"job,omitempty"`
	Metrics []Metric `json:"metrics"`
}

// Write is one goal write: the metrics a run read, the day they are for,
// the day it runs, and whether to keep the file's other metrics.
type Write struct {
	Date    string   // the day the numbers are for (YYYY-MM-DD)
	Today   string   // the day the write runs (YYYY-MM-DD), the run stamp
	Metrics []Metric // the metrics this write sets
	Merge   bool     // keep the file's metrics this write doesn't name
}

// Written is what a write left in the job's file.
type Written struct {
	Path string
	// Kept are the metrics a merge kept from the file, with their own
	// dates; Dropped the ids a whole write left out that the old file had.
	Kept    []Metric
	Dropped []string
}

// lockWait is how long a write waits for another write of the same file.
const lockWait = 10 * time.Second

// WriteMetrics writes job r's metrics file: whole (w.Merge false: a
// metric the run didn't read is left out) or merged (the file's other
// metrics kept with their own dates). It holds a lock on the file's folder
// while it reads
// and writes, and writes a temporary file beside it, syncs it and renames
// it into place, so a reader sees the old file or the new one, never half
// of one. A merge into a file it can't read refuses rather than drop what
// is there.
func WriteMetrics(p *tracker.Project, r *jobs.Ref, w Write) (*Written, error) {
	if len(w.Metrics) == 0 {
		return nil, fmt.Errorf("no metrics to write")
	}
	for _, d := range []string{w.Date, w.Today} {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return nil, fmt.Errorf("date %q: want YYYY-MM-DD", d)
		}
	}
	seen := map[string]bool{}
	set := make([]Metric, 0, len(w.Metrics))
	for _, m := range w.Metrics {
		if strings.TrimSpace(m.ID) == "" {
			return nil, fmt.Errorf("a metric has no id")
		}
		if seen[m.ID] {
			return nil, fmt.Errorf("metric %q appears twice", m.ID)
		}
		seen[m.ID] = true
		m.Date = w.Date
		set = append(set, m)
	}
	path, err := ResolvePath(p, JobFileSource(r.Department, r.Job.ID))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	unlock, err := lockDir(filepath.Dir(path), lockWait)
	if err != nil {
		return nil, err
	}
	defer unlock()
	old, err := readJobFile(path)
	if err != nil && w.Merge {
		return nil, fmt.Errorf("%v: --merge keeps what the file holds, so it can't write over a file it can't read; rerun without --merge to replace it", err)
	}
	res := &Written{Path: path}
	if old != nil {
		for _, m := range old.Metrics {
			if seen[m.ID] {
				continue
			}
			if !w.Merge {
				res.Dropped = append(res.Dropped, m.ID)
				continue
			}
			if m.Date == "" {
				m.Date = old.Date
			}
			res.Kept = append(res.Kept, m)
		}
	}
	all := append(set, res.Kept...)
	if err := jsonfile.Write(path, jobFile{Date: w.Date, Written: w.Today, Job: r.Key, Metrics: all}, 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}
	return res, nil
}

// readJobFile is the job file at path; nil, nil when there is none.
func readJobFile(path string) (*jobFile, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %v", path, err)
	}
	var f jobFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s doesn't parse as a job's metrics file (%v)", path, err)
	}
	for _, m := range f.Metrics {
		if strings.TrimSpace(m.ID) == "" {
			return nil, fmt.Errorf("%s has a metric with no id", path)
		}
	}
	return &f, nil
}

// lockDir takes an exclusive flock on folder dir, waiting up to timeout
// for another holder. Locking the folder, not a lock file beside the job
// file, leaves nothing behind in the issues folder for a tracker to commit.
func lockDir(dir string, timeout time.Duration) (func(), error) {
	path := dir
	f, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("locking %s: %v", path, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("locking %s: another goal write holds it (%v)", path, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// parseDay is a metrics file's date (a day or RFC3339) as its day in loc.
func parseDay(date string, loc *time.Location) (time.Time, bool) {
	date = strings.TrimSpace(date)
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, date, loc); err == nil {
			t = t.In(loc)
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc), true
		}
	}
	return time.Time{}, false
}

// missedRun reports whether a value dated date, written by job r, is from
// before the run that should have replaced it was due: a whole run missed.
// A job whose schedule doesn't parse, or a date that doesn't, is never late.
func missedRun(r *jobs.Ref, date string, now time.Time) bool {
	if r == nil || r.Err != nil {
		return false
	}
	day, ok := parseDay(date, now.Location())
	if !ok {
		return false
	}
	prev := r.Schedule.PrevDue(now)
	return day.Before(time.Date(prev.Year(), prev.Month(), prev.Day(), 0, 0, 0, 0, now.Location()))
}

// writtenBy is what a missing job file's message adds: the job that
// writes it and when it next runs.
func writtenBy(r *jobs.Ref, now time.Time) string {
	s := fmt.Sprintf("job %s writes it with issue-cli goal write %s", r.Key, r.Key)
	if r.Err == nil {
		s += "; next run " + r.Schedule.Next(now).Format("Mon 2006-01-02 15:04")
	}
	return s
}
