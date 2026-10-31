package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSession(t *testing.T, dir, name, body string) {
	t.Helper()
	sessions := filepath.Join(dir, "playtest", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, name+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testPlanWorkflow(root string) *WorkflowConfig {
	return &WorkflowConfig{
		RepoRoot: root,
		Statuses: []WorkflowStatus{{Name: "backlog"}, {Name: "in progress"}, {Name: "testing"}},
		Transitions: []WorkflowTransition{
			{From: "backlog", To: "in progress", Actions: []WorkflowAction{
				{Type: "append_section", Title: "Test Plan", Body: "### Automated\n- [ ] Unit tests\n- [ ] make validate\n\n### Manual\n- [ ] Playtest session"},
			}},
			{From: "in progress", To: "testing", Actions: []WorkflowAction{
				{Type: "validate", Rule: "section_checkboxes_checked: Test Plan"},
			}},
		},
	}
}

// A session written in design names its steps after the Test Plan boxes
// that in progress will append. The append keeps those ids for the steps
// and numbers its own boxes after them.
func TestTestPlanAppendKeepsDesignSessionStepIDs(t *testing.T) {
	root := t.TempDir()
	slug := "ui/report-card"
	// The session is on the issue branch, in its worktree.
	writeSession(t, filepath.Join(root, ".worktrees", slug), "report-card", `session: report-card
issue: ui/report-card
steps:
  - id: TP1
    title: The card shows the count
  - id: TP2
    title: Pause holds it
  - id: TP3
    title: Looks right on a phone
    judgement: { do: look, expect: fits }
  - id: AC9
    title: not a Test Plan id
`)
	// Another issue's session is not read.
	writeSession(t, root, "other", "session: other\nissue: ui/other\nsteps:\n  - id: TP7\n    title: other\n")

	issue := &Issue{Slug: slug, BodyRaw: "## Acceptance criteria\n- [ ] One"}
	testPlanWorkflow(root).ApplyTransition(issue, "backlog", "in progress")

	ids := map[string]string{}
	for _, it := range ListCheckboxes(issue.BodyRaw) {
		ids[it.ID] = it.Label()
	}
	want := map[string]string{
		"AC1": "One",
		"TP1": "The card shows the count (playtest report-card)",
		"TP2": "Pause holds it (playtest report-card)",
		"TP3": "Looks right on a phone (playtest report-card)",
		"TP4": "Unit tests",
		"TP5": "make validate",
		"TP6": "Playtest session",
	}
	for id, label := range want {
		if ids[id] != label {
			t.Errorf("%s = %q, want %q\nbody:\n%s", id, ids[id], label, issue.BodyRaw)
		}
	}
	if len(ids) != len(want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

// Gaps in the session's numbering keep their slots, so TP5 is still TP5.
func TestTestPlanAppendKeepsGapsInSessionIDs(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "s", "session: s\nissue: x\nsteps:\n  - id: tp2\n    title: two\n  - id: TP4\n    title: four\n")
	issue := &Issue{Slug: "x", BodyRaw: "Intro."}
	testPlanWorkflow(root).ApplyTransition(issue, "backlog", "in progress")
	got := map[string]string{}
	for _, it := range ListCheckboxes(issue.BodyRaw) {
		got[it.ID] = it.Label()
	}
	if got["TP2"] != "two (playtest s)" || got["TP4"] != "four (playtest s)" || got["TP5"] != "Unit tests" {
		t.Fatalf("ids = %v\nbody:\n%s", got, issue.BodyRaw)
	}
	if _, ok := got["TP1"]; ok {
		t.Fatalf("a gap must not be a box: %v", got)
	}
}

// An existing Test Plan is never renumbered, and without a session the
// append is unchanged.
func TestTestPlanAppendLeavesExistingSectionAndNoSession(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "s", "session: s\nissue: x\nsteps:\n  - id: TP1\n    title: one\n")
	body := "## Test Plan\n### Automated\n- [x] Unit tests"
	issue := &Issue{Slug: "x", BodyRaw: body}
	testPlanWorkflow(root).ApplyTransition(issue, "backlog", "in progress")
	if strings.Contains(issue.BodyRaw, "playtest s") {
		t.Fatalf("existing section got session boxes:\n%s", issue.BodyRaw)
	}
	if it := ListCheckboxes(issue.BodyRaw)[0]; it.ID != "TP1" || !it.Checked {
		t.Fatalf("TP1 = %+v", it)
	}

	issue = &Issue{Slug: "y", BodyRaw: "Intro."}
	testPlanWorkflow(root).ApplyTransition(issue, "backlog", "in progress")
	if first := ListCheckboxes(issue.BodyRaw)[0]; first.ID != "TP1" || first.Text != "Unit tests" {
		t.Fatalf("no session: first box = %+v", first)
	}
}

// The rule docs/patterns/playtest/README.md shows: "## Reports" gives R1,
// and after "## Repro" took R it gives Re1.
func TestSectionAbbreviationDocExamples(t *testing.T) {
	got := func(body string) string {
		var out []string
		for _, it := range ListCheckboxes(body) {
			out = append(out, it.ID)
		}
		return strings.Join(out, " ")
	}
	if g := got("## Reports\n- [ ] a\n- [x] b"); g != "R1 R2" {
		t.Errorf("Reports = %q, want R1 R2", g)
	}
	if g := got("## Repro\n- [ ] a\n## Reports\n- [ ] b\n## Test Plan\n- [ ] c"); g != "R1 Re1 TP1" {
		t.Errorf("Repro then Reports = %q, want R1 Re1 TP1", g)
	}
}
