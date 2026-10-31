package tracker

import (
	"errors"
	"strings"
	"testing"
)

// A rule this build doesn't know (main's workflow.yaml uses `critique`, an
// older installed issue-cli lacks it) names the build and the fix instead
// of a bare unknown-rule error.
func TestUnknownRuleNamesTheBuildBehindAndTheFix(t *testing.T) {
	old := BuildVersion
	BuildVersion = "v0.58.0-rc.1"
	t.Cleanup(func() { BuildVersion = old })
	w := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "in design"}, {Name: "backlog"}},
		Transitions: []WorkflowTransition{{From: "in design", To: "backlog",
			Actions: []WorkflowAction{{Type: "validate", Rule: "rule_from_a_newer_main"}}}},
	}
	err := w.ValidateTransition(&Issue{Slug: "s", Status: "in design"}, "in design", "backlog", nil)
	var ue *UnknownRuleError
	if !errors.As(err, &ue) || ue.Rule != "rule_from_a_newer_main" {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"unknown validation rule: rule_from_a_newer_main", "this build (v0.58.0-rc.1, ", "behind the AI Workflow Engine's main", "AI Workflow Engine checkout (the issue-viewer repo, not this project)", "make install", "GOPATH)/bin", "newest release", "issue-cli process schema"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	BuildVersion = ""
	if err := w.ValidateTransition(&Issue{Slug: "s", Status: "in design"}, "in design", "backlog", nil); !strings.Contains(err.Error(), "this build (dev") {
		t.Errorf("unset version: %v", err)
	}
}
