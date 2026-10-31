package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func TestReportPage(t *testing.T) {
	proj := setupStatusProject(t, time.Now())
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, body := getResp(t, ts.URL+"/p/status/status")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d:\n%s", resp.StatusCode, body)
	}
	assertContains(t, body,
		`data-testid="report.page"`,
		// Header: Map › project, Board | Report with Report on.
		`<a class="wb-crumb wb-crumb--link" href="/">Map</a>`,
		`href="/p/status/company" data-testid="view.board"`, `aria-current="page" data-testid="view.report">Report</span>`,
		`>3 waiting on you</a>`,
		// Title band and its buttons.
		`data-copy-md="/p/status/status.md"`, `href="/p/status/status.md" data-testid="report.raw_md"`,
		`href="/p/status/status?classic=1" data-testid="report.classic"`,
		// The three cards: a gate, a playtest and a question wait.
		`data-testid="report.card.waiting"`, `data-testid="report.card.agents"`, `data-testid="report.card.next"`,
		`<b>1</b> <span>to approve</span>`, `<b>1</b> <span>to check</span>`, `question from an agent`,
		`No agent is running in this project.`,
		// The bottleneck with real links to the oldest of each kind.
		`data-testid="report.bottleneck"`, `No agent is running and 3 things wait on you.`,
		`href="/p/status/issue/ui/ready#approve-`, `data-testid="report.action.gates"`,
		`data-testid="report.action.testing"`, `data-testid="report.action.question"`,
		// Where we are: no versions, so milestones are locked.
		`data-testid="report.where"`, `data-testid="report.where.locked"`, `Milestones locked.`,
		`data-testid="report.shipped"`, `data-testid="report.health"`, `href="/p/status/health"`,
		`data-testid="report.unlock"`, `data-testid="report.unlock.milestones"`, `data-copy="issue-cli set-meta &lt;slug&gt; --key version --value 0.1"`,
	)
	if strings.Contains(body, `data-testid="status.page"`) {
		t.Error("/status renders the classic page")
	}
}

func TestReportClassicAndMarkdownUnchanged(t *testing.T) {
	proj := setupStatusProject(t, time.Now())
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, classic := getResp(t, ts.URL+"/p/status/status?classic=1")
	if !strings.Contains(classic, `data-testid="status.page"`) || strings.Contains(classic, `data-testid="report.page"`) {
		t.Error("?classic=1 does not render the classic page")
	}
	resp, md := getResp(t, ts.URL+"/p/status/status.md")
	if resp.StatusCode != 200 || !strings.HasPrefix(md, "# Status status, ") || !strings.Contains(md, "## Waiting on you (3)") {
		t.Errorf("status.md changed:\n%s", md)
	}
	if strings.Contains(md, "<") && strings.Contains(md, "report.") {
		t.Error("status.md carries the report's HTML")
	}
}

func TestCompanyPageLinksTheReport(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, html := getResp(t, ts.URL+"/p/agents/company?classic=1")
	assertContains(t, html, `aria-current="page" data-testid="view.board">Board</span>`, `href="/p/agents/status" data-testid="view.report"`)
}

func TestReportBottleneckSentences(t *testing.T) {
	gate := WaitingItem{Kind: "gate", Target: "testing", Issue: &tracker.Issue{Slug: "ui/a", Status: "in progress"}}
	m := &StatusModel{Prefix: "/p/x", Waiting: []WaitingItem{gate}}
	robots := []reportRobot{{State: "gate"}, {State: "gate"}, {State: "working"}}
	b := reportBottleneck(m, robots, "/p/x", true)
	if b.Sentence != "2 of 3 agents wait on you. The factory is stuck on gates, not on code." {
		t.Errorf("sentence = %q", b.Sentence)
	}
	if len(b.Actions) != 1 || b.Actions[0].URL != "/p/x/issue/ui/a#approve-testing" || b.Actions[0].Count != 1 {
		t.Errorf("actions = %+v", b.Actions)
	}
	calm := reportBottleneck(&StatusModel{Prefix: "/p/x"}, []reportRobot{{State: "working"}}, "/p/x", true)
	if !calm.Calm || calm.Sentence != "Nothing waits on you. 1 agent is working." {
		t.Errorf("calm = %+v", calm)
	}
	empty := reportBottleneck(&StatusModel{Prefix: "/p/x"}, nil, "/p/x", false)
	if len(empty.Actions) != 1 || empty.Actions[0].Spawn != "/p/x/lead/spawn" {
		t.Errorf("an empty queue offers to start the CEO: %+v", empty.Actions)
	}
}

func TestNextSteps(t *testing.T) {
	got := nextSteps("Intro line.\n\n- **First** step\n  continued\n- `ui/a` second\n1. third\n")
	if strings.Join(got, "|") != "First step|ui/a second|third" {
		t.Errorf("steps = %q", got)
	}
	if got := nextSteps("Just prose.\n\nAnd more."); len(got) != 2 {
		t.Errorf("paragraphs = %q", got)
	}
}

// TestReportComponentsStandAlone renders every report component with only
// its own data.
func TestReportComponentsStandAlone(t *testing.T) {
	srv, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]any{
		"wb/view-toggle":  wbViewToggle("/p/orchard", "report"),
		"wb/lock-icon":    nil,
		"wb/badge":        WbBadge{Tone: "red", Label: "No backup", URL: "#unlock"},
		"wb/report-title": WbReportTitle{Title: "Report · Orchard", Session: "lead-orchard", MDURL: "/p/orchard/status.md"},
		"wb/action":       WbAction{Label: "Copy", Copy: "issue-cli ci status", TestID: "ci"},
		"wb/stack-bar":    []WbSegment{{Value: 2, Color: "#F57A1F", Label: "gates"}},
		"wb/squares":      []WbSquare{{Color: "#2BB673", Title: "ui/a · working"}},
		"wb/legend":       []WbLegendRow{{Count: 2, Label: "working"}},
		"wb/count-card":   WbCountCard{ID: "waiting", Icon: "you", Title: "Waiting on you", TestID: "waiting", Count: 2},
		"wb/bottleneck":   WbBottleneck{Eyebrow: "BOTTLENECK", Sentence: "2 of 3 agents wait on you."},
		"wb/bar":          WbBar{Label: "All issues", Done: 1, Total: 4},
		"wb/where":        WbWhere{All: WbBar{Label: "All issues"}, Locked: "Milestones locked."},
		"wb/shipped":      WbShipped{Count: 1, Rows: []WbShippedRow{{Area: "ui", Slug: "ui/a"}}},
		"wb/score-box":    WbScoreBox{Label: "AGENT OPINION", Value: "—"},
		"wb/health":       WbHealth{Weakest: &HealthWeakest{Name: "big files", Score: 30}},
		"wb/unlock-item":  WbUnlockItem{Key: "epics", Title: "Epics", Action: WbAction{Label: "Copy", Copy: "x"}},
		"wb/unlock":       WbUnlock{Total: 1, Items: []WbUnlockItem{{Key: "review", Title: "Review", On: true}}},
		"wb/bench":        &WbBench{Rows: []WbBenchRow{{Name: "v", NoRun: true, Cmd: "issue-cli bench run v"}}},
		"wb/check-mark":   true,
		"wb/release":      &StatusModel{ReleaseWait: &ReleaseWait{Changelog: "CHANGELOG.md"}},
		"wb/ci":           &StatusModel{CI: &CIRow{Line: "main green at abc1234"}},
	} {
		var b strings.Builder
		if err := srv.tmpl.ExecuteTemplate(&b, name, data); err != nil || strings.TrimSpace(b.String()) == "" {
			t.Errorf("%s: %v (output %q)", name, err, b.String())
		}
	}
}

// getReport is the Report (/status, the Workshop Board look) for proj.
func getReport(t *testing.T, proj tracker.Project) string {
	t.Helper()
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	resp, page := getResp(t, ts.URL+"/p/"+proj.Slug+"/status")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d:\n%s", resp.StatusCode, page)
	}
	if !strings.Contains(page, `data-testid="report.page"`) {
		t.Fatal("/status is not the Report")
	}
	return page
}

// TestReportWaitingForRelease: the Report carries the classic page's
// Waiting for release card: milestone readiness, a link per open issue,
// ## Unreleased and unpushed commits.
func TestReportWaitingForRelease(t *testing.T) {
	proj := releaseProject(t, releaseFixture{
		changelog: releaseChangelog, ciBlock: ciBlockOn, versions: true,
		ciState:  &ci.State{Branch: "main", State: ci.Green, GreenSha: "d89c1b3000", GreenAt: time.Now()},
		statuses: map[string]string{"shipped": "done", "busy": "in progress", "fresh": "idea", "shelved": "parked"},
	})
	stubBranchState(t, &BranchState{Branch: "main", Upstream: "origin/main", Ahead: 14, Head: "7c41e0a0000", HeadAt: time.Now().Add(-17 * time.Minute)})
	page := getReport(t, proj)

	wantAll(t, "report", page,
		`data-testid="status.release_wait"`,
		`data-testid="status.release_wait.state">v0.53.0 not ready</span>`,
		`<span class="wb-rbar-label">Milestone 0.53</span><span class="wb-rbar-num">1/3 <span>done</span>`,
		`data-testid="status.release_wait.check.issues" data-ok="false"`,
		`data-testid="status.release_wait.check.ci" data-ok="false"`,
		`<a class="wb-rlink" href="#ci-h">Local CI</a>`,
		`data-testid="status.release_wait.check.changelog" data-ok="true"`,
		`data-testid="status.release_wait.open.row.ui/busy"`,
		`<a class="wb-rel-title" href="/p/rel/issue/ui/busy">Busy</a>`,
		`agent-busy`,
		`Unreleased · 2`,
		`<strong>Local CI.</strong> Runs on every merge, wrapped onto a second line.`,
		`data-testid="status.release_wait.unpushed"`,
		`14 commits on main not on origin/main`,
		`newest 17m ago`,
		// The Local CI card is there for the check's link.
		`id="ci-h">Local CI</h2>`,
		`data-testid="status.ci.state">green</span>`,
	)
	wantNone(t, "report", page, `status.release_wait.ready"`, `open.row.ui/shelved`)
}

func TestReportReadyToCut(t *testing.T) {
	head := "7c41e0a0000"
	proj := releaseProject(t, releaseFixture{
		changelog: releaseChangelog, ciBlock: ciBlockOn, versions: true,
		ciState:  &ci.State{Branch: "main", State: ci.Green, GreenSha: head, GreenAt: time.Now()},
		statuses: map[string]string{"shipped": "done", "busy": "done", "shelved": "parked"},
	})
	stubBranchState(t, &BranchState{Branch: "main", Upstream: "origin/main", Head: head, HeadAt: time.Now()})
	page := getReport(t, proj)
	wantAll(t, "report", page,
		`data-testid="status.release_wait.state">ready to cut</span>`,
		`data-testid="status.release_wait.ready"`,
		`<b>Ready to cut v0.53.0</b>`,
		`push main to origin/main`,
		`Nothing: main matches origin/main`,
	)
}

// TestReportReleaseCandidate: cut, then soak and publish on the owner's ok.
func TestReportReleaseCandidate(t *testing.T) {
	proj := readyRCProject(t, rcBlock)
	stubCandidates(t, nil, 0)
	page := getReport(t, proj)
	wantAll(t, "report", page,
		`data-testid="status.release_wait.cut_rc"`,
		`<b>Ready to cut release candidate v0.53.0-rc.1</b>`,
		`Cut it: <code class="wb-code">issue-cli release cut v0.53.0</code>`,
		`issue-cli release pick &lt;slug&gt;`,
	)

	proj = readyRCProject(t, rcBlock)
	stubCandidates(t, map[string]time.Time{"v0.53.0-rc.2": time.Now().Add(-49 * time.Hour)}, 0)
	page = getReport(t, proj)
	wantAll(t, "report", page,
		`data-testid="status.release_wait.state">v0.53.0-rc.2 soaking</span>`,
		`data-testid="status.rc_badge"`,
		`data-testid="status.release_wait.candidate.summary"><span class="wb-mono-inline">v0.53.0-rc.2</span> · cut 2d ago · nothing merged since</summary>`,
		`data-testid="status.release_wait.publish"`,
		`<b>v0.53.0-rc.2 soaking: publish v0.53.0 only on the owner's ok</b>`,
		`Never publish without their ok.`,
	)
}

// TestReportLocalCI: red CI with its last runs, what failed and what landed.
func TestReportLocalCI(t *testing.T) {
	proj, root := ciViewProject(t, ciBlockOn, redState())
	t0 := time.Now().Add(-time.Hour)
	for _, r := range []ci.Run{
		{Sha: "aaaaaaa111", Tier: "default", State: ci.Green, Started: t0, Finished: t0.Add(time.Minute)},
		{Sha: "bbbbbbb222", Tier: "default", State: ci.Red, Failed: "make validate", Started: t0.Add(10 * time.Minute), Finished: t0.Add(11 * time.Minute)},
	} {
		data, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(ci.RunsDir(root), r.Sha+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	page := getReport(t, proj)
	wantAll(t, "report", page,
		`data-testid="status.ci"`,
		`class="wb-chip wb-chip--red" data-testid="status.ci.state">red</span>`,
		`main red since def5678: make validate: TestBroken`,
		`data-testid="status.ci.runs"`,
		`data-testid="status.ci.run.aaaaaaa" data-state="green"`,
		`data-testid="status.ci.run.bbbbbbb" data-state="red"`,
		`data-testid="status.ci.failed"`,
		`<a class="wb-rel-title" href="/p/civ/issue/ui/a">a</a>`,
		`and 1 commit without an issue`,
		`data-testid="status.ci.log"`,
	)
}

// TestReportNoReleaseOrCI: neither card without a milestone, changelog or
// ci: block.
func TestReportNoReleaseOrCI(t *testing.T) {
	proj := releaseProject(t, releaseFixture{statuses: map[string]string{"busy": "in progress"}})
	stubBranchState(t, nil)
	page := getReport(t, proj)
	wantNone(t, "report", page, `data-testid="status.release_wait"`, `data-testid="status.ci"`)
}
