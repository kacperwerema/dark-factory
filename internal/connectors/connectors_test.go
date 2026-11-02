package connectors

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// fakeValue stands in for a real credential; a test fails if it shows up
// anywhere but the child's environment.
const fakeValue = "fake-secret-value-7f3a"

func fakeSecrets(t *testing.T, set map[string]string) {
	t.Helper()
	present, value := tracker.SecretPresent, tracker.SecretValue
	tracker.SecretPresent = func(r tracker.SecretRef) bool { _, ok := set[r.String()]; return ok }
	tracker.SecretValue = func(r tracker.SecretRef) (string, bool) { v, ok := set[r.String()]; return v, ok }
	t.Cleanup(func() { tracker.SecretPresent, tracker.SecretValue = present, value })
}

func project(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0755); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{Name: "Acme", Slug: "acme", IssueDir: issues, WorkDir: root, Departments: []tracker.Department{{
		Name: "Marketing",
		Connectors: []tracker.Connector{
			{Name: "gsc", Kind: tracker.ConnectorMCP, Command: "gsc-mcp", Args: []string{"--stdio"}, Secrets: map[string]string{"GSC_TOKEN": "env:ACME_GSC"}, Check: `test "$GSC_TOKEN" = "` + fakeValue + `"`},
			{Name: "wp", Kind: tracker.ConnectorTool, Secrets: map[string]string{"WP_PASSWORD": "keychain:acme-wp"}, Check: "exit 3", Sends: true},
			{Name: "qb", Kind: tracker.ConnectorMCP, Command: "qb", Secrets: map[string]string{"QB_TOKEN": "env:ACME_QB"}, Check: "true"},
			{Name: "plain", Kind: tracker.ConnectorTool, Command: "echo"},
		},
	}, {Name: "Sales"}}}
}

func TestFind(t *testing.T) {
	p := project(t)
	d, c, err := Find(p, "marketing/wp")
	if err != nil || d.Name != "Marketing" || c.Name != "wp" {
		t.Fatalf("Find = %v %v %v", d, c, err)
	}
	for addr, want := range map[string]string{
		"wp":            "name a connector as <department>/<name>",
		"ops/wp":        `no department "ops"`,
		"sales/wp":      "Sales has no connectors",
		"marketing/foo": "it has: marketing/gsc, marketing/wp, marketing/qb, marketing/plain",
	} {
		if _, _, err := Find(p, addr); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Find(%q) err = %v, want %q", addr, err, want)
		}
	}
}

func TestAllowed(t *testing.T) {
	p := project(t)
	mk := &p.Departments[0]
	if err := Allowed(p, mk, Caller{Department: "Marketing", Agent: true}); err != nil {
		t.Errorf("Marketing worker refused: %v", err)
	}
	if err := Allowed(p, mk, Caller{Department: "marketing", Lead: true, Agent: true}); err != nil {
		t.Errorf("Marketing lead refused: %v", err)
	}
	if err := Allowed(p, mk, Caller{Owner: true}); err != nil {
		t.Errorf("owner refused: %v", err)
	}
	for name, c := range map[string]struct {
		caller Caller
		want   string
	}{
		"sales":   {Caller{Department: "Sales", Agent: true}, "belongs to Marketing; a Sales agent may not use it. Ask Marketing's lead lead-acme-marketing"},
		"product": {Caller{Agent: true}, "a Product agent may not use it"},
		"ceo":     {Caller{Lead: true, Agent: true}, "the CEO does not use Marketing's connectors"},
		"shell":   {Caller{}, "add --as-owner in your own shell"},
	} {
		if err := Allowed(p, mk, c.caller); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestEnvAndCommand(t *testing.T) {
	fakeSecrets(t, map[string]string{"env:ACME_GSC": fakeValue})
	p := project(t)
	cs := p.Departments[0].Connectors
	env, err := Env(&cs[0], []string{"PATH=/bin"})
	if err != nil || strings.Join(env, " ") != "PATH=/bin GSC_TOKEN="+fakeValue {
		t.Errorf("Env = %v, %v", env, err)
	}
	_, err = Env(&cs[1], nil)
	if err == nil || !strings.Contains(err.Error(), "secret WP_PASSWORD (keychain:acme-wp) is missing on this machine: the owner adds it with security add-generic-password -s acme-wp -w") {
		t.Errorf("missing keychain err = %v", err)
	}
	_, err = Env(&cs[2], nil)
	if err == nil || !strings.Contains(err.Error(), "the owner exports ACME_QB") {
		t.Errorf("missing env err = %v", err)
	}

	if argv, _ := Command(&cs[0], nil); strings.Join(argv, " ") != "gsc-mcp --stdio" {
		t.Errorf("mcp argv = %v", argv)
	}
	if argv, _ := Command(&cs[1], []string{"wp", "post", "list"}); strings.Join(argv, " ") != "wp post list" {
		t.Errorf("tool argv = %v", argv)
	}
	if _, err := Command(&cs[1], nil); err == nil || !strings.Contains(err.Error(), "has no command:") {
		t.Errorf("tool without command err = %v", err)
	}
	if argv, _ := Command(&cs[3], nil); strings.Join(argv, " ") != "echo" {
		t.Errorf("tool command argv = %v", argv)
	}
}

func TestChecksAndList(t *testing.T) {
	fakeSecrets(t, map[string]string{"env:ACME_GSC": fakeValue, "env:ACME_QB": "x"})
	p := project(t)
	cs := p.Departments[0].Connectors
	now := func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }
	var recs []Record
	for i := range cs {
		recs = append(recs, RunCheck(context.Background(), "Marketing", &cs[i], now))
	}
	got := []string{}
	for _, r := range recs {
		got = append(got, r.Connector+"="+r.Result)
	}
	if want := "marketing/gsc=ok marketing/wp=missing marketing/qb=ok marketing/plain=no-check"; strings.Join(got, " ") != want {
		t.Errorf("checks = %v, want %s", got, want)
	}

	// A failing check keeps its exit code; a slow one times out.
	fakeSecrets(t, map[string]string{"keychain:acme-wp": fakeValue})
	if r := RunCheck(context.Background(), "Marketing", &cs[1], now); r.Result != ResultFailing || r.Exit != 3 {
		t.Errorf("failing check = %+v", r)
	}
	orig := CheckTimeout
	CheckTimeout = 200 * time.Millisecond
	t.Cleanup(func() { CheckTimeout = orig })
	slow := tracker.Connector{Name: "slow", Check: "sleep 5"}
	start := time.Now()
	if r := RunCheck(context.Background(), "Marketing", &slow, now); r.Result != ResultFailing || r.Exit != -1 || time.Since(start) > 3*time.Second {
		t.Errorf("slow check = %+v after %s", r, time.Since(start))
	}

	if err := Append(p, recs...); err != nil {
		t.Fatal(err)
	}
	later := Record{Connector: "marketing/gsc", Date: now().Add(time.Hour), Result: ResultFailing, Exit: 1}
	if err := Append(p, later); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(LogPath(p), os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("not json\n")
	f.Close()
	checks := LoadChecks(p)
	if checks["marketing/gsc"].Result != ResultFailing || len(checks) != 4 {
		t.Errorf("LoadChecks = %+v", checks)
	}
	b, _ := os.ReadFile(LogPath(p))
	if strings.Contains(string(b), fakeValue) {
		t.Errorf("the checks log holds the secret value:\n%s", b)
	}

	fakeSecrets(t, map[string]string{"env:ACME_GSC": fakeValue, "env:ACME_QB": "x"})
	list := List(p, "marketing")
	if len(list) != 4 || !list[0].Failing() || !list[1].Missing || !list[1].Failing() || list[2].Failing() || list[3].HasCheck {
		t.Errorf("List = %+v", list)
	}
	if len(List(p, "sales")) != 0 || len(List(p, "")) != 4 {
		t.Error("List by department")
	}
}

func TestServers(t *testing.T) {
	fakeSecrets(t, map[string]string{"env:ACME_GSC": fakeValue})
	p := project(t)
	servers := Servers(p, "Marketing", map[string]string{"ISSUE_VIEWER_ISSUE_SLUG": "seo/page"})
	if len(servers) != 1 || servers[0].Name != "gsc" || strings.Join(servers[0].Args, " ") != "--project acme connector exec marketing/gsc" {
		t.Fatalf("servers = %+v (qb's secret is missing, wp and plain are tools)", servers)
	}
	cfg := MCPConfig(servers)
	var parsed struct {
		MCPServers map[string]Server `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfg, &parsed); err != nil || parsed.MCPServers["gsc"].Command != "issue-cli" || parsed.MCPServers["gsc"].Env["ISSUE_VIEWER_ISSUE_SLUG"] != "seo/page" {
		t.Errorf("MCPConfig = %s (%v)", cfg, err)
	}
	flags := strings.Join(CodexFlags(servers), " ")
	for _, want := range []string{`-c mcp_servers.gsc.command="issue-cli"`, `-c mcp_servers.gsc.args=["--project","acme","connector","exec","marketing/gsc"]`, `mcp_servers.gsc.env.ISSUE_VIEWER_ISSUE_SLUG="seo/page"`} {
		if !strings.Contains(flags, want) {
			t.Errorf("codex flags missing %q: %s", want, flags)
		}
	}
	if strings.Contains(string(cfg)+flags, fakeValue) {
		t.Error("the MCP config holds the secret value")
	}
	if Servers(p, "", nil) != nil || Servers(p, "Sales", nil) != nil {
		t.Error("Product or Sales got servers")
	}
}
