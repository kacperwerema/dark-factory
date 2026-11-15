package viewer

import (
	"log"
	"os"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// The session sweep: every SessionSweepInterval the viewer closes worker
// sessions left behind (agentrun.FinishedSessions: agent-<slug> and
// limited-<slug> whose issue is closed, or whose pane is only a shell),
// the same sweep as issue-cli agents sweep. It only ever closes: it starts,
// pastes into and prompts nothing. Only the viewer holding viewer.json
// sweeps, so a sandbox viewer never closes the machine's real sessions.

const SessionSweepInterval = 10 * time.Minute

// Seams for tests: whether this viewer holds viewer.json, what the sweep
// finds, and the close itself.
var (
	holdsViewerRecord = func() bool {
		st, err := viewerstate.Read()
		return err == nil && st.PID == os.Getpid()
	}
	finishedSessions = agentrun.FinishedSessions
	takeSnapshot     = agentrun.TakeSnapshot
	closeSession     = func(proj *tracker.Project, name string) error { return agentrun.CloseSession(proj, name) }
)

// watchSessions sweeps on a tick until stop closes.
func (s *Server) watchSessions(stop <-chan struct{}) {
	ticker := time.NewTicker(SessionSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		if holdsViewerRecord() {
			sweepSessions(s.projects)
		}
	}
}

// sweepSessions closes every project's finished worker sessions and logs
// each close. A name an open issue holds in any project is kept: projects
// share one tmux server and the same agent-<slug> names.
func sweepSessions(projects []tracker.Project) []string {
	type loaded struct {
		proj   *tracker.Project
		issues []*tracker.Issue
	}
	var all []loaded
	keep := map[string]bool{}
	for i := range projects {
		proj := &projects[i]
		issues, err := proj.LoadIssues()
		if err != nil {
			continue
		}
		closed := tracker.ClosedIssueFunc(proj)
		for _, issue := range issues {
			if !closed(issue) {
				for _, name := range agentrun.WorkerSessions(issue.Slug) {
					keep[name] = true
				}
			}
		}
		all = append(all, loaded{proj, issues})
	}
	snap := takeSnapshot()
	var closedNames []string
	done := map[string]bool{}
	for _, l := range all {
		in := agentrun.SweepInput{Issues: l.issues, Closed: tracker.ClosedIssueFunc(l.proj), Snap: snap}
		for _, f := range finishedSessions(l.proj, projectRoot(l.proj), in) {
			// A closed issue's name an open issue elsewhere holds may be
			// that one's worker; a dead pane is dead whoever holds it.
			if done[f.Name] || (keep[f.Name] && !f.Dead) {
				continue
			}
			done[f.Name] = true
			if err := closeSession(l.proj, f.Name); err != nil {
				log.Printf("session sweep: %s: %s not closed: %v", l.proj.Slug, f.Name, err)
				continue
			}
			log.Printf("session sweep: %s: closed %s (%s)", l.proj.Slug, f.Name, f.Why)
			closedNames = append(closedNames, f.Name)
		}
	}
	return closedNames
}
