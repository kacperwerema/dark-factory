package briefing

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The lead's check-in is one command, named with its --watch form and the
// project injected; the brief names the one way to wait and its cost, and the queue line says the tick runs queue next.
func TestLeadBriefNamesLeadTick(t *testing.T) {
	proj := &tracker.Project{Name: "P", Slug: "p"}
	brief := LeadBrief(&tracker.WorkflowConfig{}, LeadBriefInput{Project: proj, Root: t.TempDir()})
	for _, want := range []string{
		"- Tick: your 5-minute check-in is issue-cli --project p lead tick.",
		"- Waiting: you wait in one way only, a background shell that exits on news",
		"issue-cli --project p lead tick --watch --exit-on-news (add --observe",
		"issue-cli --project p lead watch --run '<cmd>'",
		"Never a /loop cron, a ScheduleWakeup poll or a Monitor",
		"476k-960k tokens per wake",
		"lead context <n>k: rotate, run issue-cli --project p lead rotate and end your turn",
		"issue-cli --project p lead state note",
		"issue-cli --project p lead history <words>",
		"Each tick runs issue-cli --project p queue next --start",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief missing %q", want)
		}
	}
}

// A department lead's brief names its read-only wait for its own
// department, with the project injected.
func TestDepartmentLeadBriefNamesObserveWait(t *testing.T) {
	proj := &tracker.Project{Name: "P", Slug: "p", Departments: []tracker.Department{{Name: "Marketing"}}}
	brief := DepartmentLeadBrief(nil, &proj.Departments[0], LeadBriefInput{Project: proj, Root: t.TempDir()})
	for _, want := range []string{
		"issue-cli --project p lead tick --watch --exit-on-news --observe --department marketing",
		"issue-cli --project p lead watch --run '<cmd>'",
		"Never a /loop cron, a ScheduleWakeup poll or a Monitor",
		"issue-cli --project p lead rotate",
		"issue-cli --project p lead state",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief missing %q", want)
		}
	}
}
