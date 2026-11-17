package tracker

import "strings"

// MapSection rewrites the raw text of the first section named title (a bare
// title is a "## " heading) with f: the lines between its heading and the
// next heading of the same or a higher level, as SectionText finds them but
// untrimmed. Every other byte of body stays as it was. ok is false when
// there is no such section; changed when f returned other text.
func MapSection(body, title string, f func(text string) string) (next string, ok, changed bool) {
	matches := findHeadingMatches(body, normalizeHeading(title))
	if len(matches) == 0 {
		return body, false, false
	}
	m := matches[0]
	lines := strings.Split(body, "\n")
	old := strings.Join(lines[m.StartLine+1:m.EndLine], "\n")
	repl := f(old)
	if repl == old {
		return body, true, false
	}
	out := append([]string(nil), lines[:m.StartLine+1]...)
	out = append(out, repl)
	out = append(out, lines[m.EndLine:]...)
	return strings.Join(out, "\n"), true, true
}
