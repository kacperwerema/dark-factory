package main

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ------------------- help -------------------

func TestRunHelpNoArgsPrintsTopLevel(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.AllProjects = []tracker.Project{*proj}
	if err := runHelp(ctx, nil); err != nil {
		t.Fatalf("runHelp: %v", err)
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Fatal("expected help text, got empty")
	}
}

func TestRunHelpWithTopicDelegatesToProcess(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runHelp(ctx, []string{"workflow"}); err != nil {
		t.Fatalf("runHelp: %v", err)
	}
	assertContains(t, stdout.String(), "Status Lifecycle")
}

func TestRunHelpWithCommandNamePrintsCommandHelp(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runHelp(ctx, []string{"transition"}); err != nil {
		t.Fatalf("runHelp transition: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "issue-cli transition")
	assertContains(t, out, "--to")
	// Bot-friendly pointer to the rules topic.
	assertContains(t, out, "Related topic: issue-cli help transitions")
}

func TestRunHelpAcceptsSingularTransitionTopic(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	// `transition` is also a command — command help wins. The point of this
	// test is that the call doesn't error like it used to.
	if err := runHelp(ctx, []string{"transition"}); err != nil {
		t.Fatalf("runHelp transition: %v", err)
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Fatal("expected output for help transition")
	}
}

func TestRunHelpUnknownTopicMentionsCommandHelp(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	err := runHelp(ctx, []string{"nope-not-a-thing"})
	if err == nil {
		t.Fatal("expected error for unknown topic")
	}
	if !strings.Contains(err.Error(), "issue-cli help <command>") {
		t.Fatalf("error should hint at per-command help, got: %v", err)
	}
}

func TestRunProcessTopicSingularPluralAliases(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	for _, topic := range []string{"doc", "docs", "test", "testing", "reference", "references", "format"} {
		ctx, stdout, _ := newTestContext(proj, false)
		if err := runProcess(ctx, []string{topic}); err != nil {
			t.Fatalf("runProcess %s: %v", topic, err)
		}
		if strings.TrimSpace(stdout.String()) == "" {
			t.Fatalf("topic %s produced no output", topic)
		}
	}
}

func TestPrintHelpListsTopicsAndNextActions(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.AllProjects = []tracker.Project{*proj}
	if err := runHelp(ctx, nil); err != nil {
		t.Fatalf("runHelp: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "Topics (issue-cli help <topic>):")
	assertContains(t, out, "Per-command help (with flags):\n  issue-cli <command> --help")
	assertContains(t, out, "Next viable actions for a bot:")
	assertContains(t, out, "issue-cli transition <slug> --to <status>")
}

// ------------------- process dispatcher -------------------

func TestRunProcessOverviewNoTopic(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProcess(ctx, nil); err != nil {
		t.Fatalf("runProcess: %v", err)
	}
	assertContains(t, stdout.String(), "Dark Factory")
}

func TestRunProcessFormatTopic(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProcess(ctx, []string{"format"}); err != nil {
		t.Fatalf("runProcess format: %v", err)
	}
	assertContains(t, stdout.String(), "Issue File Format")
}

func TestRunProcessUnknownTopic(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runProcess(ctx, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "unknown topic") {
		t.Fatalf("expected unknown-topic error, got %v", err)
	}
}

func TestRunProcessWorkflowPrintsLifecycle(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProcessWorkflow(ctx, nil); err != nil {
		t.Fatalf("runProcessWorkflow: %v", err)
	}
	assertContains(t, stdout.String(), "Status Lifecycle")
}

func TestRunProcessWorkflowRejectsNilProject(t *testing.T) {
	ctx, _, _ := newTestContext(nil, false)
	if err := runProcessWorkflow(ctx, nil); err == nil || !strings.Contains(err.Error(), "needs a project") {
		t.Fatalf("expected needs-a-project error, got %v", err)
	}
}

func TestRunProcessSystemsListsSystems(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runProcessSystems(ctx); err != nil {
		t.Fatalf("runProcessSystems: %v", err)
	}
	out := stdout.String()
	assertContains(t, out, "Available Systems")
	assertContains(t, out, "CLI")
}

func TestRunProcessSystemsRejectsNilProject(t *testing.T) {
	ctx, _, _ := newTestContext(nil, false)
	if err := runProcessSystems(ctx); err == nil || !strings.Contains(err.Error(), "needs a project") {
		t.Fatalf("expected needs-a-project error, got %v", err)
	}
}

// ------------------- workflow dispatcher -------------------

func TestRunWorkflowRequiresSubcommand(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runWorkflow(ctx, nil); err == nil || !strings.Contains(err.Error(), "requires a subcommand") {
		t.Fatalf("expected requires-subcommand error, got %v", err)
	}
}

func TestRunWorkflowRejectsUnknownSubcommand(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"frobnicate"}); err == nil || !strings.Contains(err.Error(), "unknown workflow subcommand") {
		t.Fatalf("expected unknown-subcommand error, got %v", err)
	}
}
