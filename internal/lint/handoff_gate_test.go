package lint

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A handoff_answered rule naming no department of the project is a
// departments finding; one naming a department isn't.
func TestHandoffGateFindings(t *testing.T) {
	wf := strings.Replace(minimalWorkflow, "    actions:\n", "    actions:\n      - type: validate\n        rule: \"handoff_answered: legal\"\n        paths: [\"server/**\"]\n", 1)
	if wf == minimalWorkflow {
		t.Fatal("minimalWorkflow changed: update the test")
	}
	env := lintProject(t, wf, nil)
	find := func() string {
		var got []string
		for _, f := range checkDepartments(env) {
			got = append(got, f.Where+": "+f.Message)
		}
		return strings.Join(got, "\n")
	}
	want := "projects.yaml (Product): idea → done: handoff_answered: legal names no department of this project"
	if all := find(); !strings.Contains(all, want) {
		t.Fatalf("missing %q in:\n%s", want, all)
	}
	env.Project.Departments = []tracker.Department{{Name: "Legal"}}
	if all := find(); strings.Contains(all, "handoff_answered") {
		t.Fatalf("flagged a department that exists:\n%s", all)
	}
}
