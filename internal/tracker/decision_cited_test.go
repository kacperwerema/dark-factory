package tracker

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// citedFixture is a board with a hard project rule, a soft project rule, a
// replaced rule and its replacer, an expired rule, and an epic with a hard
// issue-scoped rule. design is the issue under test's ## Design text.
func citedFixture(design, extra string) (*WorkflowConfig, *Issue) {
	rules := &Issue{Slug: "orchard/rules", Status: "done", BodyRaw: "## Decisions\n\n" +
		`- **D-1** · 2026-10-01 · chat: Agents merge into local main, "merge locally" {scope: project; hard}` + "\n" +
		`- **D-2** · 2026-10-02 · chat: Prefer short names {scope: project}` + "\n" +
		`- **D-3** · 2026-10-03 · chat: Old storage rule, "old"` + "\n" +
		`- **D-4** · 2026-10-04 · chat: New storage rule, "new" (replaces D-3)` + "\n" +
		`- **D-5** · 2026-10-05 · chat: Temporary freeze, "freeze" {expires: 2026-10-20}` + "\n"}
	epic := &Issue{Slug: "orchard/epic", Status: "in progress", BodyRaw: "## Decisions\n\n" +
		`- **D-1** · 2026-10-06 · chat: Children share one schema, "one schema" {hard}` + "\n"}
	issue := &Issue{Slug: "orchard/child", Status: "in design", Created: "2026-10-31",
		ExtraFields: []ExtraField{{Key: "parent", Value: "orchard/epic"}},
		BodyRaw: "## Idea\n\nSomething.\n" + extra + "\n## Design\n\n" + design + "\n\n## Decisions\n\n" +
			`- **D-1** · 2026-10-30 · chat: Own hard rule, "mine" {hard}` + "\n"}
	x := BuildDecisionIndex([]*Issue{rules, epic, issue}, nil)
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "in design"}, {Name: "backlog"}},
		Transitions: []WorkflowTransition{{From: "in design", To: "backlog", Actions: []WorkflowAction{
			{Type: "validate", Rule: DecisionsCitedRule}, {Type: "validate", Rule: CitationsCurrentRule},
		}}},
		DecisionsInForce: func() (*DecisionIndex, InForceOptions) {
			return x, InForceOptions{Today: time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local)}
		},
	}
	return wf, issue
}

const citesBoth = "Follows orchard/rules#D-1 and orchard/epic#D-1."

// TestDecisionsCited is AC1: a hard project decision not cited in ## Design
// fails; citing it passes; "n/a <ref>: <why>" passes; a bare n/a fails.
func TestDecisionsCited(t *testing.T) {
	for _, tc := range []struct {
		name, design string
		want         []string // substrings of the refusal; none: passes
		not          []string
	}{
		{"uncited", "We build it.", []string{"2 hard decisions in force are not cited in ## Design", "orchard/rules#D-1 (project): Agents merge into local main", "orchard/epic#D-1 (parent orchard/epic)", "issue-cli append orchard/child --section Design", "n/a orchard/epic#D-1: <why>", `decision challenge orchard/epic#D-1 --why`}, []string{"orchard/rules#D-2", "orchard/child#D-1"}},
		{"cited", citesBoth, nil, nil},
		{"cited in backticks and a list", "- follows `orchard/rules#D-1`\n- keeps [the schema](/issue/orchard/epic#D-1)", nil, nil},
		{"n/a with a reason", "n/a orchard/rules#D-1: this issue merges nothing\nFollows orchard/epic#D-1.", nil, nil},
		{"n/a list item with a reason", "- n/a `orchard/rules#D-1`: docs only\nFollows orchard/epic#D-1.", nil, nil},
		{"bare n/a", "n/a orchard/rules#D-1\nFollows orchard/epic#D-1.", []string{"1 hard decision in force is not cited", "its n/a line has no reason"}, nil},
		{"n/a with an empty reason", "- n/a orchard/rules#D-1:   \nFollows orchard/epic#D-1.", []string{"its n/a line has no reason"}, nil},
		{"cited outside Design only", "Nothing here.", []string{"orchard/rules#D-1"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := ""
			if tc.name == "cited outside Design only" {
				extra = citesBoth
			}
			wf, issue := citedFixture(tc.design, extra)
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
			for _, w := range tc.not {
				if strings.Contains(err.Error(), w) {
					t.Errorf("refusal names %q:\n%v", w, err)
				}
			}
		})
	}
}

// TestDecisionsCitedSoftNeverFails is AC2: soft decisions in force, at any
// scope, never fail decisions_cited.
func TestDecisionsCitedSoftNeverFails(t *testing.T) {
	wf, issue := citedFixture(citesBoth, "")
	x, _ := wf.DecisionsInForce()
	var soft int
	for _, f := range InForce(x, issue, issue.Status, InForceOptions{}) {
		if !f.Entry.Hard {
			soft++
		}
	}
	if soft == 0 {
		t.Fatal("fixture has no soft decision in force")
	}
	if err := wf.ValidateTransition(issue, "in design", "backlog", nil); err != nil {
		t.Fatalf("soft decisions held the gate:\n%v", err)
	}
}

// TestCitationsCurrent is AC3: a replaced ref fails and names its replacer;
// an expired one fails with its day; the own ## Decisions and (replaces …)
// are history.
func TestCitationsCurrent(t *testing.T) {
	for _, tc := range []struct {
		name, design string
		want         []string
	}{
		{"replaced", citesBoth + " Stores per orchard/rules#D-3.", []string{"citations_current: 1 cited decision ref is no longer in force", "orchard/rules#D-3 is replaced by orchard/rules#D-4: New storage rule", "cite orchard/rules#D-4 instead"}},
		{"expired", citesBoth + " Frozen per orchard/rules#D-5.", []string{"orchard/rules#D-5 expired after 2026-10-20"}},
		{"replacer cited", citesBoth + " Stores per orchard/rules#D-4.", nil},
		{"history as replaces", citesBoth + " Moved on (replaces orchard/rules#D-3).", nil},
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

	// A ref in the issue's own ## Decisions is history, not a citation.
	wf, issue := citedFixture(citesBoth, "")
	issue.BodyRaw += `- **D-2** · 2026-10-31 · chat: Was orchard/rules#D-3, now the new rule` + "\n"
	if err := wf.ValidateTransition(issue, "in design", "backlog", nil); err != nil {
		t.Fatalf("own ## Decisions held the gate:\n%v", err)
	}
}

// TestDecisionRulesSince: an issue created before the rule's date passes
// both rules; a bad date is the rule's error.
func TestDecisionRulesSince(t *testing.T) {
	wf, issue := citedFixture("Stores per orchard/rules#D-3.", "")
	for i := range wf.Transitions[0].Actions {
		wf.Transitions[0].Actions[i].Rule += ": since 2026-11-01"
	}
	if err := wf.ValidateTransition(issue, "in design", "backlog", nil); err != nil {
		t.Fatalf("an issue before the date was held:\n%v", err)
	}
	issue.Created = "2026-11-02"
	if err := wf.ValidateTransition(issue, "in design", "backlog", nil); err == nil {
		t.Fatal("an issue after the date passed uncited")
	}
	wf.Transitions[0].Actions[0].Rule = DecisionsCitedRule + ": since yesterday"
	err := wf.ValidateTransition(issue, "in design", "backlog", nil)
	if err == nil || !strings.Contains(err.Error(), `decisions_cited: want "since YYYY-MM-DD"`) {
		t.Fatalf("bad since: %v", err)
	}
}

// TestDecisionHoldsDeferred: under YOLO the rules record each hold once
// and pass.
func TestDecisionHoldsDeferred(t *testing.T) {
	wf, issue := citedFixture("n/a orchard/rules#D-1\nStores per orchard/rules#D-3.", "")
	wf.DeferDecisionHolds = true
	for i := 0; i < 2; i++ {
		if err := wf.ValidateTransition(issue, "in design", "backlog", nil); err != nil {
			t.Fatalf("deferred holds still refused:\n%v", err)
		}
	}
	got := wf.DecisionHolds()
	want := []DecisionHold{
		{Rule: DecisionsCitedRule, Ref: "orchard/epic#D-1", Why: "not cited in ## Design"},
		{Rule: DecisionsCitedRule, Ref: "orchard/rules#D-1", Why: "n/a without a reason in ## Design"},
		{Rule: CitationsCurrentRule, Ref: "orchard/rules#D-3", Why: "replaced by orchard/rules#D-4"},
	}
	if len(got) != len(want) {
		t.Fatalf("holds = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hold %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDecisionRulesNotBuiltWithoutRule is AC5's tracker half: a workflow
// without the rules never builds the index.
func TestDecisionRulesNotBuiltWithoutRule(t *testing.T) {
	wf, issue := citedFixture("We build it.", "")
	built := false
	wf.DecisionsInForce = func() (*DecisionIndex, InForceOptions) { built = true; return nil, InForceOptions{} }
	wf.Transitions[0].Actions = []WorkflowAction{{Type: "validate", Rule: "body_not_empty"}}
	if err := wf.ValidateTransition(issue, "in design", "backlog", nil); err != nil {
		t.Fatal(err)
	}
	if built || wf.UsesDecisionRules() {
		t.Fatalf("built=%v uses=%v without the rules", built, wf.UsesDecisionRules())
	}
}

func TestYOLOHoldCommentRoundTrip(t *testing.T) {
	h := DecisionHold{Rule: DecisionsCitedRule, Ref: "orchard/rules#D-1", Why: "not cited in ## Design"}
	c := YOLOHoldComment(h, "in design")
	if c != "yolo-hold: decisions_cited orchard/rules#D-1 from in design: not cited in ## Design" {
		t.Fatalf("comment = %q", c)
	}
	got, from, ok := ParseYOLOHoldComment(c)
	if !ok || got != h || from != "in design" {
		t.Fatalf("parse = %+v %q %v", got, from, ok)
	}
	if _, _, ok := ParseYOLOHoldComment("yolo-hold: nonsense"); ok {
		t.Fatal("parsed a malformed hold")
	}
}

// TestLintKnowsDecisionRules is AC4's lint half: the workflow lint knows
// both rule names, flags an unknown one and a bad since date, and finds
// nothing in this repo's workflow or the bundled templates.
func TestLintKnowsDecisionRules(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "in design"}, {Name: "backlog"}},
		Transitions: []WorkflowTransition{{From: "in design", To: "backlog", Actions: []WorkflowAction{
			{Type: "validate", Rule: "decisions_cited: since 2026-11-01"},
			{Type: "validate", Rule: "citations_current"},
			{Type: "validate", Rule: "decisions_citd"},
			{Type: "validate", Rule: "citations_current: since soon"},
		}}},
	}
	got := strings.Join(wf.Lint(), "\n")
	for _, want := range []string{
		`in design → backlog: unknown validate rule "decisions_citd"`,
		`in design → backlog: citations_current: want "since YYYY-MM-DD", got "since soon"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lint lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"decisions_cited"`) || strings.Count(got, "\n") != 1 {
		t.Errorf("lint flags a known rule:\n%s", got)
	}

	files, _ := filepath.Glob("../../cmd/issue-cli/templates/workflow/*.yaml")
	files = append(files, "../../workflow.yaml")
	for _, f := range files {
		w, err := LoadWorkflow(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, msg := range w.lintRules() {
			t.Errorf("%s: %s", f, msg)
		}
	}
}
