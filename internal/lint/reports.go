package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/outputs"
)

// checkReports reads the issues whose path gates on has_output (the report
// type) and that have passed that gate: the shelf must still hold their
// output. One that is gone (never added since a hand edit, its file
// deleted, or rejected and not replaced) disagrees with the gate it passed,
// and the issue page shows no report.
func checkReports(env *Env) []Finding {
	var list []*outputs.Output
	loaded := false
	var out []Finding
	for _, issue := range env.Issues {
		wf := env.Workflow.ForIssue(issue)
		to, kind, ok := wf.HasOutputRule()
		if !ok {
			continue
		}
		at, gate := wf.GetStatusIndex(issue.Status), wf.GetStatusIndex(to)
		if at < 0 || gate < 0 || at < gate || strings.EqualFold(issue.Status, "parked") {
			continue
		}
		if !loaded {
			list, _ = outputs.Load(env.Project)
			loaded = true
		}
		err := outputs.GateError(list, issue.Slug, kind)
		if err == nil {
			continue
		}
		msg, _, _ := strings.Cut(err.Error(), " — ")
		what := kind
		if what == "" {
			what = "<kind>"
		}
		out = append(out, Finding{
			Where:   issue.Slug,
			Message: fmt.Sprintf("at %s, past the has_output gate into %s, but %s", issue.Status, to, msg),
			Fix:     fmt.Sprintf(`issue-cli output add --issue %s --kind %s --title "..." --file <file>`, issue.Slug, what),
		})
	}
	return out
}
