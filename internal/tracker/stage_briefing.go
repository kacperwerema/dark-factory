package tracker

import (
	"strings"
)

// The stage briefing is what a worker gets at a session: fresh status: paths
// to read instead of the issue's body and earlier chat, the stage's own
// guidance, its output and the gate out, and only the decisions and memories
// for this status, kept under the status's briefing_budget. Parts with a
// command that prints them again fold to that command, in StageFoldOrder;
// the owner's rules, the YOLO rule, the stop lines and the worktree rules
// never fold. docs/workflow.md, "Fresh session per stage".

// The parts of a stage briefing that fold, in the order they fold.
const (
	StagePartMemories  = "memories"
	StagePartDecisions = "decisions"
	StagePartShell     = "shell"
	StagePartCI        = "ci"
	StagePartBuild     = "build"
	StagePartRedRounds = "red-rounds"
	StagePartMainHolds = "main-holds"
)

// StageFoldOrder is the order parts fold in when the briefing is over its
// budget: the first is the cheapest to fetch again.
var StageFoldOrder = []string{StagePartMemories, StagePartDecisions, StagePartShell, StagePartCI, StagePartMainHolds, StagePartBuild, StagePartRedRounds}

// StageBriefing is the size of a stage briefing against its budget, for the
// dispatch-prompt JSON and the trace's briefing record.
type StageBriefing struct {
	Budget int `json:"budget"`
	// Tokens is tokens.Estimate of the briefing as sent.
	Tokens int `json:"tokens"`
	// Folded names the parts folded to their command, in fold order.
	Folded []string `json:"folded,omitempty"`
	// OverBudget is true when the briefing is still over its budget with
	// every foldable part folded.
	OverBudget bool `json:"over_budget,omitempty"`
}

// PreviousStageSections is the stage before status that wrote sections, and
// their titles: the sections the transition into it appended. It walks back
// over statuses that append none (a queue status) and side statuses.
func (w *WorkflowConfig) PreviousStageSections(status string) (string, []string) {
	if w == nil {
		return "", nil
	}
	for i := w.GetStatusIndex(status) - 1; i >= 0; i-- {
		name := w.Statuses[i].Name
		if w.IsSideStatus(name) {
			continue
		}
		if sections := w.EntrySections(name); len(sections) > 0 {
			return name, sections
		}
	}
	return "", nil
}

// EntrySections are the section titles the transition into status appends,
// from the status before it on the list.
func (w *WorkflowConfig) EntrySections(status string) []string {
	if w == nil {
		return nil
	}
	i := w.GetStatusIndex(status)
	if i <= 0 {
		return nil
	}
	from := ""
	for j := i - 1; j >= 0; j-- {
		if !w.IsSideStatus(w.Statuses[j].Name) {
			from = w.Statuses[j].Name
			break
		}
	}
	t := w.ResolveTransition(from, status)
	if t == nil {
		return nil
	}
	var out []string
	for _, a := range t.Actions {
		if a.Type == "append_section" && strings.TrimSpace(a.Title) != "" {
			out = append(out, strings.TrimSpace(a.Title))
		}
	}
	return out
}

// GateOut is the status after status on the path and what the transition
// into it requires, in DescribeAction's words.
func (w *WorkflowConfig) GateOut(status string) (string, []string) {
	if w == nil {
		return "", nil
	}
	next, _ := w.DefaultNextStatus(status)
	if next == "" {
		return "", nil
	}
	var lines []string
	if t := w.ResolveTransition(status, next); t != nil {
		for _, a := range t.Actions {
			if a.Type == "validate" || a.Type == "require_human_approval" {
				lines = append(lines, DescribeAction(a, next))
			}
		}
	}
	return next, lines
}
