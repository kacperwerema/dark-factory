package tracker

import (
	"fmt"
	"regexp"
	"strings"
)

// Named N/A forms: how a worker says a box doesn't fit the change (a
// test-only fix, a CLI-only bug, a runner-internal change) in words
// anyone can check, instead of new words each time. issue-cli check --na
// writes one, the na-boxes lint check reads them, and the testing prompt
// lists them. An N/A never takes a box from the owner: the callers refuse
// it on owner boxes.

// NAForm is one named N/A form.
type NAForm struct {
	// Form is what the worker writes after "N/A: ", placeholders in <>.
	Form string
	// When says which change it fits.
	When string
}

// NAForms is every named form, in the order the hints list them. The last
// is the free form.
var NAForms = []NAForm{
	{"the repro is an automated test (<TestName>)", "a test-only fix, or a bug whose repro is a go test"},
	{"no page changes", "a CLI, tmux, runner or docs change: nothing a playtest session or ux-test can see"},
	{"runner-internal, covered by <TestName>", "playtest runner, CI or check library internals"},
	{"covered by playtest session <name>", "the guided-check box when a playtest session drives the manual check"},
	{"cli flow covered by <TestName>", "a CLI-only change whose flow a go test walks: no page, no check script"},
	{"page state a scenario can't build (<what>), shown by <evidence>", "a page change a scenario board can't reach (git state, live agents): say what, and what shows it instead"},
	{"<why, in a few words>", "anything else"},
}

// NAFormsHelp lists the forms, one per line, indented.
func NAFormsHelp(indent string) string {
	var b strings.Builder
	const width = 46
	for _, f := range NAForms {
		if len(f.Form) > width {
			// A form too long for the column: its use on the next line.
			fmt.Fprintf(&b, "%sN/A: %s\n%s     %-*s %s\n", indent, f.Form, indent, width, "", f.When)
			continue
		}
		fmt.Fprintf(&b, "%sN/A: %-*s %s\n", indent, width, f.Form, f.When)
	}
	return strings.TrimRight(b.String(), "\n")
}

// naClaimRe finds an N/A claim: "N/A: <reason>" or "(n/a: <reason>)". The
// template words "or N/A with the reason" have no colon, so they are not a
// claim.
var naClaimRe = regexp.MustCompile(`(?i)\bn/a\s*:`)

var naPlaceholderRe = regexp.MustCompile(`<[^<>]+>`)

// NAMinWords is the fewest words a free N/A reason may have.
const NAMinWords = 3

// NANoFormSections are sections whose boxes have no N/A form: they are
// the shipping steps, ticked plainly when done (a box that says "or none"
// is ticked as it is).
var NANoFormSections = []string{"Shipping"}

// NANoForm reports whether a box in section takes no N/A.
func NANoForm(section string) bool {
	for _, s := range NANoFormSections {
		if strings.EqualFold(strings.TrimSpace(section), s) {
			return true
		}
	}
	return false
}

// NAClaim returns the reason of the last N/A claim in a box's text, and
// whether there is one.
func NAClaim(text string) (string, bool) {
	locs := naClaimRe.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return "", false
	}
	start, end := locs[len(locs)-1][0], locs[len(locs)-1][1]
	reason := strings.TrimSpace(text[end:])
	// "(n/a: why)": drop the closing paren of the claim, not one inside it.
	if start > 0 && text[start-1] == '(' && strings.HasSuffix(reason, ")") &&
		strings.Count(reason, ")") > strings.Count(reason, "(") {
		reason = strings.TrimSpace(strings.TrimSuffix(reason, ")"))
	}
	return reason, true
}

// CheckNA says what is wrong with an N/A reason, or "" when it is in a
// named form or a free reason of a few words.
func CheckNA(reason string) string {
	r := strings.TrimSpace(reason)
	lower := strings.ToLower(r)
	switch {
	case r == "":
		return "the N/A gives no reason"
	case naPlaceholderRe.MatchString(r):
		return fmt.Sprintf("the N/A still has the placeholder %s (text in <>): write what it stands for, the test, session or reason, without the <>", naPlaceholderRe.FindString(r))
	case strings.HasPrefix(lower, "the repro is an automated test"):
		if !naNamed(r[len("the repro is an automated test"):]) {
			return `"the repro is an automated test" needs the test's name: the repro is an automated test (<TestName>)`
		}
	case strings.HasPrefix(lower, "runner-internal"):
		i := strings.Index(lower, "covered by")
		if i < 0 || strings.TrimSpace(r[i+len("covered by"):]) == "" {
			return `"runner-internal" needs the test that covers it: runner-internal, covered by <TestName>`
		}
	case strings.HasPrefix(lower, "cli flow covered by"):
		if strings.TrimSpace(r[len("cli flow covered by"):]) == "" {
			return `"cli flow covered by" needs the test that walks the flow: cli flow covered by <TestName>`
		}
	case strings.HasPrefix(lower, naPageStatePrefix):
		if !naNamed(r[len(naPageStatePrefix):]) {
			return `"page state a scenario can't build" needs what state, in parens: page state a scenario can't build (<what>), shown by <evidence>`
		}
		i := strings.Index(lower, "shown by")
		if i < 0 || strings.TrimSpace(r[i+len("shown by"):]) == "" {
			return `"page state a scenario can't build" needs what shows the change instead: …, shown by <evidence>`
		}
	case strings.HasPrefix(lower, "covered by"):
		if strings.TrimSpace(r[len("covered by"):]) == "" {
			return `"covered by" needs what covers it: covered by playtest session <name>, or covered by <TestName>`
		}
	case len(strings.Fields(r)) < NAMinWords:
		n := len(strings.Fields(r))
		return fmt.Sprintf("the N/A reason %q is too short: %d word%s, a free reason needs at least %d words; use a named form or say why in %d words or more",
			r, n, map[bool]string{true: "", false: "s"}[n == 1], NAMinWords, NAMinWords)
	}
	return ""
}

// naPageStatePrefix starts the form for a page a scenario can't reach.
const naPageStatePrefix = "page state a scenario can't build"

// naNamed reports whether rest ("(TestFoo)") names something in parens.
func naNamed(rest string) bool {
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "(") {
		return false
	}
	end := strings.Index(rest, ")")
	return end > 1 && strings.TrimSpace(rest[1:end]) != ""
}

// WithNA returns a box's text with its N/A reason: an earlier N/A claim is
// replaced, so a box carries one.
func WithNA(text, reason string) string {
	text = strings.TrimSpace(text)
	if locs := naClaimRe.FindAllStringIndex(text, -1); len(locs) > 0 {
		start := locs[len(locs)-1][0]
		// Inside "(…)", such as the template's "(or N/A: …)": cut from
		// the open paren.
		if open := strings.LastIndex(text[:start], "("); open >= 0 && !strings.Contains(text[open:start], ")") {
			start = open
		}
		text = strings.TrimRight(text[:start], " —–-")
	}
	return fmt.Sprintf("%s (N/A: %s)", text, strings.TrimSpace(reason))
}
