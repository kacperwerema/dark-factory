package proof

import (
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Owner-only criteria: some criteria only the owner can prove — one a
// playtest judgement step shows, one a guided check's machine run left to
// the owner (needs your eyes, or skipped), or one marked "(owner)". A
// trailer, test or value never ticks such a box; only a passing judgement
// step (the owner's playtest) or the owner does. Under YOLO the gate passes
// without them and each waits in the after-the-fact queue (YOLOGateError,
// OwnerWaiting).
//
// Other boxes are the owner's too: the ## Human Testing section's (HT1,
// "Human verification complete if required") and any box marked "(owner)",
// such as the bugfix path's Manual repro box. Nothing gates on them; under
// YOLO they wait in the same queue.

// humanTestingSection is the section whose boxes are the owner's.
const humanTestingSection = "Human Testing"

// OwnerItem is a box only the owner can check, still waiting for them.
type OwnerItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// Why says what makes it the owner's ("playtest judgement step TP9").
	Why string `json:"why"`
}

// OwnerProven reports whether the owner proved the criterion: the owner's
// playtest passed a judgement step it links.
func (c Criterion) OwnerProven() bool {
	for _, l := range c.Links {
		if l.Owner && l.OwnerPassed {
			return true
		}
	}
	return false
}

// Tickable reports whether the criterion's proof may tick its box: it is
// proven, and when only the owner can prove it, the owner did.
func (c Criterion) Tickable() bool {
	if c.Owner != "" {
		return c.OwnerProven()
	}
	return c.Proven()
}

// OwnerWaiting is the unticked boxes only the owner can check that they
// have not: owner-only criteria, then other boxes a guided check left to
// the owner.
func (r Report) OwnerWaiting() []OwnerItem {
	var out []OwnerItem
	for _, c := range r.Criteria {
		if c.Owner != "" && !c.Checked && !c.OwnerProven() {
			out = append(out, OwnerItem{ID: c.ID, Text: c.Text, Why: c.Owner})
		}
	}
	return append(out, r.OwnerBoxes...)
}

// YOLOGateError is GateError while YOLO is on: owner-only criteria don't
// block, they wait for the owner after the fact.
func YOLOGateError(r Report) error {
	var blocking []Criterion
	for _, c := range r.Unproven() {
		if c.Owner == "" {
			blocking = append(blocking, c)
		}
	}
	return gateError(r, blocking)
}

// IsOwnerCriterion reports whether a box is marked for the owner:
// "(owner)" anywhere outside quotes, or a start like "Owner visual check …"
// (tracker.IsOwnerMarked, the one rule lint reads too).
func IsOwnerCriterion(text string) bool {
	return tracker.IsOwnerMarked(text)
}

// sessionStep is one step as the newest run of its session that has the
// step gives it.
type sessionStep struct {
	session string
	step    timeline.PlaytestStep
}

// newestSteps is each session's steps as its newest run gives them, newest
// run first: an older run's judgement_step or proves: never outlives the
// session file that dropped it. A step the newest run left out (a -step run
// of other steps) keeps its own newest run. runs are oldest first.
func newestSteps(runs []timeline.PlaytestRun) []sessionStep {
	var out []sessionStep
	seen := map[string]bool{}
	for i := len(runs) - 1; i >= 0; i-- {
		for _, st := range runs[i].Steps {
			key := runs[i].Session + "|" + strings.ToUpper(strings.TrimSpace(st.ID))
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, sessionStep{session: runs[i].Session, step: st})
		}
	}
	return out
}

// judgementSteps maps the ids of the steps the newest run of their session
// marks as the owner's judgement to that session. A step its session file
// no longer has (current, when the file is there) is not one.
func judgementSteps(runs []timeline.PlaytestRun, current map[string]map[string]bool) map[string]string {
	out := map[string]string{}
	for _, s := range newestSteps(runs) {
		id := strings.ToUpper(strings.TrimSpace(s.step.ID))
		if ids, ok := current[s.session]; ok && !ids[id] {
			continue
		}
		if s.step.Judgement {
			if _, dup := out[id]; !dup {
				out[id] = s.session
			}
		}
	}
	return out
}

// judgementHint is what to do about a box a judgement step makes the
// owner's when the step is really automated.
func judgementHint(step, session string) string {
	return step + " is a judgement step (do:/expect: in " + session + "); drop them if the step is automated, then re-run make ux-test"
}

// JudgementHint is the hint for box id when a playtest judgement step makes
// it the owner's: the box is that step, or a criterion a judgement step
// proves. "" otherwise.
func (r Report) JudgementHint(id string) string {
	id = strings.ToUpper(strings.TrimSpace(id))
	if session, ok := r.judgement[id]; ok {
		return judgementHint(id, session)
	}
	for _, c := range r.Criteria {
		if !strings.EqualFold(c.ID, id) {
			continue
		}
		for _, l := range c.Links {
			if session, ok := r.judgement[strings.ToUpper(l.Ref)]; ok && l.Kind == tracker.ProofStep {
				return judgementHint(strings.ToUpper(l.Ref), session)
			}
		}
	}
	return ""
}

// ownerPassedSteps is the judgement steps whose latest verdict in the
// owner's own playtest (judge-mode runs) is pass.
func ownerPassedSteps(runs []timeline.PlaytestRun) map[string]bool {
	out := map[string]bool{}
	for _, run := range runs { // oldest first
		if run.Mode != "judge" {
			continue
		}
		for _, st := range run.Steps {
			if !st.Judgement {
				continue
			}
			v := st.Status
			if st.Judge != nil && st.Judge.Status != "" {
				v = st.Judge.Status
			}
			if v == "pass" || v == "fail" {
				out[strings.ToUpper(st.ID)] = v == "pass"
			}
		}
	}
	return out
}

// guidedOwnerBoxes is, by box id, why a guided check left it to the owner:
// the newest machine run of each of the issue's scripts, in its worktree,
// the git worktree on its branch, or the project checkout.
func guidedOwnerBoxes(root, worktree, slug string) map[string]string {
	roots := []string{worktree}
	if wt := checks.IssueWorktree(root, slug); wt != "" && filepath.Clean(wt) != filepath.Clean(worktree) {
		roots = append(roots, wt)
	}
	roots = append(roots, root)
	out := map[string]string{}
	for _, s := range checks.Find(roots, slug) {
		for _, st := range checks.MachineRunOwnerSteps(s.Root, s.Name) {
			left := "needs your eyes"
			if st.Verdict == "skip" {
				left = "skipped"
			}
			for _, b := range st.Boxes {
				if id := strings.ToUpper(b); out[id] == "" {
					out[id] = "guided check " + s.Name + " step " + st.ID + ": " + left
				}
			}
		}
	}
	return out
}

// markOwner sets each criterion's Owner and its judgement step links, and
// lists the other unticked boxes that are the owner's: a guided check left
// them, they are a playtest judgement step (the session's step id is the
// box id, as "TP7 … (owner's look)"), they are in ## Human Testing, or they
// are marked "(owner)".
func (r *Report) markOwner(issue *tracker.Issue, judgement map[string]string, ownerPassed map[string]bool, guided map[string]string) {
	r.judgement = judgement
	isCriterion := map[string]bool{}
	for i := range r.Criteria {
		c := &r.Criteria[i]
		isCriterion[strings.ToUpper(c.ID)] = true
		for j := range c.Links {
			l := &c.Links[j]
			if _, j := judgement[strings.ToUpper(l.Ref)]; j && l.Kind == tracker.ProofStep {
				l.Owner = true
				l.OwnerPassed = ownerPassed[strings.ToUpper(l.Ref)]
				if c.Owner == "" {
					c.Owner = "playtest judgement step " + l.Ref
				}
			}
		}
		if c.Owner == "" {
			c.Owner = guided[strings.ToUpper(c.ID)]
		}
		if c.Owner == "" && IsOwnerCriterion(c.Text) {
			c.Owner = "marked (owner)"
		}
	}
	for _, it := range tracker.ListCheckboxes(issue.BodyRaw) {
		id := strings.ToUpper(it.ID)
		if it.Checked || id == "" || isCriterion[id] {
			continue
		}
		why := guided[id]
		if _, j := judgement[id]; why == "" && j {
			why = "playtest judgement step " + it.ID
		}
		if why == "" && strings.EqualFold(it.Section, humanTestingSection) {
			why = "Human Testing box"
		}
		if why == "" && IsOwnerCriterion(it.Label()) {
			why = "marked (owner)"
		}
		if why != "" {
			r.OwnerBoxes = append(r.OwnerBoxes, OwnerItem{ID: it.ID, Text: it.Label(), Why: why})
		}
	}
}
