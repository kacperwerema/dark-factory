package tracker

import (
	"strings"
	"testing"
)

// The briefing's How-to-work steps name the one call that ticks the last
// boxes and moves, not check then transition.
func TestBriefingNamesTheOneCallTransition(t *testing.T) {
	steps := AgentDispatchPromptTemplate[strings.Index(AgentDispatchPromptTemplate, "## How to work\n"):]
	steps = steps[:strings.Index(steps[1:], "\n## ")+1]
	for _, want := range []string{
		`issue-cli transition {{slug}} --to "<next-status>" --check <id> [<id>...]`,
		"an owner box refuses the call",
		"no --dry-run needed",
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("How-to-work steps lack %q:\n%s", want, steps)
		}
	}
	if strings.Contains(steps, "Add --dry-run to list every unmet requirement") {
		t.Errorf("How-to-work steps still send the worker to a dry run before each move:\n%s", steps)
	}
}
