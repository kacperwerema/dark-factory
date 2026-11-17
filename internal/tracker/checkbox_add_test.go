package tracker

import (
	"strings"
	"testing"
)

func TestAddCheckboxTakesTheNextIDAfterTheLastBox(t *testing.T) {
	body := "## Design\n- [x] one\n\n## Acceptance criteria\n- [ ] a\n<!-- removed AC2: b -->\n- [ ] c\n\nTrailing prose.\n\n## Notes\n- [ ] n\n"
	next, item, err := AddCheckbox(body, "acceptance CRITERIA", "AC4: d")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "AC4" || item.Section != "Acceptance criteria" || item.Text != "d" {
		t.Errorf("item = %+v", item)
	}
	if !strings.Contains(next, "- [ ] c\n- [ ] d\n\nTrailing prose.") {
		t.Errorf("box not placed after the last box:\n%s", next)
	}
	before, after := ListCheckboxes(body), ListCheckboxes(next)
	ids := map[string]string{}
	for _, it := range after {
		ids[it.Text] = it.ID
	}
	for _, it := range before {
		if ids[it.Text] != it.ID {
			t.Errorf("%q moved from %s to %s", it.Text, it.ID, ids[it.Text])
		}
	}
}

func TestAddCheckboxKeepsIndentAndFillsAnEmptySection(t *testing.T) {
	next, item, err := AddCheckbox("## Design\n  - [ ] one\n## Notes\nsome words\n\n", "Design", "two")
	if err != nil || item.ID != "D2" || !strings.Contains(next, "  - [ ] one\n  - [ ] two\n") {
		t.Fatalf("indent: %v %+v\n%s", err, item, next)
	}
	next, item, err = AddCheckbox("## Design\n- [ ] one\n## Notes\nsome words\n\n", "Notes", "first")
	if err != nil || item.ID != "N1" || !strings.Contains(next, "some words\n- [ ] first\n\n") {
		t.Fatalf("empty section: %v %+v\n%s", err, item, next)
	}
}

func TestAddCheckboxRefusals(t *testing.T) {
	body := "## Design\n- [ ] one\n## Docs\n\n## Done\n- [ ] d\n"
	for _, c := range []struct{ section, text, want string }{
		{"Nope", "x", `no section "Nope"; sections in this issue: "Design", "Docs", "Done"`},
		{"Design", "a\nb", "one line"},
		{"Design", " ", "needs text"},
		// A first box in Docs would claim "Do" before Done: Done's DO1 would move.
		{"Docs", "x", "would change other boxes' ids (Do1 would become Don1)"},
	} {
		next, _, err := AddCheckbox(body, c.section, c.text)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s/%q: want %q, got %v", c.section, c.text, c.want, err)
		}
		if next != body {
			t.Errorf("%s/%q: a refusal changed the body", c.section, c.text)
		}
	}
}
