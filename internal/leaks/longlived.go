package leaks

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/procs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// What runs for days: lead sessions and the factory's long-lived processes
// (factory/no-disk-or-memory-leaks-over-a-247-run-…). On 2026-11-21 three
// leads had run 6 to 19 days: a department lead the nightly flush never
// rotated, a CEO on a project whose agents the owner had stopped (no tick,
// so no flush), and a test's lead on the shared server. mem-guard caps the
// machine's total; nothing watched one process growing over days. This
// file finds both and stops nothing: each finding carries the command.

// LeadMaxAge is how long a lead session runs before it is past its
// rotation: the nightly flush rotates every lead each day, so a lead older
// than a day and two hours missed one.
const LeadMaxAge = 26 * time.Hour

// Session finding kinds.
const (
	// SessionUnrotated is a project's lead (CEO, department or group lead)
	// past LeadMaxAge.
	SessionUnrotated = "lead-unrotated"
	// SessionOrphan is a lead-* session no project in projects.yaml owns.
	SessionOrphan = "lead-orphan"
)

// SessionFinding is one lead session past its rotation.
type SessionFinding struct {
	Kind    string        `json:"kind"`
	Session string        `json:"session"`
	Project string        `json:"project,omitempty"`
	Age     time.Duration `json:"age_ns"`
	// Why is the rest of the line; Command rotates or closes it.
	Why     string `json:"why,omitempty"`
	Command string `json:"command"`
}

// Line is the finding as one line.
func (f SessionFinding) Line() string {
	s := fmt.Sprintf("%s has run %s without rotating", f.Session, Age(f.Age))
	if f.Kind == SessionOrphan {
		s = fmt.Sprintf("%s has run %s and no project in projects.yaml owns it", f.Session, Age(f.Age))
	}
	if f.Why != "" {
		s += " (" + f.Why + ")"
	}
	return s + ": " + f.Command
}

// SessionsInput is what LongSessions reads.
type SessionsInput struct {
	Sessions []agentrun.Session
	Projects []*tracker.Project
	Now      time.Time
	// FlushOn answers whether a project's lead-flush has its standing
	// start (nil: unknown, nothing is said about it).
	FlushOn func(*tracker.Project) bool
}

// LongSessions lists the lead sessions past LeadMaxAge: each project's CEO
// and department or group leads, and lead-* sessions no project owns. A
// worker's session (agent-*) is not a lead: freeing an idle one is the
// tick's sweep's.
func LongSessions(in SessionsInput) []SessionFinding {
	var out []SessionFinding
	for _, s := range in.Sessions {
		name := s.Name
		if strings.Contains(name, ":") || !strings.HasPrefix(name, "lead-") {
			continue
		}
		started, err := time.ParseInLocation("2006-01-02 15:04:05", s.StartTime, time.Local)
		if err != nil {
			continue
		}
		age := in.Now.Sub(started)
		if age < LeadMaxAge {
			continue
		}
		proj := OwnerOf(name, in.Projects)
		if proj == nil {
			out = append(out, SessionFinding{Kind: SessionOrphan, Session: name, Age: age,
				Command: "tmux kill-session -t " + name})
			continue
		}
		f := SessionFinding{Kind: SessionUnrotated, Session: name, Project: proj.Slug, Age: age,
			Command: tracker.InjectProject(proj, "issue-cli lead rotate --lead "+name)}
		if in.FlushOn != nil && !in.FlushOn(proj) {
			f.Why = "its nightly lead-flush has no standing start, so nothing rotates it; a project the owner stopped can close it: tmux kill-session -t " + name
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Age > out[j].Age })
	return out
}

// OwnerOf is the project whose CEO or department lead is name.
func OwnerOf(name string, projects []*tracker.Project) *tracker.Project {
	for _, p := range projects {
		if p == nil {
			continue
		}
		if tracker.LeadSessionName(p) == name || p.DepartmentOfLead(name) != nil {
			return p
		}
	}
	return nil
}

// Growth: the factory's long-lived processes (the viewer, each lead's
// agent CLI with its children, a local CI run) are read every 10 minutes
// by the viewer (RecordMem); one whose RSS more than doubled over a day is
// a finding.

// MemKeep is how far back the readings file goes.
const MemKeep = 26 * time.Hour

// GrowthWindow is how far back a growth is measured, GrowthFactor how much
// it must grow, GrowthFloorMB the size below which it never counts (a 30 →
// 70 MB tick is no leak).
const (
	GrowthWindow  = 24 * time.Hour
	GrowthFactor  = 2.0
	GrowthFloorMB = 256
)

// MemReading is one long-lived process's RSS at one time. Key names the
// process across readings: its name and its start, so a restarted viewer
// is a new process.
type MemReading struct {
	At    time.Time `json:"at"`
	Key   string    `json:"key"`
	Name  string    `json:"name"`
	PID   int       `json:"pid"`
	RSSMB float64   `json:"rss_mb"`
}

// MemPath is the readings file in the state dir.
func MemPath() string {
	d := viewerstate.Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "machine", "rss.json")
}

// leadPanes lists the shared tmux server's lead panes: session and pane
// pid. A var for tests.
var leadPanes = func() map[int]string {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", agentrun.Format("session_name", "pane_pid")).Output()
	if err != nil {
		return nil
	}
	panes := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := agentrun.SplitFields(line, 2)
		if len(f) < 2 || !strings.HasPrefix(f[0], "lead-") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(f[1])); err == nil {
			panes[n] = f[0]
		}
	}
	return panes
}

// readMems is the process table with RSS; a var for tests.
var readMems = procs.Mems

// LongLived reads the factory's long-lived processes now: each
// issue-viewer, each lead pane's tree (summed), each issue-cli ci run.
func LongLived(now time.Time) ([]MemReading, error) {
	ms, err := readMems(now)
	if err != nil {
		return nil, err
	}
	return longLivedFrom(ms, leadPanes(), now), nil
}

func longLivedFrom(ms []procs.Mem, panes map[int]string, now time.Time) []MemReading {
	children := map[int][]procs.Mem{}
	byPID := map[int]procs.Mem{}
	for _, m := range ms {
		children[m.PPID] = append(children[m.PPID], m)
		byPID[m.PID] = m
	}
	var tree func(pid int, seen map[int]bool) int64
	tree = func(pid int, seen map[int]bool) int64 {
		if seen[pid] {
			return 0
		}
		seen[pid] = true
		n := byPID[pid].RSSKB
		for _, c := range children[pid] {
			n += tree(c.PID, seen)
		}
		return n
	}
	var out []MemReading
	add := func(name string, m procs.Mem, kb int64) {
		out = append(out, MemReading{At: now, Key: name + "@" + strconv.FormatInt(m.Started.Unix(), 10), Name: name, PID: m.PID, RSSMB: float64(kb) / 1024})
	}
	for _, m := range ms {
		f := strings.Fields(m.Command)
		if len(f) == 0 {
			continue
		}
		switch base := filepath.Base(f[0]); {
		case base == "issue-viewer":
			add("issue-viewer", m, m.RSSKB)
		case base == "issue-cli" && hasArgs(f[1:], "ci", "run"):
			add(fmt.Sprintf("issue-cli ci run (pid %d)", m.PID), m, m.RSSKB)
		}
	}
	for pid, session := range panes {
		m, ok := byPID[pid]
		if !ok {
			continue
		}
		add(session, m, tree(pid, map[int]bool{}))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// hasArgs reports whether args hold a then b next to each other.
func hasArgs(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}

// LoadMem reads the readings file; a missing or broken one has none.
func LoadMem(path string) []MemReading {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rs []MemReading
	if json.Unmarshal(data, &rs) != nil {
		return nil
	}
	return rs
}

// RecordMem adds now's readings to the file at path and drops those older
// than MemKeep, so the file stays small however long the machine runs.
func RecordMem(path string, now time.Time) error {
	if path == "" {
		return nil
	}
	fresh, err := LongLived(now)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return jsonfile.Write(path, keepMem(append(LoadMem(path), fresh...), now), 0o600)
}

func keepMem(rs []MemReading, now time.Time) []MemReading {
	kept := rs[:0]
	for _, r := range rs {
		if now.Sub(r.At) <= MemKeep && !r.At.After(now) {
			kept = append(kept, r)
		}
	}
	return kept
}

// Growth is one process whose RSS grew over GrowthFactor in GrowthWindow.
type Growth struct {
	Name   string    `json:"name"`
	PID    int       `json:"pid"`
	FromMB float64   `json:"from_mb"`
	ToMB   float64   `json:"to_mb"`
	Since  time.Time `json:"since"`
}

// Line is the growth as one line, with what restarts it.
func (g Growth) Line() string {
	return fmt.Sprintf("%s grew %.0f → %.0f MB since %s: %s", g.Name, g.FromMB, g.ToMB, g.Since.Local().Format("01-02 15:04"), restartCommand(g.Name))
}

// restartCommand is how a grown process is restarted by hand.
func restartCommand(name string) string {
	switch {
	case name == "issue-viewer":
		return "restart the viewer (GOBIN=~/.local/bin make install restarts it)"
	case strings.HasPrefix(name, "lead-"):
		return "rotate it: issue-cli lead rotate --lead " + name
	}
	return "it ends with its run; a run that hangs: issue-cli ci status"
}

// Grown lists the processes whose newest reading (at most 30 minutes old)
// is over GrowthFactor times their reading from GrowthWindow ago and over
// GrowthFloorMB. A process with no reading that old is never one: it is
// younger than the window, or the viewer wasn't reading.
func Grown(rs []MemReading, now time.Time) []Growth {
	first := map[string]MemReading{}
	last := map[string]MemReading{}
	for _, r := range rs {
		if r.At.After(now) {
			continue
		}
		if f, ok := first[r.Key]; !ok || r.At.Before(f.At) {
			first[r.Key] = r
		}
		if l, ok := last[r.Key]; !ok || r.At.After(l.At) {
			last[r.Key] = r
		}
	}
	var out []Growth
	for key, l := range last {
		f := first[key]
		if now.Sub(l.At) > 30*time.Minute || l.At.Sub(f.At) < GrowthWindow || f.RSSMB <= 0 {
			continue
		}
		if l.RSSMB > GrowthFactor*f.RSSMB && l.RSSMB >= GrowthFloorMB {
			out = append(out, Growth{Name: l.Name, PID: l.PID, FromMB: f.RSSMB, ToMB: l.RSSMB, Since: f.At})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LongReport is what runs too long: lead sessions past their rotation and
// long-lived processes that grew. issue-cli health, the lead tick and the
// viewer's Needs you read the same report.
type LongReport struct {
	Sessions []SessionFinding `json:"sessions,omitempty"`
	Grown    []Growth         `json:"grown,omitempty"`
}

// Empty reports whether nothing is past its bound.
func (r LongReport) Empty() bool { return len(r.Sessions) == 0 && len(r.Grown) == 0 }

// listSessions is the shared server's sessions; a var for tests.
var listSessions = agentrun.ListSessions

// LongNow reads the report now: the shared tmux server's sessions against
// projects, and the readings file the viewer keeps.
var LongNow = func(projects []*tracker.Project, now time.Time) LongReport {
	return LongReport{
		Sessions: LongSessions(SessionsInput{Sessions: listSessions(), Projects: projects, Now: now, FlushOn: FlushStanding}),
		Grown:    Grown(LoadMem(MemPath()), now),
	}
}

// FlushJob is the nightly flush's job key (projects.yaml Product entry).
const FlushJob = "product/lead-flush"

// FlushStanding reports whether proj's nightly lead-flush has a standing
// start: without one nothing rotates its CEO.
func FlushStanding(proj *tracker.Project) bool {
	dir := proj.WorkDir
	if dir == "" && proj.IssueDir != "" {
		dir = filepath.Dir(proj.IssueDir)
	}
	root := gitmain.Root(dir)
	if root == "" {
		root = dir
	}
	st, err := jobs.LoadState(root, proj.Slug)
	if err != nil {
		return true // unknown: say nothing about it
	}
	_, ok := st.For(FlushJob)
	return ok
}
