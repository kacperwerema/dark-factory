package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/telemetry"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ownAssigneeWorkflow is the development template's design → backlog →
// in progress stretch: backlog clears the assignee, in progress needs one.
const ownAssigneeWorkflow = `
statuses:
  - name: "in design"
  - name: "backlog"
  - name: "in progress"
  - name: "testing"
transitions:
  - from: "in design"
    to: "backlog"
    actions:
      - type: require_human_approval
        status: "backlog"
      - type: set_fields
        field: "assignee"
        value: ""
  - from: "backlog"
    to: "in progress"
    actions:
      - type: require_human_approval
        status: "in progress"
      - type: validate
        rule: "has_assignee"
  - from: "in progress"
    to: "testing"
    actions: []
  - from: "testing"
    to: "backlog"
    actions:
      - type: set_fields
        field: "assignee"
        value: ""
`

// newOwnAssigneeProject writes a project with one issue cli/sample at status,
// assigned to its worker, with the approvals given.
func newOwnAssigneeProject(t *testing.T, status, approval, queued string) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "workflow.yaml"), strings.TrimSpace(ownAssigneeWorkflow)+"\n")
	issuePath := filepath.Join(root, "issues", "CLI", "sample.md")
	if err := os.MkdirAll(filepath.Dir(issuePath), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, issuePath, fmt.Sprintf("---\ntitle: \"sample\"\nstatus: %q\nsystem: \"CLI\"\nassignee: %q\nhuman_approval: %q\nqueued_approval: %q\n---\n\nbody\n",
		status, tracker.AgentSessionName("cli/sample"), approval, queued))
	cfg := filepath.Join(root, "projects.yaml")
	mustWrite(t, cfg, fmt.Sprintf("projects:\n  - name: Demo\n    slug: demo\n    workdir: %q\n    issues: %q\n    workflow: %q\n",
		root, filepath.Join(root, "issues"), filepath.Join(root, "workflow.yaml")))
	t.Setenv("ISSUE_VIEWER_CONFIG", cfg)
	t.Setenv("ISSUE_CLI_LOG", "")
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv(telemetry.DisableEnv, "off")
	t.Setenv("AGENT_NAME", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "cli/sample")
	return issuePath
}

func runOwn(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errw bytes.Buffer
	err := runTraced(append([]string{"--project", "demo"}, args...), strings.NewReader(""), &out, &errw, &invocation{})
	return out.String() + errw.String(), err
}

func readSample(t *testing.T, path string) *tracker.Issue {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	issue, err := tracker.ParseIssue(filepath.Base(path), data)
	if err != nil {
		t.Fatal(err)
	}
	return issue
}

// AC1: the issue's own worker walks in design → backlog → in progress with
// two transitions; backlog's clear leaves its own assignee.
func TestOwnWorkerKeepsAssigneeThroughBacklog(t *testing.T) {
	path := newOwnAssigneeProject(t, "in design", "backlog", "in progress")
	if out, err := runOwn(t, "transition", "cli/sample", "--to", "backlog"); err != nil {
		t.Fatalf("to backlog: %v\n%s", err, out)
	}
	if got := readSample(t, path).Assignee; got != tracker.AgentSessionName("cli/sample") {
		t.Fatalf("assignee after backlog = %q, want the worker kept", got)
	}
	out, err := runOwn(t, "transition", "cli/sample", "--to", "in progress")
	if err != nil {
		t.Fatalf("to in progress (second transition, no second start): %v\n%s", err, out)
	}
	if got := readSample(t, path).Status; got != "in progress" {
		t.Fatalf("status = %q, want in progress", got)
	}
}

// The lead (or the viewer) moving the issue still hands it back unassigned,
// and so does a worker's move backwards.
func TestBacklogClearsAssigneeForOthersAndBackwardMoves(t *testing.T) {
	path := newOwnAssigneeProject(t, "in design", "backlog", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	if out, err := runOwn(t, "transition", "cli/sample", "--to", "backlog"); err != nil {
		t.Fatalf("lead to backlog: %v\n%s", err, out)
	}
	if got := readSample(t, path).Assignee; got != "" {
		t.Fatalf("lead's move kept assignee %q, want it cleared", got)
	}

	path = newOwnAssigneeProject(t, "testing", "", "")
	if out, err := runOwn(t, "transition", "cli/sample", "--to", "backlog"); err != nil {
		t.Fatalf("worker back to backlog: %v\n%s", err, out)
	}
	if got := readSample(t, path).Assignee; got != "" {
		t.Fatalf("backward move kept assignee %q, want it cleared", got)
	}
}

// AC2: start's own auto-advance shows in its Status line and its Next.
func TestStartShowsStatusAfterItsAdvance(t *testing.T) {
	newOwnAssigneeProject(t, "backlog", "in progress", "")
	out, err := runOwn(t, "start", "cli/sample")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Status: in progress") {
		t.Fatalf("Status line doesn't read in progress:\n%s", out)
	}
	_, next, ok := strings.Cut(out, "== Next ==")
	if !ok || !strings.Contains(next, `--to "testing"`) {
		t.Fatalf("Next doesn't name the transition to testing:\n%s", out)
	}
}

// AC3: a transition to the status the issue is already at is a no-op, exit 0.
func TestTransitionToCurrentStatusSaysAlready(t *testing.T) {
	path := newOwnAssigneeProject(t, "in progress", "", "")
	out, err := runOwn(t, "transition", "cli/sample", "--to", "in progress")
	if err != nil {
		t.Fatalf("transition to current status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already") {
		t.Fatalf("output doesn't say already:\n%s", out)
	}
	if got := readSample(t, path).Status; got != "in progress" {
		t.Fatalf("status = %q", got)
	}
	if out, err := runOwn(t, "transition", "cli/sample", "--to", "in progress", "--dry-run"); err != nil || !strings.Contains(out, "already") {
		t.Fatalf("dry-run: %v\n%s", err, out)
	}
}
