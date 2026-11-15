package validations

import (
	"fmt"
	"strings"
)

func init() { register("children_closed", ChildrenClosed) }

// ChildrenClosed passes when every child of the issue (the issues naming it
// in parent:) is closed: in values: when set, else by its own workflow
// (tracker.IsClosedStatus, the Closed the tracker sets on each child). An epic
// with no children passes. Requires cfg.Children, which the tracker injects
// from its issue store.
func ChildrenClosed(action Action, issue *IssueView, cfg Config) error {
	if cfg.Children == nil {
		return fail(action, issue,
			"children_closed requires the issue store, but none is configured",
			"run validation via the issue-cli or server (sets it automatically)")
	}
	var open []string
	for _, c := range cfg.Children(issue.Slug) {
		closed := c.Closed
		if len(action.Values) > 0 {
			closed = inFold(c.Status, action.Values)
		}
		if !closed {
			open = append(open, c.Slug+" ("+c.Status+")")
		}
	}
	if len(open) == 0 {
		return nil
	}
	return fail(action, issue,
		fmt.Sprintf("%d of its children are still open: %s", len(open), strings.Join(open, ", ")),
		fmt.Sprintf("finish or park them first (issue-cli list --parent %s)", issue.Slug))
}

func inFold(s string, list []string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(v)) {
			return true
		}
	}
	return false
}
