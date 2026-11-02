package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// outsideStatuses are status names whose step spends money or publishes
// outside the machine: the gate into one should be owner_only.
var outsideStatuses = map[string]bool{"publish": true, "launch": true, "send": true}

// checkOwnerOnly warns where a step that spends or publishes could pass in
// YOLO: a path entering publish / launch / send through a gate without
// owner_only, a department with a sends: connector whose workflow has no
// owner_only gate, and owner_only on an action that isn't an approval
// (docs/Workflow/yolo.md, "Gates YOLO never passes").
func checkOwnerOnly(env *Env) []Finding {
	wf := env.Workflow
	if wf == nil {
		return nil
	}
	var out []Finding
	fix := func(from, to string) string {
		return fmt.Sprintf(`owner_only: true on the require_human_approval of %s → %s`, from, to)
	}
	misplaced := func(trs []tracker.WorkflowTransition) {
		for _, t := range trs {
			for _, a := range t.Actions {
				if a.OwnerOnly && a.Type != "require_human_approval" {
					out = append(out, Finding{Where: env.where(env.lineOf("owner_only")),
						Message: fmt.Sprintf("%s → %s: owner_only on a %s action does nothing; it belongs on the require_human_approval", t.From, t.To, a.Type)})
				}
			}
		}
	}
	misplaced(wf.Transitions)
	for _, name := range wf.TypeNames() {
		misplaced(wf.Types[name].Transitions)
	}

	hasOwnerGate := false
	reported := map[string]bool{}
	walk := func(scoped *tracker.WorkflowConfig, typ string) {
		order := scoped.GetStatusOrder()
		for i := 0; i+1 < len(order); i++ {
			from, to := order[i], order[i+1]
			if scoped.OwnerOnlyGate(from, to) {
				hasOwnerGate = true
				continue
			}
			if !outsideStatuses[strings.ToLower(strings.TrimSpace(to))] || reported[from+"\x00"+to] {
				continue
			}
			reported[from+"\x00"+to] = true
			what := "the path"
			if typ != "" {
				what = "type " + typ
			}
			why := "its gate isn't owner_only"
			if scoped.RequiredHumanApproval(from, to) == "" {
				why = "with no approval at all"
			}
			line := env.lineOf(`to: "` + to + `"`)
			if line == 0 {
				line = env.lineOf("to: " + to)
			}
			out = append(out, Finding{Where: env.where(line),
				Message: fmt.Sprintf("%s enters %s from %s, %s: YOLO would let it spend or publish without the owner", what, to, from, why),
				Fix:     fix(from, to)})
		}
	}
	if names := wf.TypeNames(); len(names) > 0 {
		for _, name := range names {
			walk(wf.ForType(name), name)
		}
	} else {
		walk(wf, "")
	}

	if hasOwnerGate || env.Project == nil {
		return out
	}
	for _, d := range env.Project.Departments {
		own := strings.TrimSpace(d.Workflow) != ""
		if own != (env.Department != "") || (own && d.Name != env.Department) {
			continue // this file isn't the one d's issues walk
		}
		for _, c := range d.Connectors {
			if c.Sends {
				out = append(out, Finding{Where: env.where(0),
					Message: fmt.Sprintf("department %s has connector %s with sends: true, but its workflow has no owner_only gate: YOLO would pass every step that uses it", d.Name, c.Name),
					Fix:     "owner_only: true on the approval before the step that uses it (the publish gate)"})
			}
		}
	}
	return out
}
