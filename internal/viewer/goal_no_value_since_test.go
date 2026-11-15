package viewer

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/goals"
)

// A job-written goal a whole run late: the chip and the map's bar say "no
// value since <date>" instead of a number, muted, and name the job.
func TestGoalChipNoValueSince(t *testing.T) {
	target := 500.0
	g := &goals.Goal{Key: "sales/visits", ID: "visits", Name: "Visits", Target: &target,
		Date: "2026-10-12", NoValueSince: "2026-10-12", Job: "sales/weekly-funnel"}
	chip := goalChip(g, "#2BB673")
	if chip.Text != "no value since 2026-10-12" || !chip.Off {
		t.Errorf("chip = %+v", chip)
	}
	if !strings.Contains(chip.Title, "job sales/weekly-funnel missed a run") {
		t.Errorf("chip title = %q", chip.Title)
	}
	rep := &goals.Report{Departments: []*goals.Owner{{Key: "sales", Name: "Sales", Goals: []*goals.Goal{g}}}}
	if p := mapProgressOf(nil, rep, nil, "#2BB673"); p == nil || p.Text != "no value since 2026-10-12" {
		t.Errorf("map bar = %+v", p)
	}
}
