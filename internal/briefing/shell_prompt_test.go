package briefing

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestBuildDispatchPrompt_ShellSection(t *testing.T) {
	old := tracker.DetectShell
	t.Cleanup(func() { tracker.DetectShell = old })
	issue := &tracker.Issue{Slug: "api/fix", Title: "Fix", Status: "in progress"}

	wf := &tracker.WorkflowConfig{CI: &tracker.CIConfig{Run: []tracker.CICommand{{Cmd: "make validate"}}}}
	tracker.DetectShell = func() tracker.ShellFacts { return tracker.ShellFacts{Shell: "zsh", BSD: true, RTK: true} }
	if p := BuildDispatchPrompt(&tracker.Project{Slug: "proj"}, issue, wf, "/w", nil).Prompt; !strings.Contains(p, tracker.ShellPromptSection(tracker.DetectShell(), tracker.ProjectTestsFor("/w", wf))) {
		t.Fatalf("prompt missing the shell section: %q", p)
	} else {
		// The rendered briefing says when to stop and which line is the verdict.
		for _, want := range []string{"A `validate: red so far: <step>` line means that step is red: stop", "The verdict is the last `validate:` line, `validate: PASS` or `validate: FAIL (<step>, <step>)`"} {
			if !strings.Contains(p, want) {
				t.Errorf("briefing missing %q", want)
			}
		}
	}
	tracker.DetectShell = func() tracker.ShellFacts { return tracker.ShellFacts{Shell: "bash"} }
	if p := BuildDispatchPrompt(&tracker.Project{Slug: "proj"}, issue, nil, "/w", nil).Prompt; strings.Contains(p, "## Shell on this machine") {
		t.Fatalf("prompt has a shell section on a machine with no facts: %q", p)
	}
}

// A project whose CI is npm test and that has no go.mod: the whole briefing
// names npm test and never this repo's make validate or go test.
func TestBuildDispatchPrompt_NonGoProjectGetsItsOwnCI(t *testing.T) {
	oldShell, oldCI := tracker.DetectShell, tracker.CIStatusLine
	t.Cleanup(func() { tracker.DetectShell, tracker.CIStatusLine = oldShell, oldCI })
	tracker.DetectShell = func() tracker.ShellFacts { return tracker.ShellFacts{Shell: "zsh", BSD: true, RTK: true} }
	tracker.CIStatusLine = func(string, *tracker.WorkflowConfig) string { return "Local CI: main green at abc1234" }

	wf := &tracker.WorkflowConfig{CI: &tracker.CIConfig{Run: []tracker.CICommand{{Cmd: "npm test"}}}}
	issue := &tracker.Issue{Slug: "app/fix", Title: "Fix", Status: "in progress"}
	p := BuildDispatchPrompt(&tracker.Project{Slug: "app"}, issue, wf, t.TempDir(), nil).Prompt
	for _, want := range []string{"## Shell on this machine", "## Local CI", "When `npm test` or a test fails on your branch", "`npm test | tail`"} {
		if !strings.Contains(p, want) {
			t.Errorf("briefing missing %q", want)
		}
	}
	for _, not := range []string{"make validate", "go test"} {
		if strings.Contains(p, not) {
			t.Errorf("briefing for an npm project names %q:\n%s", not, p)
		}
	}
}
