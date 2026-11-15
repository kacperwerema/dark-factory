package tracker

import (
	"os"
	"strings"
	"testing"
)

// TestAPIOverlayNamesRouteRules keeps the API overlay's route guidance in the
// prompts an API worker reads: the in design prompt (features) and the
// prompt_append on in progress (every type that walks it: features, tweaks, bugfixes).
func TestAPIOverlayNamesRouteRules(t *testing.T) {
	wants := []string{"make security-check", "R/W/O/X", `docs/API/security.md's "Route inventory"`, "newTestServer", "strangerHeader"}
	has := func(where, text string) {
		t.Helper()
		for _, w := range wants {
			if !strings.Contains(text, w) {
				t.Errorf("%s doesn't name %q:\n%s", where, w, text)
			}
		}
	}
	feature := repoWorkflow(t).ForIssue(&Issue{Type: "feature", System: "API", Status: "idea"})
	has("API feature in design prompt", feature.StatusPrompt("in design"))
	if !strings.Contains(feature.StatusPrompt("in design"), "When the design is complete") {
		t.Error("API in design prompt lost the base guidance it replaces")
	}
	for _, typ := range []string{"feature", "tweak", "bugfix"} {
		wf := repoWorkflow(t).ForIssue(&Issue{Type: typ, System: "API", Status: "in progress"})
		has("API "+typ+" in progress prompt", wf.StatusPrompt("in progress"))
		has("API "+typ+" briefing", BuildAgentPrompt(nil, &Issue{Slug: "api/x", Title: "x", Type: typ, System: "API", Status: "in progress"}, wf, "", ""))
		if strings.Contains(strings.Join(wf.TransitionPrompts("in progress", "testing"), "\n"), "Route inventory") {
			t.Errorf("API %s still injects the route guidance leaving in progress: it moved to prompt_append", typ)
		}
		tr := wf.GetTransition("in progress", "testing")
		gated := false
		for _, a := range tr.Actions {
			gated = gated || a.Type == "validate"
		}
		if !gated {
			t.Errorf("API %s in progress → testing lost its validations: %+v", typ, tr.Actions)
		}
	}
	// A docs issue walks in progress → shipping: the overlay adds no edge to it.
	docs := repoWorkflow(t).ForIssue(&Issue{Type: "docs", System: "API", Status: "in progress"})
	if docs.GetTransition("in progress", "testing") != nil {
		t.Error("the API overlay opened an in progress → testing edge on the docs path")
	}

	other := repoWorkflow(t).ForIssue(&Issue{Type: "tweak", System: "CLI", Status: "in progress"})
	if strings.Contains(other.StatusPrompt("in progress"), "make security-check") {
		t.Error("the API route guidance reached a CLI issue")
	}
}

// TestCLIOverlayPointsAtAddingACommand: every CLI worker reads the pointer
// while building, and the heading it names is in docs/CLI/overview.md.
func TestCLIOverlayPointsAtAddingACommand(t *testing.T) {
	const pointer = `"Adding a command" in docs/CLI/overview.md`
	for _, typ := range []string{"feature", "tweak", "bugfix"} {
		wf := repoWorkflow(t).ForIssue(&Issue{Type: typ, System: "CLI", Status: "in progress"})
		prompt := wf.StatusPrompt("in progress")
		if !strings.Contains(prompt, pointer) {
			t.Errorf("CLI %s in progress prompt lacks the pointer:\n%s", typ, prompt)
		}
	}
	tweak := repoWorkflow(t).ForIssue(&Issue{Type: "tweak", System: "CLI", Status: "in progress"})
	if !strings.Contains(tweak.StatusPrompt("in progress"), "Tweak: build exactly what the body says") {
		t.Error("the CLI append replaced the tweak's own prompt")
	}
	other := repoWorkflow(t).ForIssue(&Issue{Type: "tweak", System: "API", Status: "in progress"})
	if strings.Contains(other.StatusPrompt("in progress"), pointer) {
		t.Error("the CLI pointer reached an API issue")
	}
	doc, err := os.ReadFile("../../docs/CLI/overview.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "\n### Adding a command\n") {
		t.Error(`docs/CLI/overview.md has no "Adding a command" section`)
	}
}
