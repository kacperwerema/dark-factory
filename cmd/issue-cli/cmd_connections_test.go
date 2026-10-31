package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/connections"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// asDepartmentLead makes whoAmI see the Marketing lead.
func asDepartmentLead(t *testing.T) {
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", tracker.LeadScopeDepartmentPrefix+"marketing")
}

func createdFile(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "file: "); ok {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
	}
	t.Fatalf("no file: line in %s", out)
	return ""
}

func TestCreateFillsRequestedByFromDepartmentLead(t *testing.T) {
	asDepartmentLead(t)
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runCreate(ctx, []string{"--title", "Engine brand fields", "--system", "Factory"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, createdFile(t, out.String()), "requested_by: \"Marketing\"")
	assertContains(t, out.String(), "Requested by Marketing: drawn as a request Marketing → Product on the company page")

	// Its own department's issue is no request.
	ctx, out = goalsCtx(t, proj, false)
	if err := runCreate(ctx, []string{"--title", "Weekly pass", "--system", "SEO"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(createdFile(t, out.String()), "requested_by") {
		t.Errorf("a department's own issue has no requested_by:\n%s", out)
	}
}

func TestCreateRequestedByFlag(t *testing.T) {
	asLead(t)
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runCreate(ctx, []string{"--title", "CEO ask", "--system", "SEO", "--requested-by", "hq"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, createdFile(t, out.String()), "requested_by: \"HQ\"")

	// The CEO alone fills nothing.
	ctx, out = goalsCtx(t, proj, false)
	if err := runCreate(ctx, []string{"--title", "Plain", "--system", "Factory"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(createdFile(t, out.String()), "requested_by") {
		t.Errorf("the CEO's own create has no requested_by:\n%s", out)
	}

	ctx, _ = goalsCtx(t, proj, false)
	err := runCreate(ctx, []string{"--title", "Bad", "--requested-by", "accounting"})
	if err == nil || !strings.Contains(err.Error(), "one of: hq, product, marketing") {
		t.Errorf("unknown --requested-by: %v", err)
	}
}

func TestSetMetaRequestedByAndCrossDepartmentHint(t *testing.T) {
	asLead(t)
	proj := goalsProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runSetMeta(ctx, []string{"engine", "--key", "requested_by", "--value", "nobody"}); err == nil {
		t.Error("requested_by naming no department is refused")
	}
	ctx, out := goalsCtx(t, proj, false)
	if err := runSetMeta(ctx, []string{"engine", "--key", "requested_by", "--value", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), `Set requested_by = "Marketing"`)
	assertContains(t, out.String(), "Drawn as a request Marketing → Product")

	ctx, out = goalsCtx(t, proj, false)
	if err := runSetMeta(ctx, []string{"seo-pass", "--key", "depends_on", "--value", "engine"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Drawn as a dependency Product → Marketing on the company page")
}

func TestOutputUse(t *testing.T) {
	asLead(t)
	proj := outputsProject(t)
	o := addOutput(t, proj, "--issue", "seo-pass", "--title", "ICP", "--kind", "report", "--url", "https://acme.example/icp")

	ctx, _ := goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"use", shortID(o.ID), "--by", "marketing"}); err == nil || !strings.Contains(err.Error(), "own output") {
		t.Errorf("a department using its own output: %v", err)
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"use", shortID(o.ID), "--by", "nobody"}); err == nil {
		t.Error("--by naming nothing is refused")
	}
	ctx, out := goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"use", shortID(o.ID), "--by", "engine"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Product uses "+shortID(o.ID)+": ICP")
	assertContains(t, out.String(), "Drawn as an output Marketing → Product")

	ctx, out = goalsCtx(t, proj, false)
	if err := runOutput(ctx, []string{"show", shortID(o.ID)}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Used by: engine")
}

func TestConnectionsCommand(t *testing.T) {
	asLead(t)
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runConnections(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "No connections yet")
	assertContains(t, out.String(), "issue-cli output use <id> --by <department>")

	mustWrite(t, filepath.Join(proj.IssueDir, "ask.md"), "---\ntitle: \"Ask\"\nstatus: \"idea\"\nrequested_by: \"Marketing\"\n---\nBody.\n")
	mustWrite(t, filepath.Join(proj.IssueDir, "seo-wait.md"), "---\ntitle: \"Wait\"\nstatus: \"idea\"\nsystem: \"SEO\"\ndepends_on: \"engine\"\n---\nBody.\n")
	ctx, out = goalsCtx(t, proj, false)
	if err := runConnections(ctx, nil); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "== Connections · 2 open ==")
	assertContains(t, s, "Marketing → Product · 1 request")
	assertContains(t, s, "ask [idea] · requested_by: Marketing")
	assertContains(t, s, "Product → Marketing · 1 dependency")
	assertContains(t, s, "/p/acme/connections")

	ctx, out = goalsCtx(t, proj, true)
	if err := runConnections(ctx, []string{"--kind", "dependency", "--to", "Marketing"}); err != nil {
		t.Fatal(err)
	}
	var edges []connections.Edge
	if err := json.Unmarshal(out.Bytes(), &edges); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(edges) != 1 || edges[0].Kind != connections.Dependency || edges[0].From != "product" {
		t.Errorf("filtered json = %+v", edges)
	}

	ctx, _ = goalsCtx(t, proj, false)
	if err := runConnections(ctx, []string{"--kind", "gossip"}); err == nil {
		t.Error("an unknown --kind is refused")
	}
	ctx, _ = goalsCtx(t, proj, false)
	if err := runConnections(ctx, []string{"--from", "nowhere"}); err == nil {
		t.Error("an unknown --from is refused")
	}

	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"show", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Connections: in 1 · out 1 open (issue-cli connections --to marketing, --from marketing)")
}

func TestConnectionsWithoutDepartments(t *testing.T) {
	asLead(t)
	proj := goalsProject(t)
	proj.Departments = nil
	ctx, out := goalsCtx(t, proj, false)
	if err := runConnections(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "No departments")
}
