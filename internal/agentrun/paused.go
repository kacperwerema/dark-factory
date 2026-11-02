package agentrun

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Paused agents: mem-guard pauses a heavy process with SIGSTOP when memory
// passes its pause limit and resumes it later. A session whose pane process,
// or any process under it, is in state T is "paused by mem-guard": the Crew
// page across projects shows it and never stops or nudges it (killing it
// would lose work mem-guard means to resume).

// Pane is one tmux pane: its session, its window and the pid of the
// process it runs.
type Pane struct {
	Session, Window string
	PID             int
}

// Proc is one row of the process table.
type Proc struct {
	PID, PPID int
	Stat      string
}

// ListPanes and ProcessTable read tmux and ps; vars so tests can stub them.
var (
	ListPanes    = defaultListPanes
	ProcessTable = defaultProcessTable
)

func defaultListPanes() []Pane {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{session_name}\t#{window_name}\t#{pane_pid}").Output()
	if err != nil {
		return nil
	}
	return ParsePanes(string(out))
}

// ParsePanes reads `tmux list-panes -a -F
// '#{session_name}\t#{window_name}\t#{pane_pid}'` output.
func ParsePanes(out string) []Pane {
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) != 3 {
			continue
		}
		pid, err := strconv.Atoi(f[2])
		if err != nil || pid <= 0 {
			continue
		}
		panes = append(panes, Pane{Session: f[0], Window: f[1], PID: pid})
	}
	return panes
}

func defaultProcessTable() []Proc {
	out, err := exec.Command("ps", "-Ao", "pid=,ppid=,stat=").Output()
	if err != nil {
		return nil
	}
	return ParseProcs(string(out))
}

// ParseProcs reads `ps -Ao pid=,ppid=,stat=` output.
func ParseProcs(out string) []Proc {
	var procs []Proc
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		procs = append(procs, Proc{PID: pid, PPID: ppid, Stat: f[2]})
	}
	return procs
}

// Snapshot is the panes and the process table read once, so a page over
// many agents runs two commands, not two per agent.
type Snapshot struct {
	panes    []Pane
	children map[int][]int
	stopped  map[int]bool
}

// TakeSnapshot reads tmux's panes and the process table now.
func TakeSnapshot() *Snapshot {
	s := &Snapshot{panes: ListPanes(), children: map[int][]int{}, stopped: map[int]bool{}}
	for _, p := range ProcessTable() {
		s.children[p.PPID] = append(s.children[p.PPID], p.PID)
		if strings.HasPrefix(p.Stat, "T") {
			s.stopped[p.PID] = true
		}
	}
	return s
}

// Paused reports whether the agent called name in proj (a bare session name,
// or a window of the project's shared session) has a process in state T in
// any of its panes' trees.
func (s *Snapshot) Paused(proj *tracker.Project, name string) bool {
	if s == nil {
		return false
	}
	shared := SharedSession(proj)
	for _, p := range s.panes {
		mine := p.Session == name
		if shared != "" {
			mine = p.Session == shared && p.Window == name
		}
		if mine && s.treeStopped(p.PID) {
			return true
		}
	}
	return false
}

func (s *Snapshot) treeStopped(root int) bool {
	seen := map[int]bool{}
	stack := []int{root}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if s.stopped[pid] {
			return true
		}
		stack = append(stack, s.children[pid]...)
	}
	return false
}
