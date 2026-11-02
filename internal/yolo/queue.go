package yolo

import (
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Pass is one gate an issue passed in YOLO without the owner.
type Pass struct {
	Slug   string `json:"slug"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status"`
	// From is the status the issue left when the gate passed.
	From string `json:"from,omitempty"`
	By   string `json:"by,omitempty"`
	At   string `json:"at"`
	// Comment is the approval comment's id on the issue.
	Comment int `json:"comment"`
	// Review is the owner's verdict ("agreed", "back to <status>"); empty
	// while the pass waits in the queue.
	Review string `json:"review,omitempty"`
	// Check is set on an owner check instead of a gate: the id of a box only
	// the owner can check, which the gate passed without (AC6). What is its
	// text, Why what makes it the owner's.
	Check string `json:"check,omitempty"`
	What  string `json:"what,omitempty"`
	Why   string `json:"why,omitempty"`
	// Question is set on an assumed answer instead: the id (Q2) of a
	// ## Clarify question the agent answered alone after YOLO passed the
	// issue's gate; What is the question, Why the assumed answer and reason.
	Question string `json:"question,omitempty"`
	// Decision is set on a lead's relayed owner act on a hard decision
	// instead: the ref; What is what the act did, By the lead.
	Decision string `json:"decision,omitempty"`
	// Hold is set with Decision on a decision hold a gate passed in YOLO:
	// the rule (decisions_cited, citations_current); Why is why it held.
	Hold string `json:"hold,omitempty"`
}

// Key is what the owner's review names: the box of an owner check, else the
// approved status.
func (p Pass) Key() string {
	if p.Check != "" {
		return p.Check
	}
	if p.Question != "" {
		return p.Question
	}
	if p.Decision != "" {
		return p.Decision
	}
	return p.Status
}

// Label is the entry in a few words: "AC6: owner check, not done", else
// the approved status.
func (p Pass) Label() string {
	if p.Check != "" {
		return p.Check + ": owner check, not done"
	}
	if p.Question != "" {
		return p.Question + ": assumed answer"
	}
	if p.Decision != "" && p.Hold != "" {
		return p.Decision + ": " + p.Hold + " passed in YOLO"
	}
	if p.Decision != "" {
		return p.Decision + ": hard decision relayed"
	}
	return p.Status
}

// Passes is every YOLO pass on one issue, oldest first, each with the
// review that settled it, if any: a yolo-review: for the same status after
// the pass.
func Passes(issue *tracker.Issue, comments []tracker.Comment) []Pass {
	var out []Pass
	for _, c := range comments {
		if a, ok := tracker.ParseApprovalComment(c.Text); ok && a.YOLO() {
			by, from := a.YOLOFrom()
			for _, s := range a.Statuses {
				out = append(out, Pass{Slug: issue.Slug, Title: issue.Title, Status: s, From: from, By: by, At: c.Date, Comment: c.ID})
			}
			continue
		}
		if r, ok := tracker.ParseYOLOReviewComment(c.Text); ok {
			for i := range out {
				if out[i].Review == "" && strings.EqualFold(out[i].Status, r.Status) {
					out[i].Review = r.Verdict
				}
			}
		}
	}
	return out
}

// OwnerChecks is every box a YOLO gate passed without on one issue (the
// yolo-owner-check: comments), oldest first, once per box. One is settled
// when the box is ticked ("ticked"), by the owner's review naming it, or
// when the owner sends the issue back after it ("sent back"; a later gate
// queues it again if it is still unticked).
func OwnerChecks(issue *tracker.Issue, comments []tracker.Comment) []Pass {
	var out []Pass
	seen := map[string]int{}
	for _, c := range comments {
		if oc, ok := tracker.ParseYOLOOwnerCheckComment(c.Text); ok {
			key := strings.ToUpper(oc.ID)
			if i, dup := seen[key]; dup && out[i].Review == "" {
				continue
			}
			seen[key] = len(out)
			out = append(out, Pass{Slug: issue.Slug, Title: issue.Title, From: oc.From, By: c.Author, At: c.Date, Comment: c.ID, Check: oc.ID, What: oc.Text, Why: oc.Why})
			continue
		}
		if r, ok := tracker.ParseYOLOReviewComment(c.Text); ok {
			for i := range out {
				if out[i].Review == "" && strings.EqualFold(out[i].Check, r.Status) {
					out[i].Review = r.Verdict
				}
			}
			continue
		}
		if c.Author == tracker.OwnerAuthor && tracker.IsRejectedComment(c.Text) {
			for i := range out {
				if out[i].Review == "" {
					out[i].Review = "sent back"
				}
			}
		}
	}
	ticked := map[string]bool{}
	for _, it := range tracker.ListCheckboxes(issue.BodyRaw) {
		if it.Checked && it.ID != "" {
			ticked[strings.ToUpper(it.ID)] = true
		}
	}
	for i := range out {
		if out[i].Review == "" && ticked[strings.ToUpper(out[i].Check)] {
			out[i].Review = "ticked"
		}
	}
	return out
}

// Questions is every assumed answer in the issue's ## Clarify once a YOLO
// gate passed on it, oldest pass first, each with the review that settled
// it: a yolo-review: naming its Q-id. An answer the owner gave since is no
// longer assumed and drops out. Each is dated by the issue's first YOLO
// pass, the one that left the design with them, and goes back to its from.
func Questions(issue *tracker.Issue, comments []tracker.Comment) []Pass {
	var first *Pass
	reviews := map[string]string{}
	for _, c := range comments {
		if a, ok := tracker.ParseApprovalComment(c.Text); ok && a.YOLO() && first == nil {
			by, from := a.YOLOFrom()
			first = &Pass{By: by, From: from, At: c.Date, Comment: c.ID}
			continue
		}
		if r, ok := tracker.ParseYOLOReviewComment(c.Text); ok && first != nil {
			if id := tracker.NormalizeQuestionID(r.Status); id != "" && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(r.Status)), "Q") {
				reviews[id] = r.Verdict
			}
		}
	}
	if first == nil {
		return nil
	}
	var out []Pass
	for _, q := range tracker.ParseClarify(issue.BodyRaw).Assumed() {
		why := q.Answer.Reason
		if ch := q.Chosen(); ch != nil {
			why = "assumed " + ch.Label + " — " + q.Answer.Reason
		}
		p := *first
		p.Slug, p.Title, p.Question, p.What, p.Why, p.Review = issue.Slug, issue.Title, q.ID, q.Text, why, reviews[q.ID]
		out = append(out, p)
	}
	return out
}

// Decisions is every hard-decision act a lead relayed while YOLO was on (a
// decision: comment ending "(YOLO)") and every decision hold a gate passed
// in YOLO (a yolo-hold: comment), oldest first, each with the review that
// settled it: a yolo-review: naming its ref.
func Decisions(issue *tracker.Issue, comments []tracker.Comment) []Pass {
	var out []Pass
	for _, c := range comments {
		if d, ok := tracker.ParseDecisionRelayComment(c.Text); ok && d.YOLO {
			out = append(out, Pass{Slug: issue.Slug, Title: issue.Title, Decision: d.Ref, What: d.Action, By: d.Lead, At: c.Date, Comment: c.ID})
			continue
		}
		if h, from, ok := tracker.ParseYOLOHoldComment(c.Text); ok {
			out = append(out, Pass{Slug: issue.Slug, Title: issue.Title, Decision: h.Ref, Hold: h.Rule, What: h.Why, Why: h.Why, From: from, By: c.Author, At: c.Date, Comment: c.ID})
			continue
		}
		if r, ok := tracker.ParseYOLOReviewComment(c.Text); ok {
			for i := range out {
				if out[i].Review == "" && strings.EqualFold(out[i].Decision, r.Status) {
					out[i].Review = r.Verdict
				}
			}
		}
	}
	return out
}

// Queue is the passes and owner checks across issues still waiting for the
// owner's review.
func Queue(issues []*tracker.Issue) []Pass {
	var out []Pass
	for _, issue := range issues {
		comments, err := tracker.LoadComments(issue.FilePath)
		if err != nil || len(comments) == 0 {
			continue
		}
		out = append(out, Unreviewed(issue, comments)...)
	}
	return out
}

// Unreviewed is the issue's passes, then its owner checks, then its
// assumed answers, then its relayed hard decisions, still waiting for review.
func Unreviewed(issue *tracker.Issue, comments []tracker.Comment) []Pass {
	var out []Pass
	all := append(Passes(issue, comments), OwnerChecks(issue, comments)...)
	all = append(all, Questions(issue, comments)...)
	for _, p := range append(all, Decisions(issue, comments)...) {
		if p.Review == "" {
			out = append(out, p)
		}
	}
	return out
}

// BackTo is the status an issue goes back to when the owner undoes p: the
// status it left, else the first forward step whose gate approves p.Status.
func BackTo(wf *tracker.WorkflowConfig, p Pass) string {
	if p.From != "" {
		return p.From
	}
	status := p.Status
	order := wf.GetStatusOrder()
	for i := 0; i+1 < len(order); i++ {
		for _, to := range order[i+1:] {
			if strings.EqualFold(wf.RequiredHumanApproval(order[i], to), status) {
				return order[i]
			}
		}
	}
	return ""
}

// Gate is one gate YOLO lets through, for the turn-on dialog.
type Gate struct {
	From, To, Approves string
}

// Gates is the project's gates YOLO passes: each forward step between two
// neighbouring statuses on the base path that needs a human approval and
// isn't owner_only (OwnerGates lists those).
func Gates(wf *tracker.WorkflowConfig) []Gate {
	var out []Gate
	order := wf.GetStatusOrder()
	for i := 0; i+1 < len(order); i++ {
		to := order[i+1]
		if wf.IsSideStatus(to) {
			continue
		}
		if req := wf.RequiredHumanApproval(order[i], to); req != "" && !wf.OwnerOnlyGate(order[i], to) {
			out = append(out, Gate{From: order[i], To: to, Approves: req})
		}
	}
	return out
}

// OwnerGate is one gate YOLO never passes, for yolo status and the band.
type OwnerGate struct {
	Department string   `json:"department,omitempty"` // a department's own workflow file; "" Product's
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
	Approves   string   `json:"approves,omitempty"`
	Types      []string `json:"types,omitempty"` // the types whose path has it; none: every path
	Why        string   `json:"why"`
}

// Label is "Marketing: owner review → publish (piece, seo-pass)", or the
// rule itself for one that holds everywhere.
func (g OwnerGate) Label() string {
	if g.From == "" {
		return g.Why
	}
	s := g.From + " → " + g.To
	if g.Department != "" {
		s = g.Department + ": " + s
	}
	if len(g.Types) > 0 {
		s += " (" + strings.Join(g.Types, ", ") + ")"
	}
	return s
}

// OwnerGates lists the gates YOLO never passes in proj: each owner_only
// approval on the paths of Product's workflow and of every department
// with its own file, then the rules that hold everywhere (the Human
// Testing approval, experiments' gates, release publish unless the owner
// unlocked releases).
func OwnerGates(proj *tracker.Project, v Valves) []OwnerGate {
	wf := proj.LoadWorkflow()
	out := WorkflowOwnerGates(wf, "")
	for _, d := range proj.WorkflowDepartments() {
		if dwf, err := proj.WorkflowFor(d.Name); err == nil {
			out = append(out, WorkflowOwnerGates(dwf, d.Name)...)
		}
	}
	if wf != nil {
		for _, s := range wf.Statuses {
			if strings.EqualFold(strings.ReplaceAll(s.Name, " ", "-"), "human-testing") {
				out = append(out, OwnerGate{Why: "the Human Testing approval: the human's own"})
				break
			}
		}
	}
	out = append(out, OwnerGate{Why: "every gate of an issue labelled experiment: the owner adopts experiments"})
	if !v.Releases { // unlocked: the valves line says so
		out = append(out, OwnerGate{Why: "release publish: external releases wait for the owner"})
	}
	return out
}

// WorkflowOwnerGates is wf's owner_only gates between neighbouring statuses
// on each type's path (the base path without types), once each, with the
// types that walk it when not all do.
func WorkflowOwnerGates(wf *tracker.WorkflowConfig, dept string) []OwnerGate {
	if wf == nil {
		return nil
	}
	var out []OwnerGate
	index := map[string]int{}
	add := func(scoped *tracker.WorkflowConfig, typ string) {
		order := scoped.GetStatusOrder()
		for i := 0; i+1 < len(order); i++ {
			from, to := order[i], order[i+1]
			if !scoped.OwnerOnlyGate(from, to) {
				continue
			}
			key := from + "\x00" + to
			if j, ok := index[key]; ok {
				if typ != "" {
					out[j].Types = append(out[j].Types, typ)
				}
				continue
			}
			g := OwnerGate{Department: dept, From: from, To: to, Approves: scoped.RequiredHumanApproval(from, to),
				Why: "owner_only: spends money or publishes outside the machine"}
			if typ != "" {
				g.Types = []string{typ}
			}
			index[key] = len(out)
			out = append(out, g)
		}
	}
	names := wf.TypeNames()
	if len(names) == 0 {
		add(wf, "")
		return out
	}
	for _, name := range names {
		add(wf.ForType(name), name)
	}
	for i := range out {
		if len(out[i].Types) == len(names) {
			out[i].Types = nil // every type walks it
		}
	}
	return out
}

// Entry is one issue waiting for the owner's review after the fact: the
// review unit and what every count counts. Gates are its gate passes not
// yet reviewed (one Agree covers them all), Checks the owner checks a gate
// left open (each ticked or agreed on its own), Questions the answers the
// agent assumed in its clarify round (the same Agree covers them).
type Entry struct {
	Slug      string `json:"slug"`
	Title     string `json:"title,omitempty"`
	Gates     []Pass `json:"gates"`
	Checks    []Pass `json:"checks"`
	Questions []Pass `json:"questions,omitempty"`
	// Decisions are hard-decision acts a lead relayed (the same Agree).
	Decisions []Pass `json:"decisions,omitempty"`
	// Oldest is the oldest waiting item's comment date.
	Oldest string `json:"oldest"`
}

// Detail is "3 gates, 2 owner checks" (a part that is zero is left out).
func (e Entry) Detail() string {
	var parts []string
	if n := len(e.Gates); n == 1 {
		parts = append(parts, "1 gate")
	} else if n > 1 {
		parts = append(parts, strconv.Itoa(n)+" gates")
	}
	if n := len(e.Checks); n == 1 {
		parts = append(parts, "1 owner check")
	} else if n > 1 {
		parts = append(parts, strconv.Itoa(n)+" owner checks")
	}
	if n := len(e.Questions); n == 1 {
		parts = append(parts, "1 assumed answer")
	} else if n > 1 {
		parts = append(parts, strconv.Itoa(n)+" assumed answers")
	}
	var relayed, holds int
	for _, d := range e.Decisions {
		if d.Hold != "" {
			holds++
		} else {
			relayed++
		}
	}
	if relayed == 1 {
		parts = append(parts, "1 relayed decision")
	} else if relayed > 1 {
		parts = append(parts, strconv.Itoa(relayed)+" relayed decisions")
	}
	if holds == 1 {
		parts = append(parts, "1 decision hold")
	} else if holds > 1 {
		parts = append(parts, strconv.Itoa(holds)+" decision holds")
	}
	return strings.Join(parts, ", ")
}

// Items is the entry's gates, then its owner checks, then its assumed
// answers.
func (e Entry) Items() []Pass {
	return append(append(append(append([]Pass(nil), e.Gates...), e.Checks...), e.Questions...), e.Decisions...)
}

// Entries groups passes by issue, in the order each issue first appears;
// each entry's Oldest is its oldest item by date.
func Entries(queue []Pass) []Entry {
	var out []Entry
	index := map[string]int{}
	oldest := map[string]time.Time{}
	for _, p := range queue {
		key := strings.ToLower(p.Slug)
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, Entry{Slug: p.Slug, Title: p.Title, Oldest: p.At})
		}
		if p.Check != "" {
			out[i].Checks = append(out[i].Checks, p)
		} else if p.Question != "" {
			out[i].Questions = append(out[i].Questions, p)
		} else if p.Decision != "" {
			out[i].Decisions = append(out[i].Decisions, p)
		} else {
			out[i].Gates = append(out[i].Gates, p)
		}
		if t, ok := tracker.CommentTime(p.At); ok {
			if o, seen := oldest[key]; !seen || t.Before(o) {
				oldest[key] = t
				out[i].Oldest = p.At
			}
		}
	}
	return out
}

// QueueEntries is the queue across issues, one entry per issue.
func QueueEntries(issues []*tracker.Issue) []Entry { return Entries(Queue(issues)) }

// Totals is how many gates and owner checks the entries hold.
func Totals(entries []Entry) (gates, checks int) {
	for _, e := range entries {
		gates += len(e.Gates)
		checks += len(e.Checks)
	}
	return gates, checks
}
