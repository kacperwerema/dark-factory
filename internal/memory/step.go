package memory

import (
	"regexp"
	"strings"
)

// stepWords are the words that make a one-word status name a step ("at
// shipping", "before testing") rather than an ordinary word in prose
// ("Chrome for Testing").
const stepWords = `at|in|into|to|before|after|during|while|when|on entering|entering`

// NamesStep is the first of statuses that text names as a step of the
// workflow, "" for none. A status of two or more words or with a hyphen
// (in design, human-testing) counts anywhere; a one-word status only after
// a step word or as the text's first word ("Shipping on a busy night: …").
// A name next to an arrow (idea → in progress) is a workflow edge in a
// memory about the workflow, not a step it is about. Matching ignores case.
// The caller leaves out statuses no memory should be tied to (closed ones).
func NamesStep(text string, statuses []string) string {
	for _, s := range statuses {
		name := strings.TrimSpace(s)
		if name == "" {
			continue
		}
		q := "(" + regexp.QuoteMeta(name) + ")"
		var re *regexp.Regexp
		if strings.ContainsAny(name, " -") {
			re = regexp.MustCompile(`(?i)(?:^|[^\pL\pN_-])` + q + `(?:$|[^\pL\pN_-])`)
		} else {
			re = regexp.MustCompile(`(?i)(?:^\W*|\b(?:` + stepWords + `)\s+(?:the\s+)?)` + q + `(?:$|[^\pL\pN_-])`)
		}
		for _, loc := range re.FindAllStringSubmatchIndex(text, -1) {
			if !nextToArrow(text, loc[2], loc[3]) {
				return name
			}
		}
	}
	return ""
}

// nextToArrow: the name at [start, end) has an arrow just before or after it.
func nextToArrow(text string, start, end int) bool {
	before := strings.TrimRight(text[:start], " \t")
	after := strings.TrimLeft(text[end:], " \t")
	for _, a := range []string{"→", "->"} {
		if strings.HasSuffix(before, a) || strings.HasPrefix(after, a) {
			return true
		}
	}
	return false
}
