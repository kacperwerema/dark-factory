package checks

import "testing"

// A branch "ends in the slug" by the whole slug or by its last segment; a
// HANDOFF.md names a slug only as a whole word, after a slash too
// (work/<slug>), never inside a longer slug or a path below it.
func TestBranchAndHandoffMatching(t *testing.T) {
	for _, c := range []struct {
		branch string
		want   bool
	}{
		{"work/testy/ocena", true},
		{"testy/ocena", true},
		{"work/ocena", true},
		{"feature/ocena", true},
		{"work/ocena-2", false},
		{"work/ocen", false},
		{"", false},
	} {
		if got := BranchNamesSlug(c.branch, "testy/ocena"); got != c.want {
			t.Errorf("BranchNamesSlug(%q) = %v, want %v", c.branch, got, c.want)
		}
	}
	for _, c := range []struct {
		text string
		want bool
	}{
		{"# HANDOFF: testy/ocena\n", true},
		{"branch work/testy/ocena.", true},
		{"(testy/ocena)", true},
		{"testy/ocena-more", false},
		{"testy/ocena/sub", false},
		{"xtesty/ocena", false},
		{"", false},
	} {
		if got := namesWord(c.text, "testy/ocena"); got != c.want {
			t.Errorf("namesWord(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}
