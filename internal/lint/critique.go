package lint

import (
	"fmt"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/critique"
)

// checkCritique reads the fresh critic's settings (critique: and the
// critique rule, docs/Workflow/critique.md): a timeout that doesn't parse,
// an agent with no read-only command and no command: of its own, a
// critique: block no edge uses, and a critique gate out of the first
// status, where nothing is written yet for a critic to read.
func checkCritique(env *Env) []Finding {
	wf := env.Workflow
	if wf == nil {
		return nil
	}
	var out []Finding
	edges := wf.CritiqueEdges()
	for _, name := range wf.TypeNames() {
		for _, e := range wf.ForType(name).CritiqueEdges() {
			dup := false
			for _, have := range edges {
				dup = dup || (have.From == e.From && have.To == e.To)
			}
			if !dup {
				edges = append(edges, e)
			}
		}
	}
	if c := wf.Critique; c != nil {
		where := env.where(env.lineOf("critique:"))
		if _, err := critique.Timeout(c); err != nil {
			out = append(out, Finding{Where: where, Message: err.Error(), Fix: "timeout: 15m"})
		}
		if a := strings.TrimSpace(c.Agent); a != "" && strings.TrimSpace(c.Command) == "" {
			if _, err := critique.Argv(a); err != nil {
				out = append(out, Finding{Where: where, Message: "critique: agent: " + err.Error(), Fix: "agent: claude | codex, or a command:"})
			}
		}
		if len(edges) == 0 {
			out = append(out, Finding{Where: where, Message: "critique: is set but no transition validates rule \"critique\", so no critic gates anything", Fix: "add - type: validate / rule: \"critique\" to the edge the critic should hold"})
		}
	}
	if len(wf.Statuses) > 0 {
		first := wf.Statuses[0].Name
		for _, e := range edges {
			if strings.EqualFold(e.From, first) {
				out = append(out, Finding{Where: env.where(env.lineOf("rule: \"critique\"")),
					Message: fmt.Sprintf("critique gate on %s → %s: at %s nothing is written yet for a critic to read", e.From, e.To, first),
					Fix:     "move the rule to the edge out of design or testing"})
			}
		}
	}
	return out
}
