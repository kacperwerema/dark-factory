// Package leaks finds what the factory started and left running: issue-cli
// doctor leaks. It works with tracing off by matching known shapes
// (Patterns), and with a resource trace (internal/restrace) it also lists
// what was started, never stopped, and is still alive (Traced). It stops
// nothing by itself: Finding.Stop does, when the owner passes --stop.
//
// The shapes, each one seen on the owner's Mac:
//   - a playtest Chrome whose runner is gone (procs.OrphanChromes);
//   - a process whose CODEX_HOME or CLAUDE_CONFIG_DIR is a temp or
//     scratchpad folder, detached (parent pid 1) or with that folder gone:
//     codex app-server-daemon out of a test's or a sandbox's home;
//   - an issue-viewer on a temp or scratchpad folder, detached or with
//     that folder gone: a test's or a sandbox's board (testViewers);
//   - the tmux server of a test run that was killed (tmuxguard's stale
//     folders, its socket still there);
//   - a per_run: tool the engine started that no run holds any more;
//   - a registered worktree nothing uses, outside what worktrees prune
//     sweeps: one in a temp or scratchpad folder, or a subagent's under
//     .claude/worktrees whose branch is on main (worktrees.go).
package leaks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/procs"
	"github.com/kacperwerema/dark-factory/internal/restrace"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The kinds of finding.
const (
	KindChrome    = "chrome"
	KindAgentHome = "agent-home"
	KindTmux      = "tmux-server"
	KindPerRun    = "per-run"
	KindViewer    = "test-viewer"
)

// Finding is one thing still alive that should not be.
type Finding struct {
	Kind string `json:"kind"`
	// PID is its process, 0 when it has none of its own (a per_run: tool).
	PID int           `json:"pid,omitempty"`
	Age time.Duration `json:"age_ns"`
	// What names it, Why says why it counts as left behind.
	What string `json:"what"`
	Why  string `json:"why"`
	// StopCommand is what stops it by hand.
	StopCommand string `json:"stop"`
	// Traced: it comes from the trace (started, never stopped, alive), not
	// from a known shape.
	Traced bool `json:"traced,omitempty"`

	stop func() error
}

// Stop stops it.
func (f Finding) Stop() error {
	if f.stop != nil {
		return f.stop()
	}
	if f.PID > 0 {
		if !procs.Stop(f.PID, 3*time.Second) {
			return fmt.Errorf("pid %d did not stop", f.PID)
		}
		return nil
	}
	return fmt.Errorf("nothing to stop")
}

// Options say where to look.
type Options struct {
	Now time.Time
	// Projects are read for per_run: tools.
	Projects []*tracker.Project
	// TmpRoot is where the private tmux folders live (default /tmp).
	TmpRoot string
	// TracePath is the resource trace, "" for restrace.Path().
	TracePath string
	// Under, when set, narrows the look to processes whose agent CLI home
	// (or command line) is inside this folder, at any parent: a check
	// script's sandbox at its exit (lib.sh). Nothing else is looked at.
	Under string
	// LiveDirs is what is live on the machine (agentrun.LiveDirs when nil):
	// a worktree named in it is in use.
	LiveDirs func() []string
}

// Report is what Find found: the known shapes and, from the trace, what is
// still running.
type Report struct {
	Leaks  []Finding `json:"leaks"`
	Traced []Finding `json:"traced"`
	// TraceRead: a trace file was there to read.
	TraceRead bool `json:"trace_read"`
}

// Find looks.
func Find(o Options) Report {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.TmpRoot == "" {
		o.TmpRoot = "/tmp"
	}
	mine, _ := procs.Mine()
	all, _ := procs.List()
	skip := procs.Ancestors(all, os.Getpid())
	var r Report
	if o.Under != "" {
		r.Leaks = underHome(mine, skip, o.Under)
		return r
	}
	r.Leaks = append(r.Leaks, chromes(mine)...)
	r.Leaks = append(r.Leaks, agentHomes(mine, skip)...)
	r.Leaks = append(r.Leaks, testViewers(mine, skip)...)
	r.Leaks = append(r.Leaks, tmuxServers(o.TmpRoot, o.Now)...)
	r.Leaks = append(r.Leaks, perRunTools(o.Projects)...)
	if roots := projectRoots(o.Projects); len(roots) > 0 {
		live := o.LiveDirs
		if live == nil {
			live = agentrun.LiveDirs
		}
		r.Leaks = append(r.Leaks, worktrees(roots, o.Now, live())...)
	}
	path := o.TracePath
	if path == "" {
		path = restrace.Path()
	}
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			r.TraceRead = true
			r.Traced = traced(path, mine, skip, r.Leaks, o.Now)
		}
	}
	sort.SliceStable(r.Leaks, func(i, j int) bool { return r.Leaks[i].Age > r.Leaks[j].Age })
	return r
}

func killCommand(pid int) string { return "kill " + strconv.Itoa(pid) }

func chromes(mine []procs.Proc) []Finding {
	var out []Finding
	for _, o := range procs.OrphanChromes(mine) {
		o := o
		out = append(out, Finding{
			Kind: KindChrome, PID: o.PID, Age: o.Age,
			What:        "Chrome on " + o.Profile,
			Why:         o.Why,
			StopCommand: killCommand(o.PID) + " && rm -rf " + shell.Quote(o.Profile),
			stop: func() error {
				if !procs.Stop(o.PID, 3*time.Second) {
					return fmt.Errorf("Chrome %d did not stop", o.PID)
				}
				if procs.TempPlace(o.Profile) {
					_ = os.RemoveAll(o.Profile)
				}
				return nil
			},
		})
	}
	return out
}

// testViewers are issue-viewers a test or a sandbox started (their -config
// or -dir is in a temp or scratchpad folder) that nothing owns any more:
// detached to launchd for procs.DetachedMinAge or more, or that folder is
// gone. The owner's viewer reads a config in the home folder, never these.
func testViewers(mine []procs.Proc, skip map[int]bool) []Finding {
	var out []Finding
	for _, p := range mine {
		if skip[p.PID] {
			continue
		}
		place, ok := viewerPlace(p.Command)
		if !ok || !procs.TempPlace(place) {
			continue
		}
		_, statErr := os.Stat(place)
		gone := os.IsNotExist(statErr)
		if !gone && (p.PPID != 1 || p.Age < procs.DetachedMinAge) {
			continue
		}
		why := "an issue-viewer on a temp folder whose parent is gone (pid 1)"
		if gone {
			why = "an issue-viewer on a temp folder that no longer exists"
		}
		out = append(out, Finding{
			Kind: KindViewer, PID: p.PID, Age: p.Age,
			What:        "issue-viewer on " + place,
			Why:         why,
			StopCommand: killCommand(p.PID),
		})
	}
	return out
}

// viewerPlace reads an issue-viewer command line: the folder its -config
// or -dir names, ok false when the command is no issue-viewer.
func viewerPlace(command string) (string, bool) {
	f := strings.Fields(command)
	if len(f) == 0 || filepath.Base(f[0]) != "issue-viewer" {
		return "", false
	}
	for i := 1; i < len(f); i++ {
		a := strings.TrimLeft(f[i], "-")
		if !strings.HasPrefix(f[i], "-") {
			continue
		}
		name, val, has := strings.Cut(a, "=")
		if name != "config" && name != "dir" {
			continue
		}
		if !has && i+1 < len(f) {
			val = f[i+1]
		}
		if val != "" {
			return val, true
		}
	}
	return "", false
}

// agentHomes are processes whose CLI home is a throwaway folder and that
// nothing owns any more: detached to launchd for procs.DetachedMinAge or
// more, or their home is gone (that counts at once). A CLI a running test drives sits in its pane under a
// tmux server, never pid 1.
func agentHomes(mine []procs.Proc, skip map[int]bool) []Finding {
	var out []Finding
	for _, p := range mine {
		if skip[p.PID] {
			continue
		}
		env, err := procs.Env(p.PID)
		if err != nil {
			continue
		}
		for _, v := range procs.HomeVars {
			home := procs.Lookup(env, v)
			if !procs.TempPlace(home) {
				continue
			}
			_, statErr := os.Stat(home)
			gone := os.IsNotExist(statErr)
			if !gone && (p.PPID != 1 || p.Age < procs.DetachedMinAge) {
				continue
			}
			why := v + " is a temp folder and its parent is gone (pid 1)"
			if gone {
				why = v + " is a temp folder that no longer exists"
			}
			out = append(out, Finding{
				Kind: KindAgentHome, PID: p.PID, Age: p.Age,
				What:        short(p.Command) + " (" + v + "=" + home + ")",
				Why:         why,
				StopCommand: killCommand(p.PID),
			})
			break
		}
	}
	return out
}

func underHome(mine []procs.Proc, skip map[int]bool, dir string) []Finding {
	var out []Finding
	for _, p := range mine {
		if skip[p.PID] {
			continue
		}
		why := ""
		if env, err := procs.Env(p.PID); err == nil {
			if v, ok := procs.UnderHome(env, dir); ok {
				why = v + " is inside " + dir
			}
		}
		if why == "" && strings.Contains(p.Command, dir) {
			why = "its command line names " + dir
		}
		if why != "" {
			out = append(out, Finding{Kind: KindAgentHome, PID: p.PID, Age: p.Age, What: short(p.Command), Why: why, StopCommand: killCommand(p.PID)})
		}
	}
	return out
}

func tmuxServers(root string, now time.Time) []Finding {
	var out []Finding
	for _, st := range tmuxguard.StaleFolders(root, now) {
		if st.Socket == "" {
			continue
		}
		cmd := exec.Command("tmux", "-S", st.Socket, "display-message", "-p", "#{pid}")
		cmd.Env = tmuxguard.EnvWithoutTMUX()
		b, err := cmd.Output()
		if err != nil {
			continue // no server answers: only a folder
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		st := st
		out = append(out, Finding{
			Kind: KindTmux, PID: pid, Age: st.Age,
			What:        "tmux server on " + st.Socket,
			Why:         "the private server of a test run that was killed (folder older than a run lasts)",
			StopCommand: "env -u TMUX tmux -S " + shell.Quote(st.Socket) + " kill-server && rm -rf " + shell.Quote(st.Dir),
			stop: func() error {
				kill := exec.Command("tmux", "-S", st.Socket, "kill-server")
				kill.Env = tmuxguard.EnvWithoutTMUX()
				err := kill.Run()
				_ = os.RemoveAll(st.Dir)
				return err
			},
		})
	}
	return out
}

func perRunTools(projects []*tracker.Project) []Finding {
	var out []Finding
	for _, p := range projects {
		for i := range p.Departments {
			d := &p.Departments[i]
			for j := range d.Connectors {
				c := &d.Connectors[j]
				if c.PerRun == nil {
					continue
				}
				up, runs := connectors.Up(p, d.Name, c)
				if !up || runs > 0 {
					continue
				}
				p, dept := p, d.Name
				out = append(out, Finding{
					Kind:        KindPerRun,
					What:        p.Slug + ": " + tracker.ConnectorAddress(dept, c.Name),
					Why:         "a per_run: tool the engine started that no run holds",
					StopCommand: "issue-cli --project " + p.Slug + " connector sweep " + tracker.DepartmentKey(dept),
					stop: func() error {
						if errs := connectors.Sweep(p, dept, time.Now); len(errs) > 0 {
							return errs[0]
						}
						return nil
					},
				})
			}
		}
	}
	return out
}

// traced are the trace's open starts whose pid is alive, younger than its
// start (an older process is a reused pid), and not already a finding.
func traced(path string, mine []procs.Proc, skip map[int]bool, known []Finding, now time.Time) []Finding {
	age := map[int]procs.Proc{}
	for _, p := range mine {
		age[p.PID] = p
	}
	seen := map[int]bool{}
	for _, f := range known {
		seen[f.PID] = true
	}
	var out []Finding
	for _, e := range restrace.Live(restrace.Open(restrace.Read(path))) {
		p, ok := age[e.PID]
		if !ok || skip[e.PID] || seen[e.PID] || p.Age > now.Sub(e.At)+time.Minute {
			continue
		}
		seen[e.PID] = true
		what := e.Kind
		if e.ID != "" {
			what += " " + e.ID
		}
		if e.Detail != "" {
			what += " (" + e.Detail + ")"
		}
		out = append(out, Finding{
			Kind: e.Kind, PID: e.PID, Age: p.Age, Traced: true,
			What:        what,
			Why:         "started by " + e.Owner + " at " + e.At.Local().Format("2006-01-02 15:04") + ", never stopped",
			StopCommand: killCommand(e.PID),
		})
	}
	return out
}

// Age is a finding's age in a few characters: 12d19h, 10h27m, 5m, 40s.
func Age(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}

func short(cmd string) string {
	if len(cmd) > 100 {
		return cmd[:97] + "..."
	}
	return cmd
}
