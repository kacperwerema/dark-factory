package viewer

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestSystemWb_Configured(t *testing.T) {
	f := setupSystemsProject(t)
	noBranches(t)
	ts := newTestServer(t, []tracker.Project{f.proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/rd/systems/AI")
	assertContains(t, page,
		`data-testid="system.wb.page"`, `<body class="wb">`,
		`data-testid="system.wb.trail"`, `href="/p/rd/systems">Systems</a>`, `<b aria-current="page">AI</b>`,
		`data-testid="system.wb.summary"`, "How teams choose plays",
		`href="/p/rd/systems/AI?classic=1"`,
		// figures: 11 open, 2 done
		`<div class="wb-figure-num">11</div><div class="wb-figure-label">open</div>`,
		`<div class="wb-figure-num">2</div><div class="wb-figure-label">done</div>`,
		// floor: crates and the status bar (obsolete not counted)
		`data-testid="system.wb.crates"`, `data-testid="system.wb.bar"`, "1 obsolete not counted",
		// issues as crate cards, linking to their station
		`data-testid="issues.crate.ai/caller-fit"`, `href="/p/rd/issue/ai/caller-fit"`,
		// families
		`data-testid="system.wb.family.play_calls"`, "Calls per stage", "1 of 2 done",
		// decisions as decision-log rows
		`data-testid="system.wb.decisions"`, `data-testid="decisions.row.ai/shouts.d-2"`, `href="/p/rd/decisions?system=AI"`,
		// design doc sections
		`data-testid="system.wb.doc"`, `data-testid="system.wb.doc.section.rules"`,
		// code map: AI has no paths:, so it's a draft
		`data-testid="systems.wb.codemap.draft"`, "No files yet: no AI issue names a file in the repo.",
		// balance note
		`data-testid="system.wb.balance"`, "dominant-plays",
	)
	if testing.Verbose() {
		t.Log(page)
	}
	for _, classic := range []string{`data-testid="system.header"`, `class="app"`, `class="card sys-panel"`} {
		if strings.Contains(page, classic) {
			t.Errorf("Workshop page carries classic markup %q", classic)
		}
	}
}

func TestSystemWb_Family(t *testing.T) {
	f := setupSystemsProject(t)
	noBranches(t)
	ts := newTestServer(t, []tracker.Project{f.proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/rd/systems/ai/play%20CALLS")
	assertContains(t, page,
		`<a href="/p/rd/systems/AI">AI</a>`, `<b aria-current="page">Play calls</b>`,
		"Calls per stage",
		`data-testid="issues.crate.ai/caller-fit"`, `data-testid="issues.crate.ai/call-modal"`,
		"the system&#39;s design doc",
		`href="/p/rd/systems/AI/Play%20calls?classic=1"`,
	)
	if strings.Contains(page, `data-testid="system.wb.families"`) {
		t.Error("a family page lists no families")
	}
	if strings.Contains(page, "issues.crate.ai/idea-a") {
		t.Error("a family page lists only the umbrella's children")
	}
}

func TestSystemWb_Unconfigured(t *testing.T) {
	f := setupSystemsProject(t)
	noBranches(t)
	ts := newTestServer(t, []tracker.Project{f.proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/rd/systems/Sim")
	assertContains(t, page,
		`data-testid="system.wb.hint"`, "workbench.systems.Sim",
		`data-testid="system.wb.epic.sim/siege"`, "Epics with issues here",
		`data-testid="system.wb.decisions.empty"`, "1 issue keeps a ## Decisions section",
	)
	for _, absent := range []string{`data-testid="system.wb.doc"`, `data-testid="system.wb.metrics"`, `data-testid="system.wb.knobs"`, `data-testid="system.wb.balance"`} {
		if strings.Contains(page, absent) {
			t.Errorf("Sim has no such data, yet the page shows %s", absent)
		}
	}
}

func TestSystemWb_UnknownIs404(t *testing.T) {
	f := setupSystemsProject(t)
	ts := newTestServer(t, []tracker.Project{f.proj})
	defer ts.Close()

	for _, path := range []string{"/p/rd/systems/Nope", "/p/rd/systems/AI/Nope", "/p/rd/systems/AI/Play%20calls/x"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
		if !strings.Contains(string(body), `data-testid="systems.wb.notfound"`) || !strings.Contains(string(body), `systems.wb.card.ai`) {
			t.Errorf("GET %s: 404 page should name the miss and list the workshops", path)
		}
	}
}

func TestSystemWb_MetricPanels(t *testing.T) {
	r := setupMetricsRepo(t, metricsTestWorkflow)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{r.proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/mp/systems/Sim")
	assertContains(t, page, `data-testid="system.wb.metrics"`, `data-testid="system.wb.metrics.row.stuck"`, `data-testid="system.wb.knobs"`)
	writeSystemIssue(t, filepath.Dir(r.proj.IssueDir), "economy/shop", "Shop", "idea", "Economy", "", "")
	other := pageGet(t, ts.URL+"/p/mp/systems/Economy")
	if strings.Contains(other, "system.wb.metrics") || strings.Contains(other, "system.wb.knobs") {
		t.Error("Economy lists no metrics or knobs but shows a panel")
	}
}

func TestWbToneAndVerdict(t *testing.T) {
	for in, want := range map[string]string{"green": "green", "red": "red", "amber": "you", "blue": "agent", "": "idle", "muted": "idle"} {
		if got := wbTone(in); got != want {
			t.Errorf("wbTone(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string][2]string{tracker.VerdictOK: {"green", "ok"}, tracker.VerdictNear: {"you", "near"}, tracker.VerdictOff: {"red", "off"}, tracker.VerdictUnmeasured: {"idle", "not measured"}, "": {"idle", "no verdict"}} {
		if wbVerdictTone(in) != want[0] || wbVerdictText(in) != want[1] {
			t.Errorf("verdict %q = %s/%s, want %v", in, wbVerdictTone(in), wbVerdictText(in), want)
		}
	}
}
