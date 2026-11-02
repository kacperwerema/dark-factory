package lint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A criterion is falsifiable when its text names something that could fail
// it: a test, a command, or a playtest step. The check reads words only, so
// every finding stays a warning (factory/criteria-a-command-can-fail-…#D-1).
var (
	// criterionTest names a test: a Go test name, "test(s)", a test file,
	// a fixture.
	criterionTest = regexp.MustCompile(`\bTest[A-Z0-9_]\w*|(?i:\b(tests?|tested|fixtures?|_test\.go|go test)\b)`)
	// criterionCommand names a command: a span in backticks, a CLI the
	// engine runs, an exit code.
	criterionCommand = regexp.MustCompile("`[^`]+`|(?i:\\b(issue-cli|curl|exits?|exit code|returns? (an )?error)\\b)")
	// criterionPlaytest names a playtest step, a shot or a guided check.
	criterionPlaytest = regexp.MustCompile(`(?i)\b(playtest|session|step|screenshot|shot|check[- ]script|scripts/checks)\b|\(visual\)`)
	// vagueWords are claims nothing checks on their own: the message
	// quotes the one a criterion leans on.
	vagueWords = regexp.MustCompile(`(?i)\b(works?|working|is clear|clearly|looks? (good|right|nice|fine)|nice|intuitive|properly|correctly|better|easy|easier|user[- ]friendly|seamless(ly)?|smooth(ly)?|clean|as expected|robust)\b`)
)

// Falsifiable reports whether a criterion's text names a test, a command
// or a playtest step that could fail it.
func Falsifiable(text string) bool {
	return criterionTest.MatchString(text) || criterionCommand.MatchString(text) || criterionPlaytest.MatchString(text)
}

// checkCriteria reads the open issues' acceptance criteria and their
// ## Must not section.
func checkCriteria(env *Env) []Finding {
	closed := tracker.ClosedIssueFunc(env.Project)
	memo := env.Project.WorkflowMemo()
	var out []Finding
	for _, issue := range env.Issues {
		if closed(issue) {
			continue
		}
		out = append(out, Criteria(issue, memo.ForIssue(issue))...)
	}
	return out
}

// Criteria is one issue's criteria findings, for the workflow it walks:
// open acceptance criteria no test, command or playtest step could fail,
// and a ## Must not still holding only its placeholder once the issue is
// past the edge that wrote it. issue-cli transition prints them as
// warnings when the issue leaves design.
func Criteria(issue *tracker.Issue, wf *tracker.WorkflowConfig) []Finding {
	var out []Finding
	template := map[string]bool{}
	if wf != nil {
		template = wf.SectionBoxes(tracker.CriteriaSection)
	}
	for _, it := range tracker.Criteria(issue.BodyRaw) {
		text := strings.TrimSpace(it.Label())
		if it.Checked || it.ID == "" || text == "" || template[strings.TrimSpace(it.Text)] ||
			tracker.IsOwnerMarked(it.Text) || Falsifiable(it.Text) {
			continue
		}
		if _, na := tracker.NAClaim(it.Text); na {
			continue
		}
		msg := fmt.Sprintf("%s names no test, command or playtest step that could fail it", it.ID)
		if w := vagueWords.FindString(text); w != "" {
			msg = fmt.Sprintf("%s says %q but names no test, command or playtest step that could fail it", it.ID, w)
		}
		out = append(out, Finding{
			Check:   "criteria",
			Where:   issue.Slug + " " + it.ID,
			Message: msg,
			Fix: fmt.Sprintf(`issue-cli check-edit %s %s --text %q (or a backticked command, or a playtest step; add " (owner)" when only the owner can judge it)`,
				issue.Slug, it.ID, text+" (test: TestName)"),
		})
	}
	if f, ok := mustNotUnwritten(issue, wf); ok {
		out = append(out, f)
	}
	return out
}

// mustNotUnwritten is a finding when the issue has a ## Must not section
// holding only the placeholder, and is past the status the workflow writes
// it on.
func mustNotUnwritten(issue *tracker.Issue, wf *tracker.WorkflowConfig) (Finding, bool) {
	text, ok := tracker.SectionText(issue.BodyRaw, tracker.MustNotSection)
	if !ok || strings.TrimSpace(text) == "" || len(tracker.MustNot(issue.BodyRaw)) > 0 || !pastMustNot(issue, wf) {
		return Finding{}, false
	}
	return Finding{
		Check:   "criteria",
		Where:   issue.Slug + " ## Must not",
		Message: "## Must not still holds only the template's placeholder: the critic has nothing to check the diff against",
		Fix:     fmt.Sprintf(`issue-cli replace %s --section "Must not" --body "- <what the change must not do>" (or "- nothing beyond the scope above")`, issue.Slug),
	}, true
}

// pastMustNot reports whether the issue's status comes after the status
// the workflow's Must not section is appended on (in design, in the
// development template).
func pastMustNot(issue *tracker.Issue, wf *tracker.WorkflowConfig) bool {
	if wf == nil {
		return false
	}
	order := wf.GetStatusOrder()
	index := func(s string) int {
		for i, o := range order {
			if strings.EqualFold(o, s) {
				return i
			}
		}
		return -1
	}
	at := index(issue.Status)
	for _, t := range wf.Transitions {
		for _, a := range t.Actions {
			if a.Type == "append_section" && strings.EqualFold(strings.TrimSpace(a.Title), tracker.MustNotSection) {
				if to := index(t.To); to >= 0 && at > to {
					return true
				}
			}
		}
	}
	return false
}
