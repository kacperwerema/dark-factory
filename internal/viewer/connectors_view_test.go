package viewer

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const fakeConnectorValue = "fake-secret-value-7f3a"

// TestCompanyRoomConnectors: the room lists its connectors with secrets as
// set / missing and the last check; Check now runs the checks in the
// background and comes back polling; a missing secret is a Needs you row;
// no page or JSON carries the secret's value.
func TestCompanyRoomConnectors(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	t.Setenv("ACME_WP", fakeConnectorValue)
	t.Setenv("ACME_QB", "")
	a.Departments = []tracker.Department{{Name: "Marketing", Systems: []string{"UI"}, Connectors: []tracker.Connector{
		{Name: "wp", Kind: "tool", Command: "wp", Sends: true, Note: "Client sites", Secrets: map[string]string{"WP_PASSWORD": "env:ACME_WP"}, Check: `test "$WP_PASSWORD" = "` + fakeConnectorValue + `" && echo "$WP_PASSWORD"`},
		{Name: "qb", Kind: "mcp", Command: "qb-mcp", Secrets: map[string]string{"QB_TOKEN": "env:ACME_QB"}, Check: "true"},
	}}, {Name: "Sales"}}
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	var bodies strings.Builder
	get := func(u string) string {
		t.Helper()
		code, body := getBody(t, ts.URL+u)
		if code != 200 {
			t.Fatalf("GET %s: %d %s", u, code, body)
		}
		bodies.WriteString(body)
		return body
	}
	html := get("/p/agents/company")
	assertContains(t, html,
		`id="dept-marketing" data-testid="company.room.connectors"`,
		`2 connectors · <span class="wb-chip wb-chip--red" data-testid="company.room.connectors.failing">1 failing</span>`,
		`<span class="wb-connector-name">wp</span> <span class="wb-muted">tool</span> <span class="wb-chip wb-chip--you"`,
		`>never checked</span>`,
		`<span class="wb-mono-inline">WP_PASSWORD</span> env:ACME_WP <span class="wb-connector-set">set</span>`,
		`<span class="wb-mono-inline">QB_TOKEN</span> env:ACME_QB <span class="wb-connector-missing">missing</span>`,
		`Client sites`,
		`action="/p/agents/connectors/check"`, `name="dept" value="marketing"`,
	)
	if strings.Count(html, `data-testid="company.room.connectors"`) != 1 {
		t.Error("a room without connectors shows the block")
	}
	needs := get("/p/agents/needs-you")
	assertContains(t, needs, "Marketing: qb secret missing (issue-cli connectors marketing).")

	loc := postForm(t, ts.URL+"/p/agents/connectors/check", url.Values{"dept": {"marketing"}})
	if !strings.HasPrefix(loc, "/p/agents/company?checking=") || !strings.HasSuffix(loc, "&dept=marketing#dept-marketing") {
		t.Fatalf("redirect = %s", loc)
	}
	html = get(loc[:strings.Index(loc, "#")])
	assertContains(t, html, `open data-checking="`, `data-state="/p/agents/connectors.json?dept=marketing"`, `checking…`)

	var list []connectors.Status
	for deadline := time.Now().Add(10 * time.Second); ; {
		list = nil
		json.Unmarshal([]byte(get("/p/agents/connectors.json?dept=marketing")), &list)
		if len(list) == 2 && list[0].Last != nil && list[1].Last != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("checks never landed: %+v", list)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if list[0].Last.Result != connectors.ResultOK || list[1].Last.Result != connectors.ResultMissing {
		t.Errorf("checks = %+v %+v", list[0].Last, list[1].Last)
	}
	html = get("/p/agents/company")
	assertContains(t, html, `wb-connector-check--green" data-testid="company.room.connector.check">✓ ok`, `wb-connector-check--red" data-testid="company.room.connector.check">✗ missing`)
	get("/p/agents/connectors.json")

	for _, form := range []url.Values{{"dept": {"sales"}}, {"dept": {"nope"}}} {
		resp, err := http.PostForm(ts.URL+"/p/agents/connectors/check", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%v: status %d, want 400", form, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/agents/connectors/check", strings.NewReader("dept=marketing"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site Check now: %d", resp.StatusCode)
	}
	if code, _ := getBody(t, ts.URL+"/p/agents/connectors.json?dept=ops"); code != 404 {
		t.Errorf("unknown department json: %d", code)
	}
	if strings.Contains(bodies.String(), fakeConnectorValue) {
		t.Error("a page or JSON response holds the secret value")
	}
}
