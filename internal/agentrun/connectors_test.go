package agentrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const fakeValue = "fake-secret-value-7f3a"

func connectorProject(t *testing.T) *tracker.Project {
	t.Helper()
	present, value := tracker.SecretPresent, tracker.SecretValue
	tracker.SecretPresent = func(r tracker.SecretRef) bool { return r.Name == "ACME_GSC" }
	tracker.SecretValue = func(r tracker.SecretRef) (string, bool) { return fakeValue, r.Name == "ACME_GSC" }
	t.Cleanup(func() { tracker.SecretPresent, tracker.SecretValue = present, value })
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	os.MkdirAll(issues, 0755)
	for name, sys := range map[string]string{"page": "SEO", "bug": "Engine"} {
		os.WriteFile(filepath.Join(issues, name+".md"), []byte("---\ntitle: \""+name+"\"\nstatus: \"backlog\"\nsystem: \""+sys+"\"\n---\n"), 0644)
	}
	return &tracker.Project{Slug: "acme", IssueDir: issues, WorkDir: root, Departments: []tracker.Department{{
		Name: "Marketing", Systems: []string{"SEO"}, Connectors: []tracker.Connector{
			{Name: "gsc", Kind: tracker.ConnectorMCP, Command: "gsc-mcp", Secrets: map[string]string{"GSC_TOKEN": "env:ACME_GSC"}},
			{Name: "qb", Kind: tracker.ConnectorMCP, Command: "qb", Secrets: map[string]string{"QB_TOKEN": "env:ACME_QB"}},
			{Name: "wp", Kind: tracker.ConnectorTool, Command: "wp"},
		},
	}}}
}

func TestConnectorFlags(t *testing.T) {
	proj := connectorProject(t)
	env := []string{"ISSUE_VIEWER_ISSUE_SLUG=page", "ISSUE_CLI_LOG=/l/x.clilog", "ISSUE_VIEWER_SERVER_PWD=/srv", "HOME=/h"}

	for name, spec := range map[string]Spec{
		"worker": {Session: "agent-page", IssueSlug: "page", AgentType: "claude"},
		"lead":   {Session: "lead-acme-marketing", LeadScope: "department:marketing", AgentType: "claude"},
	} {
		dir := t.TempDir()
		var steps []Step
		flags := ConnectorFlags(proj, spec, dir, env, &steps)
		path := filepath.Join(dir, "mcp.json")
		if strings.Join(flags, " ") != "--mcp-config "+path {
			t.Fatalf("%s: flags = %v", name, flags)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg := string(b)
		for _, want := range []string{`"gsc"`, `"command": "issue-cli"`, `"marketing/gsc"`, `"ISSUE_VIEWER_ISSUE_SLUG": "page"`, `"ISSUE_CLI_LOG": "/l/x.clilog"`} {
			if !strings.Contains(cfg, want) {
				t.Errorf("%s: mcp.json missing %s:\n%s", name, want, cfg)
			}
		}
		for _, not := range []string{fakeValue, `"qb"`, `"wp"`, "HOME"} {
			if strings.Contains(cfg, not) {
				t.Errorf("%s: mcp.json has %s:\n%s", name, not, cfg)
			}
		}
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0600 {
			t.Errorf("mcp.json mode %v", fi.Mode())
		}
		if !strings.Contains(stepNames(steps), "Marketing connectors: gsc") {
			t.Errorf("%s: steps %s", name, stepNames(steps))
		}
		launch := LaunchCommandWith(proj, "claude", spec.Session, "/p", flags)
		if !strings.Contains(launch, " --mcp-config "+shellWord(path)+" ") {
			t.Errorf("%s: launch = %s", name, launch)
		}
	}

	codex := ConnectorFlags(proj, Spec{IssueSlug: "page", AgentType: "codex"}, t.TempDir(), env, new([]Step))
	if !strings.Contains(strings.Join(codex, " "), `mcp_servers.gsc.command="issue-cli"`) {
		t.Errorf("codex flags = %v", codex)
	}

	// Product's workers, the CEO and agents with no issue get nothing.
	for name, spec := range map[string]Spec{
		"product": {IssueSlug: "bug", AgentType: "claude"},
		"ceo":     {LeadScope: tracker.LeadScopeProject, AgentType: "claude"},
		"review":  {Session: "review-acme", AgentType: "claude"},
	} {
		dir := t.TempDir()
		if f := ConnectorFlags(proj, spec, dir, env, new([]Step)); f != nil {
			t.Errorf("%s: flags = %v", name, f)
		}
		if _, err := os.Stat(filepath.Join(dir, "mcp.json")); err == nil {
			t.Errorf("%s: wrote mcp.json", name)
		}
	}
	if got := LaunchCommand(proj, "claude", "agent-x", "/p"); strings.Contains(got, "mcp") {
		t.Errorf("LaunchCommand = %s", got)
	}
}
