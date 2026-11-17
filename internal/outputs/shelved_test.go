package outputs

import (
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Shelved names the newest good output of the gate's kind once the issue is
// at or past its has_output gate; before the gate, parked, rejected, gone,
// another kind or a path without the gate name none.
func TestShelved(t *testing.T) {
	wf := &tracker.WorkflowConfig{
		Statuses: []tracker.WorkflowStatus{{Name: "idea"}, {Name: "in progress"}, {Name: "human-testing"}, {Name: "done"}},
		Transitions: []tracker.WorkflowTransition{
			{From: "in progress", To: "human-testing", Actions: []tracker.WorkflowAction{{Type: "validate", Rule: "has_output: report"}}},
		},
	}
	day := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	list := []*Output{
		{ID: "o-old", Issue: "r", Kind: "report", State: StateReview, Added: day},
		{ID: "o-new", Issue: "r", Kind: "report", State: StateApproved, Added: day.Add(time.Hour)},
		{ID: "o-rej", Issue: "r", Kind: "report", State: StateRejected, Added: day.Add(2 * time.Hour)},
		{ID: "o-gone", Issue: "r", Kind: "report", State: StateReview, Missing: true, Added: day.Add(3 * time.Hour)},
		{ID: "o-post", Issue: "r", Kind: "post", State: StateReview, Added: day.Add(4 * time.Hour)},
		{ID: "o-bad", Issue: "bad", Kind: "report", State: StateRejected, Added: day},
	}
	for _, c := range []struct {
		slug, status string
		wf           *tracker.WorkflowConfig
		want         string
	}{
		{"r", "human-testing", wf, "o-new"},
		{"r", "done", wf, "o-new"},
		{"r", "in progress", wf, ""},
		{"r", "parked", wf, ""},
		{"bad", "human-testing", wf, ""},
		{"none", "human-testing", wf, ""},
		{"r", "human-testing", &tracker.WorkflowConfig{Statuses: wf.Statuses}, ""},
		{"r", "human-testing", nil, ""},
	} {
		got := ""
		if o := Shelved(list, c.wf, &tracker.Issue{Slug: c.slug, Status: c.status}); o != nil {
			got = o.ID
		}
		if got != c.want {
			t.Errorf("Shelved(%s at %s) = %q, want %q", c.slug, c.status, got, c.want)
		}
	}
}
