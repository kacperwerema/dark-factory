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

	tracker.DetectShell = func() tracker.ShellFacts { return tracker.ShellFacts{Shell: "zsh", BSD: true, RTK: true} }
	if p := BuildDispatchPrompt(&tracker.Project{Slug: "proj"}, issue, nil, "/w", nil).Prompt; !strings.Contains(p, tracker.ShellPromptSection(tracker.DetectShell())) {
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
