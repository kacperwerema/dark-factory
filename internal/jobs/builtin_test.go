package jobs

import (
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// brief: builtin:memory-consolidate reads the bundled brief: no file in
// the project, the placeholders filled like a file brief's.
func TestBuiltinBriefMemoryConsolidate(t *testing.T) {
	p := project(t)
	p.Departments[0].Jobs = append(p.Departments[0].Jobs, tracker.Job{ID: "memory-consolidate", Every: "week", On: "sun", Brief: "builtin:memory-consolidate"})
	r, err := Find(p, "marketing/memory-consolidate")
	if err != nil {
		t.Fatal(err)
	}
	n, err := PlanRun(p, r, nil, "2026-W44", false)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := Brief(p, r, n, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Memory consolidation for marketing, run 2026-W44",
		"issue-cli memory consolidate",
		"--supersedes <id>,<id>",
		"never delete",
		"never edit CLAUDE.md",
		"Promote memories to CLAUDE.md (2026-W44)",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief misses %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "{{") {
		t.Errorf("unfilled placeholder:\n%s", brief)
	}
	if got := Builtins(); len(got) == 0 || got[0] != "memory-consolidate" {
		t.Errorf("Builtins() = %v", got)
	}
	for _, bad := range []string{"builtin:", "builtin:nope", "builtin:../workflow/content", "builtin:a/b"} {
		if _, err := BriefText(p, tracker.Job{Brief: bad}); err == nil || !strings.Contains(err.Error(), "bundled: builtin:memory-consolidate") {
			t.Errorf("%s: err %v", bad, err)
		}
	}
}
