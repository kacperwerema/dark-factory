package tracker

import (
	"reflect"
	"strings"
	"testing"
)

// TestShorthandRefs is the reading itself: a bare #D-n takes the slug of a
// full ref on its line, and nothing else makes it a citation.
func TestShorthandRefs(t *testing.T) {
	known := map[string]bool{"sim/rules": true, "sim/epic": true}
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"after a full ref", "Follows sim/rules#D-5 and #D-6.", []string{"sim/rules#D-6"}},
		{"a list of them", "sim/rules#D-5, #D-6, #D-7", []string{"sim/rules#D-6", "sim/rules#D-7"}},
		{"in backticks", "`sim/rules#D-5`, `#D-6`", []string{"sim/rules#D-6"}},
		{"before the full ref", "#D-6 and sim/rules#D-5", []string{"sim/rules#D-6"}},
		{"the nearest full ref before it", "sim/rules#D-1, sim/epic#D-2, #D-3", []string{"sim/epic#D-3"}},
		{"between two full refs", "sim/rules#D-1, #D-2, sim/epic#D-3, #D-4", []string{"sim/epic#D-4", "sim/rules#D-2"}},
		{"after a slash", "sim/rules#D-5/#D-6", []string{"sim/rules#D-6"}},
		{"alone on its line", "sim/rules#D-5\n#D-6", nil},
		{"only an unknown slug on the line", "sim/gone#D-5 and #D-6", nil},
		{"a full ref is not a bare one", "sim/rules#D-5 and sim/gone#D-6", nil},
		{"glued to a word", "sim/rules#D-5 and Foo#D-6", nil},
		{"inside a replaces parenthetical", "sim/rules#D-5 (replaces #D-3)", nil},
		{"the full ref only in a replaces parenthetical", "#D-6 (replaces sim/rules#D-5)", nil},
		{"in a fence", "```\nsim/rules#D-5 and #D-6\n```", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shorthandRefs(tc.text, known); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("shorthandRefs(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestDecisionsCitedShorthand is the issue's What: the gate that held five
// designs on "<ref> and #D-n" takes them, and only on the full ref's line.
func TestDecisionsCitedShorthand(t *testing.T) {
	const second = "- **D-6** · 2026-10-06 · chat: A second hard rule, \"two\" {scope: project; hard}\n"
	for _, tc := range []struct {
		name, design string
		want         []string // substrings of the refusal; none: passes
	}{
		{"shorthand after a full ref", "Follows orchard/rules#D-1 and #D-6. Keeps orchard/epic#D-1.", nil},
		{"shorthand alone on its line holds", "Follows orchard/rules#D-1. Keeps orchard/epic#D-1.\nAlso #D-6.", []string{"1 hard decision in force is not cited", "orchard/rules#D-6"}},
		{"another issue's number holds", "Follows orchard/epic#D-1 and #D-6. Keeps orchard/rules#D-1.", []string{"orchard/rules#D-6"}},
		{"shorthand in an n/a line's reason holds", "Keeps orchard/epic#D-1 and orchard/rules#D-1.\nn/a orchard/rules#D-2: see #D-6", []string{"orchard/rules#D-6"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf, issue := citedFixture(tc.design, "")
			addHardRule(t, wf, second)
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
			for _, s := range tc.want {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("refusal lacks %q:\n%v", s, err)
				}
			}
		})
	}
}

// TestBodyCitationsReadShorthand: a design decisions_cited took must not be
// held for the same ref by decisions_respected on the way to testing.
func TestBodyCitationsReadShorthand(t *testing.T) {
	x := BuildDecisionIndex([]*Issue{{Slug: "sim/rules", BodyRaw: "## Decisions\n\n" +
		"- **D-5** · 2026-10-05 · chat: One, \"one\" {scope: project; hard}\n" +
		"- **D-6** · 2026-10-06 · chat: Two, \"two\" {scope: project; hard}\n"}}, nil)
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"in Design", "## Design\n\nFollows sim/rules#D-5 and #D-6.\n", true},
		{"outside Design", "## Idea\n\nsim/rules#D-5 and #D-6.\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := BodyCitations(x, &Issue{Slug: "sim/child", BodyRaw: tc.body})
			if got["sim/rules#D-6"] != tc.want {
				t.Errorf("sim/rules#D-6 cited = %v, want %v (%v)", got["sim/rules#D-6"], tc.want, got)
			}
		})
	}
}

// addHardRule rebuilds wf's index with text added to orchard/rules' D-n
// lines.
func addHardRule(t *testing.T, wf *WorkflowConfig, text string) {
	t.Helper()
	x, opts := wf.DecisionsInForce()
	var issues []*Issue
	for _, is := range x.issues {
		c := *is
		if c.Slug == "orchard/rules" {
			c.BodyRaw += text
		}
		issues = append(issues, &c)
	}
	rebuilt := BuildDecisionIndex(issues, nil)
	wf.DecisionsInForce = func() (*DecisionIndex, InForceOptions) { return rebuilt, opts }
}
