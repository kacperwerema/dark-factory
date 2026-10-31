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
		`score: "judged" is not one of tests, output`,
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
		"development": {"code", "report"},
		"marketing":   {"foundation", "piece", "seo-pass"},
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
	for _, typ := range []string{"foundation", "market-size", "piece", "seo-pass"} {
		if e := wf.EvalFor(typ); e == nil || e.ScoreKind() != "output" || len(e.Rubric) == 0 {
			t.Errorf("marketing type %s has no output eval: %+v", typ, e)
		}
	}
}
