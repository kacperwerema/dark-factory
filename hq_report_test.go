package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// hqReportProject is GTM over Marketing (system UI) and Ops on the agents
// project: Marketing has an output waiting on review, a question that
// blocks one of its issues on the owner and its lead's progress line; Ops
// has nothing.
func hqReportProject(t *testing.T) tracker.Project {
	t.Helper()
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	stubLeadChat(t, false)
	withGTMGroup(&a)
	orig := spendProjectsDir
	empty := t.TempDir()
	spendProjectsDir = func() string { return empty }
	t.Cleanup(func() { spendProjectsDir = orig })
	write := func(rel, content string) {
		path := filepath.Join(a.IssueDir, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".outputs/outputs.jsonl", `{"op":"add","id":"o-post","date":"2026-10-28T10:00:00+01:00","department":"marketing","title":"Launch post","kind":"post","url":"https://example.test/p"}`+"\n")
	write("UI/child.md", "---\ntitle: \"Child\"\nstatus: \"in progress\"\nparent: \"ui/epic\"\n---\n\nBody.\n"+
		"<!-- issue-viewer-comments\n{\"id\":1,\"block\":0,\"date\":\"2026-10-30T10:00:00+01:00\",\"text\":\"progress: the landing copy is drafted\",\"status\":\"open\",\"source\":\"cli\",\"author\":\"lead-agents-marketing\"}\n-->\n")
	return a
}

func TestHQReportOneFoldPerBoxInTheCompanyTree(t *testing.T) {
	a := hqReportProject(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	_, company := getBody(t, ts.URL+"/p/agents/company")
	assertContains(t, company, `href="/p/agents/hq/report"`, "Report →")

	_, page := getBody(t, ts.URL+"/p/agents/hq/report")
	var at []int
	for _, box := range []string{"hq", "product", "gtm", "marketing", "ops"} {
		i := strings.Index(page, `data-testid="hq_report.box.`+box+`"`)
		if i < 0 {
			t.Fatalf("no section for %s", box)
		}
		at = append(at, i)
	}
	for i := 1; i < len(at); i++ {
		if at[i] < at[i-1] {
			t.Fatalf("sections out of the company tree's order: %v", at)
		}
	}
	if strings.Contains(page, "<details open") {
		t.Error("every section starts folded: the headlines are the page")
	}
	assertContains(t, page,
		"HQ report · week ", "1 output to review", "Pages: no value yet (target 10) · no change recorded this week",
		`data-kind="group"`, "--hqr-depth: 1",
		"1 output to review",
		"4 blocked (on the owner)",
		"waiting on your review: Launch post (post, 2026-10-28)",
		"Asker (in progress) · on the owner · an open question: comment",
		"the landing copy is drafted",
		"none written yet: issue-cli --project agents department week gtm --write",
		`data-testid="hq_report.empty.ops"`, "Nothing here yet: no goals, outputs, jobs, handoffs, open work or spend.",
		"no health snapshot with department measures yet", "issue-cli --project agents health snapshot",
	)
}

func TestHQReportMarkdownHeadlinesThenDetail(t *testing.T) {
	a := hqReportProject(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	code, md := getBody(t, ts.URL+"/p/agents/hq/report.md")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	head := strings.Index(md, "## Headlines\n\n- **HQ** — ")
	mkt := strings.Index(md, "  - **Marketing** — ")
	ops := strings.Index(md, "- **Ops** — nothing here yet")
	detail := strings.Index(md, "## GTM › Marketing\n")
	if head < 0 || mkt < head || ops < mkt || detail < ops {
		t.Fatalf("want the headlines (Marketing indented under GTM) before the detail:\n%s", md)
	}
	assertContains(t, md, "# Agents · HQ report · week ", "- Blocked:\n  - ", "  - Asker (in progress) · on the owner · an open question: comment\n", "## Ops\n\nNothing here yet")
}

// A project without departments has HQ and Product only, and says how to
// add one.
func TestHQReportWithoutDepartments(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	stubLeadChat(t, false)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, page := getBody(t, ts.URL+"/p/agents/hq/report")
	if n := strings.Count(page, `data-testid="hq_report.box.`); n != 2 {
		t.Errorf("sections = %d, want HQ and Product", n)
	}
	assertContains(t, page, `data-testid="hq_report.no_departments"`, "issue-cli --project agents department list")
}

func TestHQReportPathNamesTheGroups(t *testing.T) {
	all := []HQRBox{{Name: "HQ"}, {Name: "GTM", Kind: "group"}, {Name: "Content", Kind: "group", Depth: 1}, {Key: "seo", Name: "SEO", Depth: 2}, {Key: "ops", Name: "Ops"}}
	if got := all[3].Path(all); got != "GTM › Content › SEO" {
		t.Errorf("path = %q", got)
	}
	if got := all[4].Path(all); got != "Ops" {
		t.Errorf("path = %q", got)
	}
}

// status.md points at the HQ report when the project has departments.
func TestStatusMDNamesTheHQReport(t *testing.T) {
	a := hqReportProject(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, md := getBody(t, ts.URL+"/p/agents/status.md")
	assertContains(t, md, "Departments: the HQ report, one line each, at /p/agents/hq/report.md (issue-cli status --hq)")

	b := setupAgentsProject(t)
	ts2 := newTestServer(t, []tracker.Project{b})
	defer ts2.Close()
	if _, md := getBody(t, ts2.URL+"/p/agents/status.md"); strings.Contains(md, "HQ report") {
		t.Error("no departments, but status.md names the HQ report")
	}
}
