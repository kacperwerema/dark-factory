package tracker

import (
	"strings"
	"testing"
)

// hintWorkflow is one transition a → b gated by rule, with hint on the action.
func hintWorkflow(rule, hint string) *WorkflowConfig {
	return &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "a"}, {Name: "b"}},
		Transitions: []WorkflowTransition{{
			From:    "a",
			To:      "b",
			Actions: []WorkflowAction{{Type: "validate", Rule: rule, Hint: hint}},
		}},
	}
}

// TestLegacyRuleHint: a legacy rule's hint: replaces its built-in fix text,
// templated, in the transition error and in the preview the dry run prints;
// without a hint the built-in text stays.
func TestLegacyRuleHint(t *testing.T) {
	issue := &Issue{Slug: "sys/x", System: "Sys", BodyRaw: "## Research\n- [ ] read the sources\n"}
	cases := []struct {
		name, rule, hint string
		want, notWant    []string
	}{
		{
			name:    "comment prefix, no hint",
			rule:    "has_comment_prefix: docs:",
			want:    []string{`no comment starting with "docs:"`, `issue-cli comment sys/x --text 'docs: ...'`},
			notWant: []string{"pages changed"},
		},
		{
			name:    "comment prefix, hint",
			rule:    "has_comment_prefix: docs:",
			hint:    `issue-cli comment {{slug}} --text "docs: <pages changed>" ({{system}})`,
			want:    []string{`no comment starting with "docs:" — issue-cli comment sys/x --text "docs: <pages changed>" (Sys)`},
			notWant: []string{"add one", "{{slug}}"},
		},
		{
			name:    "section boxes, no hint",
			rule:    "section_checkboxes_checked: Research",
			want:    []string{`1 of 1 box still open in section "Research"`, "read the sources", "issue-cli check sys/x"},
			notWant: []string{"finish the research"},
		},
		{
			name:    "section boxes, hint",
			rule:    "section_checkboxes_checked: Research",
			hint:    "finish the research for {{slug}} first",
			want:    []string{`1 of 1 box still open in section "Research"`, "read the sources", "finish the research for sys/x first"},
			notWant: []string{"Tick the ones", "issue-cli check sys/x"},
		},
		{
			name:    "missing section, hint",
			rule:    "section_checkboxes_checked: Design",
			hint:    "write ## Design for {{slug}}",
			want:    []string{`section "Design" has no checkboxes — write ## Design for sys/x`},
			notWant: []string{"before transitioning"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := hintWorkflow(tc.rule, tc.hint)
			err := wf.ValidateTransition(issue, "a", "b", nil)
			if err == nil {
				t.Fatal("ValidateTransition passed, want a failure")
			}
			preview := wf.PreviewTransitionAll(issue, "a", "b", "", nil)
			if len(preview.Steps) != 1 || preview.Steps[0].Message != err.Error() {
				t.Fatalf("preview steps = %+v, want one failed step with %q", preview.Steps, err.Error())
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(err.Error(), w) {
					t.Errorf("error %q still has %q", err, w)
				}
			}
		})
	}
}
