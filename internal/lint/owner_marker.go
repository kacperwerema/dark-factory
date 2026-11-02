package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checkOwnerMarker reads the open acceptance criteria of open issues for
// one that reads like a check only the owner does ("the owner confirms it
// reads well") but carries no owner marker (tracker.IsOwnerMarked): the
// criteria_proven gate counts it as the agent's, and an agent may tick it.
// The fix adds " (owner)" at its end.
func checkOwnerMarker(env *Env) []Finding {
	closed := tracker.ClosedIssueFunc(env.Project)
	var out []Finding
	for _, issue := range env.Issues {
		if closed(issue) {
			continue
		}
		for _, it := range tracker.Criteria(issue.BodyRaw) {
			if it.Checked || it.ID == "" || !tracker.LooksLikeOwnerCheck(it.Text) {
				continue
			}
			text := strings.TrimSpace(it.Label())
			out = append(out, Finding{
				Where:   issue.Slug + " " + it.ID,
				Message: fmt.Sprintf("%s reads like the owner's check but has no (owner) marker: the criteria_proven gate counts it as the agent's", it.ID),
				Fix:     fmt.Sprintf(`issue-cli check-edit %s %s --text %q`, issue.Slug, it.ID, text+" (owner)"),
			})
		}
	}
	return out
}
