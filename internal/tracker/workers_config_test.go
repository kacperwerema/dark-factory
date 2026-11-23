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
	if got := wf.WorkerToolsDropped(); !slices.Equal(got, WorkerToolsOff[1:]) {
		t.Errorf("WorkerToolsDropped = %v", got)
	}
	if got := (&WorkflowConfig{Workers: &WorkersConfig{Tools: []string{"Workflow", "Bash"}}}).WorkersProblems(); len(got) != 1 || !strings.Contains(got[0], `"Bash"`) {
		t.Errorf("WorkersProblems = %v", got)
	}
	out, err := yaml.Marshal(wf.Workers.clone())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "- stageshow\n") || !strings.Contains(string(out), "skill: anthropic-skills:pdf") {
		t.Errorf("marshal:\n%s", out)
	}
}

// workers: nice: is the nice level a worker's session starts at; unset is 0
// (no change), and lint names a level outside 0..19.
func TestWorkersNiceLevel(t *testing.T) {
	for _, c := range []struct {
		raw     string
		want    int
		problem bool
	}{
		{"statuses: []\n", 0, false},
		{"workers:\n  setup: lean\n", 0, false},
		{"workers:\n  nice: 10\n", 10, false},
		{"workers:\n  nice: 0\n", 0, false},
		{"workers:\n  nice: 25\n", 0, true},
		{"workers:\n  nice: -5\n", 0, true},
	} {
		var wf WorkflowConfig
		if err := yaml.Unmarshal([]byte(c.raw), &wf); err != nil {
			t.Fatal(err)
		}
		if got := wf.WorkerNice(); got != c.want {
			t.Errorf("%q: WorkerNice() = %d, want %d", c.raw, got, c.want)
		}
		probs := wf.WorkersProblems()
		named := slices.ContainsFunc(probs, func(p string) bool { return strings.Contains(p, "nice") })
		if named != c.problem {
			t.Errorf("%q: WorkersProblems() = %q, want a nice problem: %v", c.raw, probs, c.problem)
		}
	}
	// A clone keeps the level and does not share it.
	n := 7
	w := (&WorkersConfig{Nice: &n}).clone()
	n = 3
	if w.Nice == nil || *w.Nice != 7 {
		t.Errorf("clone shares Nice with its source: %v", w.Nice)
	}
}
