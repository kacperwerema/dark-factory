package lint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/shell"
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
// a ## Must not still holding only its placeholder once the issue is
// past the edge that wrote it, and acceptance boxes kept outside
// ## Acceptance Criteria on a path with a criteria gate. issue-cli transition prints them as
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
			Fix: fmt.Sprintf(`issue-cli check-edit %s %s --text %s (or a backticked command, or a playtest step; add " (owner)" when only the owner can judge it)`,
				issue.Slug, it.ID, shell.Quote(text+" (test: TestName)")),
		})
	}
	if f, ok := mustNotUnwritten(issue, wf); ok {
		out = append(out, f)
	}
	if f, ok := strayCriteria(issue, wf); ok {
		out = append(out, f)
	}
	out = append(out, mustNotUnpaired(issue, wf)...)
	return out
}

// mustNotPair is a Must not item's tail: "→ AC2" (or "->"), several ids
// ("→ AC1, AC3"), or "→ none: <why>".
var (
	mustNotArrow = regexp.MustCompile(`(→|->)\s*([^→]*)$`)
	mustNotIDs   = regexp.MustCompile(`^AC\d+(\s*(,|/|&|\band\b)\s*AC\d+)*\.?$`)
	mustNotNone  = regexp.MustCompile(`(?i)^none\s*:\s*\S`)
	acID         = regexp.MustCompile(`AC\d+`)
	// mustNotNothing is the one-line Must not with nothing to pair:
	// "- nothing beyond the scope above".
	mustNotNothing = regexp.MustCompile(`(?i)^nothing\b`)
)

// mustNotUnpaired is a finding for each ## Must not item on an issue past
// design that names no criterion failing when it is broken ("→ ACn") and
// no reason there is none ("→ none: <why>"), and for each "→ ACn" naming
// a box the issue doesn't have. "nothing beyond the scope above" needs no
// pair.
func mustNotUnpaired(issue *tracker.Issue, wf *tracker.WorkflowConfig) []Finding {
	items := tracker.MustNot(issue.BodyRaw)
	if len(items) == 0 || !pastMustNot(issue, wf) {
		return nil
	}
	ids := map[string]bool{}
	for _, it := range tracker.Criteria(issue.BodyRaw) {
		if it.ID != "" {
			ids[it.ID] = true
		}
	}
	var out []Finding
	for _, item := range items {
		where := fmt.Sprintf("%s ## Must not %q", issue.Slug, shortItem(item))
		m := mustNotArrow.FindStringSubmatch(item)
		tail := ""
		if m != nil {
			tail = strings.TrimSpace(m[2])
		}
		switch {
		case mustNotNothing.MatchString(item):
			continue
		case m != nil && mustNotNone.MatchString(tail):
			continue
		case m != nil && mustNotIDs.MatchString(tail):
			var missing []string
			for _, id := range acID.FindAllString(tail, -1) {
				if !ids[id] {
					missing = append(missing, id)
				}
			}
			if len(missing) == 0 {
				continue
			}
			out = append(out, Finding{
				Check:   "criteria",
				Where:   where,
				Message: fmt.Sprintf("names %s, which the issue's acceptance criteria don't have", strings.Join(missing, ", ")),
				Fix:     fmt.Sprintf(`issue-cli replace %s --section "Must not" --body "…" (point it at an existing ACn, or add the criterion that fails when it is broken)`, issue.Slug),
			})
		default:
			out = append(out, Finding{
				Check:   "criteria",
				Where:   where,
				Message: "names no criterion that fails when it is broken: end it with → ACn, or → none: <why>",
				Fix:     fmt.Sprintf(`issue-cli replace %s --section "Must not" --body "- %s → AC<n>" (or "→ none: <why>")`, issue.Slug, item),
			})
		}
	}
	return out
}

// shortItem is a Must not item cut to fit a finding's where.
func shortItem(item string) string {
	r := []rune(item)
	if len(r) > 60 {
		return string(r[:57]) + "…"
	}
	return item
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
