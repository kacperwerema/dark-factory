package jobs

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The repro: a weekly Thu 08:00 job registered Sat 2026-11-21 with
// since: 2026-11-21 is not due for W47 (due Thu 11-19); its first period is
// W48, due Thu 11-26.
func TestSinceHoldsPeriodsBeforeIt(t *testing.T) {
	sch := mustParse(t, tracker.Job{ID: "social-week", Every: "week", On: "thu", At: "08:00", Since: "2026-11-21"})
	sat := at("2026-11-21 11:21")
	if key, due := sch.Latest(sat); key != "" || !due.IsZero() {
		t.Fatalf("Latest before the first period = %q %s, want none", key, due)
	}
	if got := sch.Next(sat); !got.Equal(at("2026-11-26 08:00")) {
		t.Fatalf("Next = %s, want Thu 2026-11-26 08:00", got)
	}
	// Once W48 is due it is the latest; nothing before since: is missed.
	fri := at("2026-11-27 09:00")
	if key, _ := sch.Latest(fri); key != "2026-W48" {
		t.Fatalf("Latest after the first due time = %q, want 2026-W48", key)
	}
	if n := sch.Missed("2026-W40", fri); n != 0 {
		t.Fatalf("Missed counts %d periods before since:, want 0", n)
	}
	// A due time on the since: day itself counts.
	same := mustParse(t, tracker.Job{ID: "x", Every: "week", On: "thu", At: "08:00", Since: "2026-11-26"})
	if key, _ := same.Latest(fri); key != "2026-W48" {
		t.Fatalf("since: on the due day: Latest = %q, want 2026-W48", key)
	}
}

func TestSinceInEvaluate(t *testing.T) {
	p := project(t)
	p.Departments[0].Jobs[0].Since = "2026-11-02"
	s := evaluate(t, p, Input{State: &State{}, Memory: lowMemory()})
	if s.Due || s.Verdict != VerdictNone || s.Period != "" {
		t.Fatalf("before since: %+v", s)
	}
	if !s.Next.Equal(at("2026-11-02 07:00")) {
		t.Fatalf("next = %s, want Mon 2026-11-02 07:00", s.Next)
	}
}

func TestSinceRefusesABadDate(t *testing.T) {
	_, err := Parse(tracker.Job{ID: "x", Every: "day", Since: "21.11.2026"})
	if err == nil || !strings.Contains(err.Error(), "since:") {
		t.Fatalf("err = %v, want a since: error", err)
	}
}
