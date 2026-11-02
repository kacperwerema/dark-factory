package tracker

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const attrsBase = `- **D-4** · 2026-10-31 · chat: Agents merge into local main, "only main" (also considered: a PR each) (replaces x/old#D-1)`

// TestDecisionAttrsRoundTrip: a line with the group parses and renders back
// to the same bytes, for each key alone and all together (AC2).
func TestDecisionAttrsRoundTrip(t *testing.T) {
	cases := map[string]struct {
		group string
		want  DecisionAttrs
	}{
		"scope project":    {"{scope: project}", DecisionAttrs{Scope: "project"}},
		"scope system":     {"{scope: system:Workflow}", DecisionAttrs{Scope: "system:Workflow"}},
		"scope department": {"{scope: department:marketing}", DecisionAttrs{Scope: "department:marketing"}},
		"scope type":       {"{scope: type:bugfix}", DecisionAttrs{Scope: "type:bugfix"}},
		"hard":             {"{hard}", DecisionAttrs{Scope: "issue", Hard: true}},
		"at":               {"{at: in design, in progress}", DecisionAttrs{Scope: "issue", At: []string{"in design", "in progress"}}},
		"paths":            {"{paths: cmd/issue-cli/**, internal/tracker/decisions*.go}", DecisionAttrs{Scope: "issue", Paths: []string{"cmd/issue-cli/**", "internal/tracker/decisions*.go"}}},
		"expires":          {"{expires: 2027-03-31}", DecisionAttrs{Scope: "issue", Expires: "2027-03-31"}},
		"ended":            {"{hard; expires: 2026-10-30; ended: 2026-11-02}", DecisionAttrs{Scope: "issue", Hard: true, Expires: "2026-10-30", Ended: "2026-11-02"}},
		"all": {"{scope: system:Workflow; hard; at: in progress; paths: cmd/**; expires: 2027-03-31}",
			DecisionAttrs{Scope: "system:Workflow", Hard: true, At: []string{"in progress"}, Paths: []string{"cmd/**"}, Expires: "2027-03-31"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			line := attrsBase + " " + c.group
			d, ok := parseDecisionLine("x/y", line)
			if !ok {
				t.Fatalf("does not parse: %s", line)
			}
			if !reflect.DeepEqual(d.DecisionAttrs, c.want) {
				t.Fatalf("attrs = %+v, want %+v", d.DecisionAttrs, c.want)
			}
			if d.Text != "Agents merge into local main" || d.Quote != "only main" || d.AlsoConsidered != "a PR each" || len(d.Replaces) != 1 {
				t.Fatalf("the rest of the line parsed differently: %+v", d)
			}
			if got := renderParsedDecision(d); got != line {
				t.Fatalf("render =\n%s\nwant\n%s", got, line)
			}
			if d.Raw[d.AttrsAt:] != " "+c.group {
				t.Fatalf("AttrsAt points at %q", d.Raw[d.AttrsAt:])
			}
			nd := NewDecision{Text: "Agents merge into local main.", How: "chat", Quote: "only main", Replaces: d.Replaces, Attrs: c.want}
			if got := RenderDecisionLine("D-4", nd, time.Date(2026, 10, 31, 0, 0, 0, 0, time.Local)); got != `- **D-4** · 2026-10-31 · chat: Agents merge into local main, "only main" (replaces x/old#D-1) `+c.group {
				t.Fatalf("new line = %s", got)
			}
		})
	}
}

// TestLegacyBraceGroupStaysText: a trailing {…} that is not an attribute
// group stays text, exactly as before the group existed (AC6).
func TestLegacyBraceGroupStaysText(t *testing.T) {
	for _, line := range []string{
		`- **D-8** · 2026-10-20 · chat: Engine requests are declared as {from: marketing, to: product, via: hq}`,
		`- **D-8** · 2026-10-20 · chat: Mixed keys stay text {scope: project; from: marketing}`,
		`- **D-8** · 2026-10-20 · chat: A bad value stays text {expires: soon}`,
		`- **D-8** · 2026-10-20 · chat: An empty one stays text {}`,
		`- **D-8** · 2026-10-20 · chat: A group mid-text {scope: project; hard} stays text`,
		`- **D-8** · 2026-10-20 · chat: Twice is not a group {hard; hard}`,
	} {
		d, ok := parseDecisionLine("x/y", line)
		if !ok {
			t.Fatalf("does not parse: %s", line)
		}
		_, text, _ := cutHowText(line)
		if d.Text != text || d.AttrsAt != -1 || !d.DecisionAttrs.Empty() || d.Hard {
			t.Errorf("%s\nparsed text %q attrs %+v at %d", line, d.Text, d.DecisionAttrs, d.AttrsAt)
		}
		if got := renderParsedDecision(d); got != line {
			t.Errorf("render changed the line:\n%s\n%s", got, line)
		}
	}
}

func cutHowText(line string) (string, string, bool) {
	m := decisionLineRe.FindStringSubmatch(line)
	return strings.Cut(m[3], ": ")
}

func TestDecisionAttrsInForceHelpers(t *testing.T) {
	a := DecisionAttrs{Expires: "2026-10-31", At: []string{"In Progress"}}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 15, 0, 0, 0, time.Local) }
	if a.ExpiredOn(day(31)) || !a.ExpiredOn(time.Date(2026, 11, 1, 0, 0, 1, 0, time.Local)) {
		t.Fatal("expires: is the last day in force")
	}
	if !a.AtStatus("in progress") || a.AtStatus("testing") || !(DecisionAttrs{}).AtStatus("testing") {
		t.Fatal("at: matching")
	}
	for _, bad := range []string{"projects", "system:", "issue:x", "team:a"} {
		if _, err := NormalizeScope(bad); err == nil {
			t.Errorf("scope %q accepted", bad)
		}
	}
}
