package tracker

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const clarifyBody = `Intro.

## Clarify

### Q1. Where does the owner answer?
1. **On the Station** (recommended) one card beside the issue, needs_you.go keeps one row. Cost: one template block.
2. **In each Needs you row.** Cost: a form per row.
**Answer:** —

### Q2. Keep the old comments?
1. **Keep them** (recommended) Cost: nothing.
2. **Drop them**
   costs a migration. Cost: one script.
**Answer:** assumed — YOLO: took the recommendation

### Q3. Which colour?
1. **Blue** (recommended) Cost: none.
2. **Red** Cost: none.
**Answer:** 2 · 2026-10-30 · owner: "Red, like the \"stop\" sign." (D-1)

### Q4. One option only
1. **Only** (recommended) Cost: x.
**Answer:** assumed 1 — the agent chose

` + "```" + `
### Q9. inside a fence
` + "```" + `

## Decisions
- **D-1** · 2026-10-30 · clarify round: Q3 Which colour → Red, "Red"

## Design
Text.`

func TestParseClarify_QuestionsOptionsAnswersAssumed(t *testing.T) {
	c := ParseClarify(clarifyBody)
	if !c.Found || c.Nothing || len(c.Questions) != 4 {
		t.Fatalf("clarify = %+v", c)
	}
	q1 := c.Questions[0]
	if q1.ID != "Q1" || q1.Text != "Where does the owner answer?" || !q1.Open() || len(q1.Options) != 2 {
		t.Fatalf("Q1 = %+v", q1)
	}
	if o := q1.Options[0]; o.Label != "On the Station" || !o.Recommended || o.Cost != "one template block" || !strings.Contains(o.Why, "needs_you.go") {
		t.Fatalf("Q1 option 1 = %+v", o)
	}
	if o := q1.Options[1]; o.Label != "In each Needs you row" || o.Recommended || o.Cost != "a form per row" {
		t.Fatalf("Q1 option 2 = %+v", o)
	}
	q2 := c.Questions[1]
	if q2.Answer.State != AnswerAssumed || q2.Answer.Reason != "YOLO: took the recommendation" || q2.Chosen().Label != "Keep them" {
		t.Fatalf("Q2 = %+v", q2)
	}
	if o := q2.Options[1]; o.Cost != "one script" || o.Why != "costs a migration." {
		t.Fatalf("Q2 option 2 (continued) = %+v", o)
	}
	q3 := c.Questions[2]
	if a := q3.Answer; a.State != AnswerOwner || a.Option != 2 || a.Date != "2026-10-30" || a.Words != `Red, like the "stop" sign.` || a.Decision != "D-1" || a.Who != "owner" {
		t.Fatalf("Q3 answer = %+v", a)
	}
	if q4 := c.Questions[3]; q4.Answer.State != AnswerAssumed || q4.Answer.Option != 1 || q4.Answer.Reason != "the agent chose" {
		t.Fatalf("Q4 = %+v", q4)
	}
	if n := len(c.Open()); n != 1 {
		t.Fatalf("open = %d", n)
	}
	if n := len(c.Assumed()); n != 2 {
		t.Fatalf("assumed = %d", n)
	}
	if n := len(c.Unanswered()); n != 3 {
		t.Fatalf("unanswered = %d", n)
	}
	if c.Question("q3") == nil || c.Question("3") == nil || c.Question("Q9") != nil {
		t.Fatal("Question lookup")
	}
	// Q2's option 2 has its cost on the continuation line, so only Q4's
	// option count is a problem.
	if got := strings.Join(c.Problems, "|"); got != "Q4: 1 option(s), a question needs 2–4" {
		t.Fatalf("problems = %q", c.Problems)
	}
}

func TestParseClarify_NothingToAskAndProblems(t *testing.T) {
	c := ParseClarify("## Clarify\n\nNothing to ask: the brief and the settled rules answer it.\n")
	if !c.Found || !c.Nothing || c.Why != "the brief and the settled rules answer it" || len(c.Problems) != 0 {
		t.Fatalf("nothing = %+v", c)
	}
	if c := ParseClarify("## Design\nx"); c.Found || len(c.Problems) != 0 {
		t.Fatalf("no section = %+v", c)
	}
	c = ParseClarify("## Clarify\n\nsome prose\n")
	if len(c.Problems) != 1 || !strings.Contains(c.Problems[0], "no `### Q1. …` question") {
		t.Fatalf("empty = %q", c.Problems)
	}
	c = ParseClarify("## Clarify\n\n### Q1. X?\n1. **A** Cost: a.\n2. **B** (recommended) Cost: b.\n3. **C** Cost: c.\n4. **D** Cost: d.\n5. **E** Cost: e.\n")
	got := strings.Join(c.Problems, "|")
	for _, want := range []string{"Q1: 5 options, at most 4", "Q1: the recommendation (option 2) goes first", "Q1: no `**Answer:** —` line"} {
		if !strings.Contains(got, want) {
			t.Fatalf("problems %q lack %q", got, want)
		}
	}
	if !c.Questions[0].Open() {
		t.Fatal("a question without an answer line reads as open")
	}
}

func TestRenderQuestion_RoundTrips(t *testing.T) {
	q := ClarifyQuestion{Num: 1, Text: "Ship it?", Options: []ClarifyOption{
		{Label: "Yes", Why: "the tests pass (cmd/x_test.go).", Cost: "nothing"},
		{Label: "No", Cost: "a week."},
	}}
	got := RenderQuestion(q)
	want := "### Q1. Ship it?\n1. **Yes** (recommended) the tests pass (cmd/x_test.go). Cost: nothing.\n2. **No** Cost: a week.\n**Answer:** —"
	if got != want {
		t.Fatalf("render =\n%s\nwant\n%s", got, want)
	}
	c := ParseClarify("## Clarify\n\n" + got + "\n")
	if len(c.Problems) != 0 || len(c.Questions) != 1 || c.Questions[0].Options[0].Why != "the tests pass (cmd/x_test.go)." || !c.Questions[0].Options[0].Recommended {
		t.Fatalf("round trip = %+v %q", c.Questions, c.Problems)
	}
}

func TestAppendQuestion_NextNumberAndNothingLineGoes(t *testing.T) {
	fp := writeDecisionIssue(t, "## Red rounds\n\nNothing to ask: green.\n\n## Design\nText.")
	q := ClarifyQuestion{Text: "Check `make test` red 4 of 4: what now?", Options: []ClarifyOption{{Label: "Split the issue", Cost: "one new issue"}, {Label: "Keep fixing", Cost: "more rounds"}}}
	id, err := AppendQuestion(fp, "Red rounds", q)
	if err != nil || id != "Q1" {
		t.Fatalf("append = %q, %v", id, err)
	}
	if id, err = AppendQuestion(fp, "Red rounds", q); err != nil || id != "Q2" {
		t.Fatalf("second append = %q, %v", id, err)
	}
	body := readBody(t, fp)
	if strings.Contains(body, "Nothing to ask") || !strings.Contains(body, "## Design\nText.") {
		t.Fatalf("body:\n%s", body)
	}
	if qs := ParseQuestions(body, "Red rounds"); len(qs) != 2 || qs[1].ID != "Q2" || !qs[1].Open() {
		t.Fatalf("questions = %+v", qs)
	}
	// A section that doesn't exist is created.
	if id, err := AppendQuestion(fp, ClarifySection, q); err != nil || id != "Q1" || !ParseClarify(readBody(t, fp)).Found {
		t.Fatalf("new section = %q, %v", id, err)
	}
}

func TestAnswerQuestion_RecordsLineAndDecision(t *testing.T) {
	fp := writeDecisionIssue(t, clarifyBody)
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local)
	q, d, err := AnswerQuestion(fp, "f/x", AnswerInput{ID: "q1", Option: 2, Words: "  In the row,\n please ", Expect: "Where does the owner answer?"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if q.Answer.State != AnswerOwner || q.Answer.Decision != "D-2" || d.ID != "D-2" || d.Quote != "In the row, please" || d.How != ClarifyHow || NeedsOwnerWords(d) {
		t.Fatalf("q = %+v, d = %+v", q, d)
	}
	body := readBody(t, fp)
	for _, want := range []string{
		`**Answer:** 2 · 2026-10-31 · owner: "In the row, please" (D-2)`,
		`- **D-2** · 2026-10-31 · clarify round: Q1 Where does the owner answer → In each Needs you row, "In the row, please"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body lacks %q:\n%s", want, body)
		}
	}
	// An assumed answer can be answered; empty words record the label.
	if q, d, err = AnswerQuestion(fp, "f/x", AnswerInput{ID: "Q2", Option: 1}, now); err != nil || d.Quote != "Keep them" || q.Answer.Words != "Keep them" {
		t.Fatalf("Q2 = %+v, %+v, %v", q, d, err)
	}
	if _, _, err := AnswerQuestion(fp, "f/x", AnswerInput{ID: "Q3", Option: 1}, now); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("answered again: %v", err)
	}
	if _, _, err := AnswerQuestion(fp, "f/x", AnswerInput{ID: "Q4", Option: 1, Expect: "something else"}, now); !errors.Is(err, ErrQuestionChanged) {
		t.Fatalf("changed: %v", err)
	}
	if _, _, err := AnswerQuestion(fp, "f/x", AnswerInput{ID: "Q7", Option: 1}, now); !errors.Is(err, ErrQuestionNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := AnswerQuestion(fp, "f/x", AnswerInput{ID: "Q4", Option: 3}, now); err == nil || !strings.Contains(err.Error(), "no option 3") {
		t.Fatalf("bad option: %v", err)
	}
	c := ParseClarify(readBody(t, fp))
	if len(c.Unanswered()) != 1 || c.Unanswered()[0].ID != "Q4" {
		t.Fatalf("unanswered = %+v", c.Unanswered())
	}
	if ds := ParseDecisions("f/x", readBody(t, fp)); len(ds) != 3 {
		t.Fatalf("decisions = %+v", ds)
	}
}

func TestAssumeOpen_MarksOnlyOpenQuestions(t *testing.T) {
	fp := writeDecisionIssue(t, "## Clarify\n\n### Q1. A?\n1. **x** (recommended) Cost: 1.\n2. **y** Cost: 2.\n**Answer:** —\n\n### Q2. B?\n1. **x** (recommended) Cost: 1.\n2. **y** Cost: 2.\n\n### Q3. C?\n1. **x** (recommended) Cost: 1.\n2. **y** Cost: 2.\n**Answer:** assumed — earlier\n\n## Design\nText.")
	got, err := AssumeOpen(fp, "", "")
	if err != nil || len(got) != 2 || got[0].ID != "Q1" || got[1].ID != "Q2" {
		t.Fatalf("assumed = %+v, %v", got, err)
	}
	c := ParseClarify(readBody(t, fp))
	if len(c.Open()) != 0 || len(c.Assumed()) != 3 || c.Questions[0].Answer.Reason != DefaultAssumedReason || c.Questions[2].Answer.Reason != "earlier" || len(c.Problems) != 0 {
		t.Fatalf("after = %+v %q\n%s", c.Questions, c.Problems, readBody(t, fp))
	}
	if got, err := AssumeOpen(fp, "", ""); err != nil || len(got) != 0 {
		t.Fatalf("second run = %+v, %v", got, err)
	}
}
