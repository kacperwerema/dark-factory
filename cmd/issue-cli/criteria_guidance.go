package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// addCriteriaCommand is the command that writes an issue's first criterion.
func addCriteriaCommand(slug string) string {
	return fmt.Sprintf("issue-cli append %s --section %s --body \"- [ ] <what someone can observe when it works>\"", slug, shell.Quote(tracker.CriteriaSection))
}

// proveNoCriteria answers prove on an issue with no ## Acceptance Criteria
// section: nothing to prove when its path never asks for criteria (exit 0),
// else an error naming the transition that does and how to add one.
func proveNoCriteria(ctx *Context, issue *tracker.Issue) error {
	wf := ctx.Project.LoadWorkflowForIssue(issue)
	if gate, ok := wf.CriteriaRequiredAt(); ok {
		return fmt.Errorf("%s has no ## %s, and its path asks for them at %s → %s — write each criterion as a checkbox:\n  %s",
			issue.Slug, tracker.CriteriaSection, gate.From, gate.To, addCriteriaCommand(issue.Slug))
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, proveOutput{Report: proof.Report{Slug: issue.Slug, Criteria: []proof.Criterion{}}, Ticked: []string{}})
	}
	fmt.Fprintln(ctx.Stdout, "no acceptance criteria: nothing to prove")
	if gate, ok := wf.ProofGateAt(); ok {
		fmt.Fprintf(ctx.Stdout, "The proof gate at %s → %s passes without them. To add one: %s\n", gate.From, gate.To, addCriteriaCommand(issue.Slug))
	}
	return nil
}

// issueTransitionContract is nextTransitionContract for one issue: the
// proof gate drops out of the requirements when it passes the issue anyway
// (no criteria, or created before the gate's date).
func issueTransitionContract(wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to string) (requires, sideEffects []string) {
	t := wf.ResolveTransition(from, to)
	if t == nil {
		return nil, nil
	}
	if issue != nil && !wf.ProofRequired(issue) {
		kept := *t
		kept.Actions = nil
		for _, a := range t.Actions {
			if a.Type == "validate" && tracker.IsProofRule(a.Rule) {
				continue
			}
			kept.Actions = append(kept.Actions, a)
		}
		t = &kept
	}
	for _, d := range transitionActionDescriptions(*t, wf) {
		if strings.HasPrefix(d, "Side-effect:") {
			sideEffects = append(sideEffects, d)
		} else {
			requires = append(requires, d)
		}
	}
	return requires, sideEffects
}

// firstWorkStatus is the status the issue's path enters first after its
// start, or "".
func firstWorkStatus(wf *tracker.WorkflowConfig) string {
	var path []string
	for _, s := range wf.Statuses {
		if !s.Optional {
			path = append(path, s.Name)
		}
	}
	if len(path) < 2 {
		return ""
	}
	return path[1]
}

// criteriaGuidance is the entry guidance line for status: on the path's
// first work status, an issue without ## Acceptance Criteria hears where its
// path asks for them, so the section is written before the code. "" when
// status is another one, the issue has criteria, or nothing checks them.
func criteriaGuidance(wf *tracker.WorkflowConfig, issue *tracker.Issue, status string) string {
	if wf == nil || issue == nil || status == "" || status != firstWorkStatus(wf) {
		return ""
	}
	if tracker.HasSection(issue.BodyRaw, tracker.CriteriaSection) {
		return ""
	}
	if gate, ok := wf.CriteriaRequiredAt(); ok {
		return fmt.Sprintf("Acceptance criteria: this path asks for ## %s at %s → %s. Write them now, before the code: %s", tracker.CriteriaSection, gate.From, gate.To, addCriteriaCommand(issue.Slug))
	}
	if gate, ok := wf.ProofGateAt(); ok {
		if proofGateDateCovers(wf, issue) {
			return fmt.Sprintf("Acceptance criteria: optional on this path; the proof gate at %s → %s passes without them. Any you write (%s) each need a proof link there, so write them before the code.", gate.From, gate.To, addCriteriaCommand(issue.Slug))
		}
	}
	return ""
}

// proofGateDateCovers reports whether the proof gate's date covers issue,
// leaving its criteria aside.
func proofGateDateCovers(wf *tracker.WorkflowConfig, issue *tracker.Issue) bool {
	probe := *issue
	probe.BodyRaw = "## " + tracker.CriteriaSection + "\n- [ ] probe\n"
	return wf.ProofRequired(&probe)
}
