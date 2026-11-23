package agentrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ownerClaude writes a stand-in for the owner's Claude Code config under a
// fresh CLAUDE_CONFIG_DIR: three user MCP servers, two plugins on and one
// off, a plugin synced from claude.ai, and three personal skills.
func ownerClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude.json", `{"mcpServers": {
  "playwright": {"type": "stdio", "command": "npx", "args": ["@playwright/mcp@latest"]},
  "chrome-devtools": {"type": "stdio", "command": "npx", "args": ["chrome-devtools-mcp@latest"]},
  "openaiDeveloperDocs": {"type": "http", "url": "https://developers.openai.com/mcp"}
}}`)
	write("settings.json", `{"enabledPlugins": {"frontend-design@claude-plugins-official": true, "pyright-lsp@claude-plugins-official": true, "old@x": false}}`)
	write("plugins/synced/acct/manifest.json", `{"plugins": [{"name": "ui-ux-pro-max"}]}`)
	for _, s := range []string{"dxverify", "humanizer", "stageshow"} {
		write("skills/"+s+"/SKILL.md", "---\nname: "+s+"\n---\n")
	}
	write("skills/not-a-skill/README.md", "no SKILL.md here")
	return dir
}

// workerProject is connectorProject with a UI issue and workers: naming
// chrome-devtools for system UI and a server the owner does not have.
func workerProject(t *testing.T) (*tracker.Project, *tracker.WorkflowConfig) {
	t.Helper()
	proj := connectorProject(t)
	os.WriteFile(filepath.Join(proj.IssueDir, "screen.md"), []byte("---\ntitle: \"screen\"\nstatus: \"backlog\"\nsystem: \"UI\"\n---\n"), 0o644)
	os.WriteFile(filepath.Join(proj.IssueDir, "labelled.md"), []byte("---\ntitle: \"labelled\"\nstatus: \"backlog\"\nsystem: \"Engine\"\nlabels: [ui]\n---\n"), 0o644)
	wf := &tracker.WorkflowConfig{Workers: &tracker.WorkersConfig{
		MCP: []tracker.WorkerMCP{
			{Server: "chrome-devtools", Systems: []string{"UI"}, Labels: []string{"ui"}},
			{Server: "no-such-server", Systems: []string{"UI"}},
		},
		Plugins: []string{"frontend-design@claude-plugins-official"},
		Skills:  []tracker.WorkerSkill{{Skill: "stageshow"}},
	}}
	return proj, wf
}

func readServers(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("mcp.json: %v\n%s", err, b)
	}
	return f.MCPServers
}

func keys(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// A lean worker's launch names its MCP servers strictly: only its
// department's connectors and the workers: mcp: servers its issue matches,
// claude.ai connectors off; a named server the owner lacks is a warning.
func TestWorkerLaunchStrictMCP(t *testing.T) {
	ownerClaude(t)
	proj, wf := workerProject(t)
	env := []string{"ISSUE_VIEWER_ISSUE_SLUG=x", "ISSUE_CLI_LOG=/l/x.clilog"}

	for _, c := range []struct {
		slug, name string
		want       []string
		missing    bool
	}{
		{"screen", "system UI", []string{"chrome-devtools"}, true},
		{"labelled", "label ui", []string{"chrome-devtools"}, false},
		{"bug", "Product, no match", nil, false},
		{"page", "Marketing worker", []string{"gsc"}, false},
	} {
		dir := t.TempDir()
		var steps []Step
		spec := Spec{Session: "agent-" + c.slug, IssueSlug: c.slug, AgentType: "claude", Workflow: wf}
		if !LeanWorker(spec) {
			t.Fatalf("%s: not a lean worker", c.name)
		}
		ws := WorkerSetupFlags(proj, spec, proj.WorkDir, dir, env, &steps)
		mcp := filepath.Join(dir, "mcp.json")
		if len(ws.Flags) < 2 || ws.Flags[0] != "--strict-mcp-config" || ws.Flags[1] != "--mcp-config="+mcp {
			t.Fatalf("%s: flags = %q", c.name, ws.Flags)
		}
		if got := keys(readServers(t, mcp)); !slices.Equal(got, c.want) {
			t.Errorf("%s: servers = %v, want %v", c.name, got, c.want)
		}
		if !slices.Contains(ws.Env, "ENABLE_CLAUDEAI_MCP_SERVERS=false") {
			t.Errorf("%s: env = %v", c.name, ws.Env)
		}
		if fi, _ := os.Stat(mcp); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: mcp.json mode %v", c.name, fi.Mode())
		}
		names := stepNames(steps)
		if !strings.Contains(names, "Worker setup (workers:)") {
			t.Errorf("%s: steps %s", c.name, names)
		}
		if got := strings.Contains(names, "Worker MCP server not found: no-such-server"); got != c.missing {
			t.Errorf("%s: missing-server warning %v, steps %s", c.name, got, names)
		}
	}

	// The copied definition is the owner's, as written; a connector keeps
	// its secret out (issue-cli connector exec) and its deny list.
	dir := t.TempDir()
	ws := WorkerSetupFlags(proj, Spec{IssueSlug: "screen", AgentType: "claude", Workflow: wf}, proj.WorkDir, dir, env, new([]Step))
	if got := string(readServers(t, filepath.Join(dir, "mcp.json"))["chrome-devtools"]); !strings.Contains(got, "chrome-devtools-mcp@latest") {
		t.Errorf("chrome-devtools = %s", got)
	}
	proj.Departments[0].Connectors[0].DenyTools = []string{"submit"}
	dir = t.TempDir()
	ws = WorkerSetupFlags(proj, Spec{IssueSlug: "page", AgentType: "claude", Workflow: wf}, proj.WorkDir, dir, env, new([]Step))
	if !slices.Contains(ws.Flags, "--disallowedTools=mcp__gsc__submit,Workflow,SendFeedback,ReportFindings,ListAgents") {
		t.Errorf("flags = %q", ws.Flags)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "mcp.json"))
	if strings.Contains(string(b), fakeValue) || !strings.Contains(string(b), `"marketing/gsc"`) {
		t.Errorf("mcp.json:\n%s", b)
	}

	// No workers: block at all is lean with nothing added.
	dir = t.TempDir()
	WorkerSetupFlags(proj, Spec{IssueSlug: "screen", AgentType: "claude"}, proj.WorkDir, dir, env, new([]Step))
	if got := keys(readServers(t, filepath.Join(dir, "mcp.json"))); len(got) != 0 {
		t.Errorf("no workers: block: servers %v", got)
	}
}

// The project's .mcp.json and its local entry in ~/.claude.json are the
// owner's servers too, local over project over user as in Claude Code.
func TestOwnerMCPServersScopes(t *testing.T) {
	cfg := ownerClaude(t)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers": {"proj": {"command": "p"}, "playwright": {"command": "from-project"}}}`), 0o644)
	state := `{"mcpServers": {"playwright": {"command": "from-user"}}, "projects": {"` + root + `": {"mcpServers": {"playwright": {"command": "from-local"}}}}}`
	os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(state), 0o644)
	got := connectors.OwnerMCPServers(root, root)
	if !strings.Contains(string(got["playwright"]), "from-local") || got["proj"] == nil {
		t.Errorf("servers = %v", got)
	}
}

// Every plugin and personal skill the owner has on is named off in the
// session's settings, except what workers: keeps.
func TestWorkerSetupTurnsOffPluginsAndSkills(t *testing.T) {
	ownerClaude(t)
	proj, wf := workerProject(t)
	os.MkdirAll(filepath.Join(proj.WorkDir, ".claude", "skills", "projskill"), 0o755)
	os.WriteFile(filepath.Join(proj.WorkDir, ".claude", "skills", "projskill", "SKILL.md"), []byte("x"), 0o644)

	dir := t.TempDir()
	var steps []Step
	ws := WorkerSetupFlags(proj, Spec{IssueSlug: "bug", AgentType: "claude", Workflow: wf}, proj.WorkDir, dir, nil, &steps)
	path := filepath.Join(dir, "settings.json")
	if !slices.Contains(ws.Flags, "--settings="+path) {
		t.Fatalf("flags = %q", ws.Flags)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		EnabledPlugins map[string]bool   `json:"enabledPlugins"`
		SkillOverrides map[string]string `json:"skillOverrides"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	wantPlugins := map[string]bool{"pyright-lsp@claude-plugins-official": false, "ui-ux-pro-max@synced": false}
	if len(doc.EnabledPlugins) != len(wantPlugins) {
		t.Errorf("enabledPlugins = %v, want %v", doc.EnabledPlugins, wantPlugins)
	}
	for id, v := range wantPlugins {
		if got, ok := doc.EnabledPlugins[id]; !ok || got != v {
			t.Errorf("enabledPlugins[%s] = %v, %v", id, got, ok)
		}
	}
	wantSkills := map[string]string{"dxverify": "off", "humanizer": "off", "projskill": "off"}
	if len(doc.SkillOverrides) != len(wantSkills) {
		t.Errorf("skillOverrides = %v, want %v", doc.SkillOverrides, wantSkills)
	}
	for k, v := range wantSkills {
		if doc.SkillOverrides[k] != v {
			t.Errorf("skillOverrides[%s] = %q", k, doc.SkillOverrides[k])
		}
	}
	if !strings.Contains(stepNames(steps), "plugins off 2, skills off 3") {
		t.Errorf("steps %s", stepNames(steps))
	}

	// The read nudge (spend.ReadNudge) runs after every tool call.
	var hooks struct {
		Hooks struct {
			Post []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct{ Type, Command string }
			} `json:"PostToolUse"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &hooks) != nil || len(hooks.Hooks.Post) != 1 || hooks.Hooks.Post[0].Matcher != "" ||
		len(hooks.Hooks.Post[0].Hooks) != 1 || hooks.Hooks.Post[0].Hooks[0].Command != ReadNudgeCommand {
		t.Errorf("hooks = %s", b)
	}
	if !strings.Contains(stepNames(steps), "claude.ai connectors off, read nudge on") {
		t.Errorf("steps %s", stepNames(steps))
	}

	// Nothing on: the settings only turn the bundled skills off, and carry
	// the read nudge.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	bare := &tracker.Project{Slug: "b", IssueDir: proj.IssueDir, WorkDir: t.TempDir()}
	dir = filepath.Join(t.TempDir(), "agent-bug") // no earlier worker's settings beside it
	os.Mkdir(dir, 0o755)
	WorkerSetupFlags(bare, Spec{IssueSlug: "bug", AgentType: "claude"}, bare.WorkDir, dir, nil, new([]Step))
	if b, _ := os.ReadFile(filepath.Join(dir, "settings.json")); strings.Join(strings.Fields(string(b)), "") != `{"disableBundledSkills":true,"hooks":{"PostToolUse":[{"hooks":[{"command":"issue-clihookread-nudge","timeout":5,"type":"command"}]}]}}` {
		t.Errorf("settings = %s", b)
	}
}
