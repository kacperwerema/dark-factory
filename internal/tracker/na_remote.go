package tracker

import (
	"regexp"
	"strings"
)

// NANoGitRemote is the N/A form for a shipping step that needs a git
// remote (a push, a PR) in a project whose repo has none.
const NANoGitRemote = "no git remote"

var (
	remotePushRe = regexp.MustCompile(`(?i)\bpush(ed|es|ing)?\b`)
	// "PR" is matched in capitals only: "pr" in lower case is rarely a
	// pull request.
	remotePRRe = regexp.MustCompile(`\bPRs?\b|(?i:\bpull[- ]requests?\b)`)
	// A mention that forbids the step ("no push, no pull request", "never
	// open a PR") is not the step.
	remoteNegRe = regexp.MustCompile(`(?i)\b(no|never|not|don'?t|do not|without)\s+(git\s+)?(push|pull[- ]request|PR|open)`)
)

// IsRemoteStepBox reports whether a box's text names a step that needs a
// git remote: a push to the remote, or a PR opened or linked. A box that
// names either only to forbid it does not count.
func IsRemoteStepBox(text string) bool {
	if remoteNegRe.MatchString(text) {
		return false
	}
	return remotePushRe.MatchString(text) || remotePRRe.MatchString(text)
}

// TickNoRemoteBoxes ticks, in body, each open Shipping box that
// IsRemoteStepBox matches, its text carrying "(N/A: no git remote)". The
// owner's boxes are left alone. It returns the new body and the boxes it
// ticked, as they read now; the caller has checked the repo has no remote.
func TickNoRemoteBoxes(body string) (string, []CheckboxItem) {
	var ids []string
	for _, it := range ListCheckboxes(body) {
		if it.Checked || it.ID == "" || !NANoForm(it.Section) || IsOwnerMarked(it.Label()) || !IsRemoteStepBox(it.Label()) {
			continue
		}
		ids = append(ids, it.ID)
	}
	var ticked []CheckboxItem
	for _, id := range ids {
		it, st := ResolveCheckboxRef(ListCheckboxes(body), id)
		if st != RefFound {
			continue
		}
		next, edited, err := EditCheckboxText(body, id, WithNA(it.Label(), NANoGitRemote))
		if err != nil {
			continue
		}
		body = CheckLines(next, []int{edited.Line})
		edited.Checked = true
		ticked = append(ticked, edited)
	}
	return body, ticked
}

// TickedIDs lists the ids of boxes, for a one-line report.
func TickedIDs(items []CheckboxItem) string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return strings.Join(ids, ", ")
}
