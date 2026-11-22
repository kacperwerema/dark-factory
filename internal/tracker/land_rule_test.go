package tracker

import (
	"strings"
	"testing"
)

// TestWorkerBriefingSaysTheWorkerLands: a workflow whose merge status tells
// the worker to merge or fast-forward gets the one rule on who lands the
// branch, the worker with issue-cli ship, and that a brief line saying the
// lead merges doesn't change it (a worker once waited 31 minutes on that
// contradiction).
func TestWorkerBriefingSaysTheWorkerLands(t *testing.T) {
	wf := &WorkflowConfig{Statuses: []WorkflowStatus{
		{Name: "in progress"},
		{Name: "shipping", Prompt: "Fast-forward main to the issue branch (`git merge --ff-only`)."},
		{Name: "done"},
	}}
	got := LandPromptLine(wf)
	for _, want := range []string{"At shipping you land your own branch", "`issue-cli ship`", "the lead merges", "ship anyway"} {
		if !strings.Contains(got, want) {
			t.Errorf("LandPromptLine = %q, want it to contain %q", got, want)
		}
	}
}

// TestLandLineOnlyWhereTheWorkerMerges: a chore or report, whose last
// status before the end ships no branch, gets no line.
func TestLandLineOnlyWhereTheWorkerMerges(t *testing.T) {
	chore := &WorkflowConfig{Statuses: []WorkflowStatus{
		{Name: "in progress", Prompt: "Chore: work through the brief's steps. There is no branch to ship and no commit to make."},
		{Name: "done"},
	}}
	if got := LandPromptLine(chore); got != "" {
		t.Errorf("chore: LandPromptLine = %q, want empty", got)
	}
	if got := LandPromptLine(nil); got != "" {
		t.Errorf("nil workflow: LandPromptLine = %q, want empty", got)
	}
}

// TestLeadBriefSaysWorkersLand: every lead brief carries the lead's side of
// the same rule, so no lead writes "the lead merges" into a brief again.
func TestLeadBriefSaysWorkersLand(t *testing.T) {
	for _, want := range []string{"Workers land their own branch", "issue-cli ship", "Never write \"the lead merges\"", "merge: line"} {
		if !strings.Contains(LeadRules, want) {
			t.Errorf("LeadRules lacks %q", want)
		}
	}
}
