package testids

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestParseSourceReadsEveryListedID: lint reads a project's ids from these
// files on disk (ParseSource), so the parse of this package's own sources
// must give back exactly the list built into the binary. An id listed some
// other way (a helper, a constant) would be unknown to lint.
func TestParseSourceReadsEveryListedID(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	var got []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		ids, err := ParseSource(f, src)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ids...)
	}
	want := allIDs()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		for _, id := range want {
			if !slices.Contains(got, id) {
				t.Errorf("listed but not parsed: %q", id)
			}
		}
		for _, id := range got {
			if !slices.Contains(want, id) {
				t.Errorf("parsed but not listed: %q", id)
			}
		}
	}
}

func TestSetKnownAndSuggest(t *testing.T) {
	s := NewSet([]string{"a.b.c", "a.row.#", "a.col.*"})
	for id, want := range map[string]bool{"a.b.c": true, "a.row.3": true, "a.col.x": true, "a.row.x": false, "a.b.d": false} {
		if s.Known(id) != want {
			t.Errorf("Known(%q) = %v", id, !want)
		}
	}
	if got := s.Suggest("a.b.cc"); got != "a.b.c" {
		t.Errorf("Suggest = %q", got)
	}
}
