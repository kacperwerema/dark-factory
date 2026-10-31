package tracker

import (
	"os"
	"strings"
	"testing"
)

// TestFactoryOverlayPointsAtSettledRules keeps the Factory overlay's
// pointer and the section it names in step: a Factory feature reads
// "Settled rules" leaving idea and in design, and docs/departments.md has it.
func TestFactoryOverlayPointsAtSettledRules(t *testing.T) {
	wf := repoWorkflow(t).ForIssue(&Issue{Type: "feature", System: "Factory", Status: "idea"})

	design := wf.GetStatus("in design")
	if design == nil || !strings.Contains(design.Prompt, `"Settled rules" in docs/departments.md`) {
		t.Errorf("Factory in design prompt doesn't point at Settled rules: %+v", design)
	}
	tr := wf.GetTransition("idea", "in design")
	if tr == nil {
		t.Fatal("no idea → in design edge for a Factory feature")
	}
	injected := false
	for _, a := range tr.Actions {
		if a.Type == "inject_prompt" && strings.Contains(a.Prompt, `"Settled rules" in docs/departments.md`) {
			injected = true
		}
	}
	if !injected {
		t.Errorf("Factory idea → in design injects no Settled rules pointer: %+v", tr.Actions)
	}
	if !strings.Contains(wf.GetStatus("in design").Prompt, "When the design is complete") {
		t.Error("Factory in design prompt lost the base guidance it replaces")
	}

	other := repoWorkflow(t).ForIssue(&Issue{Type: "feature", System: "CLI", Status: "idea"})
	if strings.Contains(other.GetStatus("in design").Prompt, "Settled rules") {
		t.Error("the Settled rules pointer reached a CLI issue")
	}

	doc, err := os.ReadFile("../../docs/departments.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "\n## Settled rules\n") {
		t.Error("docs/departments.md has no ## Settled rules section")
	}
}
