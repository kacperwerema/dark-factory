package jobs

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Ref is one configured job with its department.
type Ref struct {
	// Key is <dept key>/<job id>: marketing/weekly-seo-pass.
	Key        string
	Department string // the department's name
	Job        tracker.Job
	Schedule   Schedule
	// Err is a schedule that does not parse; the job is skipped.
	Err error
}

// Name is the job's name, else its id.
func (r *Ref) Name() string {
	if strings.TrimSpace(r.Job.Name) != "" {
		return r.Job.Name
	}
	return r.Job.ID
}

// KeyOf is a job's key in department dept.
func KeyOf(dept, id string) string {
	return tracker.DepartmentKey(dept) + "/" + strings.TrimSpace(id)
}

// List is every department's jobs, in projects.yaml order, then the
// Product entry's (key product/<id>: the engine's own jobs, such as
// builtin:nightly-improve). HQ's jobs are not read (lint warns about them).
func List(p *tracker.Project) []*Ref {
	if p == nil {
		return nil
	}
	var out []*Ref
	add := func(dept string, js []tracker.Job) {
		for _, j := range js {
			r := &Ref{Key: KeyOf(dept, j.ID), Department: dept, Job: j}
			r.Schedule, r.Err = Parse(j)
			out = append(out, r)
		}
	}
	for _, d := range p.Departments {
		add(d.Name, d.Jobs)
	}
	if p.Product != nil {
		add(tracker.ProductName, p.Product.Jobs)
	}
	return out
}

// IsProduct reports a job on the Product entry: its runs are Product issues.
func (r *Ref) IsProduct() bool { return strings.EqualFold(r.Department, tracker.ProductName) }

// Find is the job with key (dept/id, any case), or an error listing the
// jobs.
func Find(p *tracker.Project, key string) (*Ref, error) {
	all := List(p)
	want := strings.ToLower(strings.TrimSpace(key))
	var keys []string
	for _, r := range all {
		if strings.ToLower(r.Key) == want {
			return r, nil
		}
		keys = append(keys, r.Key)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no job %q: this project has no recurring jobs", key)
	}
	return nil, fmt.Errorf("no job %q (jobs: %s)", key, strings.Join(keys, ", "))
}

// Run is one run of a job: an issue with job: and run: fields.
type Run struct {
	Issue  *tracker.Issue
	Period string
	Open   bool
}

// Runs are the runs of job key among issues, oldest first.
func Runs(p *tracker.Project, issues []*tracker.Issue, key string) []Run {
	var out []Run
	for _, is := range issues {
		if !strings.EqualFold(is.Field("job"), key) {
			continue
		}
		out = append(out, Run{Issue: is, Period: is.Field("run"), Open: open(p, is)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Period != out[j].Period {
			return out[i].Period < out[j].Period
		}
		return out[i].Issue.Created < out[j].Issue.Created
	})
	return out
}

func open(p *tracker.Project, is *tracker.Issue) bool {
	return !p.IssueDone(is) && !strings.EqualFold(is.Status, "parked")
}

// Log events.
const (
	OpDue   = "due"
	OpStart = "start"
	OpHold  = "hold"

	HowUnattended = "unattended"
	HowOwner      = "owner"
)

// Event is one line of the run log.
type Event struct {
	Op   string `json:"op"`
	Job  string `json:"job"`
	Run  string `json:"run"`
	Date string `json:"date"`
	// start
	Issue  string `json:"issue,omitempty"`
	How    string `json:"how,omitempty"`
	Quote  string `json:"quote,omitempty"`
	By     string `json:"by,omitempty"`
	Memory int    `json:"memory,omitempty"`
	// due
	Missed int `json:"missed,omitempty"`
	// hold
	Reason string `json:"reason,omitempty"`
}

// LogPath is the run log, with the work files so a private tracker keeps
// it private.
func LogPath(p *tracker.Project) string {
	return filepath.Join(p.IssueDir, ".jobs", "runs.jsonl")
}

// ReadLog reads the run log; a missing log is empty, a bad line skipped.
func ReadLog(p *tracker.Project) []Event {
	f, err := os.Open(LogPath(p))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal([]byte(strings.TrimSpace(sc.Text())), &e) == nil && e.Op != "" {
			out = append(out, e)
		}
	}
	return out
}

// Append adds e to the run log, in one write.
func Append(p *tracker.Project, e Event) error {
	path := LogPath(p)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Note logs a due or hold once: a due per job and run, a hold again only
// when its reason changed since the last line for that run.
func Note(p *tracker.Project, log []Event, e Event) error {
	for i := len(log) - 1; i >= 0; i-- {
		l := log[i]
		if l.Job != e.Job || l.Run != e.Run {
			continue
		}
		if e.Op == OpDue && l.Op == OpDue {
			return nil
		}
		if e.Op == OpHold && l.Op == OpHold {
			if l.Reason == e.Reason {
				return nil
			}
			break
		}
	}
	return Append(p, e)
}

// Auto is the owner's standing start for one job (or every job: key "*").
type Auto struct {
	Words string    `json:"words"`
	By    string    `json:"by"`
	How   string    `json:"how"`
	Since time.Time `json:"since"`
}

// AllJobs is the standing start's key for every job of the project.
const AllJobs = "*"

// State is .jobs/<project>.json: the standing starts that are on.
type State struct {
	Auto map[string]Auto `json:"auto"`
}

// For is the standing start that covers key: its own, else every job's.
func (s *State) For(key string) (Auto, bool) {
	if s == nil {
		return Auto{}, false
	}
	if a, ok := s.Auto[strings.ToLower(key)]; ok {
		return a, true
	}
	a, ok := s.Auto[AllJobs]
	return a, ok
}

// Dir is root's .jobs directory (the main checkout, next to .autostart/).
func Dir(root string) string { return filepath.Join(root, ".jobs") }

// StatePath is the project's standing starts.
func StatePath(root, project string) string { return filepath.Join(Dir(root), project+".json") }

// OffLogPath logs each standing start turned off, one JSON line each.
func OffLogPath(root, project string) string {
	return filepath.Join(Dir(root), project+".log.jsonl")
}

// LoadState reads the standing starts; a missing file has none.
func LoadState(root, project string) (*State, error) {
	st := &State{Auto: map[string]Auto{}}
	data, err := os.ReadFile(StatePath(root, project))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("%s: %w", StatePath(root, project), err)
	}
	if st.Auto == nil {
		st.Auto = map[string]Auto{}
	}
	return st, nil
}

func saveState(root, project string, st *State) error {
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ignore := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(ignore, []byte("# recurring jobs' standing starts (issue-cli job auto); never committed\n*\n"), 0o644); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	path := StatePath(root, project)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// TurnOn stores the owner's words as key's standing start ("*" for every
// job), replacing one already there.
func TurnOn(root, project, key string, a Auto) error {
	if strings.TrimSpace(a.Words) == "" {
		return errors.New("a standing start needs the owner's words")
	}
	st, err := LoadState(root, project)
	if err != nil {
		return err
	}
	st.Auto[strings.ToLower(key)] = a
	return saveState(root, project, st)
}

// offRecord is one line of the off log.
type offRecord struct {
	Job    string    `json:"job"`
	Auto   Auto      `json:"auto"`
	Ended  time.Time `json:"ended"`
	Reason string    `json:"reason"`
}

// TurnOff removes key's standing start ("*": every job's, each one) and
// logs each window it closed. It reports whether one was on.
func TurnOff(root, project, key string, now time.Time, reason string) (bool, error) {
	st, err := LoadState(root, project)
	if err != nil {
		return false, err
	}
	key = strings.ToLower(key)
	var closed []offRecord
	for k, a := range st.Auto {
		if k == key || key == AllJobs {
			closed = append(closed, offRecord{Job: k, Auto: a, Ended: now, Reason: reason})
			delete(st.Auto, k)
		}
	}
	if len(closed) == 0 {
		return false, nil
	}
	if err := saveState(root, project, st); err != nil {
		return false, err
	}
	f, err := os.OpenFile(OffLogPath(root, project), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return true, err
	}
	defer f.Close()
	sort.Slice(closed, func(i, j int) bool { return closed[i].Job < closed[j].Job })
	for _, c := range closed {
		line, _ := json.Marshal(c)
		if _, err := f.Write(append(line, '\n')); err != nil {
			return true, err
		}
	}
	return true, nil
}
