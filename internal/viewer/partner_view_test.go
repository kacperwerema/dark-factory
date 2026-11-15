package viewer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/partner"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// partnerFixture is the stand-in OpenMontage checkout written for the
// adapter's tests (internal/partner/testdata/openmontage).
func partnerFixture(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "partner", "testdata", "openmontage"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// setupPartnerProject is the station fixture with a Video department that
// owns the UI system, on its own workflow file, with an OpenMontage
// partner; the station issue is partner project project.
func setupPartnerProject(t *testing.T, project string, cfg tracker.Partner) (tracker.Project, string) {
	t.Helper()
	p := setupStationProject(t)
	path := filepath.Join(p.IssueDir, "UI", "station.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("system: \"UI\"\n"), []byte("system: \"UI\"\npartner_project: \""+project+"\"\n"), 1)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// The department's workflow file: Product's, copied, so the station
	// keeps its path; AC3 checks it is byte for byte the same after a swap.
	wfSrc := filepath.Join(p.Root(), "workflow.yaml")
	wfRaw, err := os.ReadFile(wfSrc)
	if err != nil {
		t.Fatal(err)
	}
	wfRel := filepath.Join("departments", "video", "workflow.yaml")
	if err := os.MkdirAll(filepath.Join(p.Root(), "departments", "video"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Root(), wfRel), wfRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	p.Departments = []tracker.Department{{Name: "Video", Systems: []string{"UI"}, Workflow: wfRel, Partner: &cfg}}
	return p, filepath.Join(p.Root(), wfRel)
}

func omPartner(t *testing.T) tracker.Partner {
	return tracker.Partner{Name: "OpenMontage", Adapter: "openmontage", Path: partnerFixture(t)}
}

func stationHTML(t *testing.T, p tracker.Project) string {
	t.Helper()
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/issue/ui/station")
	if code != 200 {
		t.Fatalf("station: %d %s", code, html)
	}
	return html
}

func rowsFor(rows []WbQueueCard, slug string) []WbQueueCard {
	var out []WbQueueCard
	for _, r := range rows {
		if r.Slug == slug {
			out = append(out, r)
		}
	}
	return out
}

// AC1: a fixture project at awaiting_human shows on the Station and as one
// Needs you row, with the summary and the spend against the budget; the
// room's foot says it waits on you.
func TestPartnerAwaitingOnStationAndNeedsYou(t *testing.T) {
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	p, _ := setupPartnerProject(t, "waiting", omPartner(t))

	html := stationHTML(t, p)
	assertContains(t, html,
		`data-testid="station.partner" data-state="awaiting"`, `Partner · OpenMontage`, `project waiting`,
		`<span class="wb-chip-dot"></span>OpenMontage: pitch · awaiting you</span>`,
		`Three concepts; the second fits a 30 s cut`,
		`data-testid="station.partner.spend">$0.40 spent of $2.00</li>`,
		`<code>cost_plan</code>, <code>pitch_packet</code>`)

	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	rows := rowsFor(needsYouRows(&p, "", "/p/agents", openOnly(&p, issues), time.Now()), "ui/station")
	if len(rows) != 1 {
		t.Fatalf("%d Needs you rows for the issue, want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Partner != "OpenMontage: pitch · awaiting you" || !strings.Contains(r.PartnerNote, "$0.40 spent of $2.00") || !strings.Contains(r.PartnerNote, "Three concepts") {
		t.Errorf("row partner %q note %q", r.Partner, r.PartnerNote)
	}
	if r.Kind == "partner" && (r.Label != "OpenMontage: pitch" || !strings.HasSuffix(r.URL, "#partner")) {
		t.Errorf("partner row label %q url %q", r.Label, r.URL)
	}

	// The page renders the row's partner line.
	ts := newTestServer(t, []tracker.Project{p})
	defer ts.Close()
	_, needs := getBody(t, ts.URL+"/p/agents/needs-you")
	assertContains(t, needs, `data-testid="needs_you.partner">OpenMontage: pitch · awaiting you · Three concepts; the second fits a 30 s cut · $0.40 spent of $2.00`)

	// The room.
	room := companyDeptRoom(&p, "/p/agents", p.Departments[0], 0, issues, nil, nil, map[string]bool{}, time.Now())
	if !hasFoot(room.Foot, "OpenMontage · 1 awaiting you", "you") {
		t.Errorf("room foot %+v", room.Foot)
	}
}

// One decision, one row: a ready gate on the issue carries the partner's
// words instead of a second row; without the gate the partner row stands
// alone.
func TestPartnerRowMergesWithGate(t *testing.T) {
	issue := &tracker.Issue{Slug: "ui/v", Title: "A video", Status: "proposal"}
	w := &WbPartner{Name: "OpenMontage", Stage: "pitch", Awaiting: true, Line: "OpenMontage: pitch · awaiting you", Spend: "$0.40 spent of $2.00"}
	p := &tracker.Project{Name: "P", Slug: "p"}
	gate := mapQueueRow(p, "", "/p/p", WaitingItem{Issue: issue, Kind: "gate", Target: "script", Partner: w}, time.Now())
	if gate.Kind != "gate" || !gate.Ready || gate.Partner != w.Line || gate.PartnerNote != "$0.40 spent of $2.00" {
		t.Errorf("gate row %+v", gate)
	}
	alone := mapQueueRow(p, "", "/p/p", WaitingItem{Issue: issue, Kind: "partner", Partner: w}, time.Now())
	if alone.Kind != "partner" || alone.Ready || alone.Label != "OpenMontage: pitch" || alone.CTA != "Open" {
		t.Errorf("partner row %+v", alone)
	}
	g := needsYouGroup(p, "", "/p/p", []WbQueueCard{alone}, nil)
	if len(g.Sections) != 1 || g.Sections[0].Key != "approve" {
		t.Errorf("partner row section %+v", g.Sections)
	}
}

// End to end through waitingOnOwner: the station issue (ready gate) gets
// one gate row carrying the partner; a backlog issue (no gate) waiting at
// the partner gets one partner row.
func TestPartnerRowsFromWaitingOnOwner(t *testing.T) {
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	pr, _ := setupPartnerProject(t, "waiting", omPartner(t))
	body := "---\ntitle: \"Second video\"\nstatus: \"backlog\"\nsystem: \"UI\"\npartner_project: \"waiting\"\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(pr.IssueDir, "UI", "second.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	issues, _ := tracker.LoadIssues(pr.IssueDir)
	rows := needsYouRows(&pr, "", "/p/agents", openOnly(&pr, issues), time.Now())
	st := rowsFor(rows, "ui/station")
	if len(st) != 1 || st[0].Kind != "gate" || st[0].Partner == "" {
		t.Errorf("station rows %+v, want one gate row with the partner", st)
	}
	second := rowsFor(rows, "ui/second-video")
	if len(second) != 1 || second[0].Kind != "partner" || second[0].Label != "OpenMontage: pitch" {
		t.Errorf("backlog rows %+v, want one partner row", second)
	}
}

// AC2: an unknown checkpoint version shows "partner format not understood"
// and no stage, and is no Needs you row.
func TestPartnerUnknownFormatOnStation(t *testing.T) {
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	p, _ := setupPartnerProject(t, "newer-format", omPartner(t))
	html := stationHTML(t, p)
	assertContains(t, html, `data-testid="station.partner" data-state="unknown"`,
		`data-testid="station.partner.line"><span class="wb-chip wb-chip--red">OpenMontage: partner format not understood (checkpoint_brief.json version &#34;2.0&#34;)</span></p>`)
	if strings.Contains(html, "· awaiting you") || strings.Contains(html, "· working") {
		t.Error("a stage was guessed")
	}
	issues, _ := tracker.LoadIssues(p.IssueDir)
	for _, r := range rowsFor(needsYouRows(&p, "", "/p/agents", openOnly(&p, issues), time.Now()), "ui/station") {
		if r.Kind == "partner" || r.Partner != "" {
			t.Errorf("an unknown format made a partner row: %+v", r)
		}
	}
	room := companyDeptRoom(&p, "/p/agents", p.Departments[0], 0, issues, nil, nil, map[string]bool{}, time.Now())
	if !hasFoot(room.Foot, "OpenMontage · 1 not understood", "") {
		t.Errorf("room foot %+v", room.Foot)
	}
}

// stubPartner is a second partner for AC3: it reads nothing and says the
// project waits at its own stage.
type stubPartner struct{}

func (stubPartner) Status(cfg tracker.Partner, dir, project string) (partner.Status, error) {
	return partner.Status{Stage: "storyboard review", State: partner.StateAwaiting, Summary: "stub says hi"}, nil
}

// AC3: swapping partner: to a second (stub) adapter changes what the
// Station and Needs you show, with the department's workflow file left
// byte for byte as it was.
func TestPartnerSwapNeedsNoWorkflowChange(t *testing.T) {
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	defer partner.Register("stub-studio", stubPartner{})()

	p, wfPath := setupPartnerProject(t, "waiting", omPartner(t))
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, stationHTML(t, p), `OpenMontage: pitch · awaiting you`)

	// The swap: only the partner: block changes.
	p.Departments[0].Partner = &tracker.Partner{Name: "Stub Studio", Adapter: "stub-studio", Path: t.TempDir()}
	html := stationHTML(t, p)
	assertContains(t, html, `Partner · Stub Studio`, `<span class="wb-chip-dot"></span>Stub Studio: storyboard review · awaiting you</span>`, `stub says hi`)
	if strings.Contains(html, "OpenMontage") {
		t.Error("the old partner still shows")
	}
	issues, _ := tracker.LoadIssues(p.IssueDir)
	rows := rowsFor(needsYouRows(&p, "", "/p/agents", openOnly(&p, issues), time.Now()), "ui/station")
	if len(rows) != 1 || rows[0].Partner != "Stub Studio: storyboard review · awaiting you" {
		t.Errorf("rows after the swap: %+v", rows)
	}
	after, _ := os.ReadFile(wfPath)
	if !bytes.Equal(before, after) {
		t.Error("the department's workflow file changed")
	}
	if wf, err := p.WorkflowFor("Video"); err != nil || wf == nil {
		t.Errorf("the department's workflow no longer loads: %v", err)
	}
}

// A department whose partner config is broken says so in its room, and
// the Station names the problem instead of failing.
func TestPartnerConfigProblemsShown(t *testing.T) {
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	p, _ := setupPartnerProject(t, "waiting", tracker.Partner{Name: "OpenMontage", Adapter: "openmontage", Path: filepath.Join(t.TempDir(), "gone")})
	assertContains(t, stationHTML(t, p), `data-state="problem"`, `is missing`)
	issues, _ := tracker.LoadIssues(p.IssueDir)
	room := companyDeptRoom(&p, "/p/agents", p.Departments[0], 0, issues, nil, nil, map[string]bool{}, time.Now())
	if !hasFoot(room.Foot, "OpenMontage: path missing", "red") {
		t.Errorf("room foot %+v", room.Foot)
	}
	p.Departments[0].Partner = &tracker.Partner{Name: "X", Adapter: "nope", Path: partnerFixture(t)}
	room = companyDeptRoom(&p, "/p/agents", p.Departments[0], 0, issues, nil, nil, map[string]bool{}, time.Now())
	if !hasFoot(room.Foot, "X: adapter unknown", "red") {
		t.Errorf("room foot %+v", room.Foot)
	}
}

func hasFoot(foot []WbFootItem, text, tone string) bool {
	for _, f := range foot {
		if f.Text == text && f.Tone == tone {
			return true
		}
	}
	return false
}
