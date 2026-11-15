package tracker

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestApplyTransitionToFile_ErrorPointsAtRequiredNext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "1.md")
	body := "---\ntitle: \"Test\"\nstatus: \"a\"\n---\n\nbody\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "a"},
			{Name: "b", Optional: true},
			{Name: "c"},
			{Name: "d"},
		},
	}

	// Try to jump from a directly to d — c is required and cannot be skipped.
	_, _, err := wf.ApplyTransitionToFile(path, "d")
	if err == nil {
		t.Fatal("expected error when skipping a required status")
	}
	if !strings.Contains(err.Error(), "\"c\"") {
		t.Errorf("error should mention required next status %q, got: %v", "c", err)
	}
}

func TestApplyTransition(t *testing.T) {
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
					{Type: "append_section", Title: "Implementation", Body: "- [ ] Code complete"},
					{Type: "inject_prompt", Prompt: "Implement carefully"},
					{Type: "set_fields", Field: "assignee", Value: ""},
				},
			},
		},
	}

	issue := &Issue{BodyRaw: "Existing", HumanApproval: "in progress"}
	result := wf.ApplyTransition(issue, "backlog", "in progress")

	if result.Update.Status == nil || *result.Update.Status != "in progress" {
		t.Fatalf("status update = %#v", result.Update.Status)
	}
	if result.Update.Assignee == nil || *result.Update.Assignee != "" {
		t.Fatalf("assignee update = %#v", result.Update.Assignee)
	}
	if result.Update.HumanApproval == nil || *result.Update.HumanApproval != "" {
		t.Fatalf("human_approval update = %#v", result.Update.HumanApproval)
	}
	if result.Update.Body == nil || !strings.Contains(*result.Update.Body, "## Implementation") {
		t.Fatalf("body update missing implementation section: %#v", result.Update.Body)
	}
	if len(result.InjectedPrompts) != 1 || result.InjectedPrompts[0] != "Implement carefully" {
		t.Fatalf("unexpected prompts: %#v", result.InjectedPrompts)
	}
}

func TestApplyTransition_DoesNotDuplicateExistingSection(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "idea"},
			{Name: "in design"},
		},
		Transitions: []WorkflowTransition{
			{
				From: "idea",
				To:   "in design",
				Actions: []WorkflowAction{
					{Type: "append_section", Title: "Idea", Body: "- [ ] Problem described clearly"},
					{Type: "append_section", Title: "Design", Body: "- [ ] Acceptance criteria defined as checkboxes"},
				},
			},
			{
				From: "in design",
				To:   "backlog",
				Actions: []WorkflowAction{
					{Type: "validate", Rule: "section_checkboxes_checked: Design"},
					{Type: "validate", Rule: "section_checkboxes_checked: Idea"},
				},
			},
		},
	}

	body := "## Idea\n- [ ] Problem described clearly\n\n## Design\n- [ ] Existing design checklist"
	issue := &Issue{BodyRaw: body}
	result := wf.ApplyTransition(issue, "idea", "in design")

	// ## Idea already holds its box; ## Design exists but lacks the gate's
	// box, so only that box is added.
	want := "## Idea\n- [ ] Problem described clearly\n\n## Design\n- [ ] Existing design checklist\n- [ ] Acceptance criteria defined as checkboxes\n"
	if result.Update.Body == nil || *result.Update.Body != want {
		t.Fatalf("body update = %v, want %q", result.Update.Body, want)
	}
	wantSections := []SectionAppend{
		{Title: "Idea"},
		{Title: "Design", BoxesAdded: 1},
	}
	if !reflect.DeepEqual(result.Sections, wantSections) {
		t.Fatalf("sections = %+v, want %+v", result.Sections, wantSections)
	}
}

func TestApplyTransition_DeeperSameNameHeadingDoesNotBlockAppend(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "in progress"}, {Name: "testing"}},
		Transitions: []WorkflowTransition{{
			From: "in progress", To: "testing",
			Actions: []WorkflowAction{{Type: "append_section", Title: "Testing", Body: "- [ ] Relevant tests passing"}},
		}},
	}
	issue := &Issue{BodyRaw: "## Design\n### Testing\nUnit tests cover the parser."}
	result := wf.ApplyTransition(issue, "in progress", "testing")

	if total, _ := CountCheckboxesInSection(issue.BodyRaw, "Testing"); total != 1 {
		t.Fatalf("## Testing boxes = %d, want 1; body:\n%s", total, issue.BodyRaw)
	}
	if want := []SectionAppend{{Title: "Testing", Created: true, BoxesAdded: 1}}; !reflect.DeepEqual(result.Sections, want) {
		t.Fatalf("sections = %+v, want %+v", result.Sections, want)
	}
}

func TestApplyTransition_SecondRunIsIdempotent(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "idea"},
			{Name: "in design"},
		},
		Transitions: []WorkflowTransition{
			{
				From: "idea",
				To:   "in design",
				Actions: []WorkflowAction{
					{Type: "append_section", Title: "Idea", Body: "- [ ] Problem described clearly"},
					{Type: "append_section", Title: "Design", Body: "- [ ] Acceptance criteria defined as checkboxes"},
				},
			},
		},
	}

	issue := &Issue{BodyRaw: "Problem statement"}

	first := wf.ApplyTransition(issue, "idea", "in design")
	if !first.BodyChanged || first.Update.Body == nil {
		t.Fatalf("expected first transition to scaffold body, got %#v", first)
	}

	bodyAfterFirst := issue.BodyRaw
	second := wf.ApplyTransition(issue, "idea", "in design")

	if second.Update.Body != nil {
		t.Fatalf("expected second transition to skip body update, got %#v", *second.Update.Body)
	}
	if second.BodyChanged {
		t.Fatalf("expected second transition to leave body unchanged, got %#v", second)
	}
	if issue.BodyRaw != bodyAfterFirst {
		t.Fatalf("issue body changed on second run:\n%s", issue.BodyRaw)
	}
}

func TestApplyTransitionToFile_AppendsDocumentationWhenBodyHasFencedExample(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "fenced.md")
	content := "---\n" +
		"title: \"fenced example\"\n" +
		"status: \"human-testing\"\n" +
		"human_approval: \"documentation\"\n" +
		"assignee: \"agent-fenced\"\n" +
		"---\n" +
		"\n" +
		"The body shows what a Documentation section looks like:\n" +
		"\n" +
		"```markdown\n" +
		"## Documentation\n" +
		"- [ ] User-facing docs updated if behavior changed\n" +
		"```\n" +
		"\n" +
		"## Human Testing\n" +
		"- [x] Human verification complete if required\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	wf, err := LoadWorkflow(filepath.Join("..", "..", "workflow.yaml"))
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	_, result, err := wf.ApplyTransitionToFile(fp, "documentation")
	if err != nil {
		t.Fatalf("ApplyTransitionToFile: %v", err)
	}
	if !result.BodyAppended {
		t.Fatal("BodyAppended = false; the fenced example should not block the real append")
	}

	got, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	body := string(got)

	// The real `## Documentation` section must follow `## Human Testing`
	// (the last existing section), and both new checkboxes must be present.
	humanIdx := strings.Index(body, "## Human Testing")
	docsIdx := strings.LastIndex(body, "## Documentation")
	if humanIdx < 0 || docsIdx < humanIdx {
		t.Fatalf("real `## Documentation` heading not appended after `## Human Testing`:\n%s", body)
	}
	if !strings.Contains(body[docsIdx:], "- [ ] User-facing docs updated if behavior changed") {
		t.Fatalf("appended Documentation section missing first checkbox:\n%s", body)
	}
	if !strings.Contains(body[docsIdx:], "- [ ] Docs comment added") {
		t.Fatalf("appended Documentation section missing second checkbox:\n%s", body)
	}
}

func TestApplyTransitionToFile_HumanTestingToDocumentation_PersistsAppendedSection(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "14.md")
	content := `---
title: "sample"
status: "human-testing"
human_approval: "documentation"
assignee: "agent-sample"
---

Some prose.

## Implementation
- [x] Code changes complete

## Test Plan
### Automated
- [x] Automated verification recorded

### Manual
- [x] Manual verification listed if needed

## Testing
- [x] Relevant tests for changed code passing
- [x] Known unrelated failures documented if full suite is red
- [x] Test results logged as comment

## Human Testing
- [x] Human verification complete if required


<!-- issue-viewer-comments
{"id":1,"block":0,"date":"2026-04-28","text":"tests: ok","status":"open","source":"cli"}
-->`
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	// Use the project workflow.yaml so the test exercises the same definitions
	// agents hit at runtime, not just the hard-coded DefaultWorkflow.
	wf, err := LoadWorkflow(filepath.Join("..", "..", "workflow.yaml"))
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	from, result, err := wf.ApplyTransitionToFile(fp, "documentation")
	if err != nil {
		t.Fatalf("ApplyTransitionToFile: %v", err)
	}
	if from != "human-testing" {
		t.Fatalf("from = %q, want human-testing", from)
	}
	if !result.BodyAppended {
		t.Fatalf("BodyAppended = false, want true (Documentation should be appended)")
	}

	got, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	body := string(got)

	if !strings.Contains(body, "## Documentation") {
		t.Fatalf("on-disk body missing `## Documentation` heading after transition:\n%s", body)
	}
	if !strings.Contains(body, "- [ ] User-facing docs updated if behavior changed") {
		t.Fatalf("on-disk body missing first Documentation checkbox:\n%s", body)
	}
	if !strings.Contains(body, "- [ ] Docs comment added") {
		t.Fatalf("on-disk body missing second Documentation checkbox:\n%s", body)
	}

	// Comment block must survive the rewrite.
	if !strings.Contains(body, "<!-- issue-viewer-comments") {
		t.Fatalf("on-disk body lost the comments block:\n%s", body)
	}
}

func TestPreviewTransition(t *testing.T) {
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
					{Type: "append_section", Title: "Implementation", Body: "- [ ] Code complete"},
					{Type: "inject_prompt", Prompt: "Implement carefully"},
					{Type: "set_fields", Field: "assignee", Value: ""},
				},
			},
		},
	}

	preview := wf.PreviewTransition(&Issue{Slug: "x", BodyRaw: "Existing", Assignee: "alice"}, "backlog", "in progress", "", nil)

	if !preview.Allowed {
		t.Fatalf("preview unexpectedly blocked: %#v", preview)
	}
	if len(preview.Steps) != 4 {
		t.Fatalf("steps = %d, want 4", len(preview.Steps))
	}
	if preview.Steps[0].Outcome != "passed" {
		t.Fatalf("first step outcome = %q, want passed", preview.Steps[0].Outcome)
	}
	if preview.Steps[1].Outcome != "changed" {
		t.Fatalf("append step outcome = %q, want changed", preview.Steps[1].Outcome)
	}
	if preview.Result.Update.Status == nil || *preview.Result.Update.Status != "in progress" {
		t.Fatalf("status update = %#v", preview.Result.Update.Status)
	}
}

func TestPreviewTransition_SkipsExistingSection(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "idea"},
			{Name: "in design"},
		},
		Transitions: []WorkflowTransition{
			{
				From: "idea",
				To:   "in design",
				Actions: []WorkflowAction{
					{Type: "append_section", Title: "Idea", Body: "- [ ] Problem described clearly"},
				},
			},
			{
				From:    "in design",
				To:      "backlog",
				Actions: []WorkflowAction{{Type: "validate", Rule: "section_checkboxes_checked: Idea"}},
			},
		},
	}

	body := "## Idea\n- [x] Problem described clearly"
	preview := wf.PreviewTransition(&Issue{Slug: "x", BodyRaw: body}, "idea", "in design", "", nil)

	if !preview.Allowed {
		t.Fatalf("preview unexpectedly blocked: %#v", preview)
	}
	if len(preview.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(preview.Steps))
	}
	if preview.Steps[0].Outcome != "skipped" {
		t.Fatalf("step outcome = %q, want skipped", preview.Steps[0].Outcome)
	}
	if preview.Steps[0].Message != "Section exists with all its boxes" {
		t.Fatalf("step message = %q", preview.Steps[0].Message)
	}
	if preview.Result.Update.Body != nil {
		t.Fatalf("expected no body update, got %#v", *preview.Result.Update.Body)
	}
	if preview.Result.BodyChanged {
		t.Fatalf("expected preview result to leave body unchanged, got %#v", preview.Result)
	}

	topUp := wf.PreviewTransition(&Issue{Slug: "x", BodyRaw: "## Idea\n- [ ] Existing content"}, "idea", "in design", "", nil)
	if topUp.Steps[0].Outcome != "changed" || topUp.Steps[0].Message != "Section exists — would add 1 missing box" {
		t.Fatalf("top-up step = %q / %q", topUp.Steps[0].Outcome, topUp.Steps[0].Message)
	}
}

func TestPreviewTransition_Failure(t *testing.T) {
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

	preview := wf.PreviewTransition(&Issue{Slug: "x"}, "backlog", "in progress", "", nil)

	if preview.Allowed {
		t.Fatal("expected preview to be blocked")
	}
	if preview.ValidationError == "" {
		t.Fatal("expected validation error")
	}
	if len(preview.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(preview.Steps))
	}
	if preview.Steps[0].Outcome != "failed" {
		t.Fatalf("step outcome = %q, want failed", preview.Steps[0].Outcome)
	}
}

// TestApplyTransitionPreservesSetFieldsHumanApproval guards against the
// post-loop unconditional clear that silently overwrote any `set_fields` action
// whose `field` was `human_approval`. The clear must run before the action
// loop so an explicit `set_fields` value wins.
func TestApplyTransitionPreservesSetFieldsHumanApproval(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "A"}, {Name: "B"}},
		Transitions: []WorkflowTransition{{
			From: "A",
			To:   "B",
			Actions: []WorkflowAction{
				{Type: "set_fields", Field: "human_approval", Value: "yes"},
			},
		}},
	}

	// Issue arrives with a standing approval — the old code's post-loop clear
	// path triggers in this case, which is exactly when the bug overwrote the
	// set_fields value.
	issue := &Issue{HumanApproval: "yes"}

	result := wf.ApplyTransitionWithFields(issue, "A", "B", nil)

	if result.Update.HumanApproval == nil {
		t.Fatalf("HumanApproval was not set on result")
	}
	if got := *result.Update.HumanApproval; got != "yes" {
		t.Errorf("HumanApproval after transition = %q, want %q (set_fields was overwritten)", got, "yes")
	}
	if !result.ClearedApproval {
		t.Errorf("ClearedApproval = false, want true (an incoming approval was consumed)")
	}
}
