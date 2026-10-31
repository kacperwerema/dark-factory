package retro

import (
	"reflect"
	"testing"
)

// The workflow's own vocabulary groups retros too: comment and trailer
// prefixes inside a sentence and all-caps names, beside the code-like
// terms. A label at a sentence start and a file name don't.
func TestTermsFindPrefixesAndCapsNames(t *testing.T) {
	text := "Friction: the Proves: trailers read UNPROVEN until a tests: comment exists. " +
		"Catalog: n/a came late; the CHANGELOG conflicted and CLAUDE.md said so. " +
		"Also check-script and tests: again, and docs/departments.md."
	got := Terms(text, map[string]bool{"unproven": true})
	want := []string{"Proves:", "tests:", "CHANGELOG", "check-script", "docs/departments.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Terms = %q, want %q", got, want)
	}
}
