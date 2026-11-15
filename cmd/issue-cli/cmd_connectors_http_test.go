package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/connectors/connectortest"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// fakeHeaderValue stands in for an API key sent as a header: it may only
// reach the stub server's requests.
const fakeHeaderValue = "fake-http-key-c41d"

// httpConnectorFixture adds marketing/openseo to connectorFixture: an mcp
// connector with url: at a stub Streamable HTTP server and an
// Authorization header from env:ACME_SEO.
func httpConnectorFixture(t *testing.T) (root string, stub *connectortest.Server) {
	t.Helper()
	root = connectorFixture(t)
	stub = connectortest.New(t, "Bearer "+fakeHeaderValue)
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+`          - name: openseo
            kind: mcp
            url: `+stub.MCPURL()+`
            headers:
              Authorization: "Bearer env:ACME_SEO"
`)
	t.Setenv("ACME_SEO", fakeHeaderValue)
	return root, stub
}

// launchMCP is what a claude worker on seo/page is launched with: the
// launch's mcp.json, read back.
func launchMCP(t *testing.T, root string) (path string, servers map[string]struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}) {
	t.Helper()
	projects, err := tracker.LoadProjects(os.Getenv("ISSUE_VIEWER_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	proj := &projects[0]
	sessionDir := filepath.Join(root, ".agent-logs", "agent-seo-page")
	os.MkdirAll(sessionDir, 0755)
	flags := agentrun.ConnectorFlags(proj, agentrun.Spec{IssueSlug: "seo/page", AgentType: "claude"}, sessionDir, []string{"ISSUE_VIEWER_ISSUE_SLUG=seo/page"}, new([]agentrun.Step))
	if len(flags) == 0 || !strings.HasPrefix(flags[0], "--mcp-config=") {
		t.Fatalf("flags = %v", flags)
	}
	mcpPath := strings.TrimPrefix(flags[0], "--mcp-config=")
	b, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("mcp.json: %v\n%s", err, b)
	}
	return mcpPath, cfg.MCPServers
}

// runMCPServer runs the mcp.json entry name as the agent's MCP client
// would: its command (issue-cli, run in process) with its env, the
// client's messages on stdin.
func runMCPServer(t *testing.T, args []string, env map[string]string, stdin string) (string, error) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	l := newCallLog(args, time.Now())
	inv := &invocation{}
	var out, errw strings.Builder
	err := runTraced(args, strings.NewReader(stdin), &out, &errw, inv)
	l.write(inv, err, false)
	if err != nil {
		return out.String() + errw.String() + err.Error(), err
	}
	return out.String() + errw.String(), nil
}

const (
	clientInit   = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"1"}}}`
	clientInited = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	clientList   = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
)

// AC1: an agent launched with an HTTP MCP connector lists the remote
// server's tools through connector exec: the launch's mcp.json names
// issue-cli connector exec, and that command, fed the client's messages,
// answers tools/list with the stub's tools.
func TestHTTPConnectorListsToolsThroughExec(t *testing.T) {
	root, stub := httpConnectorFixture(t)
	_, servers := launchMCP(t, root)
	srv, ok := servers["openseo"]
	if !ok {
		t.Fatalf("mcp.json has no openseo: %v", servers)
	}
	if srv.Command != "issue-cli" || strings.Join(srv.Args, " ") != "--project demo connector exec marketing/openseo" {
		t.Fatalf("openseo server = %+v", srv)
	}
	out, err := runMCPServer(t, srv.Args, srv.Env, clientInit+"\n"+clientInited+"\n"+clientList+"\n")
	if err != nil {
		t.Fatalf("exec: %s", out)
	}
	var list struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var head struct {
			ID float64 `json:"id"`
		}
		json.Unmarshal([]byte(line), &head)
		if head.ID == 2 && json.Unmarshal([]byte(line), &list) == nil && len(list.Result.Tools) == 1 && list.Result.Tools[0].Name == connectortest.ToolName {
			found = true
		}
	}
	if !found {
		t.Fatalf("tools/list did not list %s:\n%s", connectortest.ToolName, out)
	}
	if reqs := stub.Requests(); len(reqs) < 3 || reqs[2].RPC != "tools/list" || reqs[2].Session != connectortest.Session {
		t.Errorf("requests = %+v", reqs)
	}

	// The same guard as a stdio connector: a Product worker is refused.
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "cli/sample")
	if out, err := runMCPServer(t, srv.Args, nil, clientInit+"\n"); err == nil || !strings.Contains(out, "this connector belongs to Marketing") {
		t.Errorf("Product worker: %s", out)
	}
	// No command after --, and a missing header secret stops it first.
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo/page")
	if out, err := runCLI(t, "connector", "exec", "marketing/openseo", "--", "sh"); err == nil || !strings.Contains(out, "bridges it itself") {
		t.Errorf("command after --: %s", out)
	}
	t.Setenv("ACME_SEO", "")
	if out, err := runCLI(t, "connector", "exec", "marketing/openseo"); err == nil || !strings.Contains(out, "header Authorization (Bearer env:ACME_SEO) is missing on this machine") {
		t.Errorf("missing header secret: %s", out)
	}
}

// AC2: the secret header is in the stub's requests and in no file the
// engine writes: mcp.json, the clilog, the checks log, issue files, the
// listing, and every command's output.
func TestHTTPConnectorSecretNeverWritten(t *testing.T) {
	root, stub := httpConnectorFixture(t)
	t.Setenv("ISSUE_CLI_LOG", filepath.Join(root, ".agent-logs", "agent-seo-page", "agent-seo-page.clilog"))
	var printed strings.Builder
	run := func(args ...string) {
		out, _ := runCLI(t, args...)
		printed.WriteString(out)
	}
	run("connectors")
	run("connectors", "--json")
	run("connector", "check", "marketing/openseo")
	run("health", "lint")
	mcpPath, servers := launchMCP(t, root)
	srv := servers["openseo"]
	out, err := runMCPServer(t, srv.Args, srv.Env, clientInit+"\n"+clientInited+"\n"+clientList+"\n")
	if err != nil {
		t.Fatalf("exec: %s", out)
	}
	printed.WriteString(out)

	reqs := stub.Requests()
	if len(reqs) < 3 {
		t.Fatalf("requests = %+v", reqs)
	}
	for _, r := range reqs {
		if r.Header.Get("Authorization") != "Bearer "+fakeHeaderValue {
			t.Errorf("%s %s went without the header", r.Method, r.RPC)
		}
	}
	if strings.Contains(printed.String(), fakeHeaderValue) {
		t.Errorf("printed output holds the secret:\n%s", printed.String())
	}
	assertContains(t, printed.String(), "  header Authorization: Bearer env:ACME_SEO set")
	seen := 0
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		seen++
		if b, _ := os.ReadFile(path); strings.Contains(string(b), fakeHeaderValue) {
			t.Errorf("%s holds the secret", path)
		}
		return nil
	})
	for _, want := range []string{mcpPath, filepath.Join(root, "issues", ".connectors", "checks.jsonl"), os.Getenv("ISSUE_CLI_LOG")} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected %s to be written and scanned: %v", want, err)
		}
	}
	if seen < 5 {
		t.Errorf("scanned only %d files", seen)
	}
}

// A url: connector with per_run: holds its tool for the bridge's life, and
// the launch gives the agent CLI time for the start.
func TestConnectorBridgeHoldsAPerRunTool(t *testing.T) {
	root, _ := httpConnectorFixture(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	up := filepath.Join(root, "up")
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+`            per_run:
              start: 'echo x >> `+up+`.starts; touch `+up+`'
              ready: 'test -f `+up+`'
              stop: 'rm -f `+up+`'
              timeout: 40
`)
	out, err := runCLI(t, "connector", "exec", "marketing/openseo", "--as-owner")
	if err != nil {
		t.Fatal(out)
	}
	if !fileExists(up+".starts") || fileExists(up) {
		t.Errorf("the bridge's run: started %v, still up %v", fileExists(up+".starts"), fileExists(up))
	}
	if out, err := runCLI(t, "connector", "exec", "marketing/openseo", "--as-owner", "--", "x"); err == nil || !strings.Contains(out, "no command after --") {
		t.Errorf("a command after -- on a url: connector: %s", out)
	}
	b, _ = os.ReadFile(up + ".starts")
	if strings.Count(string(b), "\n") != 1 {
		t.Errorf("a refused call started the tool: %q", b)
	}

	projects, err := tracker.LoadProjects(cfg)
	if err != nil {
		t.Fatal(err)
	}
	spec := agentrun.Spec{IssueSlug: "seo/page", AgentType: "claude"}
	var steps []agentrun.Step
	sessionDir := filepath.Join(root, ".agent-logs", "s")
	os.MkdirAll(sessionDir, 0755)
	agentrun.ConnectorFlags(&projects[0], spec, sessionDir, nil, &steps)
	if len(steps) != 1 || !strings.Contains(steps[0].Name, "openseo (starts per run)") {
		t.Errorf("steps = %+v", steps)
	}
	env := agentrun.ConnectorEnv(&projects[0], spec)
	if strings.Join(env, " ") != "MCP_TIMEOUT=50000" {
		t.Errorf("claude env = %v", env)
	}
	spec.AgentType = "codex"
	if env := agentrun.ConnectorEnv(&projects[0], spec); env != nil {
		t.Errorf("codex env = %v", env)
	}
}
