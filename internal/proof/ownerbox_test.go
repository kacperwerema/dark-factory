package proof

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestOwnerBoxWhyIsTheOneRule(t *testing.T) {
	body := "## Design\n- [ ] plain\n\n## Human Testing\n- [ ] looked\n\n## Notes\n- [ ] glows (owner)\n- [ ] TP7 judged\n\n## Acceptance Criteria\n- [ ] shows a step\n"
	r := Report{
		Criteria:   []Criterion{{ID: "AC1", Owner: "playtest judgement step TP9"}},
		OwnerBoxes: []OwnerItem{{ID: "N2", Why: "playtest judgement step N2"}},
	}
	want := map[string]string{
		"D1":  "",
		"HT1": "a Human Testing box",
		"N1":  `marked "(owner)"`,
		"N2":  "playtest judgement step N2",
		"AC1": "playtest judgement step TP9",
	}
	for _, it := range tracker.ListCheckboxes(body) {
		if got := r.OwnerBoxWhy(it); got != want[it.ID] {
			t.Errorf("%s: OwnerBoxWhy = %q, want %q", it.ID, got, want[it.ID])
		}
	}
	// A zero report (no proof links in the workflow) still knows the markers.
	for _, it := range tracker.ListCheckboxes(body) {
		if got := (Report{}).OwnerBoxWhy(it); (got != "") != (it.ID == "HT1" || it.ID == "N1") {
			t.Errorf("zero report, %s: OwnerBoxWhy = %q", it.ID, got)
		}
	}
}

func TestOwnerTicksSeesEveryWayAWriteTicksAnOwnerBox(t *testing.T) {
	before := "## Design\n- [ ] plain\n\n## Human Testing\n- [ ] looked\n- [x] already\n\n## Notes\n- [ ] glows (owner)\n"
	r := Report{}
	ids := func(items []OwnerItem) string {
		var out []string
		for _, o := range items {
			out = append(out, o.ID+":"+o.Text)
		}
		return strings.Join(out, ",")
	}
	for _, c := range []struct {
		name, after, want string
	}{
		{"own box", strings.Replace(before, "- [ ] plain", "- [x] plain", 1), ""},
		{"Human Testing box", strings.Replace(before, "- [ ] looked", "- [x] looked", 1), "HT1:looked"},
		{"(owner) box", strings.Replace(before, "- [ ] glows", "- [x] glows", 1), "N1:glows (owner)"},
		{"new ticked owner box", before + "- [x] signed (owner)\n", "N2:signed (owner)"},
		{"moved ticked box", strings.Replace(before, "- [ ] looked\n- [x] already", "- [x] already\n- [ ] looked", 1), ""},
		{"no change", before, ""},
	} {
		if got := ids(r.OwnerTicks(before, c.after)); got != c.want {
			t.Errorf("%s: OwnerTicks = %q, want %q", c.name, got, c.want)
		}
	}
}

// A criterion marked "(owner)" after the report was read still doesn't
// tick: TickProven asks the rule of the box as the file has it.
func TestTickProvenAsksTheRuleOfTheBoxOnFile(t *testing.T) {
	f := newFixture(t, "TestFoo")
	r := Collect(f.root, f.issue, testsComment(t0.Add(5*time.Hour)))
	if c := criterion(t, r, "AC2"); c.Owner != "" || !c.Proven() {
		t.Fatalf("AC2 = owner %q proven %v, want a plain proven criterion", c.Owner, c.Proven())
	}
	body, _ := os.ReadFile(f.issue.FilePath)
	marked := strings.Replace(string(body), "- [ ] The change proves it", "- [ ] The change proves it (owner)", 1)
	if err := os.WriteFile(f.issue.FilePath, []byte(marked), 0o644); err != nil {
		t.Fatal(err)
	}
	ticked, _ := TickProven(f.issue.FilePath, r)
	if strings.Contains(strings.Join(ticked, ","), "AC2") {
		t.Errorf("ticked %v: an (owner) box on file must not tick from a stale report", ticked)
	}
}
