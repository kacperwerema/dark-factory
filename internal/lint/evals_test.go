package lint

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

func evalFindings(t *testing.T, raw string) []string {
	t.Helper()
	got, err := File("/nowhere/workflow.yaml", []byte(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range got {
		if f.Check == "evals" {
			out = append(out, f.Message)
		}
	}
	return out
}

const evalsBase = `statuses: [{name: idea}, {name: done}]
transitions: [{from: idea, to: done}]
default_type: piece
types:
  piece: {}
  seo-pass: {}
`

func TestEvalsLintAcceptsAWellFormedBlock(t *testing.T) {
	raw := evalsBase + `evals:
  - id: piece
    types: [piece, seo-pass]
    score: output
    rubric: ["answers the brief"]
    assert: [{type: not-icontains, value: lorem}]
    pass: 0.8
`
	if got := evalFindings(t, raw); len(got) != 0 {
		t.Fatalf("findings = %q", got)
	}
	if got := evalFindings(t, evalsBase); len(got) != 0 {
		t.Fatalf("a file without evals: is not linted: %q", got)
	}
}

func TestEvalsLintNamesTheBasicMistakes(t *testing.T) {
	raw := evalsBase + `evals:
  - id: piece
    types: [piece, nosuch]
  - id: piece
    score: judged
    pass: 7
  - score: output
`
	got := strings.Join(evalFindings(t, raw), "\n")
	for _, want := range []string{
		`lists type "nosuch"`,
		"declared twice",
		`score: "judged" is not one of tests, output, diff`,
		"pass: 7 is outside 0-1",
		"has no id",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// The development and marketing templates come with evals for their types,
// and the check finds nothing in them.
func TestBundledTemplatesDeclareEvalsTheCheckAccepts(t *testing.T) {
	for name, want := range map[string][]string{
		"development": {"code", "report", "docs"},
		"marketing":   {"foundation", "piece", "seo-pass"},
		"content":     {"content"},
	} {
		raw, err := workflowupgrade.ReadTemplate(name)
		if err != nil {
			t.Fatal(err)
		}
		var wf tracker.WorkflowConfig
		if err := yaml.Unmarshal(raw, &wf); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, e := range wf.Evals {
			ids = append(ids, e.ID)
		}
		if strings.Join(ids, ",") != strings.Join(want, ",") {
			t.Errorf("%s evals = %v, want %v", name, ids, want)
		}
		if got := evalFindings(t, string(raw)); len(got) != 0 {
			t.Errorf("%s: %q", name, got)
		}
	}
	mkt, _ := workflowupgrade.ReadTemplate("marketing")
	var wf tracker.WorkflowConfig
	_ = yaml.Unmarshal(mkt, &wf)
	dev, _ := workflowupgrade.ReadTemplate("development")
	var dwf tracker.WorkflowConfig
	_ = yaml.Unmarshal(dev, &dwf)
	if e := dwf.EvalFor("docs"); e == nil || e.ScoreKind() != "diff" || len(e.Assert) == 0 {
		t.Errorf("development docs type has no diff eval that scores without a grader: %+v", e)
	}
	content, _ := workflowupgrade.ReadTemplate("content")
	var cwf tracker.WorkflowConfig
	_ = yaml.Unmarshal(content, &cwf)
	if e := cwf.EvalFor(""); e == nil || e.ID != "content" || e.ScoreKind() != "output" || len(e.Rubric) == 0 {
		t.Errorf("content template has no catch-all output eval: %+v", e)
	}
	for _, typ := range []string{"foundation", "market-size", "piece", "seo-pass"} {
		if e := wf.EvalFor(typ); e == nil || e.ScoreKind() != "output" || len(e.Rubric) == 0 {
			t.Errorf("marketing type %s has no output eval: %+v", typ, e)
		}
	}
}

// The four checks for what otherwise shows only at run time: an output or
// diff eval nothing grades, an assert type promptfoo won't take as one, two
// evals claiming one type, and a type no eval covers.
func TestEvalsLintNamesWhatWouldOnlyShowAtRunTime(t *testing.T) {
	raw := `statuses: [{name: idea}, {name: done}]
transitions: [{from: idea, to: done}]
default_type: piece
types:
  piece: {}
  seo-pass: {}
  docs: {}
  report: {}
evals:
  - id: empty
    types: [docs]
    score: diff
  - id: piece
    types: [piece]
    score: output
    assert:
      - {type: not-icontains, value: lorem}
      - {type: llm-rubric, value: "reads well"}
      - {type: javascript, value: "output.length > 3"}
      - {type: sounds-nice}
  - id: again
    types: [piece]
    score: output
    rubric: ["x"]
`
	got := strings.Join(evalFindings(t, raw), "\n")
	for _, want := range []string{
		"eval empty (score: diff) has no rubric: and no assert:, so nothing grades it",
		`assert type "llm-rubric" needs a model`,
		`assert type "javascript" runs code`,
		`assert type "sounds-nice" is not a promptfoo assertion`,
		`evals piece and again both list type "piece"`,
		"type report has no eval",
		"type seo-pass has no eval",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "not-icontains") || strings.Contains(got, "type docs has no eval") || strings.Contains(got, "type piece has no eval") {
		t.Errorf("a passed-through not- type or a covered type was named:\n%s", got)
	}

	// A catch-all eval covers the rest; a second catch-all is never used.
	raw = `statuses: [{name: idea}, {name: done}]
transitions: [{from: idea, to: done}]
default_type: piece
types:
  piece: {}
  report: {}
evals:
  - id: all
    score: output
    rubric: ["x"]
  - id: rest
    score: output
    rubric: ["y"]
`
	got = strings.Join(evalFindings(t, raw), "\n")
	if !strings.Contains(got, "evals all and rest both have no types:") || strings.Contains(got, "has no eval") {
		t.Errorf("catch-all findings:\n%s", got)
	}
}
