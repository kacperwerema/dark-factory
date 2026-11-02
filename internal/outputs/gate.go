package outputs

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func init() {
	tracker.SetOutputChecker(func(w *tracker.WorkflowConfig, issue *tracker.Issue, kind string) error {
		p := &tracker.Project{IssueDir: w.IssuesRoot, WorkDir: w.ProjectRoot}
		list, _ := Load(p)
		return GateError(list, issue.Slug, kind)
	})
}

// GateError is the has_output check: nil when the shelf has an output for
// slug (of kind, when kind is set) that is not rejected and whose file or
// path is still there. A rejected or gone one is named, so the worker
// knows to add a new one rather than look for it.
func GateError(list []*Output, slug, kind string) error {
	what := "an output"
	if kind != "" {
		what = "a " + kind
	}
	var rejected, gone []string
	for _, o := range Select(list, Filter{Issue: slug}) {
		if kind != "" && !strings.EqualFold(strings.TrimSpace(o.Kind), kind) {
			continue
		}
		switch {
		case o.State == StateRejected:
			rejected = append(rejected, o.ID)
		case o.Missing:
			gone = append(gone, o.ID)
		default:
			return nil
		}
	}
	k := kind
	if k == "" {
		k = "<kind>"
	}
	add := fmt.Sprintf("issue-cli output add --issue %s --kind %s --title \"...\" --file <file> (or --path <repo path> / --url <link>)", slug, k)
	msg := fmt.Sprintf("no %s on the shelf for %s", strings.TrimPrefix(strings.TrimPrefix(what, "an "), "a "), slug)
	switch {
	case len(rejected) > 0:
		msg += fmt.Sprintf(" (%s rejected by the owner: add a new one that answers the review)", strings.Join(rejected, ", "))
	case len(gone) > 0:
		msg += fmt.Sprintf(" (%s: its file is gone)", strings.Join(gone, ", "))
	}
	return fmt.Errorf("%s — put %s there:\n\n  %s", msg, what, add)
}
