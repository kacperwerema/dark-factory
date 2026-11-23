package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// checkTemplate compares the template: stamp with the template bundled in
// this issue-cli, as issue-cli workflow upgrade does, so a project falling
// behind, or a features: record missing what the file has, shows without
// anyone asking.
func checkTemplate(env *Env) []Finding {
	if env.File == "" {
		return nil
	}
	plan, err := workflowupgrade.BuildPlan(env.File, env.Raw, "", false)
	if err != nil {
		return []Finding{{Where: env.where(0), Message: "comparing with the bundled template: " + err.Error()}}
	}
	var out []Finding
	if msg := plan.StaleRecord(); msg != "" {
		out = append(out, Finding{Where: env.where(env.lineOf("features:")), Message: msg, Fix: "issue-cli workflow scan"})
	}
	where := env.where(env.lineOf("template:"))
	switch plan.Status {
	case workflowupgrade.StatusBehind:
		msg := fmt.Sprintf("template: %s@%s is behind the bundled %s@%s", plan.Template, plan.Current, plan.Template, plan.Latest)
		// Only a missing feature is one to adopt; one the file already has
		// at an older record version is the record's to catch up (the
		// unrecorded ones get the stale-record finding above).
		var parts []string
		if n := len(plan.Missing); n > 0 {
			parts = append(parts, fmt.Sprintf("%d missing %s to adopt", n, plural(n, "feature", "features")))
		}
		if n := len(plan.RecordOnly) - len(plan.Unrecorded); n > 0 {
			parts = append(parts, fmt.Sprintf("%d present, record only: `issue-cli workflow scan --installed <ids>` records them", n))
		}
		if len(parts) > 0 {
			msg += " (" + strings.Join(parts, "; ") + ")"
		}
		fix := "issue-cli workflow upgrade"
		if len(plan.Missing) == 0 {
			fix = "issue-cli workflow upgrade (it lists the scan --installed ids and the stamp to set)"
		}
		out = append(out, Finding{Where: where, Message: msg, Fix: fix})
	case workflowupgrade.StatusUnstamped:
		out = append(out, Finding{Where: env.where(0),
			Message: "no template: stamp, so workflow upgrade can't tell which features are new",
			Fix:     "issue-cli workflow upgrade --template <name> (or set template: custom)"})
	case workflowupgrade.StatusUnknownTemplate:
		out = append(out, Finding{Where: where, Message: plan.Message, Fix: "issue-cli workflow upgrade"})
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
