package agentrun

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// startRouted starts a stand-in claude on an issue of type typ, routed by
// cfg, and returns its argv, the launch steps and the issue's recorded
// dispatch.
func startRouted(t *testing.T, cfg *tracker.RoutingConfig, typ string, proj func(dir string) *tracker.Project) ([]string, []Step, tracker.Dispatch) {
	t.Helper()
	record := standInAgent(t, "claude")
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	orig := PromptDeliveryTimeout
	PromptDeliveryTimeout = 0
	t.Cleanup(func() { PromptDeliveryTimeout = orig })

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.md"), []byte("---\ntitle: \"X\"\nstatus: \"in progress\"\ntype: \""+typ+"\"\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := proj(dir)
	issues, err := p.LoadIssues()
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues: %v %d", err, len(issues))
	}
	issue := issues[0]
	route := routing.Pick(cfg, routing.IssueSubject(p, issue), "", &routing.State{}, time.Now())
	resp := Start(p, Spec{Session: "agent-x", Prompt: "the brief", AgentType: route.Agent, Route: &route, IssueSlug: issue.Slug, Headless: true})
	if resp.Status != "dispatched" {
		t.Fatalf("start: %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	argv := readArgv(t, record)
	store, err := tracker.LoadStats(issue.FilePath)
	if err != nil || len(store.Dispatches) != 1 {
		t.Fatalf("sidecar: %v %+v", err, store.Dispatches)
	}
	return argv, resp.Steps, store.Dispatches[0]
}

func plainProject(dir string) *tracker.Project {
	return &tracker.Project{Slug: "p", Terminal: "none", WorkDir: dir, IssueDir: dir}
}

// A rule's model reaches the launch as --model, the Agent line names it and
// the sidecar records it.
func TestARulesModelReachesTheLaunch(t *testing.T) {
	cfg := &tracker.RoutingConfig{Rules: []tracker.RoutingRule{{Type: "tweak", Agent: "claude", Model: "claude-haiku-5-5"}}}
	argv, steps, d := startRouted(t, cfg, "tweak", plainProject)
	if i := slices.Index(argv, "--model"); i < 0 || i+1 >= len(argv) || argv[i+1] != "claude-haiku-5-5" {
		t.Fatalf("argv %q has no --model claude-haiku-5-5", argv)
	}
	if !strings.Contains(stepNames(steps), "Agent: claude · claude-haiku-5-5 (") {
		t.Fatalf("no Agent line naming the model:\n%s", stepNames(steps))
	}
	if d.Model != "claude-haiku-5-5" || d.Agent != "claude" {
		t.Fatalf("dispatch record = %+v", d)
	}
}

// With no routing block a docs worker starts on the built-in default.
func TestADocsWorkerStartsOnTheDefaultModel(t *testing.T) {
	argv, _, d := startRouted(t, nil, "docs", plainProject)
	if i := slices.Index(argv, "--model"); i < 0 || argv[i+1] != "claude-sonnet-5-5" {
		t.Fatalf("argv %q, want --model claude-sonnet-5-5", argv)
	}
	if d.Model != "claude-sonnet-5-5" {
		t.Fatalf("dispatch record = %+v", d)
	}
}

// A feature with no rule naming a model starts with no --model and records
// none; a project that enforces agent_models keeps its model.
func TestLaunchWithoutModelRuleHasNoFlag(t *testing.T) {
	argv, steps, d := startRouted(t, nil, "feature", plainProject)
	if slices.Contains(argv, "--model") || d.Model != "" {
		t.Fatalf("argv %q, record %+v: want no model", argv, d)
	}
	if strings.Contains(stepNames(steps), "·") {
		t.Fatalf("Agent line names a model:\n%s", stepNames(steps))
	}
}

func TestProjectModelStaysForOtherTypes(t *testing.T) {
	pinned := func(dir string) *tracker.Project {
		p := plainProject(dir)
		p.AgentModelSource, p.AgentModels = "project", map[string]string{"claude": "claude-opus-5-5"}
		return p
	}
	argv, _, d := startRouted(t, nil, "feature", pinned)
	if i := slices.Index(argv, "--model"); i < 0 || argv[i+1] != "claude-opus-5-5" || d.Model != "claude-opus-5-5" {
		t.Fatalf("argv %q record %+v, want the project's claude-opus-5-5", argv, d)
	}
}

// model: default hands a docs worker back to the project's own model.
func TestModelDefaultSwitchesTheDocsDefaultOff(t *testing.T) {
	cfg := &tracker.RoutingConfig{Rules: []tracker.RoutingRule{{Type: "docs", Agent: "claude", Model: tracker.ModelDefault}}}
	argv, _, d := startRouted(t, cfg, "docs", plainProject)
	if slices.Contains(argv, "--model") || d.Model != "" {
		t.Fatalf("argv %q, record %+v: want no model", argv, d)
	}
}

// A model with shell text never reaches the command.
func TestInvalidModelNameNeverReachesTheLaunch(t *testing.T) {
	cfg := &tracker.RoutingConfig{Rules: []tracker.RoutingRule{{Type: "tweak", Agent: "claude", Model: "x; touch /tmp/pwned"}}}
	argv, steps, d := startRouted(t, cfg, "tweak", plainProject)
	if slices.Contains(argv, "--model") || d.Model != "" {
		t.Fatalf("argv %q, record %+v: want no model", argv, d)
	}
	if !strings.Contains(stepNames(steps), "is not a model name") {
		t.Fatalf("no warning step:\n%s", stepNames(steps))
	}
}
