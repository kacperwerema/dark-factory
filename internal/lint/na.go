package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checkNABoxes reads the ticked boxes of open issues that say N/A: a
// reason not in a named form (tracker.CheckNA: empty, a placeholder left,
// a form missing its test or session), and an N/A on the owner's box (a
// Human Testing box or one marked "(owner)"), which an N/A never skips.
// Closed issues keep the words they were written with.
func checkNABoxes(env *Env) []Finding {
	closed := tracker.ClosedIssueFunc(env.Project)
	var out []Finding
	for _, issue := range env.Issues {
		if closed(issue) {
			continue
		}
		for _, it := range tracker.ListCheckboxes(issue.BodyRaw) {
			if !it.Checked {
				continue
			}
			reason, ok := tracker.NAClaim(it.Text)
			if !ok {
				continue
			}
			where := issue.Slug + " " + it.ID
			if owner := naOwnerBox(it); owner != "" {
				out = append(out, Finding{
					Where:   where,
					Message: fmt.Sprintf("%s is %s with an N/A: an N/A never skips the owner's box", it.ID, owner),
					Fix:     fmt.Sprintf("issue-cli uncheck %s %s (the owner, in their shell), then leave it for the owner", issue.Slug, it.ID),
				})
				continue
			}
			if p := tracker.CheckNA(reason); p != "" {
				out = append(out, Finding{
					Where:   where,
					Message: p,
					Fix:     fmt.Sprintf(`issue-cli check %s %s --na "no page changes" (forms: issue-cli check --help)`, issue.Slug, it.ID),
				})
			}
		}
	}
	return out
}

func naOwnerBox(it tracker.CheckboxItem) string {
	switch {
	case strings.EqualFold(it.Section, "Human Testing"):
		return "a Human Testing box"
	case tracker.IsOwnerMarked(it.Label()):
		return `marked "(owner)"`
	}
	return ""
}
