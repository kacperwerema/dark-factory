package lint

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/routing"
)

func routingFindings(t *testing.T, raw string) []string {
	t.Helper()
	old := routing.Installed
	routing.Installed = func(a string) bool { return a == "claude" || a == "codex" }
	t.Cleanup(func() { routing.Installed = old })
	got, err := File("/nowhere/workflow.yaml", []byte(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range got {
		if f.Check == "routing" {
			out = append(out, f.Message)
		}
	}
	return out
}

const routingBase = `statuses: [{name: idea}, {name: in design}, {name: done}]
transitions: [{from: idea, to: in design}, {from: in design, to: done}]
default_type: feature
types:
  feature: {}
  docs: {}
`

func TestRoutingLintAcceptsAWellFormedBlock(t *testing.T) {
	raw := routingBase + `routing:
  default: claude
  fallback: [claude, codex]
  rules:
    - {type: docs, agent: codex}
    - {status: in design, agent: claude}
    - {label: design, agent: claude}
    - {lead: true, agent: claude}
`
	if got := routingFindings(t, raw); len(got) != 0 {
		t.Fatalf("findings = %q", got)
	}
	if got := routingFindings(t, routingBase); len(got) != 0 {
		t.Fatalf("a file without routing: is not linted: %q", got)
	}
}

func TestRoutingLintNamesTheMistakes(t *testing.T) {
	raw := routingBase + `routing:
  default: gemini
  fallback: [codex, codex]
  rules:
    - {type: report, agent: codex}
    - {status: reviewing, agent: claude}
    - {agent: codex}
    - {label: design, agent: "rm -rf"}
    - {type: docs}
`
	got := strings.Join(routingFindings(t, raw), "\n")
	for _, want := range []string{
		"routing default: gemini is not installed",
		"routing fallback: codex is listed twice",
		"leaves out the default gemini",
		`type "report" is not in this file's types: (feature, docs)`,
		`status "reviewing" is not in this file's statuses:`,
		"routing rule 4 (label design) comes after rule 3, which matches every start",
		`"rm -rf" is not an agent command name`,
		"routing rule 5 (type docs) has no agent:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
