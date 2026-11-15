package postmortem

import (
	"path/filepath"
	"testing"
)

// The postmortems playtest scenario's retros group as its sessions count
// them (playtest/sessions/postmortems.yaml TP4: "3 in 1 group"): the three
// workflow copies in one group, the three memories alone. A change to the
// grouping or to the fixture shows here, in the fast tier, before the
// slow tier's session goes red on it.
func TestPostmortemsScenarioGroupsAsItsSessionsCount(t *testing.T) {
	items := Queue(filepath.Join("..", "..", "playtest", "scenarios", "postmortems", "files", "retros"))
	if len(items) != 6 {
		t.Fatalf("items = %d, want 6 (two per post-mortem, one archived)", len(items))
	}
	joined, groups := 0, 0
	for _, pg := range GroupItems(items) {
		for _, g := range pg.Groups {
			if g.Alone {
				continue
			}
			groups++
			joined += len(g.Items)
			if pg.Place != PlaceWorkflow {
				var titles []string
				for _, it := range g.Items {
					titles = append(titles, it.Issue+": "+it.Title)
				}
				t.Errorf("%s actions joined, the fixture means them alone: %q", pg.Place, titles)
			}
		}
	}
	if joined != 3 || groups != 1 {
		t.Errorf("%d in %d groups, the sessions count 3 in 1 group", joined, groups)
	}
}
