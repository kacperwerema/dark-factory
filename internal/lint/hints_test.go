package lint

import (
	"os"
	"strings"
	"testing"
	"time"
)

func hintFindings(t *testing.T, path string, raw []byte) []Finding {
	t.Helper()
	got, err := File(path, raw, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out []Finding
	for _, f := range got {
		if f.Check == "hints" {
			out = append(out, f)
		}
	}
	return out
}

func TestHintsLintWarnsOnAHintTheRuleNeverShows(t *testing.T) {
	raw := `statuses: [{name: idea}, {name: in design}, {name: backlog}]
transitions:
  - from: idea
    to: in design
    actions:
      - {type: validate, rule: "has_comment_prefix: design:", hint: "comment design: <words>"}
      - {type: validate, rule: has_section, section: Design, hint: "append ## Design"}
  - from: in design
    to: backlog
    actions:
      - {type: validate, rule: "criteria_proven: since 2026-01-01", hint: "prove each box"}
`
	got := hintFindings(t, "/nowhere/workflow.yaml", []byte(raw))
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", got)
	}
	f := got[0]
	for _, want := range []string{"in design → backlog", `rule "criteria_proven"`, "never shows its hint"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message lacks %q: %s", want, f.Message)
		}
	}
	if f.Where != "workflow.yaml:11" {
		t.Errorf("where = %q, want the hint's line workflow.yaml:11", f.Where)
	}
}

// TestBundledWorkflowHintsAreShown: the repository's own workflow.yaml
// sets hints only on rules that show them.
func TestBundledWorkflowHintsAreShown(t *testing.T) {
	raw, err := os.ReadFile("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hint:") {
		t.Fatal("workflow.yaml has no hint: to check")
	}
	if got := hintFindings(t, "../../workflow.yaml", raw); len(got) != 0 {
		t.Errorf("findings on workflow.yaml: %+v", got)
	}
}
