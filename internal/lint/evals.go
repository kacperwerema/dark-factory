package lint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// checkEvals reads a workflow file's evals: (how the factory benchmark
// scores a run of each type, docs/benchmark.md#evals) for the basic
// mistakes: no id or a repeated one, a score kind it doesn't know, a pass
// bar outside 0-1, a type the file doesn't define.
func checkEvals(env *Env) []Finding {
	wf := env.Workflow
	if wf == nil || len(wf.Evals) == 0 {
		return nil
	}
	where := env.where(env.lineOf("evals:"))
	var out []Finding
	add := func(msg, fix string) { out = append(out, Finding{Where: where, Message: msg, Fix: fix}) }
	seen := map[string]bool{}
	for i, e := range wf.Evals {
		name := strings.TrimSpace(e.ID)
		switch {
		case name == "":
			name = fmt.Sprintf("evals[%d]", i)
			add(name+" has no id: a benchmark task names its eval by id", "give it id: <name>")
		case seen[name]:
			add("eval "+name+" is declared twice; the first one is used", "rename or remove one")
		}
		seen[name] = true
		if !slices.Contains(tracker.EvalScores, e.ScoreKind()) {
			add(fmt.Sprintf("eval %s: score: %q is not one of %s", name, e.Score, strings.Join(tracker.EvalScores, ", ")), "score: tests | output")
		}
		if e.Pass < 0 || e.Pass > 1 {
			add(fmt.Sprintf("eval %s: pass: %g is outside 0-1 (a mean score)", name, e.Pass), "pass: 0.7")
		}
		for _, t := range e.Types {
			if _, ok := wf.Types[t]; !ok {
				add(fmt.Sprintf("eval %s lists type %q, which this file's types: doesn't define", name, t), "fix the name or add the type")
			}
		}
	}
	return out
}
