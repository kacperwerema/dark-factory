package bench

import (
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/tracker/statustest"
)

func TestDefaultStartFollowsTheWorkRole(t *testing.T) {
	work := statustest.Name(tracker.StatusRoleWork)
	stats := tracker.StatsStore{Transitions: []tracker.TransitionStat{{To: "first"}, {To: work}}}
	if got := defaultStart(statustest.Renamed(), stats); got != work {
		t.Fatalf("defaultStart = %q", got)
	}
	stats.Transitions = stats.Transitions[:1]
	if got := defaultStart(statustest.Renamed(), stats); got != "first" {
		t.Fatalf("never reached work: %q", got)
	}
}
