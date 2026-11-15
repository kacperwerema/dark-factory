package tracker

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
	"gopkg.in/yaml.v3"
)

// TestDefaultWorkflow: the built-in default is the bundled template, whole.
// A hand-kept copy drifted from it (audit 1.4).
func TestDefaultWorkflow(t *testing.T) {
	data, err := templates.FS.ReadFile(defaultWorkflowPath)
	if err != nil {
		t.Fatal(err)
	}
	var want WorkflowConfig
	if err := yaml.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	parsed := want.Clone() // what every caller of a parsed file gets
	got := DefaultWorkflow()
	if !reflect.DeepEqual(got, parsed) {
		t.Fatalf("DefaultWorkflow() differs from %s:\ngot  %+v\nwant %+v", defaultWorkflowPath, got, parsed)
	}
	if order := got.GetStatusOrder(); order[0] != "idea" || order[len(order)-1] != "done" {
		t.Errorf("order = %v", order)
	}

	// Each call is the caller's own: overlays and attachRuntime write to it.
	got.Statuses[0].Name = "changed"
	got.Transitions[0].Actions = nil
	if again := DefaultWorkflow(); !reflect.DeepEqual(again, parsed) {
		t.Fatal("a change to one DefaultWorkflow() reached the next")
	}
}

// attachedDefault is the default workflow as a project hands it out: its
// decision rules need the runtime attachRuntime gives them.
func attachedDefault(t *testing.T) *WorkflowConfig {
	t.Helper()
	wf := DefaultWorkflow()
	(&Project{IssueDir: t.TempDir()}).attachRuntime(wf)
	return wf
}

func TestGetStatusOrder(t *testing.T) {
	wf := DefaultWorkflow()
	order := wf.GetStatusOrder()

	expected := []string{"idea", "in design", "backlog", "in progress", "testing", "human-testing", "documentation", "shipping", "done"}
	if len(order) != len(expected) {
		t.Fatalf("got %d statuses, want %d", len(order), len(expected))
	}
	for i, s := range expected {
		if order[i] != s {
			t.Errorf("order[%d] = %q, want %q", i, order[i], s)
		}
	}
}

func TestGetStatusIndex(t *testing.T) {
	wf := DefaultWorkflow()

	tests := []struct {
		status string
		want   int
	}{
		{"idea", 0},
		{"in design", 1},
		{"human-testing", 5},
		{"shipping", 7},
		{"done", 8},
		{"none", -1},
		{"unknown", -1},
	}

	for _, tt := range tests {
		got := wf.GetStatusIndex(tt.status)
		if got != tt.want {
			t.Errorf("GetStatusIndex(%q) = %d, want %d", tt.status, got, tt.want)
		}
	}
}

func TestIsValidTransition(t *testing.T) {
	wf := DefaultWorkflow()

	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"idea", "in design", true},
		{"in progress", "testing", true},
		{"idea", "done", false},    // skip
		{"done", "idea", false},    // backwards
		{"unknown", "idea", false}, // unknown from
		{"idea", "unknown", false}, // unknown to
		{"idea", "idea", false},    // same
		{"none", "idea", false},    // none no longer exists
		{"testing", "human-testing", true},
		{"human-testing", "documentation", true},
		{"documentation", "shipping", true},
		{"shipping", "done", true},
		{"documentation", "done", false}, // shipping must come first
	}

	for _, tt := range tests {
		got := wf.IsValidTransition(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("IsValidTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestIsValidTransition_HonorsYAMLBackwardEdge(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "in-review"},
			{Name: "waiting-for-team-input"},
			{Name: "approved"},
		},
		Transitions: []WorkflowTransition{
			{From: "in-review", To: "waiting-for-team-input"},
			{From: "waiting-for-team-input", To: "in-review"},
			{From: "in-review", To: "approved"},
		},
	}

	if !wf.IsValidTransition("waiting-for-team-input", "in-review") {
		t.Errorf("backward edge declared in YAML should be valid")
	}
	if !wf.IsValidTransition("in-review", "waiting-for-team-input") {
		t.Errorf("forward edge declared in YAML should be valid")
	}
	if !wf.IsValidTransition("in-review", "approved") {
		t.Errorf("index-skipping forward edge declared in YAML should be valid")
	}
	if wf.IsValidTransition("approved", "in-review") {
		t.Errorf("undeclared backward edge should remain invalid")
	}
}

func TestIsValidTransition_SkipsOptionalStatuses(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "in-review"},
			{Name: "waiting-for-team-input", Optional: true},
			{Name: "approve-comment-created"},
			{Name: "approved"},
		},
	}

	if !wf.IsValidTransition("in-review", "approve-comment-created") {
		t.Errorf("forward jump across an optional status should be valid")
	}
	if !wf.IsValidTransition("in-review", "waiting-for-team-input") {
		t.Errorf("forward step into an optional status should still be valid")
	}
	if !wf.IsValidTransition("waiting-for-team-input", "approve-comment-created") {
		t.Errorf("stepping out of an optional status should be valid")
	}
	if wf.IsValidTransition("in-review", "approved") {
		t.Errorf("skipping a required status must not be allowed")
	}
}

func TestIsValidTransition_MultipleOptionalInARow(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "a"},
			{Name: "b", Optional: true},
			{Name: "c", Optional: true},
			{Name: "d"},
		},
	}
	if !wf.IsValidTransition("a", "d") {
		t.Errorf("jumping across two consecutive optional statuses should be valid")
	}
}

func TestNextRequiredStatus(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "a"},
			{Name: "b", Optional: true},
			{Name: "c", Optional: true},
			{Name: "d"},
		},
	}

	if got := wf.NextRequiredStatus("a"); got != "d" {
		t.Errorf("NextRequiredStatus(a) = %q, want d", got)
	}
	if got := wf.NextRequiredStatus("b"); got != "d" {
		t.Errorf("NextRequiredStatus(b) = %q, want d", got)
	}
	if got := wf.NextRequiredStatus("d"); got != "" {
		t.Errorf("NextRequiredStatus(d) = %q, want empty", got)
	}
	if got := wf.NextRequiredStatus("unknown"); got != "" {
		t.Errorf("NextRequiredStatus(unknown) = %q, want empty", got)
	}
}

func TestDefaultNextStatus(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "a"},
			{Name: "b", Optional: true},
			{Name: "c", Optional: true},
			{Name: "d"},
			{Name: "e", Optional: true},
		},
	}

	required, optionals := wf.DefaultNextStatus("a")
	if required != "d" {
		t.Errorf("DefaultNextStatus(a) required = %q, want d", required)
	}
	if len(optionals) != 2 || optionals[0] != "b" || optionals[1] != "c" {
		t.Errorf("DefaultNextStatus(a) optionals = %v, want [b c]", optionals)
	}

	required, optionals = wf.DefaultNextStatus("b")
	if required != "d" {
		t.Errorf("DefaultNextStatus(b) required = %q, want d", required)
	}
	if len(optionals) != 1 || optionals[0] != "c" {
		t.Errorf("DefaultNextStatus(b) optionals = %v, want [c]", optionals)
	}

	// When only optional statuses remain, required is empty and all are returned so
	// callers can render them as alternatives rather than silently pick one.
	required, optionals = wf.DefaultNextStatus("d")
	if required != "" {
		t.Errorf("DefaultNextStatus(d) required = %q, want empty", required)
	}
	if len(optionals) != 1 || optionals[0] != "e" {
		t.Errorf("DefaultNextStatus(d) optionals = %v, want [e]", optionals)
	}

	required, optionals = wf.DefaultNextStatus("e")
	if required != "" || len(optionals) != 0 {
		t.Errorf("DefaultNextStatus(e) = (%q, %v), want (\"\", nil)", required, optionals)
	}

	required, optionals = wf.DefaultNextStatus("unknown")
	if required != "" || len(optionals) != 0 {
		t.Errorf("DefaultNextStatus(unknown) = (%q, %v), want (\"\", nil)", required, optionals)
	}
}

func TestIsSideStatus(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "done"},
			{Name: "parked", Optional: true, Global: true},
			{Name: "blocked", Optional: true},
			{Name: "archived", Optional: true},
			{Name: "review", Optional: true},
		},
		Transitions: []WorkflowTransition{
			{From: "*", To: "blocked"},
			{From: "done", To: "archived"},
			{From: "*", To: "archived"},
		},
	}
	for name, want := range map[string]bool{
		"done":     false, // not optional
		"parked":   true,  // optional + global
		"blocked":  true,  // optional, reached only by "*"
		"archived": false, // has its own lifecycle edge
		"review":   false, // optional, no edges: plain ordering
		"unknown":  false,
	} {
		if got := wf.IsSideStatus(name); got != want {
			t.Errorf("IsSideStatus(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMerge_PropagatesOptional(t *testing.T) {
	base := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "a"},
			{Name: "b"},
		},
	}
	overlay := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "b", Optional: true},
		},
	}
	base.Merge(overlay)
	if got := base.GetStatus("b"); got == nil || !got.Optional {
		t.Errorf("merged status b should be Optional=true, got %+v", got)
	}
}

func TestIsValidTransition_FallsBackToLinearWhenNoExplicitEdge(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "a"},
			{Name: "b"},
			{Name: "c"},
		},
	}

	if !wf.IsValidTransition("a", "b") {
		t.Errorf("adjacent transition should be valid via linear fallback")
	}
	if wf.IsValidTransition("a", "c") {
		t.Errorf("skip transition should be invalid without explicit edge")
	}
	if wf.IsValidTransition("b", "a") {
		t.Errorf("backward transition should be invalid without explicit edge")
	}
}

func TestGetStatusDescriptions(t *testing.T) {
	wf := DefaultWorkflow()
	descs := wf.GetStatusDescriptions()

	if _, ok := descs["none"]; ok {
		t.Errorf("none should not be in descriptions")
	}
	if descs["backlog"] != "Ready to work on" {
		t.Errorf("backlog description = %q", descs["backlog"])
	}
	if descs["human-testing"] != "Manual verification by humans" {
		t.Errorf("human-testing description = %q", descs["human-testing"])
	}
}

func TestDefaultWorkflowPromptsAndApprovals(t *testing.T) {
	wf := DefaultWorkflow()

	if got := wf.StatusPrompt("backlog"); !strings.Contains(got, "This is a handoff state") {
		t.Fatalf("backlog prompt = %q, want start approval guidance", got)
	}

	if got := wf.RequiredHumanApproval("backlog", "in progress"); got != "in progress" {
		t.Fatalf("RequiredHumanApproval(backlog, in progress) = %q, want in progress", got)
	}

	if got := wf.RequiredHumanApproval("in progress", "testing"); got != "" {
		t.Fatalf("RequiredHumanApproval(in progress, testing) = %q, want empty", got)
	}
}

func TestNextStatus(t *testing.T) {
	wf := DefaultWorkflow()

	tests := []struct {
		current string
		want    string
	}{
		{"idea", "in design"},
		{"testing", "human-testing"},
		{"human-testing", "documentation"},
		{"documentation", "shipping"},
		{"shipping", "done"},
		{"done", ""},
		{"none", ""},
		{"unknown", ""},
	}

	for _, tt := range tests {
		got := wf.NextStatus(tt.current)
		if got != tt.want {
			t.Errorf("NextStatus(%q) = %q, want %q", tt.current, got, tt.want)
		}
	}
}
