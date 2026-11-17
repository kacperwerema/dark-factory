package tracker

import (
	"strings"
	"testing"
)

func TestMapSectionLeavesTheRestAsItWas(t *testing.T) {
	body := "## A\n  keep  \n\n## Implementation\nold x\n### Sub\nold y\n\n## B\nold z\n"
	next, ok, changed := MapSection(body, "Implementation", func(s string) string { return strings.ReplaceAll(s, "old", "new") })
	want := "## A\n  keep  \n\n## Implementation\nnew x\n### Sub\nnew y\n\n## B\nold z\n"
	if !ok || !changed || next != want {
		t.Errorf("MapSection = %q, %v, %v; want %q", next, ok, changed, want)
	}
	if _, ok, _ := MapSection(body, "Nope", strings.ToUpper); ok {
		t.Error("a missing section reported ok")
	}
}
