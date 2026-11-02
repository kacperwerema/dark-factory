package viewer

import (
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const retroOpenNote = `# Workflow Retrospective

Issue: bug-in-login
Title: Bug in login
Status: human-testing
System: Auth
Date: 2026-10-28T16:07:00+01:00
ReviewStatus: open

Base workflow: a text-only change still went through two approval gates. A tweak type would fit.
Subsystem workflow for Auth: no recipe for a fake login; I built one by hand.
Missing system-specific instructions: (1) how to stub the session store; (2) that tests must never call the real check-script endpoint.
Tooling/workflow friction: check-script only looks in the main checkout.
`

const retroDoneNote = `# Workflow Retrospective

Issue: add-dark-mode
Title: Add dark mode
Status: done
System: UI
Date: 2026-10-27T10:00:00+01:00
ReviewStatus: processed

Base workflow: smooth.
Tooling/workflow friction: none.
`

const retroFreeNote = `# Workflow Retrospective

Issue: add-dark-mode
Title: Free text note
Date: 2026-10-26T10:00:00+01:00

It went fine, nothing to add.
`

// retroProject is a project with an open retro (four labelled lines, one
// split into two items), a processed one and a free-text one, and a bug
// report filed for the open retro's issue.
func retroProject(t *testing.T) (tracker.Project, string) {
	t.Helper()
	proj, dir := setupTestProject(t)
	withWorkingDir(t, dir)
	write := func(rel, body string, mod time.Time) {
		t.Helper()
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, mod, mod)
	}
	now := time.Now()
	write("retros/20261028-160700-bug-in-login.md", retroOpenNote, now)
	write("retros/20261027-100000-add-dark-mode.md", retroDoneNote, now.Add(-time.Hour))
	write("retros/20261026-100000-free.md", retroFreeNote, now.Add(-2*time.Hour))
	write("bugs/b1.json", `{"description":"check-script misses worktrees","issue_slug":"bug-in-login","tool":"issue-cli","ts":"2026-10-28T17:00:00Z"}`, now)
	return proj, dir
}

func TestRetroWbPageRenders(t *testing.T) {
	withMockTmuxSessions(t, nil)
	proj, _ := retroProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	page := html.UnescapeString(pageGet(t, ts.URL+"/p/test-project/retros"))
	for _, want := range []string{
		`data-testid="retro.page"`, `data-testid="hall.tabs"`, `aria-current="page" data-testid="hall.tab.retro"`,
		`href="/p/test-project/decisions" data-testid="hall.tab.decisions"`, "Product hall",
		"Retro · sorting room", `href="/p/test-project/retros?classic=1" data-testid="retro.classic"`,
		"Retro · 2 open", "1 processed · 1 linked bug",
		`data-presort="/p/test-project/retros/review"`, `data-testid="retro.presort.agent.claude"`, `data-testid="retro.presort.agent.codex"`,
		// The open retro is selected: header, file, progress.
		`<h2 class="wb-rhead-title">Bug in login</h2>`, "retros/20261028-160700-bug-in-login.md", "system: Auth", "human-testing",
		`href="/p/test-project/issue/bug-in-login" data-testid="retro.issue"`,
		`data-retro-status="/p/test-project/retros/retro/20261028-160700-bug-in-login.md/status" data-status="processed" data-testid="retro.close"`,
		// Five findings from four labelled lines: kinds, targets, actions.
		"Findings · 5", `data-testid="retro.finding.4"`,
		">Workflow<", ">System Auth<", ">Tools<",
		"Workflow: the base workflow", "Workflow: Auth&#39;s workflow", "Docs: Auth instructions", "Bugs: report-bug",
		"How to stub the session store", "That tests must never call the real check-script endpoint",
		"Propose an update", "Add to docs",
		`data-send="/p/test-project/lead/send"`, "From retro retros/20261028-160700-bug-in-login.md (issue bug-in-login)",
		// The bug report filed for the issue marks the Tools finding.
		"✓ filed · bugs/b1.json", ">1/5<",
		`data-testid="retro.bin.docs"`, `data-testid="retro.bin.decisions"`, `data-testid="retro.bin.workflow"`, `data-testid="retro.bin.bugs"`,
		"The agent's note as written", "/static/workshop-retro.js",
	} {
		want = html.UnescapeString(want)
		if !strings.Contains(page, want) {
			t.Errorf("retro page lacks %s", want)
		}
	}
	// The CEO is not running: sending is disabled and says why.
	if !strings.Contains(page, "The CEO (lead-test-project) is not running") {
		t.Error("send buttons don't say the CEO is off")
	}
	// Lists: two to sort (the free-text one unsorted), one processed.
	if n := strings.Count(page, `data-testid="retro.item"`); n != 3 {
		t.Errorf("%d retro items, want 3", n)
	}
	if !strings.Contains(page, ">unsorted<") || !strings.Contains(page, ">5 findings<") {
		t.Error("list items don't count findings / unsorted")
	}
	// A Workshop Board page: no classic sidebar, no classic filters.
	for _, unwanted := range []string{`data-testid="nav.sidebar"`, `name="retro_status"`} {
		if strings.Contains(page, unwanted) {
			t.Errorf("retro page has %s", unwanted)
		}
	}
}

func TestRetroWbSelection(t *testing.T) {
	withMockTmuxSessions(t, nil)
	proj, _ := retroProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	page := html.UnescapeString(pageGet(t, ts.URL+"/p/test-project/retros?retro=20261027-100000-add-dark-mode.md"))
	for _, want := range []string{
		`<h2 class="wb-rhead-title">Add dark mode</h2>`, "Findings · 1", "✓ processed with the retro", ">1/1<",
		`data-status="open" data-testid="retro.reopen"`, `aria-current="page" data-testid="retro.item"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("selected processed retro lacks %s", want)
		}
	}
	// "Tooling/workflow friction: none." is not a finding.
	if strings.Contains(page, "Bugs: report-bug") {
		t.Error("a 'none' line became a finding")
	}

	page = html.UnescapeString(pageGet(t, ts.URL+"/p/test-project/retros?retro=20261026-100000-free.md"))
	for _, want := range []string{`data-testid="retro.unsorted"`, "Findings · 0", `<details class="wb-rnote" open`, "It went fine"} {
		if !strings.Contains(page, want) {
			t.Errorf("free-text retro lacks %s", want)
		}
	}

	// An unknown file falls back to the newest open retro.
	page = pageGet(t, ts.URL+"/p/test-project/retros?retro=nope.md")
	if !strings.Contains(page, `<h2 class="wb-rhead-title">Bug in login</h2>`) {
		t.Error("unknown retro did not fall back to the newest open one")
	}
}

func TestRetroWbActionsUseRealEndpoints(t *testing.T) {
	withMockTmuxSessions(t, nil)
	proj, dir := retroProject(t)
	var gotAgent string
	orig := dispatchAgentSession
	dispatchAgentSession = func(_ *tracker.Project, session, prompt, _ string, agent string, _ *tracker.WorkflowConfig) DispatchResponse {
		gotAgent = agent
		return DispatchResponse{Status: "dispatched", Session: session}
	}
	t.Cleanup(func() { dispatchAgentSession = orig })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/test-project/retros")
	closeURL := "/p/test-project/retros/retro/20261028-160700-bug-in-login.md/status"
	if !strings.Contains(page, `data-retro-status="`+closeURL+`"`) {
		t.Fatal("close button posts elsewhere")
	}
	if resp := pagePost(t, ts.URL+closeURL, `{"status":"processed"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("close = %d", resp.StatusCode)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "retros/20261028-160700-bug-in-login.md"))
	if !strings.Contains(string(data), "ReviewStatus: processed") {
		t.Fatal("closing did not mark the retro processed")
	}
	page = html.UnescapeString(pageGet(t, ts.URL+"/p/test-project/retros?retro=20261028-160700-bug-in-login.md"))
	if !strings.Contains(page, "Retro · 1 open") || !strings.Contains(page, ">5/5<") || !strings.Contains(page, `data-testid="retro.reopen"`) {
		t.Error("after closing, the page still shows the retro to sort")
	}

	if resp := pagePost(t, ts.URL+"/p/test-project/retros/review", `{"agent":"codex"}`); resp.StatusCode != http.StatusOK || gotAgent != "codex" {
		t.Errorf("pre-sort = %d, agent %q", resp.StatusCode, gotAgent)
	}
}

func TestRetroWbClassic(t *testing.T) {
	withMockTmuxSessions(t, nil)
	proj, _ := retroProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	page := pageGet(t, ts.URL+"/p/test-project/retros?classic=1")
	for _, want := range []string{"Review Retros And Bugs", `data-testid="nav.sidebar"`, `<input type="hidden" name="classic" value="1">`, `id="bugs"`} {
		if !strings.Contains(page, want) {
			t.Errorf("classic retros page lacks %s", want)
		}
	}
	if strings.Contains(page, `data-testid="retro.page"`) {
		t.Error("classic view rendered the new page")
	}
}

func TestRetroWbEmptyProject(t *testing.T) {
	withMockTmuxSessions(t, nil)
	proj, dir := setupTestProject(t)
	withWorkingDir(t, dir)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	page := pageGet(t, ts.URL+"/p/test-project/retros")
	for _, want := range []string{`data-testid="retro.empty"`, "No retros yet", "Retro · 0 open", "0 processed · 0 linked bugs"} {
		if !strings.Contains(page, want) {
			t.Errorf("empty retro page lacks %s", want)
		}
	}
	if strings.Contains(page, `data-testid="retro.pattern"`) || strings.Contains(page, `data-testid="retro.progress"`) {
		t.Error("empty page invents a pattern or progress")
	}
}

func TestRetroFindingsParsing(t *testing.T) {
	r := &RetroEntry{FileName: "x.md", IssueSlug: "a/b", System: "Plan", BodyRaw: `## Base workflow
Clear and short.

## Subsystem workflow for Zakupy
- needs a probe

**Tooling/workflow friction**: brak.
Missing system-specific instructions: (1) seed a family; (2) the dev proxy is hard-wired. A bug report covers both.`}
	fs := retroFindings(r, "", WbMessageBox{SendURL: "/p/x/lead/send", Session: "lead-x", Live: true})
	var got []string
	for _, f := range fs {
		got = append(got, f.Kind+"|"+f.Send.Bin+"|"+f.Title)
	}
	want := []string{
		"Workflow|workflow|Clear and short.",
		"System Zakupy|workflow|Needs a probe",
		"System Plan|docs|Seed a family",
		"System Plan|docs|The dev proxy is hard-wired.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if fs[3].Text != "A bug report covers both." || !fs[0].Send.Live || fs[0].Send.Done {
		t.Errorf("finding detail wrong: %+v", fs[3])
	}
	long, rest := retroTitleText(strings.Repeat("word ", 40))
	if !strings.HasSuffix(long, "…") || !strings.HasPrefix(rest, "…word") || len(long)+len(rest) > 220 {
		t.Errorf("long title not cut: %q / %q", long, rest)
	}
	if got := retroCap("„łódź” check"); got != "„łódź” check" {
		t.Errorf("retroCap broke a multibyte start: %q", got)
	}
	if got := retroCap("ścieżka"); got != "Ścieżka" {
		t.Errorf("retroCap = %q", got)
	}
	if got := retroCap("check-script misses"); got != "check-script misses" {
		t.Errorf("retroCap capitalised code: %q", got)
	}
}

func TestRetroPatternNeedsOtherIssues(t *testing.T) {
	mk := func(file, slug, body string) *RetroEntry {
		return &RetroEntry{FileName: file, IssueSlug: slug, IssueTitle: slug, BodyRaw: "Tooling/workflow friction: " + body}
	}
	sel := mk("s.md", "s", "check-script misses the worktree; human-testing waits.")
	all := []*RetroEntry{sel,
		mk("a.md", "a", "check-script needs bash 4"), mk("b.md", "b", "Check-Script again"), mk("b2.md", "b", "check-script, same issue"),
		mk("s2.md", "s", "check-script from the same issue does not count"),
		mk("c.md", "c", "human-testing waits"), mk("d.md", "d", "human-testing waits"), mk("e.md", "e", "human-testing waits"), mk("f.md", "f", "human-testing waits"),
	}
	link := func(r *RetroEntry) string { return "?retro=" + r.FileName }
	stop := map[string]bool{"human-testing": true}
	if p := retroPattern(sel, all, stop, link); p != nil {
		t.Errorf("two other issues made a pattern: %+v", p)
	}
	all = append(all, mk("g.md", "g", "the check-script"))
	p := retroPattern(sel, all, stop, link)
	if p == nil || p.Term != "check-script" || p.Issues != 3 || p.ExampleURL != "?retro=a.md" {
		t.Errorf("pattern = %+v", p)
	}
}

func TestRetroWbComponentsStandAlone(t *testing.T) {
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	render := func(name string, data any) string {
		t.Helper()
		var b strings.Builder
		if err := srv.tmpl.ExecuteTemplate(&b, name, data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return b.String()
	}
	send := WbRetroSend{Bin: "docs", Dest: "Docs: Plan instructions", CTA: "Add to docs", Message: "do it", SendURL: "/p/x/lead/send", Session: "lead-x", Live: true, Key: "r.md", Index: 2}
	for _, c := range []struct {
		name string
		data any
		want string
	}{
		{"wb/retro-presort", WbRetroPresort{URL: "/p/x/retros/review", Agents: []string{"claude", "codex"}}, `data-agent="codex"`},
		{"wb/retro-loop", WbRetroLoop{Open: 4, Processed: 67, Steps: []WbRetroLoopStep{{"Docs", "/d"}}}, "67 processed · 0 linked bugs"},
		{"wb/retro-list", WbRetroList{Key: "to_sort", Title: "To sort", Count: 9, More: 1, MoreURL: "?all=1", Items: []WbRetroItem{{Title: "T", Date: "28 Oct", URL: "?retro=r.md"}}}, "+1 older"},
		{"wb/retro-item", WbRetroItem{Title: "T", System: "Plan", Date: "28 Oct", Findings: 6, Selected: true}, "6 findings"},
		{"wb/retro-head", WbRetroView{Title: "T", Path: "retros/r.md", System: "Plan", When: "Wed 28 Oct, 16:07"}, "system: Plan"},
		{"wb/retro-progress", WbRetroProgress{Done: 1, Total: 2, Segs: []bool{true, false}, StatusURL: "/s", Key: "r.md"}, ">1/2<"},
		{"wb/retro-note", WbRetroView{BodyHTML: "<p>note</p>"}, "<p>note</p>"},
		{"wb/retro-finding", WbRetroFinding{Index: 2, Kind: "System Plan", Tone: "sys", Title: "T", Send: send}, `data-testid="retro.finding.2"`},
		{"wb/retro-send", send, `data-mark="r.md#2"`},
		{"wb/retro-send", WbRetroSend{Dest: "Bugs: report-bug", Done: true, DoneLabel: "filed · bugs/b.json"}, "✓ filed · bugs/b.json"},
		{"wb/retro-bin", WbRetroBin{Key: "docs", Title: "Docs", Sub: "Plan instructions", Color: "#E9D7B5", Done: 0, Total: 3}, ">3<"},
		{"wb/retro-pattern", &WbRetroPattern{Term: "check-script", Issues: 4, Example: "E", ExampleURL: "?retro=e.md"}, "A pattern that comes back"},
		{"wb/retro-pattern", &WbRetroPattern{Term: "check-script", Issues: 4, Example: "E", ExampleURL: "?retro=e.md"}, "issue-cli experiment candidates"},
	} {
		if got := render(c.name, c.data); !strings.Contains(got, c.want) {
			t.Errorf("%s lacks %q:\n%s", c.name, c.want, got)
		}
	}
	if got := render("wb/retro-pattern", (*WbRetroPattern)(nil)); strings.TrimSpace(got) != "" {
		t.Errorf("nil pattern renders %q", got)
	}
}

// Proves: AC3 — the Bugs view (?view=bugs) shows the tool bug reports that
// were only on the classic page's #bugs section, and its buttons set the
// status through the same endpoint (classic-page-gaps G50, G51).
func TestRetroWbBugsView(t *testing.T) {
	withMockTmuxSessions(t, nil)
	proj, dir := retroProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	room := html.UnescapeString(pageGet(t, ts.URL+"/p/test-project/retros"))
	if !strings.Contains(room, `href="/p/test-project/retros?view=bugs">Bugs</a>`) || strings.Contains(room, "classic=1#bugs") {
		t.Error("the loop's Bugs step still goes to the classic page")
	}
	if !strings.Contains(room, `data-testid="seg.retro_view.bugs">Bugs · 1</a>`) {
		t.Error("no Bugs view switch with the count")
	}

	page := html.UnescapeString(pageGet(t, ts.URL+"/p/test-project/retros?view=bugs"))
	statusURL := "/p/test-project/retros/bug/b1.json/status"
	for _, want := range []string{`data-testid="retro.bugs"`, `1 open`, `0 closed`, `data-testid="retro.bug.b1_json"`,
		`href="/p/test-project/issue/bug-in-login">bug-in-login</a>`, `check-script misses worktrees`, `issue-cli`,
		`data-bug-status="` + statusURL + `" data-status="fixed"`, `data-testid="filter.bugs_open"`} {
		if !strings.Contains(page, want) {
			t.Errorf("bugs view missing %q", want)
		}
	}
	if strings.Contains(page, `data-testid="retro.list.to_sort"`) {
		t.Error("the bugs view still draws the sorting room")
	}

	if resp := pagePost(t, ts.URL+statusURL, `{"status":"fixed"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("set status = %d", resp.StatusCode)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "bugs/b1.json"))
	if !strings.Contains(string(data), "fixed") {
		t.Fatalf("bug file not updated: %s", data)
	}
	page = html.UnescapeString(pageGet(t, ts.URL+"/p/test-project/retros?view=bugs"))
	if !strings.Contains(page, "0 open") || !strings.Contains(page, `No bug report with this status.`) {
		t.Error("after Fixed, the open filter still shows the bug")
	}
	page = html.UnescapeString(pageGet(t, ts.URL+"/p/test-project/retros?view=bugs&bug_status=fixed"))
	if !strings.Contains(page, `data-testid="retro.bug.b1_json"`) || !strings.Contains(page, `>fixed</span>`) {
		t.Error("the fixed filter does not show the fixed bug")
	}
}
