package viewer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// What waits on the owner on one issue is read here (ownerAttention,
// waitingOnOwner). A count of "waiting on you" is never taken from it:
// needsYouRows is the list and adds what no issue holds.

// GateInfo is a transition waiting on the human's approval.
type GateInfo struct {
	// Target is the status the approval is for.
	Target string
	// Ready is true when the approval is the only unmet requirement (every
	// validate rule of the transition passes).
	Ready bool
}

// Attention is what one issue needs from the owner right now.
type Attention struct {
	Gate         *GateInfo
	OpenQuestion bool
	// Question is the open question's text (the newest comment), for the
	// Needs you row that answers it in place.
	Question string
	// Clarify is how many of the issue's ## Clarify questions wait for the
	// owner's answer: open ones, and assumed ones no YOLO review holds.
	Clarify int
	// ClarifyQs are those questions, for the row's one-tap answers.
	ClarifyQs []tracker.ClarifyQuestion
	// PlaytestDue is set in human-testing when a playtest session names the
	// issue and has no judge (owner) run yet; Playtests lists those sessions.
	PlaytestDue bool
	Playtests   []string
	// ChecksDue is set in human-testing when a guided check script names the
	// issue and has no owner run yet; Checks lists those scripts.
	ChecksDue  bool
	Checks     []string
	Parked     bool
	ParkReason string
	// Since is when the issue entered its current status: the last stats
	// sidecar transition into it, else the file's mtime (SinceApprox).
	Since       time.Time
	SinceApprox bool
}

// WaitingItem is one thing the owner has to do on an issue.
type WaitingItem struct {
	Issue *tracker.Issue
	// Kind is gate, playtest, checks, question, clarify or partner.
	Kind string
	// Count is how many clarify questions wait (Kind clarify), Questions
	// those questions; Question is an open question's text (Kind question).
	Count     int
	Questions []tracker.ClarifyQuestion
	Question  string
	// Target is the gate's target status; Sessions the playtests (or check
	// scripts) to run.
	Target   string
	Sessions []string
	Since    time.Time
	// Approx: Since is the file's mtime, not a recorded transition.
	Approx bool
	// Partner is the department partner's stage waiting for the owner
	// (Kind partner), or riding on the issue's ready gate row (Kind gate)
	// so one decision is one row (partner_view.go).
	Partner *WbPartner
}

// pendingGate returns the status the issue waits on the human for: its next
// status, when that transition needs approval not yet given.
func pendingGate(wf *tracker.WorkflowConfig, issue *tracker.Issue) string {
	_, gate := tracker.NextGate(wf, issue)
	return gate
}

// openQuestion reports whether the issue's newest comment is a question:
// nobody has answered yet.
func openQuestion(comments []tracker.Comment) bool {
	if len(comments) == 0 {
		return false
	}
	last := strings.ToLower(strings.TrimSpace(comments[len(comments)-1].Text))
	return strings.HasPrefix(last, "question:")
}

// ownerAttention collects what the issue needs from the owner.
func ownerAttention(proj *tracker.Project, issue *tracker.Issue) Attention {
	return attentionWith(proj, issue, proj.LoadWorkflowForIssue(issue))
}

// attentionWith is ownerAttention with the issue's workflow already loaded
// (a pass over many issues reads it from a tracker.WorkflowMemo).
func attentionWith(proj *tracker.Project, issue *tracker.Issue, wf *tracker.WorkflowConfig) Attention {
	comments := issue.Comments()
	var a Attention
	if _, gate := tracker.NextGate(wf, issue); gate != "" {
		a.Gate = &GateInfo{Target: gate, Ready: tracker.WaitingOnGate(wf, issue, comments) != ""}
	}
	a.OpenQuestion = openQuestion(comments)
	if a.OpenQuestion {
		a.Question = strings.TrimSpace(comments[len(comments)-1].Text)
	}
	a.ClarifyQs = clarifyWaiting(issue, comments)
	a.Clarify = len(a.ClarifyQs)
	if wf.HasRole(issue.Status, tracker.StatusRoleOwnerTest) {
		if wf.PlaytestForOwner() {
			a.Playtests = playtestsToRun(playtestRoots(proj, issue, wf), issue.Slug)
			a.PlaytestDue = len(a.Playtests) > 0
		}
		a.Checks = checks.Due(checkRoots(proj, issue, wf), issue.Slug)
		a.ChecksDue = len(a.Checks) > 0
	}
	if tracker.IsParkedStatus(issue.Status) {
		a.Parked = true
		a.ParkReason = parkReason(issue.BodyRaw)
	}
	a.Since, a.SinceApprox = statusSince(issue)
	return a
}

// waitingOnOwner lists what the owner has to do across issues: ready gates,
// playtests and check scripts to run and open questions, oldest first. Callers scope it by
// passing a subset (one system's issues).
func waitingOnOwner(proj *tracker.Project, issues []*tracker.Issue) []WaitingItem {
	var items []WaitingItem
	wfs := proj.WorkflowMemo()
	for _, issue := range issues {
		a := attentionWith(proj, issue, wfs.ForIssue(issue))
		var pt *WbPartner
		if w := partnerOf(proj, issue); w != nil && w.Awaiting {
			pt = w
		}
		if a.Gate != nil && a.Gate.Ready {
			items = append(items, WaitingItem{Issue: issue, Kind: "gate", Target: a.Gate.Target, Since: a.Since, Approx: a.SinceApprox, Partner: pt})
		} else if pt != nil {
			since, approx := pt.Since, false
			if since.IsZero() {
				since, approx = a.Since, a.SinceApprox
			}
			items = append(items, WaitingItem{Issue: issue, Kind: "partner", Since: since, Approx: approx, Partner: pt})
		}
		if a.PlaytestDue {
			items = append(items, WaitingItem{Issue: issue, Kind: "playtest", Sessions: a.Playtests, Since: a.Since, Approx: a.SinceApprox})
		}
		if a.ChecksDue {
			items = append(items, WaitingItem{Issue: issue, Kind: "checks", Sessions: a.Checks, Since: a.Since, Approx: a.SinceApprox})
		}
		if a.OpenQuestion {
			items = append(items, WaitingItem{Issue: issue, Kind: "question", Question: a.Question, Since: a.Since, Approx: a.SinceApprox})
		}
		if a.Clarify > 0 {
			items = append(items, WaitingItem{Issue: issue, Kind: "clarify", Count: a.Clarify, Questions: a.ClarifyQs, Since: a.Since, Approx: a.SinceApprox})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Since.Before(items[j].Since) })
	return items
}

// statusSince is when the issue entered its current status, from the stats
// sidecar; approx is true when it falls back to the file's mtime.
func statusSince(issue *tracker.Issue) (since time.Time, approx bool) {
	store, _ := tracker.StatsOf(issue.FilePath)
	for i := len(store.Transitions) - 1; i >= 0; i-- {
		if strings.EqualFold(store.Transitions[i].To, issue.Status) {
			return store.Transitions[i].TS, false
		}
	}
	return issue.ModTime, true
}

// parkReason is the first line of the issue's ## Parked Record section,
// without the "**Why is it parked? …:**" field label the transition writes.
func parkReason(body string) string {
	text, _ := tracker.SectionText(body, "Parked Record")
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if strings.HasPrefix(line, "**") {
			if end := strings.Index(line[2:], "**"); end >= 0 {
				line = strings.TrimSpace(line[end+4:])
			}
		}
		if line != "" {
			return line
		}
	}
	return ""
}

// playtestRoots are the checkouts that may hold the issue's playtest sessions
// (and check scripts) and their runs: the project root and the issue's
// worktree.
func playtestRoots(proj *tracker.Project, issue *tracker.Issue, wf *tracker.WorkflowConfig) []string {
	roots := []string{projectRoot(proj)}
	if wt, _, enabled := resolveWorktree(resolveProjectWorkDir(proj), issue.Slug, wf); enabled && wt != "" && fileExists(wt) {
		roots = append(roots, wt)
	}
	return roots
}

// playtestsToRun returns the sessions under <root>/playtest/sessions that
// name slug and have no judge run under <root>/.playtest/runs/<session>/ in
// any of the roots.
func playtestsToRun(roots []string, slug string) []string {
	names := map[string]bool{}
	for _, root := range roots {
		for _, name := range playtestSessions(root, slug) {
			names[name] = true
		}
	}
	var due []string
	for name := range names {
		judged := false
		for _, root := range roots {
			if runs, _ := filepath.Glob(filepath.Join(root, ".playtest", "runs", name, "*-judge")); len(runs) > 0 {
				judged = true
				break
			}
		}
		if !judged {
			due = append(due, name)
		}
	}
	sort.Strings(due)
	return due
}

// playtestSessions names the sessions under <root>/playtest/sessions that
// name slug.
func playtestSessions(root, slug string) []string {
	var names []string
	files, _ := filepath.Glob(filepath.Join(root, "playtest", "sessions", "*.yaml"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var head struct {
			Session string `yaml:"session"`
			Issue   string `yaml:"issue"`
		}
		if yaml.Unmarshal(data, &head) != nil || head.Issue != slug {
			continue
		}
		name := head.Session
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(f), ".yaml")
		}
		names = append(names, name)
	}
	return names
}
