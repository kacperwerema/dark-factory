package briefing

import (
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// fakeSecrets makes SecretPresent / SecretValue read from set.
func fakeSecrets(t *testing.T, set map[string]string) {
	t.Helper()
	present, value := tracker.SecretPresent, tracker.SecretValue
	tracker.SecretPresent = func(r tracker.SecretRef) bool { _, ok := set[r.String()]; return ok }
	tracker.SecretValue = func(r tracker.SecretRef) (string, bool) { v, ok := set[r.String()]; return v, ok }
	t.Cleanup(func() { tracker.SecretPresent, tracker.SecretValue = present, value })
}

func connectorProject() *tracker.Project {
	return &tracker.Project{Name: "Acme", Slug: "acme", Departments: []tracker.Department{{
		Name: "Marketing", Systems: []string{"SEO"},
		Connectors: []tracker.Connector{
			{Name: "search-console", Kind: tracker.ConnectorMCP, Command: "gsc-mcp", Secrets: map[string]string{"GSC_TOKEN": "keychain:acme-gsc"}},
			{Name: "wordpress", Kind: tracker.ConnectorTool, Sends: true, Secrets: map[string]string{"WP_PASSWORD": "env:ACME_WP"}},
			{Name: "quickbooks", Kind: tracker.ConnectorMCP, Command: "qb", Secrets: map[string]string{"QB_TOKEN": "env:ACME_QB", "QB_ID": "raw-value-here"}},
		},
	}, {Name: "Sales"}}}
}

func TestConnectorsBriefAndLine(t *testing.T) {
	fakeSecrets(t, map[string]string{"keychain:acme-gsc": "v1", "env:ACME_WP": "v2"})
	p := connectorProject()
	got := tracker.ConnectorsBrief(p, "Marketing", false)
	for _, want := range []string{
		"- Connectors: search-console (MCP, loaded in this session)",
		"wordpress (tool: issue-cli connector exec marketing/wordpress -- <cmd>) — it sends: use it only for an output the owner approved, then issue-cli output publish",
		"quickbooks: secret QB_ID, QB_TOKEN missing on this machine, ask your lead (issue-cli connectors marketing)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("brief missing %q:\n%s", want, got)
		}
	}
	if lead := tracker.ConnectorsBrief(p, "Marketing", true); !strings.Contains(lead, "ask the owner") {
		t.Errorf("lead brief = %s", lead)
	}
	if s := tracker.ConnectorsBrief(p, "Sales", false) + tracker.ConnectorsBrief(p, "", false); s != "" {
		t.Errorf("no connectors gave %q", s)
	}
	if l := tracker.ConnectorsLine(p, "Marketing"); l != "Connectors: 3 (1 missing) — issue-cli connectors marketing" {
		t.Errorf("line = %q", l)
	}
	if l := tracker.ConnectorsLine(p, ""); l != "" {
		t.Errorf("Product line = %q", l)
	}

	// The worker's ## Your department carries it; Product's issues get none.
	sec := tracker.DepartmentPromptSection(p, &tracker.Issue{Slug: "seo/page", System: "SEO"})
	if !strings.Contains(sec, "- Connectors: search-console") {
		t.Errorf("department section:\n%s", sec)
	}
	lead := DepartmentLeadBrief(nil, &p.Departments[0], LeadBriefInput{Project: p})
	if !strings.Contains(lead, "- Connectors: search-console") {
		t.Errorf("lead brief:\n%s", lead)
	}
}
