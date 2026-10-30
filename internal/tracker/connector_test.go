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
		"env:1BAD":             "env: wants a variable name",
		"env:":                 "env: wants a variable name",
		"keychain:":            "keychain: wants a service name",
		"vault:prod/acme":      "not env:<VAR> or keychain:<service>",
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

func TestConnectorsBriefAndLine(t *testing.T) {
	fakeSecrets(t, map[string]string{"keychain:acme-gsc": "v1", "env:ACME_WP": "v2"})
	p := connectorProject()
	got := ConnectorsBrief(p, "Marketing", false)
	for _, want := range []string{
		"- Connectors: search-console (MCP, loaded in this session)",
		"wordpress (tool: issue-cli connector exec marketing/wordpress -- <cmd>) — it sends: use it only for an output the owner approved, then issue-cli output publish",
		"quickbooks: secret QB_ID, QB_TOKEN missing on this machine, ask your lead (issue-cli connectors marketing)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("brief missing %q:\n%s", want, got)
		}
	}
	if lead := ConnectorsBrief(p, "Marketing", true); !strings.Contains(lead, "ask the owner") {
		t.Errorf("lead brief = %s", lead)
	}
	if s := ConnectorsBrief(p, "Sales", false) + ConnectorsBrief(p, "", false); s != "" {
		t.Errorf("no connectors gave %q", s)
	}
	if l := ConnectorsLine(p, "Marketing"); l != "Connectors: 3 (1 missing) — issue-cli connectors marketing" {
		t.Errorf("line = %q", l)
	}
	if l := ConnectorsLine(p, ""); l != "" {
		t.Errorf("Product line = %q", l)
	}

	// The worker's ## Your department carries it; Product's issues get none.
	sec := DepartmentPromptSection(p, &Issue{Slug: "seo/page", System: "SEO"})
	if !strings.Contains(sec, "- Connectors: search-console") {
		t.Errorf("department section:\n%s", sec)
	}
	lead := DepartmentLeadBrief(nil, &p.Departments[0], LeadBriefInput{Project: p})
	if !strings.Contains(lead, "- Connectors: search-console") {
		t.Errorf("lead brief:\n%s", lead)
	}
}
