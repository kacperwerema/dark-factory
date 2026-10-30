package main

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

func TestUpdatesWbPage(t *testing.T) {
	proj := updatesProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	latest, err := workflowupgrade.BundledVersion("development")
	if err != nil {
		t.Fatal(err)
	}

	code, body := getBody(t, ts.URL+"/p/test-project/updates")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		`data-testid="upd.page"`,
		`aria-current="page"><i class="wb-crumb-sq" style="background:` + companyProductColor + `"></i>Product hall</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, want := range []string{
		"Workflow features the engine ships that this hall's workflow.yaml does not use yet.",
		"issue-cli workflow upgrade",
		`href="/p/test-project/updates?classic=1" data-testid="upd.classic"`,
		"development@v0.36.0 → " + latest,
		`data-testid="chip.upd_state">behind`,
		"<b>1</b> installed · 1 declined",
		`data-testid="chip.to_decide">`,
		`upd-sq upd-sq--installed" title="status-prompts · installed"`,
		`upd-sq upd-sq--declined" title="scoring · declined"`,
		`upd-sq upd-sq--decide" title="ci-test-results · to decide"`,
		`data-testid="upd.filter.all">All · `,
		`href="/p/test-project/updates?category=workbench"`,
		"They unlock what you see locked",
		`data-testid="upd.card.status-changelog"`,
		`data-testid="upd.card.status-changelog.unlocks"`,
		`href="/p/test-project/status"`,
		"Status: releases from your own changelog",
		`<code class="upd-key"`,
		">workbench.changelog</code>",
		`data-testid="upd.more"`,
		`href="/p/test-project/updates?all=1" data-testid="upd.show_all"`,
		`data-testid="upd.declined.scoring"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "hall tabs") {
		t.Error("the merge placeholder comment leaked into the page")
	}
	if strings.Contains(body, `data-testid="upd.card.scoring"`) || strings.Contains(body, `data-testid="updates.page"`) {
		t.Error("declined cards and the classic page stay off")
	}
	// Folded: features without anything to unlock wait behind "N more".
	if strings.Contains(body, `data-testid="upd.card.ci-test-results"`) {
		t.Error("ci-test-results unlocks nothing and should wait behind Show all")
	}

	_, all := getBody(t, ts.URL+"/p/test-project/updates?all=1")
	if !strings.Contains(all, `data-testid="upd.card.ci-test-results"`) || strings.Contains(all, `data-testid="upd.more"`) || !strings.Contains(all, "More to decide") {
		t.Error("?all=1 should draw every card and drop the More row")
	}

	_, filtered := getBody(t, ts.URL+"/p/test-project/updates?category=workbench")
	if !strings.Contains(filtered, `data-testid="upd.card.health-tuning"`) || strings.Contains(filtered, `data-testid="upd.card.proof-links"`) {
		t.Error("the workbench filter keeps workbench cards only")
	}
	if !strings.Contains(filtered, `upd-filter upd-filter--on" href="/p/test-project/updates?category=workbench"`) {
		t.Error("the active filter is marked")
	}
}

func TestUpdatesWbFormsPostToTheEndpoints(t *testing.T) {
	proj := updatesProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	var gotPrompt string
	orig := dispatchAgentSession
	dispatchAgentSession = func(_ *tracker.Project, session, prompt, _ string, _ string, _ *tracker.WorkflowConfig) DispatchResponse {
		gotPrompt = prompt
		return DispatchResponse{Status: "dispatched", Session: session}
	}
	t.Cleanup(func() { dispatchAgentSession = orig })

	_, body := getBody(t, ts.URL+"/p/test-project/updates?all=1")
	for _, want := range []string{
		`<form method="post" action="/p/test-project/updates/install" class="upd-panel">
                <input type="hidden" name="id" value="ci-test-results">`,
		`name="words" rows="2" data-testid="upd.card.ci-test-results.words"`,
		`data-testid="upd.card.ci-test-results.start"`,
		`<form method="post" action="/p/test-project/updates/decline" class="upd-decline">
            <input type="hidden" name="id" value="ci-test-results">`,
		`<form method="post" action="/p/test-project/updates/reopen">
            <input type="hidden" name="id" value="scoring">`,
		"Which CI commands write test results",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `name="classic"`) {
		t.Error("the new page's forms must come back to the new page")
	}

	loc := postForm(t, ts.URL+"/p/test-project/updates/install", url.Values{"id": {"ci-test-results"}, "words": {"make check writes trx"}})
	if loc != "/p/test-project/updates?installing=ci-test-results" || !strings.Contains(gotPrompt, `"make check writes trx"`) {
		t.Fatalf("install: redirect %q, prompt %q", loc, gotPrompt)
	}
	_, body = getBody(t, ts.URL+loc)
	if !strings.Contains(body, `data-testid="upd.flash"`) || !strings.Contains(body, "Install agent for ci-test-results started") {
		t.Error("install flash missing on the new page")
	}

	loc = postForm(t, ts.URL+"/p/test-project/updates/decline", url.Values{"id": {"status-changelog"}})
	_, body = getBody(t, ts.URL+loc)
	for _, want := range []string{"status-changelog declined", `data-testid="upd.undo"`, `upd-declined-row upd-declined-row--just" data-testid="upd.declined.status-changelog"`} {
		if !strings.Contains(body, want) {
			t.Errorf("after decline: missing %q", want)
		}
	}
	if strings.Contains(body, `data-testid="upd.card.status-changelog"`) {
		t.Error("a declined card leaves the list")
	}
	loc = postForm(t, ts.URL+"/p/test-project/updates/reopen", url.Values{"id": {"status-changelog"}})
	_, body = getBody(t, ts.URL+loc)
	if !strings.Contains(body, `data-testid="upd.card.status-changelog"`) || !strings.Contains(body, "re-opened") {
		t.Error("re-open lists the card again")
	}
}

func TestUpdatesWbShowsARunningInstall(t *testing.T) {
	proj := updatesProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	orig := agentSessionLive
	agentSessionLive = func(_ *tracker.Project, name string) bool {
		return name == "agent-test-project-install-status-changelog"
	}
	t.Cleanup(func() { agentSessionLive = orig })

	_, body := getBody(t, ts.URL+"/p/test-project/updates")
	if !strings.Contains(body, `data-testid="upd.card.status-changelog.installing"`) || !strings.Contains(body, `data-testid="upd.card.status-changelog.session"`) {
		t.Error("a running install shows its chip and session")
	}
	if strings.Contains(body, `data-testid="upd.card.status-changelog.decline"`) || strings.Contains(body, `data-testid="upd.card.status-changelog.install"`) {
		t.Error("a running install hides Install and Decline")
	}
}

func TestUpdatesWbClassicView(t *testing.T) {
	proj := updatesProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, body := getBody(t, ts.URL+"/p/test-project/updates?classic=1")
	if !strings.Contains(body, `data-testid="updates.page"`) || strings.Contains(body, `data-testid="upd.page"`) {
		t.Fatal("?classic=1 renders the classic tab")
	}
	if !strings.Contains(body, `href="/p/test-project/updates?category=workbench&amp;classic=1"`) {
		t.Error("classic filters stay on the classic tab")
	}
	if !strings.Contains(body, `<input type="hidden" name="classic" value="1">`) {
		t.Error("classic forms ask to come back to the classic tab")
	}
}

func TestUpdatesWbNothingToDecide(t *testing.T) {
	t.Chdir(t.TempDir())
	data, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := workflowupgrade.BuildPlan("workflow.yaml", data, "", false)
	if err != nil {
		t.Fatal(err)
	}
	wf := strings.Replace(string(data), "features:\n", "features:\n  declined: ["+strings.Join(fresh.Undecided, ", ")+"]\n", 1)
	proj, dir := setupTestProject(t)
	proj.WorkflowFile = filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, body := getBody(t, ts.URL+"/p/test-project/updates")
	for _, want := range []string{`data-testid="upd.empty"`, "Nothing to decide", `data-testid="upd.declined"`, `data-testid="chip.upd_state">up to date`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, not := range []string{`data-testid="upd.filters"`, `data-testid="chip.to_decide"`, `data-testid="upd.more"`} {
		if strings.Contains(body, not) {
			t.Errorf("nothing to decide: %q should be absent", not)
		}
	}
}

func TestUpdatesWbComponentsStandAlone(t *testing.T) {
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	card := WbUpgradeCard{Letter: "CI", Group: "automation", Key: "ci.run", Prefix: "/p/x",
		GroupChip: WbChip{Tone: "outline", Text: "automation"},
		Unlocks:   []WbUnlockLink{{Text: "Hall: the CI scanner on the line", URL: "/p/x/hall"}}}
	card.ID, card.Version, card.Title, card.Question = "local-ci", "v0.54.0", "Local CI on the integration branch", "Which commands?"
	card.Experiment = &workflowupgrade.ExperimentRef{Slug: "factory/exp-1", Measured: "promising — gates_first_try 0.60→0.70"}
	for name, c := range map[string]struct {
		data any
		want []string
	}{
		"wb/upgrade-card": {card, []string{`data-testid="upd.card.local-ci"`, `upd-badge--automation`, ">CI</div>", "local-ci@v0.54.0", `action="/p/x/updates/install"`, `action="/p/x/updates/decline"`, `href="/p/x/hall"`, "Install with an agent", "Details", "Decline",
			`data-testid="upd.card.local-ci.measured"`, "Measured by experiment <code>factory/exp-1</code>: promising — gates_first_try 0.60→0.70"}},
		"wb/unlocks":      {card.Unlocks, []string{"Unlocks", "Hall: the CI scanner on the line"}},
		"wb/delivery":     {WbDelivery{From: "development@v0.36.0", To: "v0.54.0", State: &WbChip{Tone: "you", Text: "behind"}}, []string{"Delivery from the engine", "development@v0.36.0 → v0.54.0", "behind"}},
		"wb/upgrade-tree": {WbUpgradeTree{Installed: 2, ToDecide: 1, Recorded: true, Squares: []WbTreeSquare{{ID: "a", State: "installed"}, {ID: "b", State: "decide"}}, Pill: WbChip{Tone: "you", Text: "1 to decide"}}, []string{"Upgrade tree", "<b>2</b> installed · 0 declined", "upd-sq--decide", "1 to decide"}},
		"wb/filters":      {[]WbFilterChip{{Label: "All", Count: 3, URL: "/u", TestID: "all", Active: true}}, []string{`data-testid="upd.filter.all"`, "All · 3", "upd-filter--on"}},
		"wb/upgrade-more": {WbUpgradeMore{Count: 2, Groups: []string{"Gates 1", "Board 1"}, ShowURL: "/u?all=1"}, []string{"2 more", "Gates 1 · Board 1", "Show all"}},
		"wb/declined":     {[]WbDeclinedRow{{UpdateRecordRow: UpdateRecordRow{ID: "scoring", Title: "Ticket scoring"}, Prefix: "/p/x"}}, []string{`data-testid="upd.declined.scoring"`, `action="/p/x/updates/reopen"`, "Re-open"}},
		"wb/upd-flash":    {WbUpgradeFlash{Text: "scoring declined.", Undo: "scoring", Prefix: "/p/x"}, []string{`data-testid="upd.flash"`, `data-testid="upd.undo"`}},
	} {
		var b bytes.Buffer
		if err := srv.tmpl.ExecuteTemplate(&b, name, c.data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, w := range c.want {
			if !strings.Contains(b.String(), w) {
				t.Errorf("%s: missing %q in\n%s", name, w, b.String())
			}
		}
	}
	// Empty data draws nothing.
	for _, name := range []string{"wb/unlocks", "wb/filters", "wb/declined"} {
		var b bytes.Buffer
		srv.tmpl.ExecuteTemplate(&b, name, nil)
		if strings.TrimSpace(b.String()) != "" {
			t.Errorf("%s with no data: %q", name, b.String())
		}
	}
}

func TestUpdatesLetter(t *testing.T) {
	for title, want := range map[string]string{
		"Local CI on the integration branch": "CI",
		"Health snapshot settings":           "H",
		"Pull request link required":         "P",
		"No TODO/FIXME markers":              "N",
	} {
		if got := updatesLetter(title); got != want {
			t.Errorf("updatesLetter(%q) = %q, want %q", title, got, want)
		}
	}
}

// Every catalog unlocks row links a page the viewer serves under a project.
func TestCatalogUnlocksArePagesTheViewerServes(t *testing.T) {
	entries, err := workflowupgrade.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{updatesProject(t)})
	defer ts.Close()
	rows := 0
	for _, e := range entries {
		for _, u := range e.Unlocks {
			rows++
			if code, _ := getBody(t, ts.URL+"/p/test-project"+u.URL); code != http.StatusOK {
				t.Errorf("%s: unlocks %q links %s, served %d", e.ID, u.Text, u.URL, code)
			}
		}
	}
	if rows == 0 {
		t.Fatal("no catalog entry has unlocks:")
	}
}

func TestUpdatesWbOffersDepartmentTemplates(t *testing.T) {
	proj := updatesProject(t)
	countBefore := updatesNavNote(&proj)
	ts := newTestServer(t, []tracker.Project{proj})
	code, body := getBody(t, ts.URL+"/p/test-project/updates")
	ts.Close()
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		`data-testid="upd.departments"`,
		"Departments you can add",
		`data-testid="upd.dept.marketing"`,
		"workflow: content · 2 goals · role prompt",
		`style="background: #E2559B"`,
		"SEO and content: refreshed client pages and posts",
		`data-testid="upd.dept.marketing.command">issue-cli --project test-project department add marketing</code>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	// Installed: not offered, and the sidebar count never counted it.
	proj.Departments = []tracker.Department{{Name: "Marketing"}}
	if got := updatesNavNote(&proj); got != countBefore {
		t.Errorf("sidebar count %q, was %q", got, countBefore)
	}
	ts = newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	if _, body := getBody(t, ts.URL+"/p/test-project/updates"); strings.Contains(body, `data-testid="upd.departments"`) {
		t.Error("an installed template is still offered")
	}
}
