package main

import (
	"fmt"

	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// criteriaWarnings is lint's criteria findings for an issue leaving design:
// the edge from → to validates criteria_checkboxes. They are warnings, never
// a refusal (factory/criteria-a-command-can-fail-…#D-1). The issue is read
// at to, so a Must not left as its placeholder counts once it leaves design.
func criteriaWarnings(wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to string) []string {
	t := wf.GetTransition(from, to)
	if t == nil {
		return nil
	}
	checks := false
	for _, a := range t.Actions {
		if name, _ := tracker.SplitRule(a.Rule); a.Type == "validate" && name == "criteria_checkboxes" {
			checks = true
		}
	}
	if !checks {
		return nil
	}
	at := *issue
	at.Status = to
	var out []string
	for _, f := range lint.Criteria(&at, wf) {
		out = append(out, fmt.Sprintf("criteria: %s: %s\n    fix: %s", f.Where, f.Message, f.Fix))
	}
	return out
}

// afterInstallWarnings is lint's after-install findings for an issue moving
// into shipping: a Test Plan line "After install …" / "Live: …" that no
// `after install:` criterion carries is the last chance to become one
// before the branch lands. Warnings, never a refusal.
func afterInstallWarnings(wf *tracker.WorkflowConfig, issue *tracker.Issue, to string) []string {
	if !isShipStatus(wf, to) {
		return nil
	}
	var out []string
	for _, f := range lint.AfterInstallProse(issue) {
		out = append(out, fmt.Sprintf("after-install: %s: %s\n    fix: %s", f.Where, f.Message, f.Fix))
	}
	return out
}
