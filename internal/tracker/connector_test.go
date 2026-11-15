package tracker

import (
	"strings"
	"testing"
)

func TestParseSecretRef(t *testing.T) {
	for raw, want := range map[string]string{
		"env:ACME_TOKEN":         "env:ACME_TOKEN",
		" keychain:acme-gsc ":    "keychain:acme-gsc",
		"keychain:acme-gsc/bot":  "keychain:acme-gsc/bot",
		"keychain:svc/":          "keychain:svc",
		"env:ACME_TOKEN_2":       "env:ACME_TOKEN_2",
		"keychain:a b/c":         "keychain:a b/c",
		"env:_LEADING_UNDERSCOR": "env:_LEADING_UNDERSCOR",
	} {
		ref, err := ParseSecretRef(raw)
		if err != nil || ref.String() != want {
			t.Errorf("ParseSecretRef(%q) = %q, %v; want %q", raw, ref, err, want)
		}
	}
	for raw, want := range map[string]string{
		"fake-pasted-value-123": "looks like a secret value",
		"env:1BAD":              "env: wants a variable name",
		"env:":                  "env: wants a variable name",
		"keychain:":             "keychain: wants a service name",
		"vault:prod/acme":       "not env:<VAR> or keychain:<service>",
	} {
		_, err := ParseSecretRef(raw)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseSecretRef(%q) err = %v; want %q", raw, err, want)
		}
		if err != nil && strings.Contains(err.Error(), raw) && raw != "env:" && raw != "keychain:" {
			t.Errorf("error repeats the raw value %q: %v", raw, err)
		}
	}
}

// fakeSecrets makes SecretPresent / SecretValue read from set.
func fakeSecrets(t *testing.T, set map[string]string) {
	t.Helper()
	present, value := SecretPresent, SecretValue
	SecretPresent = func(r SecretRef) bool { _, ok := set[r.String()]; return ok }
	SecretValue = func(r SecretRef) (string, bool) { v, ok := set[r.String()]; return v, ok }
	t.Cleanup(func() { SecretPresent, SecretValue = present, value })
}

func connectorProject() *Project {
	return &Project{Name: "Acme", Slug: "acme", Departments: []Department{{
		Name: "Marketing", Systems: []string{"SEO"},
		Connectors: []Connector{
			{Name: "search-console", Kind: ConnectorMCP, Command: "gsc-mcp", Secrets: map[string]string{"GSC_TOKEN": "keychain:acme-gsc"}},
			{Name: "wordpress", Kind: ConnectorTool, Sends: true, Secrets: map[string]string{"WP_PASSWORD": "env:ACME_WP"}},
			{Name: "quickbooks", Kind: ConnectorMCP, Command: "qb", Secrets: map[string]string{"QB_TOKEN": "env:ACME_QB", "QB_ID": "raw-value-here"}},
		},
	}, {Name: "Sales"}}}
}

func TestConnectorSecretStates(t *testing.T) {
	fakeSecrets(t, map[string]string{"keychain:acme-gsc": "v1", "env:ACME_WP": "v2"})
	p := connectorProject()
	cs := p.Departments[0].Connectors
	if m := cs[0].Missing(); len(m) != 0 {
		t.Errorf("search-console missing %v", m)
	}
	st := cs[2].SecretStates()
	if len(st) != 2 || st[0].Var != "QB_ID" || st[0].Ref != "(invalid)" || st[0].Err == "" || st[1].Var != "QB_TOKEN" || st[1].Set {
		t.Errorf("quickbooks states = %+v", st)
	}
	for _, s := range st {
		if strings.Contains(s.Ref+s.Err, "raw-value-here") {
			t.Errorf("state repeats a raw value: %+v", s)
		}
	}
}

func TestConnectorDeniedTools(t *testing.T) {
	fakeSecrets(t, map[string]string{"keychain:acme-gsc": "v1", "env:ACME_WP": "v2"})
	p := connectorProject()
	p.Departments[0].Connectors[0].DenyTools = []string{" submit_urls", "bad name", "share", "submit_urls"}
	if got := strings.Join(p.Departments[0].Connectors[0].DeniedTools(), ","); got != "submit_urls,share" {
		t.Errorf("DeniedTools = %q", got)
	}
	if got := ConnectorsBrief(p, "Marketing", false); !strings.Contains(got, "search-console (MCP, loaded in this session; tools off: submit_urls, share)") {
		t.Errorf("brief = %s", got)
	}
	tool := Connector{Kind: ConnectorTool, DenyTools: []string{"post"}}
	if got := tool.DeniedTools(); got != nil {
		t.Errorf("tool connector DeniedTools = %v", got)
	}
}

func TestParseHeaderValue(t *testing.T) {
	for raw, want := range map[string][2]string{
		"Bearer keychain:openseo": {"Bearer ", "keychain:openseo"},
		"env:ACME_KEY":            {"", "env:ACME_KEY"},
		"Token  keychain:svc/bot": {"Token  ", "keychain:svc/bot"},
		" Basic env:ACME_BASIC ":  {"Basic ", "env:ACME_BASIC"},
	} {
		hv, err := ParseHeaderValue(raw)
		if err != nil || hv.Prefix != want[0] || hv.Ref.String() != want[1] {
			t.Errorf("ParseHeaderValue(%q) = %q %q, %v; want %q", raw, hv.Prefix, hv.Ref, err, want)
		}
	}
	for _, raw := range []string{"Bearer fake-pasted-value-123", "fake-pasted-value-123", ""} {
		_, err := ParseHeaderValue(raw)
		if err == nil {
			t.Errorf("ParseHeaderValue(%q) accepted a bare value", raw)
		} else if raw != "" && strings.Contains(err.Error(), "fake-pasted") {
			t.Errorf("error repeats the value: %v", err)
		}
	}
}

func TestCheckConnectorURL(t *testing.T) {
	for _, ok := range []string{"https://mcp.example.com/mcp", "http://localhost:3011/mcp", "http://127.0.0.1:8080/mcp", "http://[::1]:9/mcp", "http://127.5.0.1/mcp"} {
		if err := CheckConnectorURL(ok); err != nil {
			t.Errorf("CheckConnectorURL(%q) = %v", ok, err)
		}
	}
	for raw, want := range map[string]string{
		"http://mcp.example.com/mcp":         "http is allowed only on this machine",
		"http://192.168.1.4:3011/mcp":        "http is allowed only on this machine",
		"ftp://localhost/mcp":                "wants https://",
		"localhost:3011/mcp":                 "not an absolute",
		"/mcp":                               "not an absolute",
		"https://user:pw-secret@example.com": "holds a user or password",
	} {
		err := CheckConnectorURL(raw)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("CheckConnectorURL(%q) = %v; want %q", raw, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "pw-secret") {
			t.Errorf("error repeats the password: %v", err)
		}
	}
}

func TestSecretStatesListHeaders(t *testing.T) {
	fakeSecrets(t, map[string]string{"env:ACME_SEO": "v"})
	c := Connector{Name: "openseo", Kind: ConnectorMCP, URL: "http://localhost:3011/mcp",
		Headers: map[string]string{"Authorization": "Bearer env:ACME_SEO", "X-Quota": "env:ACME_QUOTA_UNSET", "X-Bad": "pasted"}}
	got := c.SecretStates()
	if len(got) != 3 || !got[0].Header || got[0].Var != "Authorization" || got[0].Ref != "Bearer env:ACME_SEO" || !got[0].Set {
		t.Fatalf("states = %+v", got)
	}
	if got[1].Var != "X-Bad" || got[1].Err == "" || got[2].Var != "X-Quota" || got[2].Set {
		t.Errorf("states = %+v", got)
	}
	if miss := c.Missing(); strings.Join(miss, ",") != "X-Bad,X-Quota" {
		t.Errorf("missing = %v", miss)
	}
}

func TestConnectorsBriefNamesAPerRunTool(t *testing.T) {
	p := &Project{Departments: []Department{{Name: "Marketing", Connectors: []Connector{
		{Name: "heavy", Kind: ConnectorMCP, Command: "x", PerRun: &PerRun{Start: "a", Ready: "b", Stop: "c"}},
	}}}}
	got := ConnectorsBrief(p, "Marketing", false)
	for _, want := range []string{"heavy (MCP, loaded in this session)", "it starts per run, stopped after", "mem-guard held it"} {
		if !strings.Contains(got, want) {
			t.Errorf("brief lacks %q: %s", want, got)
		}
	}
	// A url: server starts at the first call, and says what a held one answers.
	p.Departments[0].Connectors[0].Command, p.Departments[0].Connectors[0].URL = "", "http://localhost:3011/mcp"
	got = ConnectorsBrief(p, "Marketing", false)
	for _, want := range []string{"it starts at your first call to one of its tools", `"stays stopped" means mem-guard held it`} {
		if !strings.Contains(got, want) {
			t.Errorf("url: brief lacks %q: %s", want, got)
		}
	}
}
