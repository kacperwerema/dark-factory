package lint

import (
	"testing"

	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

const redRoundsWF = "statuses:\n  - name: idea\ntransitions: []\n"

// max_red_rounds is read from workflow.yaml with 4 as the default; lint
// knows the key (no schema finding) and warns on a value below 1; the
// catalog has its entry.
func TestMaxRedRoundsLintAndCatalog(t *testing.T) {
	env := lintProject(t, redRoundsWF, nil)
	if got := env.Workflow.RedRoundCap(); got != 4 {
		t.Fatalf("unset cap = %d, want 4", got)
	}
	env = lintProject(t, "max_red_rounds: 6\n"+redRoundsWF, nil)
	if got := env.Workflow.RedRoundCap(); got != 6 {
		t.Fatalf("cap = %d, want 6", got)
	}
	if fs := run(t, env, "schema"); has(fs, "max_red_rounds") {
		t.Fatalf("schema doesn't know max_red_rounds: %+v", fs)
	}
	if fs := run(t, env, "workflow"); has(fs, "max_red_rounds") {
		t.Fatalf("a valid cap warned: %+v", fs)
	}
	env = lintProject(t, "max_red_rounds: -1\n"+redRoundsWF, nil)
	if fs := run(t, env, "workflow"); !has(fs, "max_red_rounds is -1", "1 or more") {
		t.Fatalf("negative cap not warned: %+v", fs)
	}
	if env.Workflow.RedRoundCap() != 4 {
		t.Fatal("a negative cap must fall back to 4")
	}

	entries, err := workflowupgrade.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == "red-round-cap" {
			for _, p := range e.Detect {
				if p.Path == "max_red_rounds" {
					return
				}
			}
			t.Fatalf("red-round-cap doesn't detect max_red_rounds: %+v", e.Detect)
		}
	}
	t.Fatal("catalog has no red-round-cap entry")
}
