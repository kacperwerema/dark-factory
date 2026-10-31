package lint

import (
	"strings"
	"testing"
	"time"
)

func critiqueFindings(t *testing.T, raw string) []string {
	t.Helper()
	got, err := File("/nowhere/workflow.yaml", []byte(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range got {
		if f.Check == "critique" {
			out = append(out, f.Message)
		}
	}
	return out
}

const critiqueStatuses = "statuses: [{name: idea}, {name: in design}, {name: backlog}]\n"

func TestCritiqueLintAcceptsAGateAndBlock(t *testing.T) {
	raw := critiqueStatuses + `critique: {agent: codex, timeout: 10m}
transitions:
  - {from: idea, to: in design}
  - from: in design
    to: backlog
    actions: [{type: validate, rule: critique}]
`
	if got := critiqueFindings(t, raw); len(got) != 0 {
		t.Fatalf("findings on a good file: %v", got)
	}
	// The schema check knows the key and the rule.
	all, _ := File("/nowhere/workflow.yaml", []byte(raw), time.Now())
	for _, f := range all {
		if strings.Contains(f.Message, "critique") && f.Check != "critique" {
			t.Errorf("%s flags critique: %s", f.Check, f.Message)
		}
	}
}

func TestCritiqueLintFindsMistakes(t *testing.T) {
	raw := critiqueStatuses + `critique: {agent: gemini, timeout: soon}
transitions:
  - from: idea
    to: in design
    actions: [{type: validate, rule: critique}]
  - {from: in design, to: backlog}
`
	got := strings.Join(critiqueFindings(t, raw), "\n")
	for _, want := range []string{"timeout: \"soon\" is not a duration", "no read-only command for agent \"gemini\"", "critique gate on idea → in design: at idea nothing is written yet"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	unused := critiqueStatuses + "critique: {agent: claude}\ntransitions: [{from: idea, to: in design}]\n"
	if got := strings.Join(critiqueFindings(t, unused), "\n"); !strings.Contains(got, "no transition validates rule \"critique\"") {
		t.Errorf("unused block: %s", got)
	}
}
