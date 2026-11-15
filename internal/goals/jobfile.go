package goals

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Metric is one value a job's run read.
type Metric struct {
	ID    string  `json:"id"`
	Value float64 `json:"value"`
}

// jobFile is the metrics file a job writes, in the metric run format.
type jobFile struct {
	Date    string   `json:"date"`
	Job     string   `json:"job,omitempty"`
	Metrics []Metric `json:"metrics"`
}

// WriteMetrics replaces job r's metrics file with metrics dated date
// (YYYY-MM-DD) and returns its path. It writes a temporary file beside it,
// syncs it and renames it into place, so a reader sees the old file or the
// new one, never half of one. A metric the run didn't read is left out.
func WriteMetrics(p *tracker.Project, r *jobs.Ref, date string, metrics []Metric) (string, error) {
	if len(metrics) == 0 {
		return "", fmt.Errorf("no metrics to write")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", fmt.Errorf("date %q: want YYYY-MM-DD", date)
	}
	seen := map[string]bool{}
	for _, m := range metrics {
		if strings.TrimSpace(m.ID) == "" {
			return "", fmt.Errorf("a metric has no id")
		}
		if seen[m.ID] {
			return "", fmt.Errorf("metric %q appears twice", m.ID)
		}
		seen[m.ID] = true
	}
	path, err := ResolvePath(p, JobFileSource(r.Department, r.Job.ID))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := jsonfile.Write(path, jobFile{Date: date, Job: r.Key, Metrics: metrics}, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
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
