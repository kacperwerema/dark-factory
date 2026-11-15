package lint

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The lint playtest scenario carries exactly the three warnings its session
// counts (playtest/sessions/health-lint.yaml TP4: "3 WARNINGS"): the unknown
// board key, the untemplated prompt and the undated issue. A new check that
// fires on the fixture shows here, in the fast tier, before the slow tier's
// session goes red on it: fix the fixture so the check passes, or, when the
// warning is the point, add it to the session and to this list.
func TestLintScenarioCarriesTheSessionsThreeWarnings(t *testing.T) {
	dir := filepath.Join("..", "..", "playtest", "scenarios", "lint")
	proj := &tracker.Project{Slug: "lintdemo", Name: "Lintdemo", WorkflowFile: filepath.Join(dir, "workflow.yaml"), IssueDir: filepath.Join(dir, "tracker")}
	env, err := Load(proj, lintNow)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(env)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range rep.Findings {
		got = append(got, f.Check+" "+f.Where)
	}
	sort.Strings(got)
	want := []string{"placeholders workflow.yaml:7", "schema workflow.yaml:23", "undated cli/voice-directions"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the lint scenario's warnings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
