package tracker

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWaitingOnGate(t *testing.T) {
	var wf WorkflowConfig
	if err := yaml.Unmarshal([]byte(`statuses:
  - name: "in design"
  - name: "backlog"
  - name: "done"
transitions:
  - from: "in design"
    to: "backlog"
    actions:
      - type: "validate"
        rule: "section_checkboxes_checked: Design"
      - type: "require_human_approval"
        status: "backlog"
`), &wf); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		issue *Issue
		want  string
	}{
		{"work done, approval missing", &Issue{Status: "in design", BodyRaw: "## Design\n- [x] Approach"}, "backlog"},
		{"work left: a worker still has something to do", &Issue{Status: "in design", BodyRaw: "## Design\n- [ ] Approach"}, ""},
		{"already approved", &Issue{Status: "in design", HumanApproval: "backlog", BodyRaw: "## Design\n- [x] Approach"}, ""},
		{"no gate on the next transition", &Issue{Status: "backlog"}, ""},
		{"last status", &Issue{Status: "done"}, ""},
	} {
		if got := WaitingOnGate(&wf, tc.issue, nil); got != tc.want {
			t.Errorf("%s: WaitingOnGate = %q, want %q", tc.name, got, tc.want)
		}
	}
	if next, gate := NextGate(&wf, &Issue{Status: "in design"}); next != "backlog" || gate != "backlog" {
		t.Errorf("NextGate = %q, %q", next, gate)
	}
}

// An owner box the move into the status appended (Human Testing's HT1, a
// box marked "(owner)") is not the worker's work: an issue whose next gate
// waits only on the owner (and that box) waits on the owner. On 15.11, in another project:
// a human-testing issue with HT1 unticked and an open yolo-owner-check was
// on no Needs you list and in no "Waiting on you".
func TestWaitingOnGateIgnoresOwnerBoxes(t *testing.T) {
	var wf WorkflowConfig
	if err := yaml.Unmarshal([]byte(`statuses:
  - name: "testing"
  - name: "human-testing"
  - name: "documentation"
transitions:
  - from: "testing"
    to: "human-testing"
    actions:
      - type: "append_section"
        title: "Human Testing"
        body: "- [ ] Human verification complete if required"
      - type: "append_section"
        title: "Manual"
        body: "- [ ] (owner) Manual repro\n- [ ] Logs attached"
  - from: "human-testing"
    to: "documentation"
    actions:
      - type: "require_human_approval"
        status: "documentation"
`), &wf); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body, want string
	}{
		{"only owner boxes open", "## Human Testing\n- [ ] Human verification complete if required\n\n## Manual\n- [ ] (owner) Manual repro\n- [x] Logs attached", "documentation"},
		{"a worker box still open", "## Human Testing\n- [ ] Human verification complete if required\n\n## Manual\n- [ ] (owner) Manual repro\n- [ ] Logs attached", ""},
	} {
		issue := &Issue{Status: "human-testing", BodyRaw: tc.body}
		if got := WaitingOnGate(&wf, issue, nil); got != tc.want {
			t.Errorf("%s: WaitingOnGate = %q, want %q", tc.name, got, tc.want)
		}
	}
}
