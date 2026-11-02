package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// After backlog clears the assignee, transition --to "in progress" must name
// the missing assignee and `start` when the approval is in place (stamped,
// or passable under YOLO), and say "not human-approved" only when the
// approval is really missing.

func writeReady(t *testing.T, proj *tracker.Project, approval string) {
	t.Helper()
	fm := "---\ntitle: \"Ready\"\nstatus: \"backlog\"\n"
	if approval != "" {
		fm += "human_approval: \"" + approval + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "ready.md"), []byte(fm+"---\n\nBody of Ready.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func wantAssigneeRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("transition passed without an assignee")
	}
	msg := err.Error()
	if strings.Contains(msg, "not human-approved") || errors.Is(err, tracker.ErrApprovalMissing) {
		t.Fatalf("approval is in place but the refusal blames it:\n%s", msg)
	}
	if !strings.Contains(msg, "no assignee") || !strings.Contains(msg, "issue-cli start ready") {
		t.Fatalf("refusal doesn't name the assignee and start:\n%s", msg)
	}
}

func TestTransitionUnassignedApprovedNamesAssignee(t *testing.T) {
	proj := startYOLOProject(t)
	writeReady(t, proj, "in progress")
	_, _, err := runYOLOTransition(t, proj, time.Now(), "ready", "--to", "in progress")
	wantAssigneeRefusal(t, err)
}

func TestTransitionUnassignedUnderYOLONamesAssignee(t *testing.T) {
	proj := startYOLOProject(t)
	now := time.Now()
	yoloOn(t, proj, now, time.Hour)
	_, _, err := runYOLOTransition(t, proj, now, "ready", "--to", "in progress")
	wantAssigneeRefusal(t, err)
	// The refusal leaves no stray YOLO approval behind.
	if issue := loadLeadIssue(t, proj, "ready"); issue.Status != "backlog" || issue.HumanApproval != "" {
		t.Fatalf("refused transition changed the issue: status %q, approval %q", issue.Status, issue.HumanApproval)
	}
}

func TestTransitionUnassignedNotApprovedStaysApproval(t *testing.T) {
	proj := startYOLOProject(t)
	_, _, err := runYOLOTransition(t, proj, time.Now(), "ready", "--to", "in progress")
	if err == nil || !strings.Contains(err.Error(), "not human-approved") || !errors.Is(err, tracker.ErrApprovalMissing) {
		t.Fatalf("YOLO off, not approved: err = %v", err)
	}
}
