package lint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checkEvals reads a workflow file's evals: (how the factory benchmark
// scores a run of each type, docs/benchmark.md#evals) for the basic
// mistakes: no id or a repeated one, a score kind it doesn't know, a pass
// bar outside 0-1, a type the file doesn't define, an output or diff eval
// with nothing to grade, an assert type promptfoo won't take as one, two
// evals claiming one type, and types no eval covers.
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
			add(fmt.Sprintf("eval %s: score: %q is not one of %s", name, e.Score, strings.Join(tracker.EvalScores, ", ")), "score: tests | output | diff")
		}
		if e.Pass < 0 || e.Pass > 1 {
			add(fmt.Sprintf("eval %s: pass: %g is outside 0-1 (a mean score)", name, e.Pass), "pass: 0.7")
		}
		for _, t := range e.Types {
			if _, ok := wf.Types[t]; !ok {
				add(fmt.Sprintf("eval %s lists type %q, which this file's types: doesn't define", name, t), "fix the name or add the type")
			}
		}
		if e.Graded() && len(e.Rubric) == 0 && len(e.Assert) == 0 {
			add(fmt.Sprintf("eval %s (score: %s) has no rubric: and no assert:, so nothing grades it; bench run refuses its tasks", name, e.ScoreKind()),
				"add a rubric line or an assert (docs/benchmark.md#evals)")
		}
		for _, a := range e.Assert {
			if msg, fix := assertProblem(a.Type); msg != "" {
				add(fmt.Sprintf("eval %s: assert type %q %s", name, a.Type, msg), fix)
			}
		}
	}
	claimed := map[string]string{}
	catchAll := ""
	for _, e := range wf.Evals {
		if len(e.Types) == 0 {
			if catchAll != "" {
				add(fmt.Sprintf("evals %s and %s both have no types:; only %s, the first, scores the types no eval lists", catchAll, e.ID, catchAll), "give "+e.ID+" its types: or remove it")
			} else {
				catchAll = e.ID
			}
			continue
		}
		for _, t := range e.Types {
			if first, ok := claimed[t]; ok && first != e.ID {
				add(fmt.Sprintf("evals %s and %s both list type %q; %s, the first, scores it and %s never does", first, e.ID, t, first, e.ID), "keep the type in one eval")
				continue
			}
			claimed[t] = e.ID
		}
	}
	if catchAll == "" && len(wf.Types) > 0 {
		var missing []string
		for t := range wf.Types {
			if _, ok := claimed[t]; !ok {
				missing = append(missing, t)
			}
		}
		slices.Sort(missing)
		for _, t := range missing {
			add(fmt.Sprintf("type %s has no eval: bench run scores its tasks by hidden tests only", t),
				"list it in an eval's types:, or add an eval without types: (docs/benchmark.md#evals)")
		}
	}
	return out
}

// assertProblem says why promptfoo won't take typ as an assert: item the
// engine passes through; "" when it is fine.
func assertProblem(typ string) (string, string) {
	base := strings.TrimPrefix(strings.TrimSpace(typ), "not-")
	switch {
	case slices.Contains(tracker.EvalAssertTypes, base):
		return "", ""
	case slices.Contains(tracker.EvalAssertModelTypes, base):
		return "needs a model", "put the line under rubric:, graded by the benchmark's grader:"
	case slices.Contains(tracker.EvalAssertCodeTypes, base):
		return "runs code or calls out, so it isn't passed through", "use a model-free assertion (contains, regex, is-json, …) or a rubric line"
	}
	return "is not a promptfoo assertion the scorer passes through", "one of " + strings.Join(tracker.EvalAssertTypes, ", ") + " (each also with not-)"
}
