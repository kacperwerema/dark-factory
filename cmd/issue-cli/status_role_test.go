package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/tracker/statustest"
)

// renamedProject has one issue at each renamed role status.
func renamedProject(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues", "CLI")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, role := range tracker.StatusRoles() {
		body := "---\ntitle: \"at " + role + "\"\nstatus: \"" + statustest.Name(role) + "\"\nsystem: \"CLI\"\nversion: \"0.1\"\npriority: \"high\"\n---\n\nBody\n"
		if err := os.WriteFile(filepath.Join(issues, role+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, "workflow.yaml")
	statustest.Write(t, file, "run issue-cli check-script here")
	return &tracker.Project{Name: "P", Slug: "p", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: file, Version: "0.1"}
}

func TestRenamedStatusesKeepNext(t *testing.T) {
	proj := renamedProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runNext(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"cli/at-queue", "cli/at-work", "cli/at-verify"} {
		assertContains(t, stdout.String(), slug)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runNext(ctx, []string{"--design"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "cli/at-design")
	if strings.Contains(stdout.String(), "cli/at-queue") {
		t.Fatalf("the queue is listed as design work:\n%s", stdout)
	}
}

func TestRenamedOwnerTestRefusesApprove(t *testing.T) {
	proj := renamedProject(t)
	ctx, _, _ := newTestContext(proj, false)
	err := runApprove(ctx, []string{"cli/at-verify", "--to", statustest.Name(tracker.StatusRoleOwnerTest), "--quote", "yes"})
	if err == nil || !strings.Contains(err.Error(), "never by an agent") {
		t.Fatalf("approve --to the renamed status: %v", err)
	}
}

func TestRenamedStatusesKeepHintsAndAutostart(t *testing.T) {
	proj := renamedProject(t)
	wf := proj.LoadWorkflow()
	if !usesCheckScripts(wf) {
		t.Fatal("check-script in the renamed verify prompt isn't seen")
	}
	queue, work := statustest.Name(tracker.StatusRoleQueue), statustest.Name(tracker.StatusRoleWork)
	if cmd, _ := nextStepCommand(wf, "s", queue, work); !strings.HasPrefix(cmd, "issue-cli start ") {
		t.Fatalf("from the renamed queue the next step is %q", cmd)
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	got := agentrun.AutostartCandidates(proj, issues, false)
	if len(got) != 1 || got[0].Slug != "cli/at-queue" {
		t.Fatalf("auto-start's candidates = %+v", got)
	}
}

func TestRenamedStatusesCutCreate(t *testing.T) {
	proj := renamedProject(t)
	ctx, _, _ := newTestContext(proj, false)
	err := runCreate(ctx, []string{"--title", "late", "--status", statustest.Name(tracker.StatusRoleQueue)})
	if err == nil {
		t.Fatal("create let an issue start in the renamed queue")
	}
}

func TestProcessWorkflowPrintsRoles(t *testing.T) {
	ctx, stdout, _ := newTestContext(renamedProject(t), false)
	if err := runProcess(ctx, []string{"workflow"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "[role: owner-test]")
}
