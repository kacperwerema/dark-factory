package viewer

import (
	"regexp"
	"testing"
)

// A lost "}" (a merge that drops a block's closing line) nests every later
// rule inside the block above it (each part of a bundle is checked alone), so whole pages silently lose their styles:
// in v0.48.0 the Metrics and Status CSS ended up inside a max-width media
// query and only applied on narrow screens. Keep the braces balanced.
func TestStyleCSSBracesBalanced(t *testing.T) {
	for _, bundle := range []string{"style", "workshop"} {
		for _, p := range cssBundleParts(t, bundle) {
			bracesBalanced(t, p.name, p.text)
		}
	}
}

func bracesBalanced(t *testing.T, name, data string) {
	t.Helper()
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(data, "")
	depth, line, openedAt := 0, 1, []int{}
	for _, ch := range css {
		switch ch {
		case '\n':
			line++
		case '{':
			depth++
			openedAt = append(openedAt, line)
		case '}':
			depth--
			if depth < 0 {
				t.Errorf("%s: unmatched } near line %d (comments stripped)", name, line)
				return
			}
			openedAt = openedAt[:len(openedAt)-1]
		}
	}
	if depth != 0 {
		t.Errorf("%s: %d block(s) never closed; the first opens near line %d (comments stripped)", name, depth, openedAt[0])
	}
}
