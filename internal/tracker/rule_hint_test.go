package tracker

import (
	"errors"
	"strings"
	"testing"
)

// TestRuleShowsHintMatchesCheckRule: every legacy rule RuleShowsHint
// counts shows a hint: in place of its fix when it fails on an empty issue (one open box for all_checkboxes_checked),
// and every legacy rule in checkRule's switch is either in hintRules or
// keeps its own text (so lint check hints can warn on it).
func TestRuleShowsHintMatchesCheckRule(t *testing.T) {
	w := attachedDefault(t)
	const hint = "run the hint {{slug}}"
	for name := range hintRules {
		switch name {
		case hasOutputRule, outputsApprovedRule:
			continue // the shelf's checker lives in internal/outputs: TestShelfGatesShowTheHint
		case criteriaProvenRule:
			continue // its checker lives in internal/proof: TestProofGateShowsTheHint
		case CritiqueRule:
			continue // its checker lives in internal/critique: TestCritiqueGateShowsTheHint
		case DecisionsCitedRule, CitationsCurrentRule, DecisionUnchallengedRule, DecisionsRespectedRule:
			continue // they need a decisions log: TestDecisionRulesShowTheHint
		}
		issue := &Issue{Slug: "x/empty"}
		if name == "all_checkboxes_checked" {
			issue.BodyRaw = "- [ ] open\n"
		}
		rule := name
		switch name {
		case "section_has_checkboxes", "section_checkboxes_checked":
			rule = name + ": Implementation"
		case "has_comment_prefix":
			rule = name + ": tests:"
		case "criteria_checkboxes":
			rule = name + ": Acceptance Criteria"
		case "approved_for", "human_approval":
			rule = name + ": backlog"
		}
		err := w.checkValidateAction(WorkflowAction{Type: "validate", Rule: rule, Hint: hint}, issue, nil)
		if err == nil {
			t.Fatalf("%s passed on an empty issue: give the test a failing case", rule)
		}
		if !strings.Contains(err.Error(), "run the hint x/empty") {
			t.Errorf("%s is in hintRules but its failure doesn't show the hint:\n%s", rule, err)
		}
		if !RuleShowsHint(rule) {
			t.Errorf("RuleShowsHint(%q) = false for a rule in hintRules", rule)
		}
	}
	for _, name := range checkRuleCases(t) {
		if hintRules[name] {
			continue
		}
		if RuleShowsHint(name) {
			t.Errorf("RuleShowsHint(%q) = true, but checkRule's %s isn't in hintRules", name, name)
		}
	}
	if RuleShowsHint(HandoffAnsweredRule + ": legal") {
		t.Errorf("handoff_answered keeps its own text: RuleShowsHint should say false")
	}
	if !RuleShowsHint("has_section") {
		t.Errorf("a structured rule shows its hint")
	}
	if !RuleShowsHint("no_such_rule") {
		t.Errorf("an unknown rule is lint check schema's, not hints'")
	}
}

// TestDecisionRulesShowTheHint: each ledger rule's failure keeps its
// problem (the refs it holds on) and shows an action's hint: in place of
// its fix.
func TestDecisionRulesShowTheHint(t *testing.T) {
	const hint = "see the ledger for {{slug}}"
	for _, rule := range []string{DecisionsCitedRule, CitationsCurrentRule} {
		design := "We build it."
		if rule == CitationsCurrentRule {
			design = citesBoth + " Stores per orchard/rules#D-3."
		}
		wf, issue := citedFixture(design, "")
		err := wf.checkValidateAction(WorkflowAction{Type: "validate", Rule: rule, Hint: hint}, issue, nil)
		if err == nil {
			t.Fatalf("%s passed on the fixture", rule)
		}
		if !strings.Contains(err.Error(), "see the ledger for orchard/child") || !strings.Contains(err.Error(), rule+": ") {
			t.Errorf("%s: want the problem and the hint:\n%s", rule, err)
		}
		if strings.Contains(err.Error(), "decision challenge") {
			t.Errorf("%s: the hint replaces the built-in fix, yet it is still there:\n%s", rule, err)
		}
	}
	// decision_unchallenged and decisions_respected read the log and the
	// branch: their failures are ruleFailures, so the hint replaces the fix.
	for name, rf := range map[string]*ruleFailure{
		DecisionUnchallengedRule: challengedError("x/y", []Challenge{{Ref: "x/z#D-1", Why: "too strict"}}),
		DecisionsRespectedRule:   respectedError("x/y", []RespectedHold{{TouchedDecision: TouchedDecision{Entry: &DecisionEntry{Decision: Decision{Ref: "x/z#D-1", Text: "keep it"}}, Files: []string{"a.go"}}}}),
	} {
		got := rf.withFix("see the ledger").Error()
		if !strings.HasPrefix(got, name+": ") || !strings.HasSuffix(got, "\nsee the ledger") || strings.Contains(got, "issue-cli") {
			t.Errorf("%s with a hint:\n%s", name, got)
		}
	}
}

// TestApprovalRuleShowsTheHintAndStaysTyped: approved_for's failure shows a
// hint: and is still an *ApprovalMissingError to errors.As and errors.Is.
func TestApprovalRuleShowsTheHintAndStaysTyped(t *testing.T) {
	w := attachedDefault(t)
	issue := &Issue{Slug: "x/empty"}
	plain := w.checkValidateAction(WorkflowAction{Type: "validate", Rule: "approved_for: backlog"}, issue, nil)
	want := (&ApprovalMissingError{Required: "backlog", Verb: "validate"}).Error()
	if plain == nil || plain.Error() != want {
		t.Fatalf("approved_for without a hint = %v, want %q", plain, want)
	}
	err := w.checkValidateAction(WorkflowAction{Type: "validate", Rule: "approved_for: backlog", Hint: "ask the owner"}, issue, nil)
	if err == nil || err.Error() != `issue is not human-approved for "backlog" — ask the owner` {
		t.Errorf("approved_for with a hint = %v", err)
	}
	var missing *ApprovalMissingError
	if !errors.As(err, &missing) || missing.Required != "backlog" || !errors.Is(err, ErrApprovalMissing) {
		t.Errorf("the hinted failure lost its type: %#v", err)
	}
}
