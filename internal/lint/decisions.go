package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checkDecisions reads every D-n line's attribute group (the decision
// ledger, internal/tracker/decision_attrs.go) for what decide refuses at
// write time but nothing re-checks once a line is on the board: a group
// that names a decision key yet doesn't parse (so the line silently reads
// as scope issue, soft) or whose scope names no system, department or type
// of the project; hard without the owner's words; a paths: glob matching
// no file; an at: status no workflow has. Replaced decisions are out of
// force and skipped. One finding per line and problem.
func checkDecisions(env *Env) []Finding {
	x := tracker.BuildDecisionIndex(env.Issues, nil)
	if len(x.Entries) == 0 {
		return nil
	}
	k := env.decisionKnown()
	var files []string
	filesRead := false
	var out []Finding
	entries := append([]*tracker.DecisionEntry(nil), x.Entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Slug != entries[j].Slug {
			return entries[i].Slug < entries[j].Slug
		}
		return entries[i].Num < entries[j].Num
	})
	for _, e := range entries {
		if e.ReplacedBy != nil {
			continue
		}
		d := e.Decision
		if err := tracker.BrokenAttrs(d); err != nil {
			out = append(out, Finding{Where: d.Ref,
				Message: fmt.Sprintf("unknown scope key or value: its trailing {…} names a decision key but is read as text (%v), so the decision reaches only its own issue, soft", err),
				Fix:     fmt.Sprintf("issue-cli decide %s --replaces %s --text \"…\" with --scope/--hard/--at/--paths/--expires (keys: %s)", d.Slug, d.ID, tracker.DecisionKeyList())})
		} else if msg := k.unknownScope(d.DecisionAttrs); msg != "" {
			out = append(out, Finding{Where: d.Ref, Message: "unknown scope key or value: " + msg,
				Fix: fmt.Sprintf("issue-cli decide %s --amend %s --scope <scope>", d.Slug, d.ID)})
		}
		if d.Hard && tracker.NeedsOwnerWords(d) {
			out = append(out, Finding{Where: d.Ref,
				Message: "hard without the owner's words: no quote, or a provisional or YOLO mark on the line",
				Fix:     fmt.Sprintf("the owner confirms it: issue-cli decide %s --confirm %s --quote \"…\" (or makes it soft: --amend %s --soft)", d.Slug, d.ID, d.ID)})
		}
		if len(d.Paths) > 0 {
			if !filesRead {
				files, filesRead = tracker.RepoFiles(env.Project.Root()), true
			}
			for _, g := range d.Paths {
				if !matchesAny(g, files) {
					out = append(out, Finding{Where: d.Ref,
						Message: fmt.Sprintf("paths: %q matches no file in the repository, so the diff audit guards nothing there", g),
						Fix:     fmt.Sprintf("the owner fixes the glob: issue-cli decide %s --amend %s --paths \"…\" --quote \"…\"", d.Slug, d.ID)})
				}
			}
		}
		for _, s := range d.At {
			if len(k.statuses) > 0 && !k.statuses[strings.ToLower(strings.TrimSpace(s))] {
				out = append(out, Finding{Where: d.Ref,
					Message: fmt.Sprintf("at: %q is no status of this project's workflows, so the decision never reaches a briefing there", s),
					Fix:     fmt.Sprintf("issue-cli decide %s --amend %s --at \"…\" (statuses: %s)", d.Slug, d.ID, strings.Join(k.statusOrder, ", "))})
			}
		}
	}
	return out
}

// decisionKnown is what a decision's scope and at: may name.
type decisionKnown struct {
	systems, types, statuses map[string]bool
	statusOrder              []string
	env                      *Env
}

func (env *Env) decisionKnown() decisionKnown {
	k := decisionKnown{systems: map[string]bool{}, types: map[string]bool{}, statuses: map[string]bool{}, env: env}
	wfs := []*tracker.WorkflowConfig{env.Workflow}
	for _, d := range env.departmentEnvs() {
		wfs = append(wfs, d.Workflow)
	}
	for _, wf := range wfs {
		if wf == nil {
			continue
		}
		for _, s := range wf.GetStatusOrder() {
			if l := strings.ToLower(s); !k.statuses[l] {
				k.statuses[l] = true
				k.statusOrder = append(k.statusOrder, s)
			}
		}
		for t := range wf.Types {
			k.types[strings.ToLower(t)] = true
		}
		for s := range wf.Workbench.Systems {
			k.systems[strings.ToLower(s)] = true
		}
	}
	for _, i := range env.Issues {
		if i.System != "" {
			k.systems[strings.ToLower(i.System)] = true
		}
		if i.Type != "" {
			k.types[strings.ToLower(i.Type)] = true
		}
	}
	return k
}

// unknownScope says what a scope names that the project lacks, or "".
func (k decisionKnown) unknownScope(a tracker.DecisionAttrs) string {
	kind, name := a.ScopeKind()
	switch kind {
	case tracker.ScopeSystem:
		if !k.systems[strings.ToLower(name)] {
			return fmt.Sprintf("scope %s: no issue or workbench.systems entry has system %q", a.Scope, name)
		}
	case tracker.ScopeDepartment:
		if tracker.DepartmentKey(name) != "product" && k.env.Project.DepartmentByKey(name) == nil {
			return fmt.Sprintf("scope %s: no department %q in projects.yaml (box keys: product and each department's key)", a.Scope, name)
		}
	case tracker.ScopeType:
		if !k.types[strings.ToLower(name)] {
			return fmt.Sprintf("scope %s: no workflow defines type %q and no issue has it", a.Scope, name)
		}
	}
	return ""
}
