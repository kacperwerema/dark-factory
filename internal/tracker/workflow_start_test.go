package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartIssueOnce_SecondRunIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "issue.md")
	content := `---
title: "Sample"
status: "backlog"
human_approval: "in progress"
---

Body.
`
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	wf := DefaultWorkflow()
	first, err := wf.StartIssueOnce(fp, "sample", "alice")
	if err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	if first.Issue.StartedAt == "" {
		t.Fatal("started_at should be recorded")
	}
	if !first.Transitioned {
		t.Fatal("first start should record transition backlog → in progress")
	}
	if first.Issue.Status != "in progress" {
		t.Fatalf("first status = %q, want in progress", first.Issue.Status)
	}

	second, err := wf.StartIssueOnce(fp, "sample", "alice")
	if err != nil {
		t.Fatalf("second start unexpectedly failed: %v", err)
	}
	if second.Transitioned {
		t.Fatal("second start should not transition")
	}
	if second.Claimed {
		t.Fatal("second start should not re-claim")
	}
	if second.Issue.Status != "in progress" {
		t.Fatalf("second status = %q, want in progress", second.Issue.Status)
	}
	if second.Issue.StartedAt != first.Issue.StartedAt {
		t.Fatalf("started_at changed on second run: %q → %q", first.Issue.StartedAt, second.Issue.StartedAt)
	}
}

func TestStartIssueOnce_ClaimsWorkStatusWithoutTransition(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "issue.md")
	content := `---
title: "Sample"
status: "in progress"
---

## Implementation
- [ ] do the thing
`
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	wf := DefaultWorkflow()
	got, err := wf.StartIssueOnce(fp, "sample", "alice")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if got.Transitioned {
		t.Fatal("expected no transition when starting at a work status")
	}
	if !got.Claimed {
		t.Fatal("expected claim on an unassigned work status")
	}
	if got.Issue.Assignee != "alice" {
		t.Fatalf("assignee = %q, want alice", got.Issue.Assignee)
	}
	if got.Issue.Status != "in progress" {
		t.Fatalf("status = %q, want in progress", got.Issue.Status)
	}
	if got.Issue.StartedAt == "" {
		t.Fatal("started_at should be recorded on first claim")
	}
}

func TestStartIssueOnce_ClaimsIdeaWithoutTransition(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "issue.md")
	content := `---
title: "Sample"
status: "idea"
---

Rough thought.
`
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	wf := DefaultWorkflow()
	got, err := wf.StartIssueOnce(fp, "sample", "alice")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if got.Transitioned {
		t.Fatal("idea is a work status — no transition expected")
	}
	if !got.Claimed {
		t.Fatal("expected claim")
	}
	if got.Issue.Status != "idea" {
		t.Fatalf("status = %q, want idea", got.Issue.Status)
	}
}

func TestStartIssueOnce_RejectsDone(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "issue.md")
	content := `---
title: "Sample"
status: "done"
---

Body.
`
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	wf := DefaultWorkflow()
	if _, err := wf.StartIssueOnce(fp, "sample", "alice"); err == nil {
		t.Fatal("expected start to reject done issues")
	} else if !strings.Contains(err.Error(), "already done") {
		t.Fatalf("error = %q, want 'already done' message", err)
	}
}

func TestStartIssueOnce_HumanTestingRequiresApproval(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "issue.md")
	content := `---
title: "Sample"
status: "human-testing"
assignee: "alice"
---

Body.
`
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	wf := DefaultWorkflow()
	if _, err := wf.StartIssueOnce(fp, "sample", "alice"); err == nil {
		t.Fatal("expected start at human-testing to require approval")
	} else {
		if !strings.Contains(err.Error(), `human approval for "documentation" is missing`) {
			t.Fatalf("error = %q, want documentation approval failure", err)
		}
		if !strings.Contains(err.Error(), "no changes were made") {
			t.Fatalf("error = %q, want no-mutation message", err)
		}
	}
}

func TestMarkIssueDoneOnce_RejectsSecondRun(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "issue.md")
	content := `---
title: "Sample"
status: "shipping"
assignee: "alice"
human_approval: "done"
---

## Shipping
- [x] Changes committed with a message referencing the issue
- [x] Commit pushed to the remote
- [x] PR opened if applicable
`
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	wf := DefaultWorkflow()
	issue, err := wf.MarkIssueDoneOnce(fp, "sample")
	if err != nil {
		t.Fatalf("first done failed: %v", err)
	}
	if issue.DoneAt == "" {
		t.Fatal("done_at should be recorded")
	}
	if issue.Status != "done" {
		t.Fatalf("status = %q, want done", issue.Status)
	}

	if _, err := wf.MarkIssueDoneOnce(fp, "sample"); err == nil {
		t.Fatal("second done should fail")
	}
}

func TestStartIssueOnceMissingApprovalDoesNotClaimIssue(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "issue.md")
	content := `---
title: "Sample"
status: "backlog"
---

## Acceptance Criteria
- [ ] ship it
`
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatalf("write issue: %v", err)
	}

	wf := DefaultWorkflow()
	if _, err := wf.StartIssueOnce(fp, "sample", "alice"); err == nil {
		t.Fatal("expected missing approval to fail")
	} else {
		if !strings.Contains(err.Error(), `human approval for "in progress" is missing`) {
			t.Fatalf("error = %q, want approval-specific failure", err)
		}
		if !strings.Contains(err.Error(), "no changes were made") {
			t.Fatalf("error = %q, want explicit no-mutation guidance", err)
		}
	}

	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read issue: %v", err)
	}
	issue, err := ParseIssue(filepath.Base(fp), data)
	if err != nil {
		t.Fatalf("parse issue: %v", err)
	}
	if issue.Assignee != "" {
		t.Fatalf("issue assignee = %q, want empty", issue.Assignee)
	}
	if issue.StartedAt != "" {
		t.Fatalf("issue started_at = %q, want empty", issue.StartedAt)
	}
	if issue.Status != "backlog" {
		t.Fatalf("issue status = %q, want backlog", issue.Status)
	}
}
