package connections

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func gateHandoff(slug, status, to, forSlug string) *tracker.Issue {
	return &tracker.Issue{Slug: slug, Title: slug, Status: status, ExtraFields: []tracker.ExtraField{
		{Key: HandoffRouteField, Value: "product → " + to},
		{Key: HandoffForField, Value: forSlug},
	}}
}

// Only a done handoff to the named department, filed for the issue,
// answers handoff_answered; declined and parked ones are named as not an
// answer, and the hold says how to ask.
func TestAnsweredError(t *testing.T) {
	declined := gateHandoff("legal-declined", "parked", "legal", "work")
	comments := func(i *tracker.Issue) []tracker.Comment {
		if i == declined {
			return []tracker.Comment{{Text: "handoff: declined by lead-acme-legal: out of scope"}}
		}
		return nil
	}
	others := []*tracker.Issue{
		gateHandoff("marketing-done", "done", "marketing", "work"), // another department
		gateHandoff("legal-other", "done", "legal", "other"),       // another issue
		gateHandoff("legal-parked", "parked", "legal", "work"),
		declined,
	}
	err := AnsweredError(others, comments, "work", "legal", nil)
	if err == nil {
		t.Fatal("answered by another department's or issue's handoff")
	}
	for _, w := range []string{"Not an answer: legal-declined (declined), legal-parked (parked)", "issue-cli handoff legal --for work"} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("hold lacks %q:\n%s", w, err)
		}
	}
	if strings.Contains(err.Error(), "the branch touches") {
		t.Fatalf("no paths, yet the hold names touched files:\n%s", err)
	}

	// A declined handoff whose issue was closed as done doesn't count either.
	declined.Status = "done"
	if err := AnsweredError(others, comments, "work", "legal", nil); err == nil {
		t.Fatal("a declined handoff answered the gate")
	}

	// Through HQ: the route's last box is the department.
	via := gateHandoff("legal-via-hq", "done", "hq → legal", "work")
	if err := AnsweredError(append(others, via), comments, "work", "legal", nil); err != nil {
		t.Fatalf("done handoff via HQ: %v", err)
	}
}
