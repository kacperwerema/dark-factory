package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// tickFixture is a project whose in progress → testing gate waits on
// ## Implementation and testing → done on ## Human Testing (the owner's),
// with its issue cli/sample in progress and two Implementation boxes open.
func tickFixture(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	dir := t.TempDir()
	systemDir := filepath.Join(dir, "issues", "CLI")
	if err := os.MkdirAll(systemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	workflowPath := filepath.Join(dir, "workflow.yaml")
	workflow := `statuses:
  - name: "in progress"
  - name: "testing"
  - name: "done"
transitions:
  - from: "in progress"
    to: "testing"
    actions:
      - type: validate
        rule: "section_checkboxes_checked: Implementation"
  - from: "testing"
    to: "done"
    actions:
      - type: validate
        rule: "all_checkboxes_checked"
`
	if err := os.WriteFile(workflowPath, []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	issuePath := filepath.Join(systemDir, "sample.md")
	issue := `---
title: "sample"
status: "in progress"
system: "CLI"
---

## Implementation
- [x] Plan written
- [ ] Code changes complete
- [ ] Tests added

## Human Testing
- [ ] Owner looked at it
`
	if err := os.WriteFile(issuePath, []byte(issue), 0o644); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{Name: "test", Slug: "test", IssueDir: filepath.Join(dir, "issues"), WorkflowFile: workflowPath}, issuePath
}

func statusOf(t *testing.T, issuePath string) string {
	t.Helper()
	for _, line := range strings.Split(readFile(t, issuePath), "\n") {
		if v, ok := strings.CutPrefix(line, "status: "); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func TestTransitionCheckTicksAndMovesInOneCall(t *testing.T) {
	proj, issuePath := tickFixture(t)
	asWorkerOn(t, "cli/sample")
	ctx, stdout, _ := newTestContext(proj, false)

	if err := runTransition(ctx, []string{"cli/sample", "--to", "testing", "--check", "I2", "I3"}); err != nil {
		t.Fatalf("transition --check: %v\n%s", err, stdout)
	}
	out := stdout.String()
	assertContains(t, out, "✓ Checked: I2 [Implementation #2] Code changes complete")
	assertContains(t, out, "✓ Checked: I3 [Implementation #3] Tests added")
	assertContains(t, out, "✓ in progress → testing")
	if got := statusOf(t, issuePath); got != "testing" {
		t.Fatalf("status = %q, want testing", got)
	}
	assertContains(t, readFile(t, issuePath), "- [x] Tests added")
}

func TestTransitionCheckOfOnlyAnOwnerBoxChangesNothing(t *testing.T) {
	proj, issuePath := tickFixture(t)
	asWorkerOn(t, "cli/sample")
	before := readFile(t, issuePath)

	for name, args := range map[string][]string{
		"owner box": {"HT1"},
		"unknown":   {"I2", "I9", "HT1"},
		"not ids":   {"Zz1"},
	} {
		ctx, stdout, _ := newTestContext(proj, false)
		err := runTransition(ctx, append([]string{"cli/sample", "--to", "testing", "--check"}, args...))
		if err == nil {
			t.Fatalf("%s: want a refusal\n%s", name, stdout)
		}
		if name == "owner box" {
			// check's own message for the owner's box.
			assertContains(t, err.Error(), "these boxes are the owner's; agent-cli-sample can't tick them, and nothing changed:")
			assertContains(t, err.Error(), "HT1 Owner looked at it")
		}
		assertContains(t, err.Error(), "Nothing was moved: the issue stays at in progress")
		if got := readFile(t, issuePath); got != before {
			t.Fatalf("%s: a refused --check changed the issue:\n%s", name, got)
		}
	}
}

// An owner box among the agent's: the agent's tick, the owner's gets its
// line, and the move goes on (its gates decide).
func TestTransitionCheckTicksTheAgentsBoxesAndNamesTheOwners(t *testing.T) {
	proj, issuePath := tickFixture(t)
	asWorkerOn(t, "cli/sample")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "testing", "--check", "I2", "I3", "HT1"}); err != nil {
		t.Fatalf("transition --check I2 I3 HT1: %v\n%s", err, stdout)
	}
	out := stdout.String()
	assertContains(t, out, "✓ Checked: I3 [Implementation #3] Tests added")
	assertContains(t, out, "✗ Not ticked, the owner's: HT1 Owner looked at it (a Human Testing box)")
	assertContains(t, out, "✓ in progress → testing")
	// The new status's checklist marks the owner's box.
	assertContains(t, out, "HT1 [ ] (owner's) Owner looked at it")
	if got := statusOf(t, issuePath); got != "testing" {
		t.Fatalf("status = %q, want testing", got)
	}
	assertContains(t, readFile(t, issuePath), "- [ ] Owner looked at it")

	// A dry run previews the same: the owner's box would not tick.
	proj, _ = tickFixture(t)
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "testing", "--dry-run", "--check", "I2", "I3", "HT1"}); err != nil {
		t.Fatalf("dry run: %v\n%s", err, stdout)
	}
	assertContains(t, stdout.String(), "✗ Would not tick, the owner's: HT1 Owner looked at it")
	assertContains(t, stdout.String(), "✓ Ready: in progress → testing")
}

func TestRefusedTransitionEndsWithTheOneCallCommand(t *testing.T) {
	proj, issuePath := tickFixture(t)
	asWorkerOn(t, "cli/sample")

	ctx, _, _ := newTestContext(proj, false)
	if err := runCheck(ctx, []string{"cli/sample", "I2"}); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ = newTestContext(proj, false)
	err := runTransition(ctx, []string{"cli/sample", "--to", "testing"})
	if err == nil {
		t.Fatal("want a refusal: I3 is open")
	}
	want := "issue-cli --project test transition cli/sample --to testing --check I3"
	if got := lastLine(err.Error()); got != want {
		t.Fatalf("last line = %q, want %q\n%s", got, want, err)
	}

	// The dry run's report ends with it too.
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "testing", "--dry-run"}); err == nil {
		t.Fatal("dry run: want not ready")
	}
	assertContains(t, stdout.String(), "Tick them and move in one call:\n  "+want+"\n")

	// That line, run as printed, moves the issue.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "testing", "--check", "I3"}); err != nil {
		t.Fatalf("the printed call: %v\n%s", err, stdout)
	}
	if got := statusOf(t, issuePath); got != "testing" {
		t.Fatalf("status = %q, want testing", got)
	}

	// Open owner boxes: no line, since that call couldn't pass.
	ctx, _, _ = newTestContext(proj, false)
	err = runTransition(ctx, []string{"cli/sample", "--to", "done"})
	if err == nil {
		t.Fatal("want a refusal: HT1 is open")
	}
	assertNotContains(t, err.Error(), "--check")
}

func TestTransitionDryRunCheckPreviewsWithoutWriting(t *testing.T) {
	proj, issuePath := tickFixture(t)
	asWorkerOn(t, "cli/sample")
	before := readFile(t, issuePath)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "testing", "--dry-run", "--check", "I2,I3"}); err != nil {
		t.Fatalf("dry run --check: %v\n%s", err, stdout)
	}
	assertContains(t, stdout.String(), "✓ Ready: in progress → testing")
	ctx, _, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done", "--dry-run", "--check", "HT1"}); err == nil {
		t.Fatal("dry run --check of an owner box: want a refusal")
	}
	if got := readFile(t, issuePath); got != before {
		t.Fatalf("a dry run changed the issue:\n%s", got)
	}
}

func TestParseCheckFlag(t *testing.T) {
	for _, c := range []struct {
		args, ids, rest []string
	}{
		{[]string{"--to", "testing", "--check", "Im1", "I2"}, []string{"Im1", "I2"}, []string{"--to", "testing"}},
		{[]string{"--check", "Im1", "I2", "testing"}, []string{"Im1", "I2"}, []string{"testing"}},
		{[]string{"--check=Im1,Im2", "--dry-run"}, []string{"Im1", "Im2"}, []string{"--dry-run"}},
		{[]string{"--check", "Im1", "--to", "x", "--check", "D2"}, []string{"Im1", "D2"}, []string{"--to", "x"}},
		{[]string{"--check", "Design#3"}, []string{"Design#3"}, nil},
	} {
		ids, rest, given, err := parseCheckFlag(c.args)
		if err != nil || !given || !reflect.DeepEqual(ids, c.ids) || !reflect.DeepEqual(rest, c.rest) {
			t.Errorf("parseCheckFlag(%q) = %q, %q, %v, %v; want %q, %q", c.args, ids, rest, given, err, c.ids, c.rest)
		}
	}
	if _, _, _, err := parseCheckFlag([]string{"--check", "--to", "x"}); err == nil {
		t.Error("--check with no ids: want an error")
	}
}

func TestTelemetryKnowsTransitionCheck(t *testing.T) {
	inv := &invocation{command: "transition", cmd: transitionCommand, args: []string{"cli/x", "--to", "testing", "--check", "Im1"}}
	if !slices.Contains(eventFlags(inv), "check") {
		t.Fatalf("eventFlags = %q, want check", eventFlags(inv))
	}
	for _, c := range buildManifest().Commands {
		if c.Name == "transition" && !slices.Contains(c.Flags["transition"], "check") {
			t.Fatalf("manifest flags for transition = %q, want check", c.Flags["transition"])
		}
	}
}
