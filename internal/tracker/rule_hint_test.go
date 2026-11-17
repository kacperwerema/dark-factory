package tracker

import (
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
		if name == hasOutputRule || name == outputsApprovedRule {
			continue // the shelf's checker lives in internal/outputs: TestShelfGatesShowTheHint
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
