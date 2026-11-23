package testids

import (
	"reflect"
	"strings"
	"testing"
)

func TestSegment(t *testing.T) {
	for in, want := range map[string]string{
		"in progress":         "in_progress",
		"human-testing":       "human-testing",
		" Backlog ":           "backlog",
		"ui/dark-mode-toggle": "ui/dark-mode-toggle",
		"v1.2 release":        "v1_2_release",
	} {
		if got := Segment(in); got != want {
			t.Errorf("Segment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegexp(t *testing.T) {
	re := Regexp("board.column.*.card.#")
	for id, want := range map[string]bool{
		"board.column.in_progress.card.0": true,
		"board.column.backlog.card.12":    true,
		"board.column.backlog.card.x":     false,
		"board.column.card.0":             false,
		"board.column.a.b.card.0":         false,
		"board.column.ui/x-y.card.3":      true,
	} {
		if got := re.MatchString(id); got != want {
			t.Errorf("%q matches %s = %v, want %v", id, re, got, want)
		}
	}
}

func TestCompatible(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"board.card.*", "board.card.ui/x", true},
		{"board.card.*", "board.card.*", true},
		{"board.card.*", "board.card.x.title", false},
		{"detail.comment.eye.#", "detail.comment.eye.3", true},
		{"detail.comment.eye.#", "detail.comment.eye.*", true},
		{"detail.comment.eye.#", "detail.comment.eye.x", false},
		{"list.row.*", "board.row.x", false},
	}
	for _, c := range cases {
		if got := Compatible(c.a, c.b); got != c.want {
			t.Errorf("Compatible(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
		if got := Compatible(c.b, c.a); got != c.want {
			t.Errorf("Compatible(%q, %q) = %v, want %v (symmetry)", c.b, c.a, got, c.want)
		}
	}
}

func TestKnownAndSuggest(t *testing.T) {
	if !Known("board.card.ui/dark-mode-toggle.title") {
		t.Error("a concrete card title id should be known")
	}
	if Known("board.colum.backlog") {
		t.Error("a misspelt id should not be known")
	}
	if got := Suggest("board.colum.*"); got != "board.column.*" {
		t.Errorf("Suggest = %q, want board.column.*", got)
	}
	if got := Suggest("zzzzzzzzzzzzzzzzzzzzzzzzzz"); got != "" {
		t.Errorf("Suggest for nonsense = %q, want empty", got)
	}
}

func TestScanTemplate(t *testing.T) {
	src := `<div data-testid="board.column.{{tid .Status}}">
<span data-testid="header.count">
html += '<button data-testid="detail.comment.eye.' + idx + '">';
cb.dataset.testid = 'detail.body.checkbox.' + idx;
el.dataset.testid = 'list.empty';`
	want := []string{"board.column.*", "header.count", "detail.comment.eye.*", "detail.body.checkbox.*", "list.empty"}
	if got := ScanTemplate(src); !reflect.DeepEqual(got, want) {
		t.Errorf("ScanTemplate = %v, want %v", got, want)
	}
}

func TestIDsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range IDs {
		if seen[id] {
			t.Errorf("duplicate id %q", id)
		}
		seen[id] = true
		if !WellFormed(id) {
			t.Errorf("id %q is not lower snake case with dots", id)
		}
	}
}

// TestKnownMatchesEveryListedID checks the index against the walk it
// replaced, on every entry and the ids and patterns made from it. The walk
// is Compatible over every entry, with the entries split once and each id
// checked once: split on every call it took about 4 s (25 s on a loaded
// machine) for the same answers.
func TestKnownMatchesEveryListedID(t *testing.T) {
	var entries [][]string
	for _, e := range allIDs() {
		entries = append(entries, strings.Split(e, "."))
	}
	walk := func(id string) bool {
		segs := strings.Split(id, ".")
		for _, e := range entries {
			if segsCompatible(e, segs) {
				return true
			}
		}
		return false
	}
	checked := map[string]bool{}
	check := func(id string) {
		t.Helper()
		if checked[id] {
			return
		}
		checked[id] = true
		if got, want := Known(id), walk(id); got != want {
			t.Errorf("Known(%q) = %v, the walk says %v", id, got, want)
		}
	}
	for _, id := range []string{"", ".", "x", "*", "#", "*.*", "#.#", "*.*.*.*.*.*.*.*", "status-*.x", "a.status-*.b"} {
		check(id)
	}
	for _, e := range allIDs() {
		check(e)
		check(e + ".extra")
		segs := strings.Split(e, ".")
		check(strings.Join(segs[:len(segs)-1], "."))
		for _, fill := range []string{"x", "7", "ui/some-slug"} {
			check(strings.NewReplacer("*", fill, "#", fill).Replace(e))
		}
		for i, seg := range segs {
			for _, swap := range []string{"*", "#", "7", "nope", seg + "x"} {
				other := append([]string(nil), segs...)
				other[i] = swap
				check(strings.Join(other, "."))
			}
		}
	}
}
