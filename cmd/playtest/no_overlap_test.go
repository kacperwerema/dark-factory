package main

import (
	"strings"
	"testing"
)

// no_overlap compares what shows: a block scrolled out of its panel is cut
// at the panel's edge, so it doesn't overlap what follows the panel; two
// blocks that both show and overlap still fail.
func TestNoOverlapCutsAtScrollPanels(t *testing.T) {
	const head = `<!doctype html><html><head><style>body{margin:0} .box{height:200px}</style></head><body>`
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"side by side", `<div class="box" data-testid="a">a</div><div class="box" data-testid="b">b</div>`, nil},
		{"pulled over", `<div class="box" data-testid="a">a</div><div class="box" style="margin-top:-100px" data-testid="b">b</div>`, []string{"a no_overlap"}},
		// The panel is 150px tall; "low" lies past its bottom, under the
		// panel's own scroll, so "after" isn't covered by it.
		{"cut by a scrolling panel", `<div style="height:150px;overflow:auto"><div class="box">x</div><div class="box" data-testid="low">low</div></div><div class="box" data-testid="after">after</div>`, nil},
		// An overflowing panel that shows its overflow still overlaps.
		{"spills from a visible panel", `<div style="height:150px"><div class="box">x</div><div class="box" data-testid="low">low</div></div><div class="box" data-testid="after">after</div>`, []string{"low no_overlap"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pageViolations(t, head+tc.body+`</body></html>`, 800, 600, "no_overlap")
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("violations %q, want %q", got, tc.want)
			}
		})
	}
}
