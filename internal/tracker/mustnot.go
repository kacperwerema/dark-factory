package tracker

import "strings"

// MustNotSection is the section listing what a change must not do: plain
// bullets the fresh critic checks the diff against. A line may name a test
// or a command in backticks, as a criterion does, so the critic can run it.
// The idea → in design edge of the development template appends it.
const MustNotSection = "Must not"

// MustNotPlaceholder is the bullet the template writes: what to put there.
const MustNotPlaceholder = "<what this change must not do; name a test or a command in backticks when one can catch it>"

// MustNot returns the issue's ## Must not lines, each without its bullet,
// leaving out the template's placeholder and blank lines. It is empty when
// the section is missing or holds only the placeholder.
func MustNot(body string) []string {
	text, ok := SectionText(body, MustNotSection)
	if !ok {
		return nil
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		item, isItem := listItemText(line)
		if !isItem {
			continue
		}
		// A box written by hand reads the same as a bullet.
		for _, box := range []string{"[ ]", "[x]", "[X]"} {
			item = strings.TrimSpace(strings.TrimPrefix(item, box))
		}
		if item == "" || IsMustNotPlaceholder(item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// IsMustNotPlaceholder reports whether a Must not line is still the
// template's placeholder (any <…> line counts: a project may word its own).
func IsMustNotPlaceholder(item string) bool {
	item = strings.TrimSpace(item)
	return item == MustNotPlaceholder || (strings.HasPrefix(item, "<") && strings.HasSuffix(item, ">"))
}
