package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/memguard"
	"github.com/kacperwerema/dark-factory/internal/restrace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// A connector with per_run: is a heavy tool that runs only while a run
// needs it. Hold starts it for a run under mem-guard, Release stops it when
// the last run lets go, and Sweep stops what a killed run left behind.
//
// The state is in the state dir, one folder per connector: lock (a flock
// around every decision), state.json (whether the engine started the tool)
// and leases/ (one file per run, flock-held for the life of the process, so
// the kernel ends a lease on any exit and a reused pid can't fake one).

// Run events in <issues>/.connectors/runs.jsonl.
const (
	RunStarted     = "started"      // start: ran and ready: passed
	RunHeld        = "held"         // mem-guard blocked it; nothing started
	RunStopped     = "stopped"      // the last run let go, stop: passed
	RunStartFailed = "start-failed" // start: or ready: failed; stop: ran
	RunStopFailed  = "stop-failed"  // stop: failed; the sweep tries again
)

var (
	// readyPoll is how often ready: runs while a tool comes up.
	readyPoll = 500 * time.Millisecond
	// readyProbe caps the look at whether a tool is already up.
	readyProbe = 10 * time.Second
)

// RunRecord is one line of the run log. Note holds mem-guard's line or an
// exit code, never a command's output.
type RunRecord struct {
	Connector string    `json:"connector"`
	Date      time.Time `json:"date"`
	Event     string    `json:"event"`
	MS        int64     `json:"ms,omitempty"`
	Note      string    `json:"note,omitempty"`
}

// RunLogPath is the run log, with the work files.
func RunLogPath(p *tracker.Project) string {
	return filepath.Join(p.IssueDir, ".connectors", "runs.jsonl")
}

// AppendRun adds a record to the run log.
func AppendRun(p *tracker.Project, r RunRecord) error {
	path := RunLogPath(p)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(r)
	_, err = f.Write(append(b, '\n'))
	return err
}

// LoadRuns is the newest run record of each connector, by address. Lines
// that don't parse are skipped.
func LoadRuns(p *tracker.Project) map[string]RunRecord {
	out := map[string]RunRecord{}
	data, err := os.ReadFile(RunLogPath(p))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		var r RunRecord
		if json.Unmarshal([]byte(line), &r) != nil || r.Connector == "" {
			continue
		}
		if old, ok := out[r.Connector]; !ok || !r.Date.Before(old.Date) {
			out[r.Connector] = r
		}
	}
	return out
}

// runDir is the connector's state folder, "" without a state dir. With
// per_run: tool: it is the tool's, shared by every connector on the machine
// that names it, in any project: their runs are one set of leases. It sits
// beside connectors/, so no project slug meets a tool id.
func runDir(p *tracker.Project, dept string, c *tracker.Connector) string {
	base := viewerstate.Dir()
	if base == "" {
		return ""
	}
	if c.PerRun != nil && c.PerRun.Tool != "" {
		if c.PerRun.ToolProblem() != "" {
			return ""
		}
		return filepath.Join(base, "connector-tools", c.PerRun.Tool)
	}
	project := p.Slug
	if project == "" {
		sum := sha256.Sum256([]byte(p.IssueDir))
		project = hex.EncodeToString(sum[:6])
	}
	return filepath.Join(base, "connectors", project, tracker.DepartmentKey(dept)+"-"+c.Name)
}

// runState is state.json. Started means the tool is the engine's to stop: it
// is set before start: runs, so a run killed halfway leaves it to the sweep.
type runState struct {
	Started bool `json:"started"`
	// StopFailed keeps a failing stop: to one log line per streak.
	StopFailed bool `json:"stop_failed,omitempty"`
}

func readState(dir string) runState {
	var st runState
	if b, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func writeState(dir string, st runState) error {
	return jsonfile.Write(filepath.Join(dir, "state.json"), st, 0600)
}

// lockRun takes the connector's lock, waiting up to wait: another run may be
// starting or stopping the tool. wait 0 tries once.
func lockRun(ctx context.Context, dir string, wait time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Join(dir, "leases"), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || !time.Now().Before(deadline) || ctx.Err() != nil {
			f.Close()
			return nil, fmt.Errorf("another run holds the connector's lock: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// holders counts the live leases; a lease whose process is gone is removed
// when prune is set, which only a caller under the lock does. skip is a
// lease not to count (the caller's own).
func holders(dir, skip string, prune bool) int {
	names, _ := filepath.Glob(filepath.Join(dir, "leases", "run-*"))
	n := 0
	for _, name := range names {
		if name == skip {
			continue
		}
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		if syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil {
			n++
		} else if prune {
			os.Remove(name)
		}
		f.Close()
	}
	return n
}

// Lease is one run's hold on a connector's tool.
type Lease struct {
	project *tracker.Project
	dept    string
	c       *tracker.Connector
	now     func() time.Time
	dir     string
	env     []string
	file    *os.File
	once    sync.Once
}

func newLease(dir string) (*os.File, error) {
	f, err := os.CreateTemp(filepath.Join(dir, "leases"), "run-*")
	if err != nil {
		return nil, err
	}
	// Blocking: a reader's look at this file lasts a moment.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	return f, nil
}

func (l *Lease) drop() {
	os.Remove(l.file.Name())
	l.file.Close()
}

// shellStep runs one per_run: command in the project root with env, in its
// own process group so a signal to the run doesn't reach it and a timeout
// reaches all of it. Its output is thrown away: stdout may be an MCP
// stream, and a command that prints a token leaks nothing.
func shellStep(ctx context.Context, p *tracker.Project, env []string, script string) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Dir = p.Root()
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

// stepWords says why a step failed: an exit code or a timeout, no output.
func stepWords(name string, ctx context.Context, err error) string {
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return name + ": timed out or the run ended"
	case errors.As(err, &exitErr):
		return fmt.Sprintf("%s: exit %d", name, exitErr.ExitCode())
	}
	return name + ": could not run"
}

// Hold takes c's tool for one run. If it is up (another run, or the owner)
// the run joins it; else it starts under mem-guard, and a failed start stops
// what came up. The caller must Release, whatever happens to the run.
func Hold(ctx context.Context, p *tracker.Project, dept string, c *tracker.Connector, now func() time.Time) (*Lease, error) {
	r := c.PerRun
	if miss := r.MissingSteps(); len(miss) > 0 {
		return nil, fmt.Errorf("connector %s: per_run: needs %s", c.Name, strings.Join(miss, ", "))
	}
	if why := r.ToolProblem(); why != "" {
		return nil, fmt.Errorf("connector %s: per_run: tool: %s", c.Name, why)
	}
	dir := runDir(p, dept, c)
	if dir == "" {
		return nil, fmt.Errorf("connector %s: no state dir for per_run:", c.Name)
	}
	env, err := Env(c, os.Environ())
	if err != nil {
		return nil, err
	}
	wait := r.Wait()
	unlock, err := lockRun(ctx, dir, 2*wait+10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connector %s: %w", c.Name, err)
	}
	defer unlock()
	file, err := newLease(dir)
	if err != nil {
		return nil, err
	}
	l := &Lease{project: p, dept: dept, c: c, now: now, dir: dir, env: env, file: file}
	if holders(dir, file.Name(), true) > 0 {
		return l, nil
	}
	if isReady(ctx, l, min(readyProbe, wait)) {
		return l, nil
	}
	addr := tracker.ConnectorAddress(dept, c.Name)
	if found, blocked, msg := memguard.Check(); found && blocked {
		l.drop()
		_ = AppendRun(p, RunRecord{Connector: addr, Date: now().UTC(), Event: RunHeld, Note: msg})
		return nil, fmt.Errorf("connector %s stays stopped, nothing was started: %s. Run it when mem-guard allows; this run goes on without it", addr, msg)
	}
	if err := writeState(dir, runState{Started: true}); err != nil {
		l.drop()
		return nil, err
	}
	began := time.Now()
	if why := startTool(ctx, l, wait); why != "" {
		_ = AppendRun(p, RunRecord{Connector: addr, Date: now().UTC(), Event: RunStartFailed, MS: time.Since(began).Milliseconds(), Note: why})
		l.drop()
		stopTool(p, addr, c, dir, env, now, false)
		return nil, fmt.Errorf("connector %s did not start (%s); its stop: ran", addr, why)
	}
	_ = AppendRun(p, RunRecord{Connector: addr, Date: now().UTC(), Event: RunStarted, MS: time.Since(began).Milliseconds()})
	restrace.Start(restrace.PerRunKind(c.PerRun.Start), 0, addr, c.PerRun.Start)
	return l, nil
}

func isReady(ctx context.Context, l *Lease, wait time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	return shellStep(ctx, l.project, l.env, l.c.PerRun.Ready) == nil
}

// startTool runs start: and waits for ready:; "" when it is up, else why not.
func startTool(ctx context.Context, l *Lease, wait time.Duration) string {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	if err := shellStep(ctx, l.project, l.env, l.c.PerRun.Start); err != nil {
		return stepWords("start", ctx, err)
	}
	for {
		err := shellStep(ctx, l.project, l.env, l.c.PerRun.Ready)
		if err == nil {
			return ""
		}
		select {
		case <-ctx.Done():
			return stepWords("ready", ctx, err)
		case <-time.After(readyPoll):
		}
	}
}

// stopTool runs stop: under the caller's lock, on its own clock: the run's
// context may be the reason it stops. A failure leaves Started set, so the
// sweep tries again. record false keeps a clean-up after a failed start out
// of the log: "stopped" over "start-failed" would hide the failure.
func stopTool(p *tracker.Project, addr string, c *tracker.Connector, dir string, env []string, now func() time.Time, record bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.PerRun.Wait())
	defer cancel()
	began := time.Now()
	st := readState(dir)
	if err := shellStep(ctx, p, env, c.PerRun.Stop); err != nil {
		why := stepWords("stop", ctx, err)
		if !st.StopFailed {
			_ = AppendRun(p, RunRecord{Connector: addr, Date: now().UTC(), Event: RunStopFailed, Note: why})
		}
		_ = writeState(dir, runState{Started: true, StopFailed: true})
		return fmt.Errorf("connector %s did not stop (%s); the viewer's sweep or issue-cli connector sweep tries again", addr, why)
	}
	if record {
		_ = AppendRun(p, RunRecord{Connector: addr, Date: now().UTC(), Event: RunStopped, MS: time.Since(began).Milliseconds()})
	}
	restrace.Stop(restrace.PerRunKind(c.PerRun.Start), 0, addr)
	return writeState(dir, runState{})
}

// Release ends the run's hold. The last run out stops a tool the engine
// started; one the owner started stays up. Safe to call twice.
func (l *Lease) Release() error {
	var err error
	l.once.Do(func() {
		unlock, lockErr := lockRun(context.Background(), l.dir, 2*l.c.PerRun.Wait()+10*time.Second)
		l.drop()
		if lockErr != nil {
			err = fmt.Errorf("connector %s: %w; the sweep stops it", l.c.Name, lockErr)
			return
		}
		defer unlock()
		if holders(l.dir, "", true) > 0 || !readState(l.dir).Started {
			return
		}
		err = stopTool(l.project, tracker.ConnectorAddress(l.dept, l.c.Name), l.c, l.dir, l.env, l.now, true)
	})
	return err
}

// Sweep stops every per_run: tool of department dept ("" for all) that the
// engine started and no run holds any more: a run killed before its
// Release. It never starts anything, and skips a connector mid-decision.
func Sweep(p *tracker.Project, dept string, now func() time.Time) []error {
	var errs []error
	for i := range p.Departments {
		d := &p.Departments[i]
		if dept != "" && tracker.DepartmentKey(d.Name) != tracker.DepartmentKey(dept) {
			continue
		}
		for j := range d.Connectors {
			c := &d.Connectors[j]
			if c.PerRun == nil {
				continue
			}
			if err := sweepOne(p, d.Name, c, now); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

func sweepOne(p *tracker.Project, dept string, c *tracker.Connector, now func() time.Time) error {
	dir := runDir(p, dept, c)
	if dir == "" || !readState(dir).Started {
		return nil
	}
	unlock, err := lockRun(context.Background(), dir, 0)
	if err != nil {
		return nil
	}
	defer unlock()
	if holders(dir, "", true) > 0 || !readState(dir).Started {
		return nil
	}
	env, err := Env(c, os.Environ())
	if err != nil {
		env = os.Environ() // a stop that needs no secret still stops
	}
	return stopTool(p, tracker.ConnectorAddress(dept, c.Name), c, dir, env, now, true)
}

// Up reports whether c's tool is held by a run or was left up by the engine.
func Up(p *tracker.Project, dept string, c *tracker.Connector) (up bool, runs int) {
	dir := runDir(p, dept, c)
	if dir == "" {
		return false, 0
	}
	runs = holders(dir, "", false)
	return runs > 0 || readState(dir).Started, runs
}

// Check is one health check: a per_run: tool that no run holds is down on
// purpose, so its check isn't run and it isn't failing.
func Check(ctx context.Context, p *tracker.Project, dept string, c *tracker.Connector, now func() time.Time) Record {
	if c.PerRun != nil {
		if up, _ := Up(p, dept, c); !up {
			return Record{Connector: tracker.ConnectorAddress(dept, c.Name), Date: now().UTC(), Result: ResultStopped}
		}
	}
	return RunCheck(ctx, dept, c, now)
}
