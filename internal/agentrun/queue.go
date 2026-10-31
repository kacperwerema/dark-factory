package agentrun

import (
	"fmt"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/autostart"
	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/queue"
	"github.com/michal-franc/issue-viewer/internal/routing"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// QueueItem is one active queue item and, when it can't start now, why.
type QueueItem struct {
	queue.Item
	Hold string `json:"hold,omitempty"`
	// Words / How are what its start would quote: its own, or auto-start's
	// stored words (Standing).
	Words    string `json:"words,omitempty"`
	How      string `json:"how,omitempty"`
	Standing bool   `json:"standing,omitempty"`
	// Blocked is true when the hold is a queue blocker or depends_on.
	Blocked bool `json:"blocked,omitempty"`
}

// QueueVerdict is what queue next says: the item that starts (Start,
// Slug, Item), or why nothing does, with every active item's hold.
type QueueVerdict struct {
	autostart.Verdict
	Item  *QueueItem  `json:"item,omitempty"`
	Items []QueueItem `json:"items"`
	// Refill: no active item is left, only "think up more".
	Refill bool `json:"refill"`
	// Standing: auto-start is on, so items without their own words start
	// on the owner's stored ones; Limits names whose limits apply.
	Standing bool   `json:"standing"`
	Limits   string `json:"limits"`
}

// QueueInput is everything one queue verdict reads.
type QueueInput struct {
	Proj     *tracker.Project
	Issues   []*tracker.Issue
	Queue    *queue.Queue
	Auto     *autostart.State // auto-start's state; nil or off: the queue's limits
	YOLO     bool
	CIHold   string // "local CI red on main …" holds every start
	Last     *autostart.Start
	WorkRoot string
	Now      time.Time
}

// IssueShipped reports whether slug's issue is at its workflow's last
// status; found is false when no issue has that slug.
func IssueShipped(proj *tracker.Project, issues []*tracker.Issue, slug string) (done, found bool) {
	for _, issue := range issues {
		if strings.EqualFold(issue.Slug, slug) {
			return proj.LoadWorkflowForIssue(issue).IsLastStatus(issue.Status), true
		}
	}
	return false, false
}

// QueueReconcile marks done (no SHA) the active items whose issue reached
// its last status some other way than transition (close, a lead's move).
// It changes q only; the caller saves. Returns the numbers marked.
func QueueReconcile(proj *tracker.Project, issues []*tracker.Issue, q *queue.Queue, now time.Time) []int {
	var ids []int
	for i := range q.Items {
		it := &q.Items[i]
		if !it.Active() || it.Slug == "" {
			continue
		}
		if done, _ := IssueShipped(proj, issues, it.Slug); done {
			it.State, it.DoneAt = queue.Done, now
			ids = append(ids, it.ID)
		}
	}
	return ids
}

// QueueCIHold is "local CI red on main at abc1234: no new starts until it
// is green", or "" when CI is green, not configured or never ran.
func QueueCIHold(proj *tracker.Project, root string) string {
	if !proj.LoadWorkflow().CI.Enabled() {
		return ""
	}
	st, err := ci.ReadState(root)
	if err != nil || !st.IsRed() {
		return ""
	}
	return fmt.Sprintf("local CI red on %s at %s: no new starts until it is green", valueOrStr(st.Branch, "main"), ci.Short(st.LastSha))
}

func valueOrStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// QueueNoCap is the worker cap Decide sees when the queue has none.
const QueueNoCap = 1 << 20

// QueueEvaluate decides what queue next starts: each active item's hold
// in order, then auto-start's machine checks (Decide: mem-guard, CPU load,
// worker cap, the machine-wide gap) over the startable ones. An item
// starts only on the owner's words: its own quote, or auto-start's stored
// words while auto-start is on and not paused.
func QueueEvaluate(in QueueInput) QueueVerdict {
	q := in.Queue
	auto := in.Auto
	standing := auto != nil && auto.On && !auto.Paused
	out := QueueVerdict{Standing: standing}
	var st autostart.State
	if standing {
		st = *auto
		out.Limits = "auto-start's"
	} else {
		st = autostart.State{On: true, Below: q.BelowOr(), MaxWorkers: q.MaxWorkers, GapMinutes: autostart.DefaultGapMinutes}
		if st.MaxWorkers <= 0 {
			st.MaxWorkers = QueueNoCap
		}
		out.Limits = "the queue's"
	}

	bySlug := map[string]*tracker.Issue{}
	for _, issue := range in.Issues {
		bySlug[strings.ToLower(issue.Slug)] = issue
	}
	isDone := func(slug string) (bool, bool) { return IssueShipped(in.Proj, in.Issues, slug) }
	subs := Subscriptions()

	var cands []autostart.Candidate
	var open []int // index into out.Items per candidate
	for _, it := range q.ActiveItems() {
		qi := QueueItem{Item: it, Words: it.Quote, How: it.How}
		if qi.Words == "" && standing {
			qi.Words, qi.How, qi.Standing = auto.Words, "auto-start: "+auto.How, true
		}
		qi.Hold, qi.Blocked = queueHold(in, &qi, bySlug, isDone, subs)
		out.Items = append(out.Items, qi)
		if it.State == queue.Open {
			cands = append(cands, autostart.Candidate{Slug: it.Label(), Hold: qi.Hold})
			open = append(open, len(out.Items)-1)
		}
	}
	out.Refill = len(out.Items) == 0

	if in.CIHold != "" {
		out.Verdict = autostart.Verdict{Reason: in.CIHold, Below: st.Below, Cap: st.MaxWorkers}
		return out
	}
	if len(cands) == 0 {
		out.Verdict = autostart.Verdict{Reason: "nothing open in the queue", Below: st.Below, Cap: st.MaxWorkers}
		if out.Refill {
			out.Verdict.Reason = "the queue is empty: only \"think up more\" is left"
		}
		return out
	}
	din := autostart.Input{Project: in.Proj.Slug, State: &st, Last: in.Last, Now: in.Now, Candidates: cands}
	din.Memory = autostart.ReadMemory()
	din.CPU = autostart.ReadLoad()
	din.Workers = LiveWorkers(in.Proj, in.WorkRoot)
	out.Verdict = autostart.Decide(din)
	if out.Verdict.Start {
		for i, c := range cands {
			if c.Hold == "" {
				item := out.Items[open[i]]
				out.Item = &item
				out.Verdict.Slug = item.Slug
				break
			}
		}
	}
	return out
}

// queueHold is why one item can't start now ("" when it can), and whether
// that is a blocker.
func queueHold(in QueueInput, qi *QueueItem, bySlug map[string]*tracker.Issue, isDone func(string) (bool, bool), subs *routing.State) (string, bool) {
	q := in.Queue
	for _, b := range qi.BlockedOn {
		done, found := q.BlockerDone(b, isDone)
		if !found {
			return "blocked on " + b + " (not found)", true
		}
		if !done {
			return "blocked on " + b, true
		}
	}
	if qi.Slug == "" {
		return fmt.Sprintf("an idea: file it, then %s", tracker.InjectProject(in.Proj, fmt.Sprintf("issue-cli queue link %d <slug>", qi.ID))), false
	}
	issue, ok := bySlug[strings.ToLower(qi.Slug)]
	if !ok {
		return "no issue " + qi.Slug + " on the board", false
	}
	wf := in.Proj.LoadWorkflowForIssue(issue)
	if wf.IsLastStatus(issue.Status) {
		return "shipped (" + issue.Status + ")", false
	}
	if wf.IsSideStatus(issue.Status) {
		return "issue is " + issue.Status, false
	}
	if qi.State == queue.Started {
		if IsLive(in.Proj, tracker.AgentSessionName(issue.Slug)) {
			return "started, worker live", false
		}
		return "started, no live worker (re-dispatch by hand if it should go on)", false
	}
	if hold := candidateHold(in.Proj, issue, bySlug, in.YOLO); hold != "" {
		return hold, strings.HasPrefix(hold, "depends on")
	}
	if c := routing.Pick(wf.Routing, routing.IssueSubject(in.Proj, issue), "", subs, in.Now); c.Hold != "" {
		return c.Hold, false
	}
	if qi.Words == "" {
		return fmt.Sprintf("no owner's words: %s, or the owner turns auto-start on", tracker.InjectProject(in.Proj, fmt.Sprintf("issue-cli queue quote %d --quote \"<owner's words>\"", qi.ID))), false
	}
	return "", false
}
