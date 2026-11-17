package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checkHints flags a validate action whose hint: its rule never shows:
// only the structured rules and the legacy rules tracker.RuleShowsHint
// lists put a hint in place of their fix; criteria_*, approved_for,
// decisions_*, critique, has_output and the like keep their own text, so
// the hint is dead text the author thinks the agent reads.
func checkHints(env *Env) []Finding {
	wf := env.Workflow
	if wf == nil {
		return nil
	}
	views := []*tracker.WorkflowConfig{wf}
	for _, name := range wf.TypeNames() {
		views = append(views, wf.ForType(name))
	}
	seen := map[string]bool{}
	var out []Finding
	for _, v := range views {
		for _, t := range v.Transitions {
			for _, a := range t.Actions {
				hint := strings.TrimSpace(a.Hint)
				if a.Type != "validate" || hint == "" || tracker.RuleShowsHint(a.Rule) {
					continue
				}
				edge := t.From + " → " + t.To
				key := edge + "\x00" + a.Rule + "\x00" + hint
				if seen[key] {
					continue
				}
				seen[key] = true
				name, _ := tracker.SplitRule(a.Rule)
				first, _, _ := strings.Cut(hint, "\n")
				out = append(out, Finding{
					Where:   env.where(env.lineOf(first)),
					Message: fmt.Sprintf("%s: rule %q never shows its hint: (it keeps its own fix text), so the agent never reads it", edge, name),
					Fix:     "drop the hint:, or move its words into the rule's own text",
				})
			}
		}
	}
	return out
}
