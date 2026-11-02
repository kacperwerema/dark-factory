package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// withHandoffs adds Marketing (system UI) and two handoffs: one Marketing
// asked of Product, waiting on HQ's forward, and one HQ handed straight to
// Marketing, at backlog.
func withHandoffs(t *testing.T, a *tracker.Project) {
	t.Helper()
	a.Departments = []tracker.Department{{Name: "Marketing", Systems: []string{"UI"}, Color: "#E2559B"}}
	write := func(rel, content string) {
		path := filepath.Join(a.IssueDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("brand-fields.md", "---\ntitle: \"Brand fields\"\nstatus: \"idea\"\nrequested_by: \"Marketing\"\nhandoff_route: \"marketing → hq → product\"\n---\n\n## Handoff\n")
	write("UI/launch-post.md", "---\ntitle: \"Launch post\"\nstatus: \"backlog\"\nrequested_by: \"HQ\"\nhandoff_route: \"hq → marketing\"\n---\n\n## Handoff\n")
}

func TestRoomsAndLineShowHandoffs(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	withHandoffs(t, &a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company := getBody(t, ts.URL+"/p/agents/company?classic=1")
	assertContains(t, company,
		`data-testid="company.room.handoffs"`,
		`data-testid="company.room.handoffs.forward"`,
		`data-testid="company.room.handoffs.asked"`,
		`data-testid="company.room.handoffs.inbox"`,
		`data-state="waiting"`,
		`waiting on HQ to forward`,
		`→ Product</span><b>Brand fields</b>`,
		`← Marketing</span><b>Brand fields</b>`,
		`← HQ</span><b>Launch post</b>`,
		`at backlog`,
		// The line goes via HQ before the forward and says what waits.
		`data-from="marketing" data-to="product" data-via="hq" data-kind="request" data-open="1"`,
		`1 waiting to be forwarded`)

	_, page := getBody(t, ts.URL+"/p/agents/connections?from=marketing&kind=request")
	assertContains(t, page, `data-testid="connections.item.handoff">handoff · waiting on HQ to forward`, `data-testid="connections.item.route">Marketing → HQ → Product`)

	_, js := getBody(t, ts.URL+"/p/agents/connections.json")
	assertContains(t, js, `"state":"waiting on HQ to forward"`, `"route":"Marketing → HQ → Product"`)
}

func TestRoomsWithoutHandoffsShowNoBlock(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	withConnections(t, &a)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company := getBody(t, ts.URL+"/p/agents/company?classic=1")
	if strings.Contains(company, `company.room.handoffs`) {
		t.Error("a plain request is no handoff: no Handoffs block")
	}
}
