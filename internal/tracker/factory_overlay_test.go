package tracker

import (
	"os"
	"strings"
	"testing"
)

// TestFactoryOverlayPointsAtSettledRules keeps the Factory overlay's
// pointer and the section it names in step: a Factory feature reads
// "Settled rules" leaving idea and in design, every Factory type reads it in
// progress, and docs/departments.md has it.
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

	// Tweaks and bugfixes never pass idea → in design: in progress carries
	// the same two rules for every type, after the type's own prompt.
	for _, typ := range []string{"feature", "tweak", "bugfix"} {
		issue := &Issue{Slug: "factory/x", Title: "x", Type: typ, System: "Factory", Status: "in progress"}
		scoped := repoWorkflow(t).ForIssue(issue)
		for _, text := range []string{scoped.StatusPrompt("in progress"), BuildAgentPrompt(nil, issue, scoped, "", "")} {
			for _, want := range []string{`"Settled rules" in docs/departments.md`, "Owner acts (", "A human starts the work:"} {
				if !strings.Contains(text, want) {
					t.Errorf("Factory %s in progress doesn't carry %q:\n%s", typ, want, text)
				}
			}
		}
	}
	tweak := repoWorkflow(t).ForIssue(&Issue{Type: "tweak", System: "Factory", Status: "in progress"})
	if !strings.Contains(tweak.StatusPrompt("in progress"), "Tweak: build exactly what the body says") {
		t.Error("the Factory append replaced the tweak's own prompt")
	}

	other := repoWorkflow(t).ForIssue(&Issue{Type: "feature", System: "CLI", Status: "idea"})
	if strings.Contains(other.GetStatus("in design").Prompt, "Settled rules") {
		t.Error("the Settled rules pointer reached a CLI issue")
	}
	cli := repoWorkflow(t).ForIssue(&Issue{Type: "tweak", System: "CLI", Status: "in progress"})
	if strings.Contains(cli.StatusPrompt("in progress"), "Settled rules") {
		t.Error("the Settled rules pointer reached a CLI tweak in progress")
	}

	doc, err := os.ReadFile("../../docs/departments.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "\n## Settled rules\n") {
		t.Error("docs/departments.md has no ## Settled rules section")
	}
}
