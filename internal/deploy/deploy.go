// Package deploy walks a project's deploy: steps (workflow.yaml) in order:
// it asks the caller whether each step's needs are met, runs the step and
// its probe, stops at the first red or refused step, and keeps one record
// per run under the issues dir's .deploys/ (docs/workflow.md, Deploy).
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Step states in a record.
const (
	Green   = "green"
	Red     = "red"
	Refused = "refused"
	// NotRun is a step after the one that stopped the deploy.
	NotRun = "not run"
)

// DirName is the folder under the issues dir that holds the records.
const DirName = ".deploys"

// Dir is the records folder of the project whose issues live in issueDir.
func Dir(issueDir string) string { return filepath.Join(issueDir, DirName) }

// StepResult is one step of a run.
type StepResult struct {
	Name  string   `json:"name"`
	Run   string   `json:"run"`
	Probe string   `json:"probe,omitempty"`
	Needs []string `json:"needs,omitempty"`
	State string   `json:"state"`
	// Why says what refused the step or made it red.
	Why       string    `json:"why,omitempty"`
	Exit      *int      `json:"exit,omitempty"`
	ProbeExit *int      `json:"probe_exit,omitempty"`
	Started   time.Time `json:"started,omitempty"`
	Seconds   float64   `json:"seconds,omitempty"`
}

// Record is one deploy run, .deploys/<id>.json.
type Record struct {
	ID       string    `json:"id"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	// By is who ran it (the lead's session, or "owner's shell").
	By string `json:"by,omitempty"`
	// Quote is the owner's words passed with --quote.
	Quote string `json:"quote,omitempty"`
	// Sha is main's head when the run started.
	Sha string `json:"sha,omitempty"`
	// Only is the step --step named, From the one --from named; both
	// empty for the whole list.
	Only  string `json:"only,omitempty"`
	From  string `json:"from,omitempty"`
	State string `json:"state"`
	// Failed is the step that stopped the run (red or refused).
	Failed string       `json:"failed,omitempty"`
	Steps  []StepResult `json:"steps"`
	// Log is the run's output file, beside the record.
	Log string `json:"log"`
}

// FailedStep is the result of the step that stopped the run, nil when
// every step was green.
func (r *Record) FailedStep() *StepResult {
	for i := range r.Steps {
		if r.Steps[i].Name == r.Failed {
			return &r.Steps[i]
		}
	}
	return nil
}

// Options is one run.
type Options struct {
	Steps    []tracker.DeployStep
	IssueDir string
	// Dir is where the commands run: the project's main checkout.
	Dir string
	// Only runs that step alone; From runs from that step to the end.
	Only  string
	From  string
	By    string
	Quote string
	Sha   string
	// Check returns why step may not run now ("" when its needs are met).
	Check func(step tracker.DeployStep) string
	// Out gets the progress lines; the commands' output goes to the log.
	Out io.Writer
	Now func() time.Time
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Select is the steps a run walks: all of them, the one only names, or
// from the one from names to the end.
func Select(steps []tracker.DeployStep, only, from string) ([]tracker.DeployStep, error) {
	if only != "" && from != "" {
		return nil, fmt.Errorf("deploy: pick --step or --from, not both")
	}
	want := valueOr(only, from)
	if want == "" {
		return steps, nil
	}
	var names []string
	for i, s := range steps {
		if s.Name == want {
			if only != "" {
				return []tracker.DeployStep{s}, nil
			}
			return steps[i:], nil
		}
		names = append(names, s.Name)
	}
	return nil, fmt.Errorf("deploy: no step is called %q; steps: %s", want, strings.Join(names, ", "))
}

func valueOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Run walks the steps, writes the record and returns it. An error means no
// run took place (bad config, another deploy running, the record folder
// unwritable); a red or refused step is in the record, not an error.
func Run(opts Options) (*Record, error) {
	if err := tracker.CheckDeploy(opts.Steps); err != nil {
		return nil, err
	}
	steps, err := Select(opts.Steps, opts.Only, opts.From)
	if err != nil {
		return nil, err
	}
	dir := Dir(opts.IssueDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	release, err := lock(dir, opts.now())
	if err != nil {
		return nil, err
	}
	defer release()

	start := opts.now()
	id := newID(dir, start)
	rec := &Record{ID: id, Started: start, By: opts.By, Quote: opts.Quote, Sha: opts.Sha, Only: opts.Only, From: opts.From, Log: id + ".log"}
	logf, err := os.Create(filepath.Join(dir, rec.Log))
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	out := opts.Out
	if out == nil {
		out = io.Discard
	}

	stopped := false
	for i, s := range steps {
		res := StepResult{Name: s.Name, Run: s.Run, Probe: s.Probe, Needs: append([]string(nil), s.Needs...)}
		if stopped {
			res.State = NotRun
			rec.Steps = append(rec.Steps, res)
			continue
		}
		res.Started = opts.now()
		fmt.Fprintf(logf, "== %d. %s (%s)\n", i+1, s.Name, res.Started.Format(time.RFC3339))
		if opts.Check != nil {
			if why := opts.Check(s); why != "" {
				res.State, res.Why = Refused, why
				fmt.Fprintf(logf, "refused: %s\n", why)
				fmt.Fprintf(out, "✗ %d. %s refused: %s\n", i+1, s.Name, why)
				rec.Steps = append(rec.Steps, res)
				rec.Failed, stopped = s.Name, true
				continue
			}
		}
		res.State = runStep(s, opts.Dir, logf, &res)
		res.Seconds = opts.now().Sub(res.Started).Seconds()
		rec.Steps = append(rec.Steps, res)
		if res.State != Green {
			fmt.Fprintf(out, "✗ %d. %s: %s (%s)\n", i+1, s.Name, res.Why, seconds(res.Seconds))
			rec.Failed, stopped = s.Name, true
			continue
		}
		fmt.Fprintf(out, "✓ %d. %s (%s)\n", i+1, s.Name, seconds(res.Seconds))
	}
	rec.Finished = opts.now()
	rec.State = Green
	if f := rec.FailedStep(); f != nil {
		rec.State = f.State
	}
	if err := save(dir, rec); err != nil {
		return rec, err
	}
	return rec, nil
}

// runStep runs the step's command and then its probe under one limit.
func runStep(s tracker.DeployStep, dir string, log io.Writer, res *StepResult) string {
	ctx, cancel := context.WithTimeout(context.Background(), s.Limit())
	defer cancel()
	code, err := shell(ctx, dir, s.Run, log)
	res.Exit = &code
	if why := failure("run", code, err, ctx, s.Limit()); why != "" {
		res.Why = why
		return Red
	}
	if strings.TrimSpace(s.Probe) == "" {
		return Green
	}
	fmt.Fprintf(log, "-- probe: %s\n", s.Probe)
	pcode, perr := shell(ctx, dir, s.Probe, log)
	res.ProbeExit = &pcode
	if why := failure("probe", pcode, perr, ctx, s.Limit()); why != "" {
		res.Why = why
		return Red
	}
	return Green
}

func failure(what string, code int, err error, ctx context.Context, limit time.Duration) string {
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return fmt.Sprintf("%s timed out after %s", what, limit)
	case err != nil && code < 0:
		return fmt.Sprintf("%s did not start: %v", what, err)
	case code != 0:
		return fmt.Sprintf("%s exit %d", what, code)
	}
	return ""
}

// shell runs command with sh -c in dir, its output to log, in its own
// process group so a timeout stops what it started too.
func shell(ctx context.Context, dir, command string, log io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if code := exit.ExitCode(); code >= 0 {
			return code, err
		}
		return 1, err
	}
	return -1, err
}

func seconds(s float64) string {
	return (time.Duration(s * float64(time.Second))).Round(time.Second).String()
}

// newID names a run by its start, with a suffix when that second is taken.
func newID(dir string, t time.Time) string {
	base := t.Format("20060102-150405")
	id := base
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Join(dir, id+".json")); os.IsNotExist(err) {
			return id
		}
		id = base + "-" + strconv.Itoa(n)
	}
}

func save(dir string, rec *Record) error {
	return jsonfile.Write(filepath.Join(dir, rec.ID+".json"), rec, 0o644)
}

// Records are the project's deploy runs, newest first.
func Records(issueDir string) []*Record {
	entries, err := os.ReadDir(Dir(issueDir))
	if err != nil {
		return nil
	}
	var out []*Record
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(Dir(issueDir), name))
		if err != nil {
			continue
		}
		var rec Record
		if json.Unmarshal(raw, &rec) != nil || rec.ID == "" {
			continue
		}
		out = append(out, &rec)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// Last is the newest deploy run, nil when there is none.
func Last(issueDir string) *Record {
	if recs := Records(issueDir); len(recs) > 0 {
		return recs[0]
	}
	return nil
}

// lockInfo is .deploys/lock while a run is going.
type lockInfo struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// lock takes .deploys/lock, or names the live run that holds it. A lock
// whose process is gone is taken over.
func lock(dir string, now time.Time) (func(), error) {
	path := filepath.Join(dir, "lock")
	raw, _ := json.Marshal(lockInfo{PID: os.Getpid(), Started: now})
	for try := 0; try < 2; try++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, werr := f.Write(raw)
			f.Close()
			if werr != nil {
				os.Remove(path)
				return nil, werr
			}
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		var held lockInfo
		if b, rerr := os.ReadFile(path); rerr == nil && json.Unmarshal(b, &held) == nil && alive(held.PID) {
			return nil, fmt.Errorf("another deploy is running (pid %d, started %s); wait for it, or remove %s if that process is gone", held.PID, held.Started.Format("15:04:05"), path)
		}
		os.Remove(path)
	}
	return nil, fmt.Errorf("could not take %s", path)
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
