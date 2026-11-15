package viewer

import (
	"regexp"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/liveboard"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// deadRefRe is an issue slug in a sentence: <system>/<words-with-dashes>.
var deadRefRe = regexp.MustCompile(`\b[a-z]+/[a-z0-9]+(?:-[a-z0-9]+){2,}\b`)

// What the score, /health and the Stats page say a missing number waits for
// names no issue that is gone from the board: the flaky component and three
// Stats cards named one for weeks after it left.
func TestNoDeadIssueRefsInScore(t *testing.T) {
	var texts []string
	for _, c := range health.DefaultComponents() {
		texts = append(texts, c.Name+" Missing: "+c.Missing)
	}
	for name, s := range healthUnlock {
		texts = append(texts, name+" unlock: "+s)
	}
	texts = append(texts, "gauge: "+checksNeed, "gauge: "+contextNeed)
	for _, p := range pendingCards(nil) {
		texts = append(texts, p.Title+": "+p.Need)
	}
	issues, err := tracker.LoadIssues(liveboard.Dir(t))
	if err != nil {
		t.Fatal(err)
	}
	onBoard := map[string]bool{}
	for _, is := range issues {
		onBoard[is.Slug] = true
	}
	for _, s := range texts {
		for _, ref := range deadRefRe.FindAllString(s, -1) {
			if !onBoard[ref] {
				t.Errorf("%s: names %s, which is not on the board", s, ref)
			}
		}
	}
}
