package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestRunTransitionNextHintSkipsOptionalStatus(t *testing.T) {
	proj, _ := makeOptionalNextFixture(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "in progress"}); err != nil {
		t.Fatalf("runTransition: %v", err)
	}
	output := stdout.String()

	assertContains(t, output, "== Next ==\n  issue-cli transition cli/sample --to testing")
	assertContains(t, output, "Optional side-paths:")
	assertContains(t, output, "issue-cli transition cli/sample --to team-feedback")
	if strings.Contains(output, "== Next ==\n  issue-cli transition cli/sample --to team-feedback") {
		t.Fatalf("primary Next hint should not point at the optional status:\n%s", output)
	}
}

func TestRunTransitionJSONCarriesOptionalNextStatuses(t *testing.T) {
	proj, _ := makeOptionalNextFixture(t)
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "in progress"}); err != nil {
		t.Fatalf("runTransition: %v", err)
	}

	var got transitionOutput
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("unmarshal transition output: %v\noutput:\n%s", err, stdout.String())
	}
	if got.NextStatus != "testing" {
		t.Fatalf("next_status = %q, want testing", got.NextStatus)
	}
	if got.NextStatusOptional {
		t.Fatal("next_status_optional = true, want false")
	}
	if len(got.OptionalNextStatuses) != 1 || got.OptionalNextStatuses[0] != "team-feedback" {
		t.Fatalf("optional_next_statuses = %v, want [team-feedback]", got.OptionalNextStatuses)
	}
}

// makeContractFixture builds a workflow where the next transition after "in progress"
// has both validations (require_human_approval, validate) and side-effects
// (append_section, inject_prompt, set_fields), so tests can assert the
// Requires/Will buckets render correctly.
func makeContractFixture(t *testing.T) (*tracker.Project, string) {
	t.Helper()

	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	systemDir := filepath.Join(issuesDir, "CLI")
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("mkdir issue dir: %v", err)
	}

	workflowPath := filepath.Join(dir, "workflow.yaml")
	workflow := strings.TrimSpace(`
statuses:
  - name: "in progress"
  - name: "testing"
transitions:
  - from: "in progress"
    to: "testing"
    actions:
      - type: require_human_approval
        status: "testing"
      - type: validate
        rule: has_assignee
      - type: append_section
        title: "Testing"
        body: |
          - [ ] Tests pass
      - type: inject_prompt
        prompt: "Run the full suite."
      - type: set_fields
        field: "assignee"
        value: ""
`)
	if err := os.WriteFile(workflowPath, []byte(workflow), 0644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	issuePath := filepath.Join(systemDir, "sample.md")
	issue := strings.TrimSpace(`
---
title: "sample"
status: "in progress"
system: "CLI"
assignee: "agent-contract"
---

- [x] already done
`)
	if err := os.WriteFile(issuePath, []byte(issue), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	return &tracker.Project{
		Name:         "test",
		Slug:         "test",
		IssueDir:     issuesDir,
		WorkflowFile: workflowPath,
	}, issuePath
}

func TestNextTransitionContractSplitsValidationsFromSideEffects(t *testing.T) {
	t.Parallel()

	wf := &tracker.WorkflowConfig{
		Statuses: []tracker.WorkflowStatus{{Name: "in progress"}, {Name: "testing"}},
		Transitions: []tracker.WorkflowTransition{{
			From: "in progress",
			To:   "testing",
			Actions: []tracker.WorkflowAction{
				{Type: "require_human_approval", Status: "testing"},
				{Type: "validate", Rule: "has_assignee"},
				{Type: "append_section", Title: "Testing"},
				{Type: "inject_prompt"},
				{Type: "set_fields", Field: "assignee", Value: ""},
			},
		}},
	}

	requires, sideEffects := nextTransitionContract(wf, "in progress", "testing")

	wantRequires := []string{
		`Must be human-approved for "testing" in the issue viewer`,
		"Validate issue has assignee",
	}
	if strings.Join(requires, "|") != strings.Join(wantRequires, "|") {
		t.Fatalf("requires = %v, want %v", requires, wantRequires)
	}

	wantSideEffects := []string{
		"Side-effect: appends ## Testing section",
		"Side-effect: injects entry guidance prompt",
		`Side-effect: clears assignee`,
	}
	if strings.Join(sideEffects, "|") != strings.Join(wantSideEffects, "|") {
		t.Fatalf("side-effects = %v, want %v", sideEffects, wantSideEffects)
	}
}

func TestNextTransitionContractReturnsEmptyWhenNoMatchingTransition(t *testing.T) {
	t.Parallel()

	wf := &tracker.WorkflowConfig{
		Statuses: []tracker.WorkflowStatus{{Name: "in progress"}, {Name: "testing"}},
	}
	requires, sideEffects := nextTransitionContract(wf, "in progress", "testing")
	if len(requires) != 0 || len(sideEffects) != 0 {
		t.Fatalf("expected empty buckets, got requires=%v sideEffects=%v", requires, sideEffects)
	}
}

func TestRunStartPrintsNextTransitionContract(t *testing.T) {
	proj, _ := makeContractFixture(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runStart(ctx, []string{"cli/sample"}); err != nil {
		t.Fatalf("runStart: %v", err)
	}
	output := stdout.String()

	assertContains(t, output, "  Requires:\n")
	assertContains(t, output, `    - Must be human-approved for "testing" in the issue viewer`)
	assertContains(t, output, "    - Validate issue has assignee")
	assertContains(t, output, "  Will:\n")
	assertContains(t, output, "    - Side-effect: appends ## Testing section")
	assertContains(t, output, "    - Side-effect: injects entry guidance prompt")
	assertContains(t, output, "    - Side-effect: clears assignee")
}

// TestRunStartAutoAdvanceShowsBanner verifies that starting a handoff status
// (backlog) that is approved for the next status announces the advance with the
// prominent AUTO-ADVANCED banner — naming from → to and the consumed approval —
// instead of the old quiet single line.
func TestRunStartAutoAdvanceShowsBanner(t *testing.T) {
	proj, _ := makeTransitionFixture(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runStart(ctx, []string{"cli/sample"}); err != nil {
		t.Fatalf("runStart: %v", err)
	}
	output := stdout.String()

	assertContains(t, output, "⚠ AUTO-ADVANCED  backlog → in progress")
	assertContains(t, output, `A "in progress" approval was present, so start moved this issue forward and consumed it.`)
	// The banner replaces the quiet status line; neither it nor the standalone
	// "Status unchanged" / "Approval consumed" lines should appear.
	assertNotContains(t, output, "✓ Status →")
	assertNotContains(t, output, "Status unchanged")
	assertNotContains(t, output, "✓ Approval consumed")
}

// TestRunStartPlainClaimNoBanner verifies that starting a non-handoff work
// status only claims, leaving the status unchanged with no AUTO-ADVANCED banner.
func TestRunStartPlainClaimNoBanner(t *testing.T) {
	proj, _ := makeContractFixture(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runStart(ctx, []string{"cli/sample"}); err != nil {
		t.Fatalf("runStart: %v", err)
	}
	output := stdout.String()

	assertContains(t, output, "Status unchanged (in progress is a work status — ready to pick up)")
	assertNotContains(t, output, "AUTO-ADVANCED")
}

func TestRunTransitionJSONCarriesNextTransitionContract(t *testing.T) {
	proj, _ := makeTransitionFixture(t)
	ctx, stdout, _ := newTestContext(proj, true)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "in progress"}); err != nil {
		t.Fatalf("runTransition: %v", err)
	}

	var got transitionOutput
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("unmarshal transition output: %v\noutput:\n%s", err, stdout.String())
	}
	// makeTransitionFixture has no rules for "in progress → testing", so the
	// JSON must carry empty contract slices (omitempty drops them entirely).
	if len(got.NextRequires) != 0 {
		t.Fatalf("next_requires = %v, want empty", got.NextRequires)
	}
	if len(got.NextSideEffects) != 0 {
		t.Fatalf("next_side_effects = %v, want empty", got.NextSideEffects)
	}
}

// makeOptionalNextFixture builds a workflow where the status following "in progress"
// is declared optional, and the required path sits after it.
func makeOptionalNextFixture(t *testing.T) (*tracker.Project, string) {
	t.Helper()

	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	systemDir := filepath.Join(issuesDir, "CLI")
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		t.Fatalf("mkdir issue dir: %v", err)
	}

	workflowPath := filepath.Join(dir, "workflow.yaml")
	workflow := strings.TrimSpace(`
statuses:
  - name: "backlog"
  - name: "in progress"
  - name: "team-feedback"
    optional: true
  - name: "testing"
transitions:
  - from: "backlog"
    to: "in progress"
    actions:
      - type: require_human_approval
        status: "in progress"
      - type: validate
        rule: has_assignee
`)
	if err := os.WriteFile(workflowPath, []byte(workflow), 0644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	issuePath := filepath.Join(systemDir, "sample.md")
	issue := strings.TrimSpace(`
---
title: "sample"
status: "backlog"
system: "CLI"
assignee: "agent-optional-next"
human_approval: "in progress"
---

- [x] already done
`)
	if err := os.WriteFile(issuePath, []byte(issue), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	return &tracker.Project{
		Name:         "test",
		Slug:         "test",
		IssueDir:     issuesDir,
		WorkflowFile: workflowPath,
	}, issuePath
}
