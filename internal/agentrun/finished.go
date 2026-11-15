package agentrun

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Finished workers: a worker's session is no use once its issue is closed
// (done, parked, a side exit), and an idle Claude Code session still holds a
// few hundred MB with its MCP servers. The transition into a closed status
// closes the issue's sessions (issue-cli), and the sweep below closes what
// is left: a worker session whose issue is closed, or whose pane is only a
// shell. Only agent-<slug> and limited-<slug> sessions are ever closed: never
// a lead, a post-mortem agent or anything the launcher did not name.

// WorkerSessions names the sessions an issue's worker can live in: its
// agent-<slug> and, after a takeover, the limited-<slug> it left.
func WorkerSessions(slug string) []string {
	return []string{tracker.AgentSessionName(slug), tracker.LimitedSessionName(slug)}
}

// workerPrefixes are the session names the sweep may close.
var workerPrefixes = []string{"agent-", tracker.LimitedSessionPrefix}

// IsWorkerSession reports whether name is a worker's session name.
func IsWorkerSession(name string) bool {
	for _, p := range workerPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Finished is a worker session the sweep closes, and why.
type Finished struct {
	Name string `json:"name"`
	// Slug is the issue the session belongs to; "" for a bare shell no
	// issue names.
	Slug string `json:"slug,omitempty"`
	Why  string `json:"why"`
	// Dead: the pane is only a shell (its agent exited), whatever its
	// issue's status.
	Dead bool `json:"dead,omitempty"`
}

// SweepInput is what FinishedSessions reads besides the project.
type SweepInput struct {
	// Issues are the project's issues; Closed answers for each one
	// (tracker.ClosedIssueFunc).
	Issues []*tracker.Issue
	Closed func(*tracker.Issue) bool
	// KeepNames are sessions never closed: names an open issue holds in
	// another project on the same tmux server, and this process's own.
	KeepNames map[string]bool
	// Snap is the panes and process table read once (TakeSnapshot).
	Snap *Snapshot
}

// FinishedSessions lists proj's live worker sessions (root scopes standalone
// sessions, as ForProject) that may be closed: their issue is closed, or
// their pane is only a shell (the agent exited). It leaves alone a session
// paused by mem-guard (state T: it means to resume it), one whose agent is
// in a turn, one a person is attached to (standalone mode) and any name in
// KeepNames. It only reads.
func FinishedSessions(proj *tracker.Project, root string, in SweepInput) []Finished {
	byName := map[string][]*tracker.Issue{}
	for _, issue := range in.Issues {
		for _, name := range WorkerSessions(issue.Slug) {
			byName[name] = append(byName[name], issue)
		}
	}
	var out []Finished
	seen := map[string]bool{}
	for _, s := range ForProject(proj, root, ListSessions()) {
		name := BareName(s.Name)
		if !IsWorkerSession(name) || seen[name] || in.KeepNames[name] {
			continue
		}
		seen[name] = true
		f := Finished{Name: name}
		if issue := closedIssue(byName[name], in.Closed); issue != nil {
			f.Slug, f.Why = issue.Slug, fmt.Sprintf("%s is %s", issue.Slug, issue.Status)
		} else if anyOpen(byName[name], in.Closed) {
			if !in.Snap.BareShell(proj, name) {
				continue
			}
			f.Slug, f.Why, f.Dead = byName[name][0].Slug, "only a shell: its agent exited", true
		} else if in.Snap.BareShell(proj, name) {
			f.Why, f.Dead = "only a shell: its agent exited, and no issue names it", true
		} else {
			// A worker no issue here names: a job run or another project's.
			continue
		}
		if in.Snap.Paused(proj, name) || AgentWorking(proj, name) {
			continue
		}
		if SharedSession(proj) == "" && SessionAttached(name) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// AgentWorking reports whether the agent called name is in a turn: its
// pane shows the working line.
func AgentWorking(proj *tracker.Project, name string) bool {
	return PaneWorking(Target(proj, name), dialogLines)
}

// closedIssue is the issue a session belongs to when every issue that names
// it is closed; nil when one is open or none names it.
func closedIssue(issues []*tracker.Issue, closed func(*tracker.Issue) bool) *tracker.Issue {
	if len(issues) == 0 || anyOpen(issues, closed) {
		return nil
	}
	return issues[0]
}

func anyOpen(issues []*tracker.Issue, closed func(*tracker.Issue) bool) bool {
	for _, issue := range issues {
		if closed == nil || !closed(issue) {
			return true
		}
	}
	return false
}

// idleClose is how CloseWhenIdle waits: a first pause, then a look every
// Poll seconds, at most Polls looks; after them the session closes anyway
// (its issue is closed: nothing in it is work). A var so tests wait less.
var idleClose = struct {
	First, Poll string
	Polls       int
}{First: "2", Poll: "5", Polls: 360}

// idleCloseScript waits until the pane at $1 shows no working line for two
// looks in a row (the agent ended its turn), then runs tmux $0 -t $1. A pane
// that is gone ends it. It runs detached: the command that starts it may
// be running in that very pane.
func idleCloseScript() string {
	return fmt.Sprintf(`sleep %s
idle=0
i=0
while [ "$i" -lt %d ]; do
  pane=$(tmux capture-pane -p -J -t "$1" 2>/dev/null) || exit 0
  if printf '%%s\n' "$pane" | grep -v '^[[:space:]]*$' | tail -n %d | grep -qi '%s'; then
    idle=0
  else
    idle=$((idle+1))
    [ "$idle" -ge 2 ] && break
  fi
  i=$((i+1))
  sleep %s
done
exec tmux "$0" -t "$1"`, idleClose.First, idleClose.Polls, dialogLines, paneWorkingMark, idleClose.Poll)
}

// closeVerb is the tmux command that closes the agent called name in proj:
// its session, or its window in the project's shared session.
func closeVerb(proj *tracker.Project) string {
	if SharedSession(proj) != "" {
		return "kill-window"
	}
	return "kill-session"
}

// CloseWhenIdle closes the agent called name once its turn ends: a detached
// shell in its own process session waits for the pane's working line to go
// and then closes it, so the output of the command that asked reaches the
// agent first, and the close outlives the pane's hangup when that command
// runs inside it. A var so tests stub it.
var CloseWhenIdle = defaultCloseWhenIdle

func defaultCloseWhenIdle(proj *tracker.Project, name string) error {
	cmd := exec.Command("sh", "-c", idleCloseScript(), closeVerb(proj), Target(proj, name))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
