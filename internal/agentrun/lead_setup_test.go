package agentrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func readLeadSettings(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("settings.json: %v\n%s", err, b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("settings.json mode %v", fi.Mode())
	}
	return doc
}

// A Claude Code lead (CEO, department, group) starts without the skill
// list, the Artifact tool and Workflow, SendFeedback and ReportFindings;
// its plugins, MCP servers and connectors stay, and a department's
// connector deny list and the lead's tools are one --disallowedTools.
func TestLeadSetupTurnsOffSkillsArtifactAndTools(t *testing.T) {
	ownerClaude(t)
	proj, wf := workerProject(t)
	proj.Departments[0].Connectors[0].DenyTools = []string{"submit"}
	env := []string{"ISSUE_CLI_LOG=/l/x.clilog"}

	for name, c := range map[string]struct {
		spec       Spec
		disallowed string
	}{
		"CEO":             {Spec{Session: "lead-acme", LeadScope: tracker.LeadScopeProject, AgentType: "claude", Workflow: wf}, "--disallowedTools=Workflow,SendFeedback,ReportFindings"},
		"department lead": {Spec{Session: "lead-acme-marketing", LeadScope: tracker.LeadScopeDepartment("Marketing"), AgentType: "claude", Workflow: wf}, "--disallowedTools=mcp__gsc__submit,Workflow,SendFeedback,ReportFindings"},
	} {
		if !LeanLead(c.spec) || LeanWorker(c.spec) {
			t.Fatalf("%s: LeanLead %v, LeanWorker %v", name, LeanLead(c.spec), LeanWorker(c.spec))
		}
		dir := t.TempDir()
		var steps []Step
		base := ConnectorFlags(proj, c.spec, dir, env, &steps)
		ls := LeadSetupFlags(proj, c.spec, proj.WorkDir, dir, base, &steps)
		var disallowed []string
		for _, f := range ls.Flags {
			if f == "--strict-mcp-config" {
				t.Errorf("%s: lead flags %q: a lead keeps the owner's MCP config", name, ls.Flags)
			}
			if strings.HasPrefix(f, "--disallowedTools=") {
				disallowed = append(disallowed, f)
			}
		}
		if !slices.Equal(disallowed, []string{c.disallowed}) {
			t.Errorf("%s: --disallowedTools %q, want %q", name, disallowed, c.disallowed)
		}
		if strings.Contains(strings.Join(ls.Flags, " "), "ListAgents") {
			t.Errorf("%s: ListAgents off: %q", name, ls.Flags)
		}
		set := filepath.Join(dir, "settings.json")
		if !slices.Contains(ls.Flags, "--settings="+set) {
			t.Fatalf("%s: flags %q", name, ls.Flags)
		}
		doc := readLeadSettings(t, set)
		if _, ok := doc["enabledPlugins"]; ok {
			t.Errorf("%s: a lead's settings turn plugins off: %s", name, doc["enabledPlugins"])
		}
		var skills map[string]string
		json.Unmarshal(doc["skillOverrides"], &skills)
		for _, s := range []string{"dxverify", "humanizer", "stageshow"} {
			if skills[s] != "off" {
				t.Errorf("%s: skillOverrides = %v", name, skills)
			}
		}
		if string(doc["disableBundledSkills"]) != "true" {
			t.Errorf("%s: disableBundledSkills = %s", name, doc["disableBundledSkills"])
		}
		if !slices.Equal(ls.Env, []string{"CLAUDE_CODE_DISABLE_ARTIFACT=1"}) {
			t.Errorf("%s: env %q", name, ls.Env)
		}
		names := stepNames(steps)
		if !strings.Contains(names, "Lead setup (leads:): skills off 3, bundled skills off, Artifact, Workflow, SendFeedback, ReportFindings off") {
			t.Errorf("%s: steps %s", name, names)
		}
		var detail string
		for _, st := range steps {
			if strings.HasPrefix(st.Name, "Lead setup") {
				detail = st.Detail
			}
		}
		if !strings.Contains(detail, "leads: tools: keeps one, setup: full") || !strings.Contains(detail, "docs/agent-dispatch.md#lead-setup") {
			t.Errorf("%s: detail %q", name, detail)
		}
	}
	// The owner's own Claude Code files are untouched.
	if b, _ := os.ReadFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")); strings.Contains(string(b), "skillOverrides") {
		t.Errorf("owner settings written: %s", b)
	}
}

// leads: tools: keeps a tool for the project's leads; leads: setup: full,
// a codex lead and a worker are no lean lead.
func TestLeadSetupKeepsAndFull(t *testing.T) {
	ownerClaude(t)
	proj, _ := workerProject(t)
	keep := &tracker.WorkflowConfig{Leads: tracker.LeadsConfig{Tools: []string{"artifact", "Workflow"}}}
	dir := t.TempDir()
	var steps []Step
	ls := LeadSetupFlags(proj, Spec{Session: "lead-acme", LeadScope: tracker.LeadScopeProject, AgentType: "claude", Workflow: keep}, proj.WorkDir, dir, nil, &steps)
	if len(ls.Env) != 0 {
		t.Errorf("Artifact kept, env %q", ls.Env)
	}
	if !slices.Contains(ls.Flags, "--disallowedTools=SendFeedback,ReportFindings") {
		t.Errorf("flags %q", ls.Flags)
	}
	if !strings.Contains(stepNames(steps), "SendFeedback, ReportFindings off") || strings.Contains(stepNames(steps), "Artifact") {
		t.Errorf("steps %s", stepNames(steps))
	}

	all := &tracker.WorkflowConfig{Leads: tracker.LeadsConfig{Tools: tracker.LeadToolsOff}}
	ls = LeadSetupFlags(proj, Spec{LeadScope: tracker.LeadScopeProject, AgentType: "claude", Workflow: all}, proj.WorkDir, t.TempDir(), []string{"--mcp-config=x"}, new([]Step))
	for _, f := range ls.Flags {
		if strings.HasPrefix(f, "--disallowedTools") {
			t.Errorf("every tool kept, flags %q", ls.Flags)
		}
	}

	full := &tracker.WorkflowConfig{Leads: tracker.LeadsConfig{Setup: tracker.WorkerSetupFull}}
	for name, spec := range map[string]Spec{
		"setup: full": {Session: "lead-acme", LeadScope: tracker.LeadScopeProject, AgentType: "claude", Workflow: full},
		"codex lead":  {Session: "lead-acme", LeadScope: tracker.LeadScopeProject, AgentType: "codex"},
		"worker":      {Session: "agent-page", IssueSlug: "page", AgentType: "claude"},
		"no issue":    {Session: "review-acme", AgentType: "claude"},
	} {
		if LeanLead(spec) {
			t.Errorf("%s: lean lead", name)
		}
	}
}

func TestMergeDisallowed(t *testing.T) {
	for _, c := range []struct {
		flags []string
		more  string
		want  []string
	}{
		{nil, "", nil},
		{[]string{"--mcp-config=m"}, "", []string{"--mcp-config=m"}},
		{nil, "Workflow", []string{"--disallowedTools=Workflow"}},
		{[]string{"--mcp-config=m", "--disallowedTools=mcp__a__b"}, "Workflow,SendFeedback", []string{"--mcp-config=m", "--disallowedTools=mcp__a__b,Workflow,SendFeedback"}},
	} {
		if got := mergeDisallowed(c.flags, c.more); !slices.Equal(got, c.want) {
			t.Errorf("mergeDisallowed(%q, %q) = %q, want %q", c.flags, c.more, got, c.want)
		}
	}
}

// Start launches a lean lead with the lead setup and a lead with leads:
// setup: full as before; a worker keeps its own lean setup.
func TestLeadLaunchSetup(t *testing.T) {
	ownerClaude(t)
	record := standInAgent(t, "claude")
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	liveAgentStart(t)

	proj, wf := workerProject(t)
	proj.Terminal = "none"
	start := func(spec Spec) []string {
		t.Helper()
		os.Remove(record)
		spec.Prompt, spec.Headless, spec.AgentType = "the brief", true, "claude"
		resp := Start(proj, spec)
		if resp.Status != "dispatched" {
			t.Fatalf("%s: %q\n%s", spec.Session, resp.Status, stepNames(resp.Steps))
		}
		return readArgv(t, record)
	}
	hasPrefix := func(argv []string, p string) bool {
		return slices.ContainsFunc(argv, func(a string) bool { return strings.HasPrefix(a, p) })
	}

	lead := start(Spec{Session: "lead-acme", LeadScope: tracker.LeadScopeProject, Workflow: wf})
	if slices.Contains(lead, "--strict-mcp-config") || !hasPrefix(lead, "--settings=") || !slices.Contains(lead, "--disallowedTools=Workflow,SendFeedback,ReportFindings") {
		t.Errorf("lead argv %q", lead)
	}
	if lead[len(lead)-1] != "the brief" {
		t.Errorf("lead argv ends %q", lead[len(lead)-1])
	}

	fullWF := wf.Clone()
	fullWF.Leads.Setup = tracker.WorkerSetupFull
	full := start(Spec{Session: "lead-acme-2", LeadScope: tracker.LeadScopeProject, Workflow: fullWF})
	if hasPrefix(full, "--settings") || hasPrefix(full, "--disallowedTools") || slices.Contains(full, "--strict-mcp-config") {
		t.Errorf("setup: full lead argv %q", full)
	}

	worker := start(Spec{Session: "agent-screen", IssueSlug: "screen", Workflow: wf})
	if !slices.Contains(worker, "--strict-mcp-config") || !hasPrefix(worker, "--settings=") || !slices.Contains(worker, "--disallowedTools=Workflow,SendFeedback,ReportFindings,ListAgents") {
		t.Errorf("worker argv %q", worker)
	}
}
