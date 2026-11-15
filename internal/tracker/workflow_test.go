package tracker

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadWorkflow(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "workflow.yaml")

	content := `statuses:
  - name: "todo"
    description: "To do"
    prompt: "Clarify the work before starting"
  - name: "doing"
    description: "In progress"
  - name: "done"
    description: "Complete"
transitions:
  - from: "todo"
    to: "doing"
    actions:
      - type: validate
        rule: body_not_empty
systems:
  Combat:
    transitions:
      - from: "doing"
        to: "done"
        actions:
          - type: inject_prompt
            prompt: "Combat-specific guidance"
`
	os.WriteFile(fp, []byte(content), 0644)

	wf, err := LoadWorkflow(fp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(wf.Statuses) != 3 {
		t.Fatalf("got %d statuses, want 3", len(wf.Statuses))
	}

	if wf.Statuses[0].Name != "todo" {
		t.Errorf("first status = %q, want %q", wf.Statuses[0].Name, "todo")
	}
	if wf.Statuses[0].Prompt != "Clarify the work before starting" {
		t.Errorf("prompt = %q, want %q", wf.Statuses[0].Prompt, "Clarify the work before starting")
	}

	if len(wf.Transitions) != 1 {
		t.Fatalf("got %d transitions, want 1", len(wf.Transitions))
	}
	if wf.Transitions[0].Actions[0].Rule != "body_not_empty" {
		t.Errorf("rule = %q, want %q", wf.Transitions[0].Actions[0].Rule, "body_not_empty")
	}
	if _, ok := wf.Systems["Combat"]; !ok {
		t.Fatal("expected Combat system overlay")
	}
}

func TestEntryPrompts(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "backlog"},
			{Name: "in progress", Prompt: "Implement according to the accepted design."},
		},
		Transitions: []WorkflowTransition{
			{
				From: "backlog",
				To:   "in progress",
				Actions: []WorkflowAction{
					{Type: "inject_prompt", Prompt: "Update the Implementation section while coding."},
				},
			},
		},
	}

	prompts := wf.EntryPrompts("backlog", "in progress")
	if len(prompts) != 2 {
		t.Fatalf("got %d prompts, want 2", len(prompts))
	}
	if prompts[0] != "Implement according to the accepted design." {
		t.Fatalf("status prompt = %q", prompts[0])
	}
	if prompts[1] != "Update the Implementation section while coding." {
		t.Fatalf("transition prompt = %q", prompts[1])
	}
}

func TestGetBoardColumns_ConfiguredReturnsInOrder(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "idea"},
			{Name: "backlog"},
			{Name: "in progress"},
			{Name: "done"},
		},
		Board: WorkflowBoardConfig{
			Columns: []string{"in progress", "backlog", "done"},
		},
	}
	got := wf.GetBoardColumns()
	want := []string{"in progress", "backlog", "done"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGetBoardColumns_FallsBackToAllStatuses(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "idea"},
			{Name: "backlog"},
			{Name: "done"},
		},
	}
	got := wf.GetBoardColumns()
	want := []string{"idea", "backlog", "done"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGetBoardCardFields_ConfiguredReturnsInOrder(t *testing.T) {
	wf := &WorkflowConfig{
		Board: WorkflowBoardConfig{
			CardFields: []string{"priority", "system", "labels"},
		},
	}
	got := wf.GetBoardCardFields()
	want := []string{"priority", "system", "labels"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGetBoardCardFields_FallsBackToDefault(t *testing.T) {
	wf := &WorkflowConfig{}
	got := wf.GetBoardCardFields()
	want := defaultBoardCardFields
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoadWorkflow_InvalidFile(t *testing.T) {
	_, err := LoadWorkflow("/nonexistent/path/workflow.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestWorkflowSchemaSections_EveryYAMLFieldHasDescTag(t *testing.T) {
	// Drift guard: every yaml-tagged field on any workflow struct must also
	// carry a `desc:"..."` tag. WorkflowSchemaSections() is what
	// `issue-cli process schema` prints, so a missing desc tag = a field
	// that silently ships with no agent-visible documentation.
	types := []reflect.Type{
		reflect.TypeOf(WorkflowConfig{}),
		reflect.TypeOf(WorkflowStatus{}),
		reflect.TypeOf(WorkflowTransition{}),
		reflect.TypeOf(WorkflowAction{}),
		reflect.TypeOf(WorkflowBoardConfig{}),
		reflect.TypeOf(WorkflowOverlay{}),
	}
	for _, tp := range types {
		for i := 0; i < tp.NumField(); i++ {
			f := tp.Field(i)
			yamlTag := f.Tag.Get("yaml")
			if yamlTag == "" || yamlTag == "-" {
				continue
			}
			if f.Tag.Get("desc") == "" {
				t.Errorf("%s.%s has yaml tag %q but no desc tag", tp.Name(), f.Name, yamlTag)
			}
		}
	}
}

func TestWorkflowActionTypes_CoverAllHandledTypes(t *testing.T) {
	// Every action.Type the preview switch handles must appear in
	// WorkflowActionTypes so the schema command can document it.
	handled := []string{
		"validate",
		"require_human_approval",
		"append_section",
		"inject_prompt",
		"set_fields",
	}
	names := map[string]bool{}
	for _, a := range WorkflowActionTypes {
		names[a.Name] = true
	}
	for _, want := range handled {
		if !names[want] {
			t.Errorf("WorkflowActionTypes missing entry for %q", want)
		}
	}
}

// TestWorkflowConfigCloneIsolatesScoringAndBoard guards against the shallow-copy
// aliasing bug in WorkflowConfig.Clone. Mutating the clone's Scoring maps or
// Board slices must not affect the original, otherwise per-system overlays
// produced by ForSystem would leak edits across requests.
func TestWorkflowConfigCloneIsolatesScoringAndBoard(t *testing.T) {
	original := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "idea"}},
		Board: WorkflowBoardConfig{
			CardFields: []string{"system", "labels"},
			Columns:    []string{"idea", "done"},
		},
		Scoring: ScoringConfig{
			Enabled: true,
			Formula: ScoringFormula{
				Priority: ScoringPriority{"high": 20, "critical": 40},
				Labels:   ScoringLabels{"bug": 5, "regression": 8},
			},
		},
	}

	clone := original.Clone()

	clone.Scoring.Formula.Priority["high"] = 999
	clone.Scoring.Formula.Priority["new-key"] = 1
	clone.Scoring.Formula.Labels["bug"] = 999
	clone.Scoring.Formula.Labels["new-label"] = 2
	// Index-write before append so a shallow-copied slice header would write
	// through to the original's backing array. Append-then-write would
	// reallocate and mask the alias.
	clone.Board.CardFields[0] = "MUTATED"
	clone.Board.CardFields = append(clone.Board.CardFields, "version")
	clone.Board.Columns[0] = "MUTATED"
	clone.Board.Columns = append(clone.Board.Columns, "extra")

	if got := original.Scoring.Formula.Priority["high"]; got != 20 {
		t.Errorf("original Scoring.Formula.Priority[high] aliased: got %v, want 20", got)
	}
	if _, leaked := original.Scoring.Formula.Priority["new-key"]; leaked {
		t.Error("original Scoring.Formula.Priority received clone's new key")
	}
	if got := original.Scoring.Formula.Labels["bug"]; got != 5 {
		t.Errorf("original Scoring.Formula.Labels[bug] aliased: got %v, want 5", got)
	}
	if _, leaked := original.Scoring.Formula.Labels["new-label"]; leaked {
		t.Error("original Scoring.Formula.Labels received clone's new key")
	}
	if got := original.Board.CardFields; len(got) != 2 || got[0] != "system" {
		t.Errorf("original Board.CardFields aliased: got %v, want [system labels]", got)
	}
	if got := original.Board.Columns; len(got) != 2 || got[0] != "idea" {
		t.Errorf("original Board.Columns aliased: got %v, want [idea done]", got)
	}
}

func TestGetAction_ResolvesByID(t *testing.T) {
	wf := &WorkflowConfig{Actions: []CustomAction{
		{ID: "defer-to-team", Label: "Defer to team", Prompt: "p"},
		{ID: "escalate", Label: "Escalate", Prompt: "q"},
	}}
	if got := wf.GetAction("escalate"); got == nil || got.Label != "Escalate" {
		t.Fatalf("GetAction(escalate) = %+v, want the escalate action", got)
	}
	if got := wf.GetAction("missing"); got != nil {
		t.Fatalf("GetAction(missing) = %+v, want nil", got)
	}
	var nilWF *WorkflowConfig
	if got := nilWF.GetAction("any"); got != nil {
		t.Fatalf("GetAction on nil config = %+v, want nil", got)
	}
}

// TestIssueActionList_MergesLegacyAlias verifies the backward-compatible rename:
// issue_actions is canonical, the legacy actions field is still honored, and on
// id clash the canonical entry wins.
func TestIssueActionList_MergesLegacyAlias(t *testing.T) {
	// Legacy-only config still resolves.
	legacy := &WorkflowConfig{Actions: []CustomAction{{ID: "defer", Label: "Defer (old)", Prompt: "p"}}}
	if got := legacy.GetAction("defer"); got == nil || got.Label != "Defer (old)" {
		t.Fatalf("legacy actions alias not honored: %+v", got)
	}

	// Canonical-only config resolves.
	canonical := &WorkflowConfig{IssueActions: []CustomAction{{ID: "defer", Label: "Defer (new)", Prompt: "p"}}}
	if got := canonical.GetAction("defer"); got == nil || got.Label != "Defer (new)" {
		t.Fatalf("issue_actions not honored: %+v", got)
	}

	// Both present: issue_actions take precedence on a clash, legacy-only ids merge in.
	both := &WorkflowConfig{
		IssueActions: []CustomAction{{ID: "defer", Label: "Defer (new)", Prompt: "p"}},
		Actions: []CustomAction{
			{ID: "defer", Label: "Defer (old)", Prompt: "p"},
			{ID: "escalate", Label: "Escalate", Prompt: "q"},
		},
	}
	list := both.IssueActionList()
	if len(list) != 2 {
		t.Fatalf("expected 2 merged actions, got %d: %+v", len(list), list)
	}
	if got := both.GetAction("defer"); got == nil || got.Label != "Defer (new)" {
		t.Fatalf("issue_actions should win on id clash, got %+v", got)
	}
	if got := both.GetAction("escalate"); got == nil {
		t.Fatalf("legacy-only action should still resolve when both lists present")
	}
}

// TestForSystemPreservesCustomActions guards the Clone() path: custom actions
// are a top-level (not per-system) list, so resolving a workflow for an issue's
// system — which clones — must carry them through. A manual struct rebuild in
// Clone() would silently drop a newly added field.
func TestForSystemPreservesCustomActions(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "in progress"}},
		Actions:  []CustomAction{{ID: "defer-to-team", Label: "Defer to team", Prompt: "p"}},
		Systems:  map[string]WorkflowOverlay{"API": {}},
	}
	merged := wf.ForSystem("API")
	if got := merged.GetAction("defer-to-team"); got == nil {
		t.Fatalf("custom action dropped by ForSystem/Clone; actions=%+v", merged.Actions)
	}
}
