package agentrun

import (
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// autostartRank is issue-cli next's priority order.
var autostartRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "": 4}

// AutostartCandidates is the project's next order, the way issue-cli next lists its
// backlog: unclaimed backlog issues of the project's version (all versions
// when it has none), departments on their own workflow file left out, by
// priority and then file order. Each says why it can't start now, if so.
// yoloOn lets an issue that only waits on a gate through: YOLO passes it.
func AutostartCandidates(proj *tracker.Project, issues []*tracker.Issue, yoloOn bool) []autostart.Candidate {
	var backlog []*tracker.Issue
	for _, issue := range issues {
		if issue.Status != "backlog" || issue.Assignee != "" {
			continue
		}
		if proj.Version != "" && issue.Version != proj.Version {
			continue
		}
		if proj.WorkflowDepartment(issue) != "" {
			continue
		}
		backlog = append(backlog, issue)
	}
	sort.SliceStable(backlog, func(i, j int) bool {
		return autostartRank[backlog[i].Priority] < autostartRank[backlog[j].Priority]
	})
	bySlug := map[string]*tracker.Issue{}
	for _, issue := range issues {
		bySlug[strings.ToLower(issue.Slug)] = issue
	}
	out := make([]autostart.Candidate, 0, len(backlog))
	subs, now := Subscriptions(), time.Now()
	for _, issue := range backlog {
		hold := candidateHold(proj, issue, bySlug, yoloOn)
		if hold == "" {
			// Routing: an issue whose agents are all resting waits; one
			// routed to a resting agent with a free fallback starts there.
			if c := routing.Pick(proj.LoadWorkflowForIssue(issue).Routing, routing.IssueSubject(proj, issue), "", subs, now); c.Hold != "" {
				hold = c.Hold
			}
		}
		out = append(out, autostart.Candidate{Slug: issue.Slug, Hold: hold})
	}
	return out
}

func candidateHold(proj *tracker.Project, issue *tracker.Issue, bySlug map[string]*tracker.Issue, yoloOn bool) string {
	if IsLive(proj, tracker.AgentSessionName(issue.Slug)) {
		return "worker live"
	}
	for _, dep := range tracker.DependsOn(issue) {
		d, ok := bySlug[dep]
		if !ok {
			return "depends on " + dep + " (not found)"
		}
		if wf := proj.LoadWorkflowForIssue(d); !wf.IsLastStatus(d.Status) {
			return "depends on " + dep + " (" + d.Status + ")"
		}
	}
	if !yoloOn {
		comments, _ := tracker.LoadComments(issue.FilePath)
		if gate := tracker.WaitingOnGate(proj.LoadWorkflowForIssue(issue), issue, comments); gate != "" {
			return "waiting on " + gate
		}
	}
	return ""
}

// LiveWorkers counts the project's live worker sessions (agent-*), without the
// lead. root is the project checkout ForProject scopes standalone sessions to.
func LiveWorkers(proj *tracker.Project, root string) int {
	n := 0
	for _, s := range ForProject(proj, root, ListSessions()) {
		if strings.HasPrefix(BareName(s.Name), "agent-") {
			n++
		}
	}
	return n
}

// AutostartEvaluate reads what Decide needs for proj and decides: the state under
// stateRoot (the main checkout), mem-guard, the live workers under workRoot
// and the next order. last is the machine-wide last start (read under the
// lock when starting). only, when set, limits the pick to that issue, so
// dispatch --autostart can't start something the next order wouldn't.
func AutostartEvaluate(proj *tracker.Project, issues []*tracker.Issue, stateRoot, workRoot string, yoloOn bool, last *autostart.Start, only string, now time.Time) (autostart.Verdict, *autostart.State, error) {
	st, err := autostart.Load(stateRoot, proj.Slug)
	if err != nil {
		return autostart.Verdict{}, nil, err
	}
	in := autostart.Input{Project: proj.Slug, State: st, Last: last, Now: now}
	if st.On && !st.Paused {
		ScanLimits(proj, workRoot, now)
		in.Memory = autostart.ReadMemory()
		in.CPU = autostart.ReadLoad()
		in.Workers = LiveWorkers(proj, workRoot)
		in.Candidates = AutostartCandidates(proj, issues, yoloOn)
		if only != "" {
			in.Candidates = autostartOnly(in.Candidates, only)
		}
	}
	return autostart.Decide(in), st, nil
}

func autostartOnly(cands []autostart.Candidate, slug string) []autostart.Candidate {
	for _, c := range cands {
		if strings.EqualFold(c.Slug, slug) {
			return []autostart.Candidate{c}
		}
	}
	return []autostart.Candidate{{Slug: slug, Hold: "not in the next order (auto-start starts unclaimed backlog issues)"}}
}
