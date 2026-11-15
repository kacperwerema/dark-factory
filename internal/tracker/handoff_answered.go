package tracker

import (
	"fmt"
	"sort"
	"strings"
)

// The handoff gate (docs/departments.md#the-handoff-gate):
//
//	handoff_answered: <department key>   (paths: [<glob>, ...] on the action)
//
// The issue needs a done handoff to that department filed for it
// (issue-cli handoff <key> --for <slug>) before the transition. With paths:
// it holds only when the work branch's own changes match one of them, so a
// product workflow can ask Legal about the branches that touch data flows,
// customer-facing text or a dependency manifest and let the rest through.
// A generic piece any department can be asked through
// (factory/factory-departments-floor-map-and-capabilities#D-9); the legal
// template is its first user.
const HandoffAnsweredRule = "handoff_answered"

// handoffAnsweredChecker finds the answer: nil when a done handoff to dept
// serves issue, else an error naming the open one or the command to file
// one; touched are the branch files that made the rule apply (nil without
// paths:). Registered by internal/connections, which reads handoffs.
var handoffAnsweredChecker func(w *WorkflowConfig, issue *Issue, dept string, touched []string) error

// SetHandoffAnsweredChecker registers the handoff_answered check.
func SetHandoffAnsweredChecker(fn func(w *WorkflowConfig, issue *Issue, dept string, touched []string) error) {
	handoffAnsweredChecker = fn
}

// branchChanges reads the work branch's files for handoff_answered; a
// variable so tests can stand in for git.
var branchChanges = BranchChanges

func (w *WorkflowConfig) checkHandoffAnswered(action WorkflowAction, issue *Issue) error {
	_, arg := SplitRule(action.Rule)
	dept := DepartmentKey(arg)
	if dept == "" {
		return fmt.Errorf("handoff_answered needs a department: rule: \"handoff_answered: <department key>\"")
	}
	if handoffAnsweredChecker == nil {
		return fmt.Errorf("handoff_answered needs the project's handoffs, which this program doesn't read — run the transition with issue-cli")
	}
	var touched []string
	if paths := cleanPaths(action.Paths); len(paths) > 0 {
		diff, err := branchChanges(w.ProjectRoot, issue)
		switch {
		case err != nil:
			// Can't read the branch: hold as if a path matched.
			touched = []string{"(" + strings.Replace(err.Error(), "decisions_respected: ", "", 1) + ")"}
		case diff.Warn != "":
			touched = []string{"(the branch's changes can't be read: " + strings.TrimPrefix(diff.Warn, "decisions_respected: ") + ")"}
		default:
			touched = MatchingFiles(diff.Files, paths)
			if len(touched) == 0 {
				return nil
			}
		}
	}
	return handoffAnsweredChecker(w, issue, dept, touched)
}

// MatchingFiles is the files that match one of the globs (MatchGlob), in
// files' order.
func MatchingFiles(files, globs []string) []string {
	var out []string
	for _, f := range files {
		for _, g := range globs {
			if MatchGlob(g, f) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

func cleanPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// HandoffAnsweredRules is every handoff_answered rule the workflow names:
// the department key and where ("<from> → <to>"), for lint.
func (w *WorkflowConfig) HandoffAnsweredRules() [][2]string {
	if w == nil {
		return nil
	}
	var out [][2]string
	add := func(ts []WorkflowTransition) {
		for _, t := range ts {
			for _, a := range t.Actions {
				if a.Type != "validate" {
					continue
				}
				name, arg := SplitRule(a.Rule)
				if name == HandoffAnsweredRule {
					out = append(out, [2]string{DepartmentKey(arg), t.From + " → " + t.To})
				}
			}
		}
	}
	add(w.Transitions)
	names := make([]string, 0, len(w.Types))
	for name := range w.Types {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		add(w.Types[name].Transitions)
	}
	return out
}
