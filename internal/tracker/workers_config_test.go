package tracker

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workers: skills: items are a bare name (every worker) or a map matching
// systems/types/labels like a workers: mcp: item; both round-trip.
func TestWorkerSkillsForTypes(t *testing.T) {
	raw := `workers:
  skills:
    - stageshow
    - {skill: "anthropic-skills:pdf", types: [report]}
    - {skill: dataviz, labels: [charts]}
  tools: [Artifact]
`
	var wf WorkflowConfig
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		issue *Issue
		typ   string
		want  []string
	}{
		{&Issue{}, "feature", []string{"stageshow"}},
		{&Issue{}, "Report", []string{"stageshow", "anthropic-skills:pdf"}},
		{&Issue{Labels: []string{"charts"}}, "feature", []string{"stageshow", "dataviz"}},
		{nil, "", []string{"stageshow"}},
	} {
		if got := wf.WorkerSkillsFor(c.issue, c.typ); !slices.Equal(got, c.want) {
			t.Errorf("%s %v: %v, want %v", c.typ, c.issue, got, c.want)
		}
	}
	if !wf.WorkerKeepsTool(WorkerToolArtifact) || (&WorkflowConfig{}).WorkerKeepsTool(WorkerToolArtifact) {
		t.Error("WorkerKeepsTool")
	}
	out, err := yaml.Marshal(wf.Workers.clone())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "- stageshow\n") || !strings.Contains(string(out), "skill: anthropic-skills:pdf") {
		t.Errorf("marshal:\n%s", out)
	}
}
