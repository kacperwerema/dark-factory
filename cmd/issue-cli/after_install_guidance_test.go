package main

import (
	"strings"
	"testing"
)

// A criterion only the installed build can prove is written, recorded and
// checked in one form: the in design line (every system, since a systems:
// overlay replaces the base prompt), the testing line, the criteria_proven
// hint and the lead addendum all name "after install:".
func TestAnAfterInstallCriterionReachesDesignTestingTheGateAndTheLead(t *testing.T) {
	wf := engineWorkflow(t)
	const form = "after install:"
	for _, sys := range []string{"", "API", "UI", "CLI", "Workflow", "Factory"} {
		w := wf
		if sys != "" {
			w = wf.ForSystem(sys)
		}
		if p := w.StatusPrompt("in design"); !strings.Contains(p, "(after install: ") || !strings.Contains(p, "T0 = the install of main") {
			t.Errorf("in design prompt (system %q) doesn't say how to write an after-install criterion:\n%s", sys, p)
		}
		if p := w.StatusPrompt("testing"); !strings.Contains(p, `--value "after install: <command>"`) || !strings.Contains(p, "--provisional") {
			t.Errorf("testing prompt (system %q) doesn't say how to record one:\n%s", sys, p)
		}
	}
	var hint string
	for _, tr := range wf.Transitions {
		for _, a := range tr.Actions {
			if strings.HasPrefix(a.Rule, "criteria_proven") {
				hint = a.Hint
			}
		}
	}
	if !strings.Contains(hint, form) {
		t.Errorf("criteria_proven hint doesn't name the after-install form:\n%s", hint)
	}
	if a := wf.Leads.ProjectAddendum; !strings.Contains(a, `issue-cli search "after install:"`) || !strings.Contains(a, "Never install an unlanded branch build") {
		t.Errorf("lead addendum doesn't send the lead to the after-install checks:\n%s", a)
	}
}
