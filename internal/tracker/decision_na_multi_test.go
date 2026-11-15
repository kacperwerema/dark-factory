package tracker

import (
	"strings"
	"testing"
)

// TestDesignCitationsMultiRefNA is AC1: "n/a <ref>, <ref>: <why>" sets each
// ref aside with the same reason and cites none of them; a later bare #D-n
// takes the slug of the ref before it.
func TestDesignCitationsMultiRefNA(t *testing.T) {
	known := map[string]bool{"a": true, "b": true}
	for _, tc := range []struct {
		name, design string
		na           map[string]string
		cited        []string
	}{
		{"two full refs", "n/a a#D-1, a#D-2: not touched", map[string]string{"a#D-1": "not touched", "a#D-2": "not touched"}, nil},
		{"list item, backticks, shorthand", "- n/a `a#D-9`, #D-7, `b#D-6`, #D-5: docs only", map[string]string{"a#D-9": "docs only", "a#D-7": "docs only", "b#D-6": "docs only", "b#D-5": "docs only"}, nil},
		{"no reason", "n/a a#D-1, a#D-2", map[string]string{"a#D-1": "", "a#D-2": ""}, nil},
		{"beside a citation", "n/a a#D-1, a#D-2: not touched\nFollows b#D-1.", map[string]string{"a#D-1": "not touched", "a#D-2": "not touched"}, []string{"b#D-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := readDesignCitations("## Design\n\n"+tc.design+"\n", known)
			if len(got.na) != len(tc.na) {
				t.Errorf("na = %v, want %v", got.na, tc.na)
			}
			for ref, why := range tc.na {
				if g, ok := got.na[ref]; !ok || g != why {
					t.Errorf("na[%s] = %q (%v), want %q", ref, g, ok, why)
				}
				if got.cited[ref] {
					t.Errorf("%s counted as cited", ref)
				}
			}
			if len(got.cited) != len(tc.cited) {
				t.Errorf("cited = %v, want %v", got.cited, tc.cited)
			}
			for _, ref := range tc.cited {
				if !got.cited[ref] {
					t.Errorf("%s not cited", ref)
				}
			}
			if len(got.badNA) != 0 {
				t.Errorf("badNA = %v, want none", got.badNA)
			}
		})
	}
}

// TestDesignCitationsUnparsedNA: a line that starts with n/a but doesn't
// parse is a format problem, never a citation.
func TestDesignCitationsUnparsedNA(t *testing.T) {
	known := map[string]bool{"a": true}
	for _, line := range []string{
		"n/a a#D-1 and a#D-2: not touched",
		"n/a a#D-1, a#D-2 because docs only",
		"- n/a: a#D-1 does not apply",
	} {
		got := readDesignCitations("## Design\n\n"+line+"\n", known)
		if len(got.cited) != 0 {
			t.Errorf("%q: cited = %v, want none", line, got.cited)
		}
		if _, ok := got.badNA["a#D-1"]; !ok {
			t.Errorf("%q: badNA = %v, want a#D-1", line, got.badNA)
		}
		if len(got.na) != 0 {
			t.Errorf("%q: na = %v, want none", line, got.na)
		}
	}
}

// TestDecisionsCitedMultiRefNA is AC2 through the gate: the several-ref form
// passes with a reason, is refused without one, and a line that doesn't
// parse is refused with the form to write.
func TestDecisionsCitedMultiRefNA(t *testing.T) {
	for _, tc := range []struct {
		name, design string
		want         []string
	}{
		{"several refs with a reason", "n/a orchard/rules#D-1, orchard/epic#D-1: docs only", nil},
		{"several refs, no reason", "n/a orchard/rules#D-1, orchard/epic#D-1", []string{"2 hard decisions in force are not cited", "n/a orchard/rules#D-1: <why", "its n/a line has no reason"}},
		{"several refs, empty reason", "- n/a orchard/rules#D-1, orchard/epic#D-1:  ", []string{"its n/a line has no reason"}},
		{"unparsed n/a", "n/a orchard/rules#D-1 and orchard/epic#D-1: docs only", []string{"2 hard decisions in force are not cited", "its n/a line doesn't parse", "n/a <ref>[, <ref>…]: <why>"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf, issue := citedFixture(tc.design, "")
			err := wf.ValidateTransition(issue, "in design", "backlog", nil)
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("want pass, got:\n%v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want a refusal, got pass")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal lacks %q:\n%v", w, err)
				}
			}
		})
	}
}
