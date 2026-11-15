package workflowupgrade

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// critiqueWorkflow is the slice of a workflow file this test reads: the
// status prompts and whether any transition runs the critique rule.
type critiqueWorkflow struct {
	Statuses []struct {
		Name   string `yaml:"name"`
		Prompt string `yaml:"prompt"`
	} `yaml:"statuses"`
	Transitions []struct {
		Actions []struct {
			Rule string `yaml:"rule"`
		} `yaml:"actions"`
	} `yaml:"transitions"`
}

func (w critiqueWorkflow) hasCritique() bool {
	for _, tr := range w.Transitions {
		for _, a := range tr.Actions {
			if strings.TrimSpace(a.Rule) == "critique" {
				return true
			}
		}
	}
	return false
}

func (w critiqueWorkflow) inDesignPrompt() (string, bool) {
	for _, s := range w.Statuses {
		if s.Name == "in design" {
			return s.Prompt, true
		}
	}
	return "", false
}

// A critic finding is answered by editing the design in place, not by
// appending an "after critic run N" note the next run reads against the old
// text (retro sweep 2026-10-31). Wherever the bundled files turn the critique
// gate on (the critique-gate catalog snippet, and any bundled template that
// runs the rule), the in design prompt says so: it names `replace` and "in
// place".
func TestCritiqueInDesignPromptSaysEditInPlace(t *testing.T) {
	wantPrompt := func(t *testing.T, where, prompt string) {
		t.Helper()
		if !strings.Contains(prompt, "issue-cli replace") {
			t.Errorf("%s: the in design prompt should name `issue-cli replace` for answering a critic finding; got:\n%s", where, prompt)
		}
		if !strings.Contains(prompt, "in place") {
			t.Errorf("%s: the in design prompt should say to edit the design \"in place\"; got:\n%s", where, prompt)
		}
	}

	entries, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.ID != "critique-gate" {
			continue
		}
		found = true
		var w critiqueWorkflow
		if err := yaml.Unmarshal([]byte(e.Snippet), &w); err != nil {
			t.Fatalf("critique-gate snippet: %v", err)
		}
		if !w.hasCritique() {
			t.Fatal("critique-gate snippet runs no critique rule; update this test")
		}
		prompt, ok := w.inDesignPrompt()
		if !ok {
			t.Fatal(`critique-gate snippet has no "in design" status prompt`)
		}
		wantPrompt(t, "catalog entry critique-gate", prompt)
	}
	if !found {
		t.Fatal("no critique-gate catalog entry")
	}

	for _, name := range Templates() {
		raw, err := ReadTemplate(name)
		if err != nil {
			t.Fatal(err)
		}
		var w critiqueWorkflow
		if err := yaml.Unmarshal(raw, &w); err != nil {
			t.Fatalf("template %s: %v", name, err)
		}
		if !w.hasCritique() {
			continue
		}
		prompt, ok := w.inDesignPrompt()
		if !ok {
			continue // no design step: no design text to edit
		}
		wantPrompt(t, "template "+name, prompt)
	}
}
