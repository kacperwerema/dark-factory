package tracker

import (
	"errors"
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker/validations"
)

// ValidateTransition checks whether an issue meets all validation rules for a transition.
// Returns nil if valid, or an error describing what's missing.
func (w *WorkflowConfig) ValidateTransition(issue *Issue, fromStatus, toStatus string, comments []Comment) error {
	if w.GetStatus(toStatus) == nil {
		return fmt.Errorf("unknown status %q", toStatus)
	}

	for _, action := range w.transitionActions(fromStatus, toStatus) {
		switch action.Type {
		case "validate":
			key, isCheck := CheckKey(action)
			if isCheck && w.CheckHold != nil {
				if err := w.CheckHold(key); err != nil {
					return err
				}
			}
			err := w.checkValidateAction(action, issue, comments)
			if isCheck && w.CheckDone != nil {
				w.CheckDone(key, err)
			}
			if err != nil {
				return err
			}
		case "require_human_approval":
			status := strings.TrimSpace(action.Status)
			if status == "" {
				status = toStatus
			}
			if !strings.EqualFold(issue.HumanApproval, status) {
				return &ApprovalMissingError{
					Slug:       issue.Slug,
					FromStatus: fromStatus,
					Required:   status,
					Verb:       "validate",
				}
			}
		}
	}
	return nil
}

// checkValidateAction runs one "validate" action, dispatching structured rules
// to the validations sub-package and legacy rules to checkRule. Shared by
// ValidateTransition and PreviewTransition so both report the same verdict.
func (w *WorkflowConfig) checkValidateAction(action WorkflowAction, issue *Issue, comments []Comment) error {
	if isStructuredRule(action.Rule) {
		if w.SkipCommandRules && strings.TrimSpace(action.Rule) == "command_succeeds" {
			return nil
		}
		return w.checkAction(action, issue, comments)
	}
	if name, _ := SplitRule(action.Rule); name == HandoffAnsweredRule {
		return w.checkHandoffAnswered(action, issue)
	}
	err := w.checkRule(action.Rule, issue, comments)
	if hint := strings.TrimSpace(action.Hint); hint != "" {
		var rf *ruleFailure
		if errors.As(err, &rf) {
			view := toIssueView(issue)
			view.Department = w.departmentKey(issue)
			return rf.withFix(validations.RenderHint(hint, view))
		}
	}
	return err
}

// hintRules are the legacy rules whose failure is a ruleFailure, so an
// action's hint: takes the place of their fix. The other legacy rules
// (criteria_*, approved_for, decisions_*, critique, …) and
// handoff_answered keep their own text and never show a hint.
// TestRuleShowsHintMatchesCheckRule keeps this list in step with checkRule.
var hintRules = map[string]bool{
	"body_not_empty": true, "has_checkboxes": true, "section_has_checkboxes": true,
	"has_assignee": true, "all_checkboxes_checked": true, "section_checkboxes_checked": true,
	"has_test_plan": true, "has_comment_prefix": true,
	hasOutputRule: true, outputsApprovedRule: true,
}

// RuleShowsHint reports whether a validate action on rule shows its hint:
// when it fails: every structured rule does, and the legacy rules in
// hintRules. An unknown rule counts as showing it (lint check schema
// names that one). Lint check hints warns on a hint set on any other rule.
func RuleShowsHint(rule string) bool {
	name, _ := SplitRule(rule)
	if isStructuredRule(name) || !KnownRule(name) {
		return true
	}
	return hintRules[name]
}

// ruleFailure is a legacy rule's failure in two parts, what is wrong and how
// to fix it, so an action's hint: can replace the fix the way it does on the
// validations package's rules. sep joins them: " — ", or "\n" after a
// problem that ends in a list.
type ruleFailure struct {
	problem, sep, fix string
}

func (f *ruleFailure) Error() string { return f.problem + f.sep + f.fix }

// withFix is f with fix in place of its built-in one.
func (f *ruleFailure) withFix(fix string) *ruleFailure {
	sep := " — "
	if strings.HasSuffix(f.problem, "\n") {
		sep = "\n"
	}
	return &ruleFailure{problem: f.problem, sep: sep, fix: fix}
}

func failRule(problem, fix string) *ruleFailure {
	return &ruleFailure{problem: problem, sep: " — ", fix: fix}
}

// RuleFailure is failRule for a rule checked outside the package (has_output
// and outputs_approved, registered by internal/outputs): an action's hint:
// replaces fix, the text after " — ".
func RuleFailure(problem, fix string) error { return failRule(problem, fix) }

// Validate is kept for compatibility with older call sites and tests.
func (w *WorkflowConfig) Validate(issue *Issue, toStatus string, comments []Comment) error {
	fromStatus := ""
	if idx := w.GetStatusIndex(toStatus); idx > 0 {
		fromStatus = w.Statuses[idx-1].Name
	}
	return w.ValidateTransition(issue, fromStatus, toStatus, comments)
}

// ValidationSummary returns a human-readable one-line description of the
// validation rule (the same text shown in transition previews).
func ValidationSummary(rule string) string {
	return validationSummary(rule)
}

// DescribeAction returns a short human-readable description of a workflow
// action, suitable for listing the rules that govern a transition.
func DescribeAction(action WorkflowAction, defaultStatus string) string {
	switch action.Type {
	case "validate":
		if isStructuredRule(action.Rule) {
			return structuredSummary(action)
		}
		return ValidationSummary(action.Rule)
	case "require_human_approval":
		status := strings.TrimSpace(action.Status)
		if status == "" {
			status = defaultStatus
		}
		owner := ""
		if action.OwnerOnly {
			owner = " (owner only: YOLO never passes it)"
		}
		if status == "" {
			return "Must be human-approved in the issue viewer" + owner
		}
		return fmt.Sprintf("Must be human-approved for %q in the issue viewer%s", status, owner)
	case "append_section":
		title := strings.TrimSpace(action.Title)
		if title == "" {
			return "Side-effect: appends a body section"
		}
		return fmt.Sprintf("Side-effect: appends ## %s section", title)
	case "inject_prompt":
		return "Side-effect: injects entry guidance prompt"
	case "set_fields":
		field := strings.TrimSpace(action.Field)
		if field == "" {
			return "Side-effect: sets a frontmatter field"
		}
		if action.Value == "" {
			return fmt.Sprintf("Side-effect: clears %s", field)
		}
		return fmt.Sprintf("Side-effect: sets %s = %q", field, action.Value)
	default:
		return action.Type
	}
}

func validationSummary(rule string) string {
	ruleName, arg := SplitRule(rule)

	switch ruleName {
	case "body_not_empty":
		return "Validate issue body is not empty"
	case "has_checkboxes":
		return "Validate issue has checkboxes"
	case "section_has_checkboxes":
		if arg == "" {
			return "Validate section has checkboxes (existence only — contents verified later)"
		}
		return fmt.Sprintf("Validate section %s has checkboxes (existence only — contents verified later)", arg)
	case "criteria_checkboxes":
		return fmt.Sprintf("Validate every item in section %s is a checkbox with its own text", arg)
	case "criteria_proven":
		return "Validate every acceptance criterion has a proof link that passes its check (issue-cli prove <slug> --list)"
	case commitsReferenceRule:
		return "Validate a commit on the branch references the issue (its slug in a subject, or a Refs: <slug> line)"
	case CritiqueRule:
		return "Validate a fresh critic found no blocks finding (issue-cli critique <slug>)"
	case hasOutputRule:
		if arg == "" {
			return "Validate the issue has an output on the shelf (issue-cli output add --issue <slug>)"
		}
		return fmt.Sprintf("Validate the issue has a %s on the shelf (issue-cli output add --issue <slug> --kind %s)", arg, arg)
	case outputsApprovedRule:
		if arg == "" {
			return "Validate the owner approved the issue's newest output on the shelf, with none still in review"
		}
		return fmt.Sprintf("Validate the owner approved the issue's newest %s on the shelf, with none still in review", arg)
	case HandoffAnsweredRule:
		return fmt.Sprintf("Validate a handoff to %s filed for the issue is done (issue-cli handoff %s --for <slug>)", arg, arg)
	case DecisionsCitedRule:
		return "Validate ## Design cites every hard decision in force for the issue, or says \"n/a <ref>: <why>\" (issue-cli decisions --in-force <slug>)"
	case CitationsCurrentRule:
		return "Validate no decision the issue cites is replaced or expired"
	case DecisionUnchallengedRule:
		return "Validate no challenge this issue made on a decision is still open (issue-cli decision challenge)"
	case DecisionsRespectedRule:
		return "Validate the issue cites every hard decision whose paths: the branch changes, with no open challenge of its own on it (issue-cli decisions --touched <slug>)"
	case "has_assignee":
		return "Validate issue has assignee"
	case "all_checkboxes_checked":
		return "Validate all checkboxes are checked"
	case "section_checkboxes_checked":
		if arg == "" {
			return "Validate section checkboxes are checked (gates this transition)"
		}
		return fmt.Sprintf("Validate section %s checkboxes are checked (gates this transition)", arg)
	case "has_test_plan":
		return "Validate test plan is present"
	case "has_comment_prefix":
		if arg == "" {
			return "Validate required comment prefix exists"
		}
		return fmt.Sprintf("Validate comment starts with %s", arg)
	case "approved_for", "human_approval":
		if arg == "" {
			return "Validate issue has human approval"
		}
		return fmt.Sprintf("Validate issue human-approved for %s", arg)
	default:
		return fmt.Sprintf("Validate %s", rule)
	}
}

func (w *WorkflowConfig) checkRule(rule string, issue *Issue, comments []Comment) error {
	ruleName, ruleArg := SplitRule(rule)

	switch ruleName {
	case "body_not_empty":
		if strings.TrimSpace(issue.BodyRaw) == "" {
			return failRule("issue body is empty", "add a description first")
		}
	case "has_checkboxes":
		total, _ := CountCheckboxes(issue.BodyRaw)
		if total == 0 {
			return failRule("no checkboxes found", "add acceptance criteria as checkboxes:\n\n  - [ ] First requirement\n  - [ ] Second requirement")
		}
	case "section_has_checkboxes":
		if ruleArg == "" {
			return fmt.Errorf("section_has_checkboxes rule requires a section name argument")
		}
		total, _ := CountCheckboxesInSection(issue.BodyRaw, ruleArg)
		if total == 0 {
			return failRule(fmt.Sprintf("no checkboxes found in section %q", ruleArg), "add explicit checklist items there")
		}
	case "criteria_checkboxes":
		return w.checkCriteriaCheckboxes(ruleArg, issue)
	case criteriaProvenRule:
		return w.checkCriteriaProven(ruleArg, issue, comments)
	case commitsReferenceRule:
		return w.checkCommitsReference(issue)
	case hasOutputRule:
		return w.checkHasOutput(ruleArg, issue)
	case outputsApprovedRule:
		return w.checkOutputsApproved(ruleArg, issue)
	case CritiqueRule:
		return w.checkCritique(issue)
	case DecisionsCitedRule:
		return w.checkDecisionsCited(ruleArg, issue)
	case CitationsCurrentRule:
		return w.checkCitationsCurrent(ruleArg, issue)
	case DecisionUnchallengedRule:
		return w.checkDecisionUnchallenged(ruleArg, issue)
	case DecisionsRespectedRule:
		return w.checkDecisionsRespected(ruleArg, issue)
	case "has_assignee":
		if issue.Assignee == "" {
			// start re-claims and moves the issue on (backlog clears the
			// assignee); claim only sets the name.
			return failRule("no assignee", fmt.Sprintf("re-claim the issue with:\n\n  issue-cli start %s\n\n(or only set the name: issue-cli claim %s --assignee \"your-name\")", issue.Slug, issue.Slug))
		}
	case "all_checkboxes_checked":
		items := ListCheckboxes(issue.BodyRaw)
		if open := openCheckboxes(items); len(open) > 0 {
			return openCheckboxesError(issue.Slug, "", open, len(items))
		}
	case "section_checkboxes_checked":
		if ruleArg == "" {
			return fmt.Errorf("section_checkboxes_checked rule requires a section name argument")
		}
		total, checked := CountCheckboxesInSection(issue.BodyRaw, ruleArg)
		if total == 0 {
			// A gated section with no checkboxes must not pass vacuously —
			// otherwise an issue imported without that section reaches the
			// next status with none of its checklist content. Require the
			// section to exist with at least one checkbox.
			return failRule(fmt.Sprintf("section %q has no checkboxes", ruleArg), fmt.Sprintf("add a %s checklist before transitioning", ruleArg))
		}
		if checked < total {
			var inSection []CheckboxItem
			for _, it := range ListCheckboxes(issue.BodyRaw) {
				if strings.EqualFold(it.Section, ruleArg) {
					inSection = append(inSection, it)
				}
			}
			return openCheckboxesError(issue.Slug, ruleArg, openCheckboxes(inSection), total)
		}
	case "has_test_plan":
		hasAuto, hasManual := HasTestPlan(issue.BodyRaw)
		if !hasAuto || !hasManual {
			return failRule("missing test plan", "add one with:\n  issue-cli append <slug> --body \"## Test Plan\\n\\n### Automated\\n- test description\\n\\n### Manual\\n- verification step\"")
		}
	case "has_comment_prefix":
		if ruleArg == "" {
			return fmt.Errorf("has_comment_prefix rule requires an argument")
		}
		if !HasCommentWithPrefix(comments, ruleArg) {
			return failRule(fmt.Sprintf("no comment starting with %q", ruleArg), fmt.Sprintf("add one:\n\n  issue-cli comment %s --text %s", issue.Slug, shell.Quote(ruleArg+" ...")))
		}
	case "approved_for", "human_approval":
		if ruleArg == "" {
			return fmt.Errorf("human_approval rule requires a status argument")
		}
		if !strings.EqualFold(issue.HumanApproval, ruleArg) {
			return &ApprovalMissingError{
				Slug:     issue.Slug,
				Required: ruleArg,
				Verb:     "validate",
			}
		}
	default:
		return &UnknownRuleError{Rule: ruleName, Version: BuildVersion}
	}
	return nil
}

// openCheckboxes returns the unchecked boxes in items.
func openCheckboxes(items []CheckboxItem) []CheckboxItem {
	var open []CheckboxItem
	for _, it := range items {
		if !it.Checked {
			open = append(open, it)
		}
	}
	return open
}

// openCheckboxesError explains a checkbox gate failure: how many boxes are
// still open out of total, each open box with its id, and the check command
// that ticks them. section is "" for the whole-body gate.
func openCheckboxesError(slug, section string, open []CheckboxItem, total int) *ruleFailure {
	var b strings.Builder
	noun := "boxes"
	if len(open) == 1 {
		noun = "box"
	}
	fmt.Fprintf(&b, "%d of %d %s still open", len(open), total, noun)
	if section != "" {
		fmt.Fprintf(&b, " in section %q", section)
	}
	b.WriteString(":\n")
	var ids []string
	for _, it := range open {
		ref := it.ID
		if ref == "" {
			ref = fmt.Sprintf("#%d", it.Index)
		} else {
			ids = append(ids, it.ID)
		}
		fmt.Fprintf(&b, "  %-4s %s\n", ref, it.Label())
	}
	fix := fmt.Sprintf("\nSee every box with:\n  issue-cli checklist %s", slug)
	if len(ids) > 0 {
		fix = fmt.Sprintf("\nTick the ones that are done (ids, several at once):\n  issue-cli check %s %s", slug, strings.Join(ids, " "))
	}
	return &ruleFailure{problem: b.String(), fix: fix}
}

// ReadyForApproval reports whether every validate rule of fromStatus →
// toStatus passes, so the human's approval is all the transition still needs.
// command_succeeds rules are skipped: callers render pages and must not run
// shell commands.
func (w *WorkflowConfig) ReadyForApproval(issue *Issue, fromStatus, toStatus string, comments []Comment) bool {
	for _, action := range w.transitionActions(fromStatus, toStatus) {
		if action.Type != "validate" || action.Rule == "command_succeeds" {
			continue
		}
		if err := w.checkValidateAction(action, issue, comments); err != nil {
			return false
		}
	}
	return true
}

// CheckValidateAction runs one validate action against issue, as the
// transition would (its hint: included), with no comments: for a preview
// of a gate's content rules after an edit (issue-cli replace, append).
func (w *WorkflowConfig) CheckValidateAction(action WorkflowAction, issue *Issue) error {
	return w.checkValidateAction(action, issue, nil)
}
