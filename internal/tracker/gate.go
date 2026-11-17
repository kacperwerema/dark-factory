package tracker

import "strings"

// NextGate returns the issue's next status and, when that transition needs
// approval not yet given, the status the approval is for.
func NextGate(wf *WorkflowConfig, issue *Issue) (next, gate string) {
	order := wf.GetStatusOrder()
	idx := wf.GetStatusIndex(issue.Status)
	if idx < 0 || idx+1 >= len(order) {
		return "", ""
	}
	next = order[idx+1]
	required := wf.RequiredHumanApproval(issue.Status, next)
	if required == "" || strings.EqualFold(issue.HumanApproval, required) {
		return next, ""
	}
	return next, required
}

// WaitingOnGate is the status the issue waits on the owner to approve when
// that approval is all its next transition still needs; "" otherwise. It is
// the Status page's "waiting on you" gate rule, and what dispatch refuses
// on: a worker started there would only stop at the same gate.
func WaitingOnGate(wf *WorkflowConfig, issue *Issue, comments []Comment) string {
	next, gate := NextGate(wf, issue)
	if gate == "" || !wf.ReadyForApproval(issue, issue.Status, next, comments) || statusBoxesOpen(wf, issue) {
		return ""
	}
	return gate
}

// statusBoxesOpen reports whether a section the move into the issue's
// status appended (Implementation on entering in progress) still has an
// open box: that is the worker's work, so a gate after it is not yet the
// owner's even when the gate's own rules don't check those boxes. A box
// only the owner may tick (IsOwnerBox: Human Testing's HT1, one marked
// "(owner)") is no worker's work: it waits on the owner like the gate.
func statusBoxesOpen(wf *WorkflowConfig, issue *Issue) bool {
	order := wf.GetStatusOrder()
	idx := wf.GetStatusIndex(issue.Status)
	if idx <= 0 || idx >= len(order) {
		return false
	}
	appended := map[string]bool{}
	for _, a := range wf.transitionActions(order[idx-1], issue.Status) {
		if a.Type == "append_section" && strings.TrimSpace(a.Title) != "" {
			appended[strings.ToLower(strings.TrimSpace(a.Title))] = true
		}
	}
	for _, it := range ListCheckboxes(issue.BodyRaw) {
		if !it.Checked && appended[strings.ToLower(strings.TrimSpace(it.Section))] && !IsOwnerBox(it) {
			return true
		}
	}
	return false
}

// HumanTestingSection is the section whose boxes are the owner's.
const HumanTestingSection = "Human Testing"

// IsOwnerBox reports whether only the owner may tick the box by its place
// and text alone: a Human Testing box, or one marked "(owner)"
// (IsOwnerMarked). proof.OwnerBoxWhy adds the owner's boxes a proof report
// knows (owner-only criteria, guided-check and playtest judgement steps).
func IsOwnerBox(it CheckboxItem) bool {
	return strings.EqualFold(strings.TrimSpace(it.Section), HumanTestingSection) || IsOwnerMarked(it.Label())
}
