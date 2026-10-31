package tracker

import (
	"reflect"
	"strings"
	"testing"
)

const editBody = `## Design
- [x] Approach documented
- [ ] Dependencies identified

## Acceptance criteria
- [ ] AC one
- [x] AC two
- [ ] AC three
- [ ] AC four`

func TestRemoveBoxesKeepsLaterIDs(t *testing.T) {
	body, removed, err := RemoveBoxes(editBody, []string{"AC2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].ID != "AC2" || removed[0].Text != "AC two" {
		t.Fatalf("removed = %+v", removed)
	}
	if !strings.Contains(body, "<!-- removed AC2: AC two -->") {
		t.Fatalf("no removed-box line in:\n%s", body)
	}
	got := map[string]string{}
	for _, it := range ListCheckboxes(body) {
		got[it.ID] = it.Text
	}
	want := map[string]string{"D1": "Approach documented", "D2": "Dependencies identified", "AC1": "AC one", "AC3": "AC three", "AC4": "AC four"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ids after removing AC2 = %v, want %v", got, want)
	}
	if total, checked := CountCheckboxesInSection(body, "Acceptance Criteria"); total != 3 || checked != 0 {
		t.Fatalf("criteria count = %d/%d, want 0/3", checked, total)
	}

	// A box added later takes the next number: AC2 is never reused.
	items := ListCheckboxes(body + "\n- [ ] AC five")
	if last := items[len(items)-1]; last.ID != "AC5" {
		t.Fatalf("new box id = %s, want AC5", last.ID)
	}
}

func TestRemoveBoxesKeepsSectionAbbreviationOrder(t *testing.T) {
	// Design took "D" first; removing all of its boxes must not hand "D"
	// to Documentation.
	body := "## Design\n- [ ] only box\n\n## Documentation\n- [ ] docs"
	body, _, err := RemoveBoxes(body, []string{"D1"})
	if err != nil {
		t.Fatal(err)
	}
	if ids := idsOf(ListCheckboxes(body)); strings.Join(ids, ",") != "Do1" {
		t.Fatalf("ids = %v, want [Do1]", ids)
	}
}

func TestRemoveBoxesAllOrNothing(t *testing.T) {
	body, removed, err := RemoveBoxes(editBody, []string{"AC1", "AC9"})
	if err == nil || !strings.Contains(err.Error(), "AC9") || !strings.Contains(err.Error(), "AC4") {
		t.Fatalf("err = %v, want one naming AC9 and listing the ids", err)
	}
	if body != editBody || removed != nil {
		t.Fatalf("body changed on error")
	}
}

func TestRemoveBoxesEscapesCommentEnd(t *testing.T) {
	body, _, err := RemoveBoxes("## Design\n- [ ] use --> arrows", []string{"D1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(body, "-->") != 1 {
		t.Fatalf("removed line closes early: %q", body)
	}
}

func TestUncheckBoxes(t *testing.T) {
	body, unticked, already, err := UncheckBoxes(editBody, []string{"D1", "ac2", "AC3"})
	if err != nil {
		t.Fatal(err)
	}
	if idsOf(unticked)[0] != "D1" || idsOf(unticked)[1] != "AC2" || len(unticked) != 2 {
		t.Fatalf("unticked = %v", idsOf(unticked))
	}
	if len(already) != 1 || already[0].ID != "AC3" {
		t.Fatalf("already = %v", idsOf(already))
	}
	if total, checked := CountCheckboxes(body); total != 6 || checked != 0 {
		t.Fatalf("count = %d/%d, want 0/6", checked, total)
	}
	if _, _, _, err := UncheckBoxes(editBody, []string{"X1"}); err == nil {
		t.Fatal("unknown id: want an error")
	}
}

func TestUncheckBoxesUpperCaseX(t *testing.T) {
	body, _, _, err := UncheckBoxes("## Design\n  - [X] indented", []string{"D1"})
	if err != nil || body != "## Design\n  - [ ] indented" {
		t.Fatalf("body = %q, err = %v", body, err)
	}
}

func TestAppendSectionSkipsStartersInTheAuthorsSection(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "idea"}, {Name: "in design"}, {Name: "backlog"}},
		Transitions: []WorkflowTransition{
			{From: "idea", To: "in design", Actions: []WorkflowAction{
				{Type: "append_section", Title: "Design", Body: "- [ ] Approach documented"},
				{Type: "append_section", Title: "Acceptance Criteria", Body: "- [ ] First observable requirement\n- [ ] Second observable requirement"},
			}},
			{From: "in design", To: "backlog", Actions: []WorkflowAction{
				{Type: "validate", Rule: "section_checkboxes_checked: Design"},
				{Type: "validate", Rule: "section_has_checkboxes: Acceptance Criteria"},
			}},
		},
	}
	body := "Intro.\n\n## Acceptance criteria\n- [ ] Real criterion one\n- [ ] Real criterion two"
	issue := &Issue{BodyRaw: body}
	result := wf.ApplyTransition(issue, "idea", "in design")

	if strings.Contains(issue.BodyRaw, "observable requirement") {
		t.Fatalf("starters added to a section with its own boxes:\n%s", issue.BodyRaw)
	}
	if !strings.Contains(issue.BodyRaw, "## Design\n- [ ] Approach documented") {
		t.Fatalf("Design not appended:\n%s", issue.BodyRaw)
	}
	want := []SectionAppend{{Title: "Design", Created: true, BoxesAdded: 1}, {Title: "Acceptance Criteria", StartersSkipped: 2}}
	if !reflect.DeepEqual(result.Sections, want) {
		t.Fatalf("sections = %+v, want %+v", result.Sections, want)
	}

	// An ungated section that exists with no boxes still gets them.
	issue = &Issue{BodyRaw: "## Acceptance Criteria\nTo be written."}
	wf.ApplyTransition(issue, "idea", "in design")
	if total, _ := CountCheckboxesInSection(issue.BodyRaw, "Acceptance Criteria"); total != 2 {
		t.Fatalf("empty section got %d boxes, want 2:\n%s", total, issue.BodyRaw)
	}
}

func TestGatesSection(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "a", Validation: []string{"section_checkboxes_checked: Shipping"}}},
		Transitions: []WorkflowTransition{{From: "a", To: "b", Actions: []WorkflowAction{
			{Type: "validate", Rule: "section_checkboxes_checked: Design"},
			{Type: "validate", Rule: "section_has_checkboxes: Acceptance Criteria"},
		}}},
	}
	for title, want := range map[string]bool{"Design": true, "## design": true, "Shipping": true, "Acceptance Criteria": false, "Idea": false} {
		if got := wf.GatesSection(title); got != want {
			t.Errorf("GatesSection(%q) = %v, want %v", title, got, want)
		}
	}
	all := &WorkflowConfig{Transitions: []WorkflowTransition{{Actions: []WorkflowAction{{Type: "validate", Rule: "all_checkboxes_checked"}}}}}
	if !all.GatesSection("Idea") {
		t.Error("all_checkboxes_checked gates every section")
	}
}
