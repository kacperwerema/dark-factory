package connections

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func init() {
	tracker.SetHandoffAnsweredChecker(func(w *tracker.WorkflowConfig, issue *tracker.Issue, dept string, touched []string) error {
		if w.IssuesRoot == "" {
			return fmt.Errorf("handoff_answered needs the project's issues folder, which this program doesn't know — run the transition with issue-cli")
		}
		issues, err := tracker.LoadIssuesWhere(w.IssuesRoot, func(data []byte) bool {
			return strings.Contains(string(data), HandoffForField)
		})
		if err != nil {
			return fmt.Errorf("handoff_answered: can't read the handoffs: %v", err)
		}
		return AnsweredError(issues, func(i *tracker.Issue) []tracker.Comment {
			cs := i.Comments()
			return cs
		}, issue.Slug, dept, touched)
	})
}

// AnsweredError is the handoff_answered check over the project's issues:
// nil when a handoff to dept filed for slug (handoff_for:) is done and
// wasn't declined. Otherwise it names the open handoffs, the closed ones
// that don't count, and the command to file one; touched (the branch
// files that made the rule apply) is quoted when set.
func AnsweredError(issues []*tracker.Issue, comments func(*tracker.Issue) []tracker.Comment, slug, dept string, touched []string) error {
	var open, notAnswer []string
	for _, i := range issues {
		route := ParseRoute(i.Field(HandoffRouteField))
		if len(route) < 2 || route[len(route)-1] != dept {
			continue
		}
		if strings.TrimSpace(i.Field(HandoffForField)) != slug {
			continue
		}
		var cs []tracker.Comment
		if comments != nil {
			cs = comments(i)
		}
		_, declined := declineReason(cs)
		switch {
		case !tracker.IsClosedStatus(nil, i.Status):
			open = append(open, fmt.Sprintf("%s (at %s)", i.Slug, i.Status))
		case done(i) && !declined:
			return nil
		case declined:
			notAnswer = append(notAnswer, i.Slug+" (declined)")
		default:
			notAnswer = append(notAnswer, i.Slug+" (parked)")
		}
	}
	sort.Strings(open)
	sort.Strings(notAnswer)
	var b strings.Builder
	fmt.Fprintf(&b, "handoff_answered: %s hasn't answered %s yet", dept, slug)
	if len(touched) > 0 {
		fmt.Fprintf(&b, " (the branch touches %s)", strings.Join(touched, ", "))
	}
	b.WriteString(".\n")
	switch {
	case len(open) > 0:
		fmt.Fprintf(&b, "  Waiting on: %s. The transition passes once it is done; don't wait or poll, say so and end your turn.\n", strings.Join(open, ", "))
	default:
		if len(notAnswer) > 0 {
			fmt.Fprintf(&b, "  Not an answer: %s.\n", strings.Join(notAnswer, ", "))
		}
		fmt.Fprintf(&b, "  Ask them:\n\n    issue-cli handoff %s --for %s --title \"...\" --why \"...\"\n", dept, slug)
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}
