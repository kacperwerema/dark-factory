package lint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A live check after the install written as Test Plan prose ("After
// install, run …", "Live: …") is a line nobody runs: the criteria_proven
// gate doesn't list it and the lead's issue-cli search "after install:"
// finds only a criterion naming its check (docs/Workflow/proof.md#proof-after-the-install).
// The queue sweep of 2026-11-21 (C6) found four such lines left unrun.
var (
	// afterInstallLead is a Test Plan line's opening words that make it a
	// live check after the install. The text is read after the list mark,
	// the box and a bold mark.
	afterInstallLead = regexp.MustCompile(`(?i)^(after\s+(the\s+|an?\s+)?install(ing|ed|ation)?\b|live\s*(check)?\s*:)`)
	// afterInstallMark is a criterion's named check: "(after install: <command>)".
	afterInstallMark = regexp.MustCompile(`(?i)\bafter install:`)
	// listMark is a line's list mark, box and emphasis before its text.
	listMark = regexp.MustCompile(`^\s*([-*+]|\d+[.)])?\s*(\[[ xX]\]\s*)?(\*\*|__)?\s*`)
)

// checkAfterInstall reads the open issues' Test Plans.
func checkAfterInstall(env *Env) []Finding {
	closed := tracker.ClosedIssueFunc(env.Project)
	var out []Finding
	for _, issue := range env.Issues {
		if closed(issue) {
			continue
		}
		out = append(out, AfterInstallProse(issue)...)
	}
	return out
}

// AfterInstallProse is a finding for each line of issue's ## Test Plan
// that opens with "After install" or "Live:" and is not tied to an
// `after install:` criterion: a line naming an acceptance criterion whose
// text holds "(after install: …)" is the criterion's note and passes. The
// fix is the criterion, which the gate lists and the lead's search finds.
// issue-cli transition prints them when the issue moves into shipping.
func AfterInstallProse(issue *tracker.Issue) []Finding {
	plan, ok := tracker.SectionText(issue.BodyRaw, "Test Plan")
	if !ok {
		return nil
	}
	marked := map[string]bool{}
	for _, it := range tracker.Criteria(issue.BodyRaw) {
		if it.ID != "" && afterInstallMark.MatchString(it.Text) {
			marked[it.ID] = true
		}
	}
	var out []Finding
	fence := false
	for _, line := range strings.Split(plan, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		text := strings.TrimSpace(strings.NewReplacer("**", "", "__", "").Replace(listMark.ReplaceAllString(line, "")))
		if !afterInstallLead.MatchString(text) || namesMarked(text, marked) {
			continue
		}
		out = append(out, Finding{
			Check:   "after-install",
			Where:   issue.Slug + " Test Plan",
			Message: fmt.Sprintf("the Test Plan line %q is a live check after the install written as prose: the criteria_proven gate doesn't list it and the lead's issue-cli search \"after install:\" doesn't find it, so nobody runs it", shortItem(text)),
			Fix: fmt.Sprintf(`issue-cli check-add %s --section %s --text %s, then name that AC in the Test Plan line or drop the line; at testing record it with issue-cli prove %s <AC> --value "after install: <command>"`,
				issue.Slug, shell.Quote(tracker.CriteriaSection), shell.Quote("<what the live check shows> (after install: <command>)"), issue.Slug),
		})
	}
	return out
}

// namesMarked reports whether text names a criterion carrying its
// after install: check.
func namesMarked(text string, marked map[string]bool) bool {
	for _, id := range acID.FindAllString(text, -1) {
		if marked[id] {
			return true
		}
	}
	return false
}
