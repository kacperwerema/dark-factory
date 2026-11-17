package agentrun

import (
	"fmt"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clilog"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Reclaimable workers: under a memory hold, an idle worker session holds a
// few hundred MB that a start is waiting for. FinishedSessions closes those
// whose issue is closed, but leaves two kinds: a done issue whose branch is
// not on main yet (the lead lands it first) and a worker parked where only
// the owner moves its issue on (a ready gate, an open question). Reclaimable
// lists both, and the closed ones, for the hold to name and the lead's tick
// to close. It reads only.

// Reclaim is one session a memory hold could free.
type Reclaim struct {
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Status string `json:"status"`
	// Why is what the issue waits on: "done", "waits on the owner's
	// approval for backlog", "an open question: comment".
	Why       string    `json:"why"`
	IdleSince time.Time `json:"idle_since"`
}

// ReclaimInput is what Reclaimable reads besides the project.
type ReclaimInput struct {
	Issues []*tracker.Issue
	// Closed answers whether an issue is closed (tracker.ClosedIssueFunc).
	Closed func(*tracker.Issue) bool
	// Waiting is what an open issue waits on the owner for, "" when it
	// doesn't: an open issue without one is never freed.
	Waiting   func(*tracker.Issue) string
	KeepNames map[string]bool
	// Snap reads the panes and process table (TakeSnapshot), once and only
	// when a session gets that far.
	Snap func() *Snapshot
	Now  time.Time
	// Idle is how long a session must have made no issue-cli call to count
	// (issue-cli passes timeline.IdleAfter): a worker that just reached a
	// gate may still write its retro.
	Idle time.Duration
}

// Reclaimable lists proj's live worker sessions (root scopes standalone
// sessions and holds the clilogs) whose every issue is closed or waits on
// the owner, whose agent is not in a turn and has been idle in.Idle,
// that mem-guard has not paused and nobody is attached to (standalone).
func Reclaimable(proj *tracker.Project, root string, in ReclaimInput) []Reclaim {
	byName := map[string][]*tracker.Issue{}
	for _, issue := range in.Issues {
		for _, name := range WorkerSessions(issue.Slug) {
			byName[name] = append(byName[name], issue)
		}
	}
	var out []Reclaim
	seen := map[string]bool{}
	var snap *Snapshot
	snapped := false
	for _, s := range ForProject(proj, root, ListSessions()) {
		name := BareName(s.Name)
		if !IsWorkerSession(name) || seen[name] || in.KeepNames[name] || len(byName[name]) == 0 {
			continue
		}
		seen[name] = true
		r, ok := reclaimWhy(byName[name], in)
		if !ok {
			continue
		}
		r.Name = name
		r.IdleSince = idleSince(proj, root, s, name)
		if r.IdleSince.IsZero() || in.Now.Sub(r.IdleSince) < in.Idle {
			continue
		}
		if !snapped && in.Snap != nil {
			snap, snapped = in.Snap(), true
		}
		if snap.Paused(proj, name) || AgentWorking(proj, name) {
			continue
		}
		if SharedSession(proj) == "" && SessionAttached(name) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// reclaimWhy is the Reclaim of a session the issues name, when each one is
// closed or waits on the owner; it names the first that waits, else the
// first.
func reclaimWhy(issues []*tracker.Issue, in ReclaimInput) (Reclaim, bool) {
	var pick *tracker.Issue
	why := ""
	for _, issue := range issues {
		if in.Closed != nil && in.Closed(issue) {
			if pick == nil {
				pick, why = issue, issue.Status
			}
			continue
		}
		w := ""
		if in.Waiting != nil {
			w = in.Waiting(issue)
		}
		if w == "" {
			return Reclaim{}, false
		}
		pick, why = issue, w
	}
	if pick == nil {
		return Reclaim{}, false
	}
	return Reclaim{Slug: pick.Slug, Status: pick.Status, Why: why}, true
}

// idleSince is when the agent last finished an issue-cli call for proj, or
// its launch when it made none.
func idleSince(proj *tracker.Project, root string, s Session, name string) time.Time {
	if rec, ok := clilog.Newest(proj.Slug, clilog.AgentPath(root, name)); ok && !rec.End().IsZero() {
		return rec.End()
	}
	at, _ := launchedAt(s, name, root)
	return at
}

// Line is "agent-x (human-testing, waits on …, idle since 14:05)".
func (r Reclaim) Line() string {
	what := r.Status
	if r.Why != "" && r.Why != r.Status {
		what += ", " + r.Why
	}
	return fmt.Sprintf("%s (%s, idle since %s)", r.Name, what, r.IdleSince.Local().Format("15:04"))
}
