package tracker

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	wf := attachedDefault(t)

	t.Run("body_not_empty passes", func(t *testing.T) {
		issue := &Issue{BodyRaw: "Some content"}
		err := wf.Validate(issue, "in design", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("body_not_empty fails", func(t *testing.T) {
		issue := &Issue{BodyRaw: ""}
		err := wf.Validate(issue, "in design", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("design and acceptance criteria gates pass", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Design\n- [x] Approach documented\n- [x] Dependencies and risks identified\n- [x] Human approval requested for backlog\n\n## Acceptance Criteria\n- [ ] task 1", HumanApproval: "backlog"}
		err := wf.Validate(issue, "backlog", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("design section blocks backlog when unchecked", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Design\n- [x] Approach documented\n- [ ] Dependencies and risks identified\n- [x] Human approval requested for backlog\n\n## Acceptance Criteria\n- [ ] task 1", HumanApproval: "backlog"}
		err := wf.Validate(issue, "backlog", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("acceptance criteria section required for backlog", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Design\n- [x] Approach documented\n- [x] Dependencies and risks identified\n- [x] Human approval requested for backlog", HumanApproval: "backlog"}
		err := wf.Validate(issue, "backlog", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("human_approval blocks backlog", func(t *testing.T) {
		issue := &Issue{BodyRaw: "- [ ] task 1"}
		err := wf.Validate(issue, "backlog", nil)
		if err == nil {
			t.Fatal("expected error for missing approval")
		}
	})

	t.Run("human_approval wrong status", func(t *testing.T) {
		issue := &Issue{BodyRaw: "- [ ] task 1", HumanApproval: "testing"}
		err := wf.Validate(issue, "backlog", nil)
		if err == nil {
			t.Fatal("expected error for wrong approval status")
		}
	})

	t.Run("has_assignee passes", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content", Assignee: "alice", HumanApproval: "in progress"}
		err := wf.Validate(issue, "in progress", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_assignee fails", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content", Assignee: ""}
		err := wf.Validate(issue, "in progress", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("implementation section gate ignores unrelated unchecked items", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Acceptance Criteria\n- [ ] still open by design\n\n## Implementation\n- [x] done 1\n- [x] done 2\n\n## Test Plan\n### Automated\nTests\n### Manual\nSteps"}
		err := wf.Validate(issue, "testing", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("implementation section gate fails", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Implementation\n- [x] done\n- [ ] not done\n\n## Test Plan\n### Automated\nTests\n### Manual\nSteps"}
		err := wf.Validate(issue, "testing", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("testing requires test plan before entering testing", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Implementation\n- [x] done"}
		err := wf.Validate(issue, "testing", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("section_checkboxes_checked passes", func(t *testing.T) {
		sectionWf := &WorkflowConfig{
			Statuses: []WorkflowStatus{
				{Name: "testing", Validation: []string{"section_checkboxes_checked: Implementation"}},
			},
		}
		issue := &Issue{
			Slug:    "test",
			BodyRaw: "## Implementation\n- [x] Code done\n- [x] Tests written\n\n## Testing\n- [ ] Not done yet",
		}
		err := sectionWf.Validate(issue, "testing", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("section_checkboxes_checked fails with unchecked", func(t *testing.T) {
		sectionWf := &WorkflowConfig{
			Statuses: []WorkflowStatus{
				{Name: "testing", Validation: []string{"section_checkboxes_checked: Implementation"}},
			},
		}
		issue := &Issue{
			Slug:    "test",
			BodyRaw: "## Implementation\n- [x] Code done\n- [ ] Tests written\n\n## Testing\n- [ ] Not done yet",
		}
		err := sectionWf.Validate(issue, "testing", nil)
		if err == nil {
			t.Fatal("expected error for unchecked section checkbox")
		}
	})

	t.Run("section_checkboxes_checked fails when gated section is missing", func(t *testing.T) {
		sectionWf := &WorkflowConfig{
			Statuses: []WorkflowStatus{
				{Name: "testing", Validation: []string{"section_checkboxes_checked: Implementation"}},
			},
		}
		issue := &Issue{
			Slug:    "test",
			BodyRaw: "## Other\n- [x] Done",
		}
		err := sectionWf.Validate(issue, "testing", nil)
		if err == nil {
			t.Fatal("expected error when the gated section is absent (must not pass vacuously)")
		}
		if !strings.Contains(err.Error(), "no checkboxes") {
			t.Errorf("error should explain the section has no checkboxes, got: %v", err)
		}
	})

	t.Run("section_checkboxes_checked fails when gated section has no checkboxes", func(t *testing.T) {
		sectionWf := &WorkflowConfig{
			Statuses: []WorkflowStatus{
				{Name: "testing", Validation: []string{"section_checkboxes_checked: Implementation"}},
			},
		}
		issue := &Issue{
			Slug:    "test",
			BodyRaw: "## Implementation\nprose only, no checkboxes here",
		}
		err := sectionWf.Validate(issue, "testing", nil)
		if err == nil {
			t.Fatal("expected error when the gated section has no checkboxes")
		}
	})

	t.Run("section_has_checkboxes passes", func(t *testing.T) {
		sectionWf := &WorkflowConfig{
			Statuses: []WorkflowStatus{
				{Name: "backlog", Validation: []string{"section_has_checkboxes: Acceptance Criteria"}},
			},
		}
		issue := &Issue{
			BodyRaw: "## Acceptance Criteria\n- [ ] observable behavior",
		}
		err := sectionWf.Validate(issue, "backlog", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("section_has_checkboxes fails", func(t *testing.T) {
		sectionWf := &WorkflowConfig{
			Statuses: []WorkflowStatus{
				{Name: "backlog", Validation: []string{"section_has_checkboxes: Acceptance Criteria"}},
			},
		}
		issue := &Issue{
			BodyRaw: "## Acceptance Criteria\nNo checklist",
		}
		err := sectionWf.Validate(issue, "backlog", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("has_test_plan passes for human-testing", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Test Plan\n### Automated\nTests\n### Manual\nSteps\n\n## Testing\n- [x] Relevant tests for changed code passing\n- [x] Known unrelated failures documented if full suite is red\n- [x] Test results logged as comment"}
		comments := []Comment{{Text: "tests: all pass"}}
		err := wf.Validate(issue, "human-testing", comments)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_test_plan fails for human-testing", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Testing\n- [x] done"}
		comments := []Comment{{Text: "tests: all pass"}}
		err := wf.Validate(issue, "human-testing", comments)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("has_comment_prefix tests: fails for human-testing", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Test Plan\n### Automated\nTests\n### Manual\nSteps\n\n## Testing\n- [x] Relevant tests for changed code passing\n- [x] Known unrelated failures documented if full suite is red\n- [x] Test results logged as comment"}
		comments := []Comment{{Text: "some other comment"}}
		err := wf.Validate(issue, "human-testing", comments)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("testing section must be complete for human-testing", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Test Plan\n### Automated\nTests\n### Manual\nSteps\n\n## Testing\n- [x] Relevant tests for changed code passing\n- [ ] Known unrelated failures documented if full suite is red"}
		comments := []Comment{{Text: "tests: all pass"}}
		err := wf.Validate(issue, "human-testing", comments)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("documentation requires approval", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content"}
		err := wf.Validate(issue, "documentation", nil)
		if err == nil {
			t.Fatal("expected error for missing approval")
		}
	})

	t.Run("documentation passes with approval", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content", HumanApproval: "documentation"}
		err := wf.Validate(issue, "documentation", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_comment_prefix docs: passes for shipping", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Documentation\n- [x] docs updated"}
		comments := []Comment{{Text: "docs: updated docs"}}
		err := wf.Validate(issue, "shipping", comments)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_comment_prefix docs: fails for shipping", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Documentation\n- [x] docs updated"}
		comments := []Comment{{Text: "some other comment"}}
		err := wf.Validate(issue, "shipping", comments)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("done requires approval", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Shipping\n- [x] committed\n- [x] pushed\n- [x] pr"}
		err := wf.Validate(issue, "done", nil)
		if err == nil {
			t.Fatal("expected error for missing approval")
		}
	})

	t.Run("done passes with approval and checked shipping", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Shipping\n- [x] committed\n- [x] pushed\n- [x] pr", HumanApproval: "done"}
		err := wf.Validate(issue, "done", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("unknown status", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content"}
		err := wf.Validate(issue, "nonexistent", nil)
		if err == nil {
			t.Fatal("expected error for unknown status")
		}
	})
}

func TestValidateTransition_WithActions(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "backlog"},
			{Name: "in progress"},
		},
		Transitions: []WorkflowTransition{
			{
				From: "backlog",
				To:   "in progress",
				Actions: []WorkflowAction{
					{Type: "validate", Rule: "has_assignee"},
					{Type: "require_human_approval", Status: "in progress"},
				},
			},
		},
	}

	err := wf.ValidateTransition(&Issue{Slug: "x", Assignee: "alice"}, "backlog", "in progress", nil)
	if err == nil {
		t.Fatal("expected missing approval to fail")
	}

	err = wf.ValidateTransition(&Issue{Slug: "x", Assignee: "alice", HumanApproval: "in progress"}, "backlog", "in progress", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateTransition_ReturnsApprovalMissingError pins the contract the CLI
// hint helper relies on: the validate path must surface a structured error so
// the top-level decorator can extract the required status and emit a deep link
// instead of a vague "approve in the viewer" message.
func TestValidateTransition_ReturnsApprovalMissingError(t *testing.T) {
	wf := DefaultWorkflow()
	issue := &Issue{Slug: "demo", BodyRaw: "- [ ] task", Assignee: "alice"}

	err := wf.ValidateTransition(issue, "backlog", "in progress", nil)
	if err == nil {
		t.Fatal("expected approval-missing error")
	}
	if !errors.Is(err, ErrApprovalMissing) {
		t.Fatalf("errors.Is(err, ErrApprovalMissing) = false; err = %v", err)
	}
	var approvalErr *ApprovalMissingError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("errors.As(err, *ApprovalMissingError) = false; err = %v", err)
	}
	if approvalErr.Required != "in progress" {
		t.Errorf("Required = %q, want %q", approvalErr.Required, "in progress")
	}
	if approvalErr.Slug != "demo" {
		t.Errorf("Slug = %q, want %q", approvalErr.Slug, "demo")
	}
	if !strings.Contains(err.Error(), `not human-approved for "in progress"`) {
		t.Errorf("error message lost back-compat phrasing: %q", err.Error())
	}
}
