package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// weekFixture is a project root with GTM over Marketing in projects.yaml
// (ISSUE_VIEWER_CONFIG), a Marketing issue GTM filed, and the cwd at the
// root: bootstrap mode, no --project (Settled rule "Bootstrap mode reads
// projects.yaml").
func weekFixture(t *testing.T) (root string) {
	t.Helper()
	f := newBootstrapFixture(t, false)
	mustWrite(t, f.cfg, "projects:\n  - name: proj\n    workdir: "+f.proj+"\n"+
		"    departments:\n"+
		"      - {name: GTM, goals: [{id: pipeline, name: New client conversations, target: 8, period: month}]}\n"+
		"      - {name: Marketing, parent: gtm, systems: [SEO]}\n")
	if err := os.MkdirAll(filepath.Join(f.proj, "issues", "SEO"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(f.proj, "issues", "SEO", "refresh.md"), "---\ntitle: \"Refresh pricing\"\nstatus: \"idea\"\ndepartment: \"Marketing\"\nrequested_by: \"gtm\"\n---\nBody.\n")
	t.Setenv("ISSUE_VIEWER_CONFIG", f.cfg)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	withCwd(t, f.proj)
	return f.proj
}

func runWeek(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errw bytes.Buffer
	err := run(append([]string{"department", "week"}, args...), strings.NewReader(""), &out, &errw)
	if err != nil {
		return out.String() + errw.String() + err.Error(), err
	}
	return out.String() + errw.String(), nil
}

func TestDepartmentWeekPrintsAndWritesTheReport(t *testing.T) {
	root := weekFixture(t)
	out, err := runWeek(t, "gtm")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"# GTM · week ", "## GTM", "## Marketing",
		"- New client conversations: no value yet of 8",
		"GTM asked:\n\n- seo/refresh-pricing (Refresh pricing, idea) to Marketing",
		"Asked of Marketing:\n\n- seo/refresh-pricing (Refresh pricing, idea) from GTM",
		"Write this week's report (shown in the GTM room on the company page):\n  issue-cli --project proj department week gtm --write",
	} {
		assertContains(t, out, want)
	}
	if _, err := os.Stat(filepath.Join(root, "issues", ".reports")); err == nil {
		t.Fatal("without --write nothing is written")
	}

	out, err = runWeek(t, "gtm", "--write")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertContains(t, out, "✓ Wrote GTM's week report ")
	assertContains(t, out, "0 goals moved · 0 outputs shipped, 0 reviewed · 0 blocked")
	assertContains(t, out, filepath.Join("issues", ".reports", "gtm")+string(filepath.Separator))
	assertContains(t, out, "/p/proj/reports/gtm/")
	assertContains(t, out, "No ## Week plan for ")
	out, _ = runWeek(t, "gtm", "--write")
	assertContains(t, out, "✓ Replaced GTM's week report ")
	// AC3: the week only reads and writes its report: no issue created or
	// moved, no job run logged, nothing dispatched.
	entries, _ := os.ReadDir(filepath.Join(root, "issues"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != ".reports,SEO" {
		t.Errorf("issues dir after --write = %v, want only the report folder added", names)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "issues", "SEO", "refresh.md")); !strings.Contains(string(b), "status: \"idea\"") {
		t.Errorf("the planned issue was moved:\n%s", b)
	}

	out, err = runWeek(t, "marketing", "--json")
	if err != nil {
		t.Fatal(out)
	}
	var w struct {
		Box      string `json:"box"`
		Group    bool   `json:"group"`
		Sections []struct {
			Name string `json:"name"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(out), &w); err != nil || w.Box != "marketing" || w.Group || len(w.Sections) != 1 {
		t.Fatalf("json: %v %+v\n%s", err, w, out)
	}

	for _, c := range [][2]string{{"hq", "HQ has the Status page"}, {"nope", `no department "nope". Departments: gtm (group), marketing`}} {
		if out, err := runWeek(t, c[0]); err == nil || !strings.Contains(out, c[1]) {
			t.Errorf("week %s: %v\n%s", c[0], err, out)
		}
	}
	if out, err := runWeek(t); err == nil || !strings.Contains(out, "usage: issue-cli department week") {
		t.Errorf("no name: %v\n%s", err, out)
	}

	// department show names the latest report for a group.
	var show, errw bytes.Buffer
	if err := run([]string{"department", "show", "gtm"}, strings.NewReader(""), &show, &errw); err != nil {
		t.Fatal(err, errw.String())
	}
	assertContains(t, show.String(), "Week report: 20")
	assertContains(t, show.String(), "The week: issue-cli department week gtm [--write]")
}
