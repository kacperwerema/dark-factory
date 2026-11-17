package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// previewRules are the content rules a design edit can break: the shape of
// the criteria section. section_checkboxes_checked is left out: boxes still
// open mid-design are expected.
var previewRules = map[string]bool{"criteria_checkboxes": true, "section_has_checkboxes": true}

// criteriaGate is the transition out of status that validates
// criteria_checkboxes (in design → backlog in the development template),
// the forward one when there are several; nil when none does.
func criteriaGate(wf *tracker.WorkflowConfig, status string) *tracker.WorkflowTransition {
	if wf == nil {
		return nil
	}
	var found *tracker.WorkflowTransition
	for i := range wf.Transitions {
		t := &wf.Transitions[i]
		if !strings.EqualFold(t.From, status) {
			continue
		}
		for _, a := range t.Actions {
			if name, _ := tracker.SplitRule(a.Rule); a.Type == "validate" && name == "criteria_checkboxes" {
				if found == nil || strings.EqualFold(t.To, wf.NextStatus(status)) {
					found = t
				}
				break
			}
		}
	}
	return found
}

// sectionChanged reports whether section's text differs between before and
// after (added, removed or rewritten).
func sectionChanged(before, after, section string) bool {
	a, okA := tracker.SectionText(before, section)
	b, okB := tracker.SectionText(after, section)
	return okA != okB || strings.TrimSpace(a) != strings.TrimSpace(b)
}

// designEditPreview is what the issue's next criteria gate would refuse or
// warn about in the sections an edit changed (D-1, D-2 of
// workflow/a-design-edit-prints-the-backlog-gates-refusals-…): the
// criteria section's shape rules, and lint's criteria findings on the
// criteria and the ## Must not lines. Nothing for an edit that changed
// neither section, or an issue at a status no criteria gate leaves.
func designEditPreview(wf *tracker.WorkflowConfig, issue *tracker.Issue, before, after string) []string {
	gate := criteriaGate(wf, issue.Status)
	if gate == nil {
		return nil
	}
	at := *issue
	at.BodyRaw = after
	var out []string
	criteriaChanged := false
	for _, a := range gate.Actions {
		name, arg := tracker.SplitRule(a.Rule)
		if a.Type != "validate" || !previewRules[name] || !sectionChanged(before, after, arg) {
			continue
		}
		if name == "criteria_checkboxes" {
			criteriaChanged = true
		}
		if err := wf.CheckValidateAction(a, &at); err != nil {
			out = append(out, fmt.Sprintf("the %s gate would refuse: %s: %s", gate.To, name, strings.TrimSpace(err.Error())))
		}
	}
	mustNotChanged := sectionChanged(before, after, tracker.MustNotSection)
	if !criteriaChanged && !mustNotChanged {
		return out
	}
	at.Status = gate.To
	for _, f := range lint.Criteria(&at, wf) {
		mustNot := strings.Contains(f.Where, "## "+tracker.MustNotSection)
		if (mustNot && mustNotChanged) || (!mustNot && criteriaChanged && criterionWhere(issue.Slug, f.Where)) {
			out = append(out, fmt.Sprintf("the %s gate would warn: %s: %s\n    fix: %s", gate.To, f.Where, f.Message, f.Fix))
		}
	}
	return out
}

// criterionWhere reports whether a criteria finding's where names one
// acceptance box ("<slug> AC2").
func criterionWhere(slug, where string) bool {
	id := strings.TrimPrefix(where, slug+" ")
	return id != where && tracker.IsCheckboxRefShaped(id) && strings.HasPrefix(id, "AC")
}

// printDesignEditPreview prints designEditPreview's lines, each a warning,
// continuation lines indented under it.
func printDesignEditPreview(w io.Writer, ctx *Context, issue *tracker.Issue, before, after string) {
	if before == after {
		return
	}
	for _, finding := range designEditPreview(ctx.Project.LoadWorkflowForIssue(issue), issue, before, after) {
		for i, line := range strings.Split(finding, "\n") {
			switch {
			case i == 0:
				fmt.Fprintf(w, "⚠ %s\n", line)
			case strings.TrimSpace(line) != "":
				fmt.Fprintf(w, "  %s\n", line)
			}
		}
	}
}
