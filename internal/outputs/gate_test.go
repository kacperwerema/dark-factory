package outputs

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestApprovedError(t *testing.T) {
	out := func(id, kind, state string, versions int) *Output {
		o := &Output{ID: id, Issue: "m/f", Kind: kind, State: state}
		for i := 1; i <= versions; i++ {
			o.Versions = append(o.Versions, Version{N: i})
		}
		return o
	}
	cases := []struct {
		name string
		list []*Output
		kind string
		want string
	}{
		{"none", nil, "report", "no report on the shelf"},
		{"approved", []*Output{out("a", "report", StateApproved, 1)}, "report", ""},
		{"published", []*Output{out("a", "report", StatePublished, 1)}, "", ""},
		{"newest in review", []*Output{out("a", "report", StateApproved, 1), out("b", "report", StateReview, 1)}, "report", "b v1 still in review"},
		{"revision in review", []*Output{out("a", "report", StateReview, 3)}, "report", "a v3 still in review"},
		{"other kind in review", []*Output{out("a", "report", StateApproved, 1), out("b", "post", StateReview, 1)}, "report", ""},
		{"only rejected", []*Output{out("a", "report", StateRejected, 1)}, "report", "every one is rejected"},
		{"rejected beside approved", []*Output{out("a", "report", StateApproved, 1), out("b", "report", StateRejected, 1)}, "report", ""},
	}
	for _, c := range cases {
		err := ApprovedError(c.list, "m/f", c.kind)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

// TestShelfGatesShowTheHint: has_output and outputs_approved put an action's
// hint: in place of their fix, as the bundled department templates expect
// (lint check hints).
func TestShelfGatesShowTheHint(t *testing.T) {
	for _, rule := range []string{"has_output: report", "outputs_approved"} {
		wf := &tracker.WorkflowConfig{
			Statuses: []tracker.WorkflowStatus{{Name: "draft"}, {Name: "review"}},
			Transitions: []tracker.WorkflowTransition{{From: "draft", To: "review", Actions: []tracker.WorkflowAction{
				{Type: "validate", Rule: rule, Hint: "shelve it for {{slug}}"},
			}}},
			IssuesRoot: t.TempDir(), ProjectRoot: t.TempDir(),
		}
		err := wf.ValidateTransition(&tracker.Issue{Slug: "m/f"}, "draft", "review", nil)
		if err == nil || !strings.Contains(err.Error(), "shelve it for m/f") || !strings.Contains(err.Error(), "on the shelf for m/f") {
			t.Errorf("%s: err = %v, want the problem and the hint", rule, err)
		}
		if !tracker.RuleShowsHint(rule) {
			t.Errorf("RuleShowsHint(%q) = false", rule)
		}
	}
}
