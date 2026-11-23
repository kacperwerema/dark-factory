package agentrun

import (
	"slices"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A worker's pane command runs under nice -n <workers: nice:>, so the agent
// and everything it starts inherit the level; a lead, and a project without
// workers: nice:, start unchanged.
func TestWorkerSessionStartsAtWorkersNice(t *testing.T) {
	argv := []string{"/bin/zsh", "-lic", "claude"}
	ten := 10
	wf := &tracker.WorkflowConfig{Workers: &tracker.WorkersConfig{Nice: &ten}}

	got, step := WorkerNiceCommand(nil, Spec{Workflow: wf, IssueSlug: "x/y"}, argv)
	if len(got) != len(argv)+3 || got[1] != "-n" || got[2] != "10" || !slices.Equal(got[3:], argv) {
		t.Fatalf("worker with workers: nice: 10 starts %q, want nice -n 10 %q", got, argv)
	}
	if step == nil || step.Status != "ok" {
		t.Errorf("worker launch reports no priority step: %+v", step)
	}

	if got, step := WorkerNiceCommand(nil, Spec{Workflow: wf, LeadScope: "project"}, argv); !slices.Equal(got, argv) || step != nil {
		t.Errorf("lead starts %q (step %+v), want %q unchanged at nice 0", got, step, argv)
	}
	if got, step := WorkerNiceCommand(nil, Spec{Workflow: &tracker.WorkflowConfig{}}, argv); !slices.Equal(got, argv) || step != nil {
		t.Errorf("worker without workers: nice: starts %q (step %+v), want %q unchanged", got, step, argv)
	}
}
