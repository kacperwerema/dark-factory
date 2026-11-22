package tracker

import "testing"

// A box above any section heading has no id: checklist lists it as "1.",
// "2.". Its number, "#n" or "n." names it, and only it: a bare number never
// reaches a box in a named section.
func TestResolveCheckboxRefLooseBoxByNumber(t *testing.T) {
	items := ListCheckboxes("- [ ] loose one\n- [ ] loose two\n\n## Design\n- [ ] Approach\n")
	for _, tc := range []struct {
		ref      string
		status   RefStatus
		wantText string
	}{
		{"1", RefFound, "loose one"},
		{"#2", RefFound, "loose two"},
		{"2.", RefFound, "loose two"},
		{" # 1 ", RefFound, "loose one"},
		{"3", RefNoSuchBox, ""},
		{"D1", RefFound, "Approach"},
	} {
		it, st := ResolveCheckboxRef(items, tc.ref)
		if st != tc.status {
			t.Errorf("ResolveCheckboxRef(%q) status = %v, want %v", tc.ref, st, tc.status)
			continue
		}
		if st == RefFound && it.Text != tc.wantText {
			t.Errorf("ResolveCheckboxRef(%q) = %q, want %q", tc.ref, it.Text, tc.wantText)
		}
	}
	// No loose boxes: a number is not a ref, so check reads it as text.
	if _, st := ResolveCheckboxRef(ListCheckboxes("## Design\n- [ ] step 1\n"), "1"); st != RefNotARef {
		t.Errorf("no loose boxes: \"1\" status = %v, want RefNotARef", st)
	}
	if !IsLooseRefShaped("#3") || !IsLooseRefShaped("3.") || IsLooseRefShaped("D3") || IsLooseRefShaped("x") {
		t.Error("IsLooseRefShaped: want true for #3 and 3., false for D3 and x")
	}
}

// A section whose heading gives no abbreviation ("## 2026") lists its boxes
// as "1." too: its long form "2026#1" names them.
func TestResolveCheckboxRefLongFormReachesIdlessSection(t *testing.T) {
	items := ListCheckboxes("## 2026\n- [ ] dated box\n")
	it, st := ResolveCheckboxRef(items, "2026#1")
	if st != RefFound || it.Text != "dated box" {
		t.Fatalf("2026#1 = %q, %v; want the dated box", it.Text, st)
	}
}
