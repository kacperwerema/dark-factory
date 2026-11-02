package viewer

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/decisionstats"
	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// decisionsFixture writes a project whose ledger, counted by hand, is:
//
//	rules#D-1 project, hard, the owner's words      in force
//	rules#D-2 replaced by rules#D-3                  out
//	rules#D-3 issue scope, soft, no quote            in force, provisional
//	rules#D-4 system:UI, soft, YOLO provisional      in force, provisional
//	rules#D-5 type:bugfix, expired 2026-01-01        out
//
// In force 3 (project 1, issue 1, system 1), hard 1, soft 2, provisional 2.
// The log, inside the window: rules#D-1 held 3 times on 2 issues (1 passed),
// rules#D-3 held once; 2 start and 1 dispatch briefings over 2 issues;
// rules#D-1 challenged and resolved, rules#D-4 challenged: 1 open of 2.
// Outside the window: one more hold and one briefing, not counted.
func decisionsFixture(t *testing.T, now time.Time) tracker.Project {
	t.Helper()
	proj, tmp := setupTestProject(t)
	proj.WorkDir = tmp
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(proj.IssueDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("rules.md", "---\ntitle: \"Rules\"\nstatus: \"done\"\n---\n\n## Decisions\n\n"+
		`- **D-1** · 2026-10-01 · chat: Agents merge into local main, "merge locally" {scope: project; hard}`+"\n"+
		`- **D-2** · 2026-10-02 · chat: Old storage rule, "old"`+"\n"+
		`- **D-3** · 2026-10-03 · chat: New storage rule (replaces D-2)`+"\n"+
		`- **D-4** · 2026-10-04 · YOLO, provisional: Cards wrap at 390px {scope: system:UI}`+"\n"+
		`- **D-5** · 2026-10-05 · chat: Bugfixes skip design, "skip it" {scope: type:bugfix; expires: 2026-01-01}`+"\n")
	write("ui-card.md", "---\ntitle: \"UI card\"\nstatus: \"in design\"\n---\n\nBody.\n")
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	held := func(write, ref, issue string, passed bool, ago time.Duration) tracker.DecisionEvent {
		return tracker.DecisionEvent{Write: write, Op: tracker.DecisionOpDone, Event: tracker.DecisionHeld, Ref: ref, At: at(ago),
			Rule: tracker.DecisionsCitedRule, Issue: issue, From: "in design", To: "backlog", Passed: passed}
	}
	injected := func(write, briefing, issue string, ago time.Duration) tracker.DecisionEvent {
		return tracker.DecisionEvent{Write: write, Op: tracker.DecisionOpDone, Event: tracker.DecisionInjected, At: at(ago),
			Briefing: briefing, Issue: issue, Refs: []string{"rules#D-1"}}
	}
	day := 24 * time.Hour
	evs := []tracker.DecisionEvent{
		{Write: "r1", Op: tracker.DecisionOpDone, Event: tracker.DecisionRecorded, Ref: "rules#D-1", At: at(40 * day)},
		held("h0", "rules#D-1", "ui-card", false, 40*day), // outside the window
		injected("i0", tracker.BriefingStart, "old", 40*day),
		held("h1", "rules#D-1", "ui-card", false, 3*day),
		held("h2", "rules#D-1", "ui-card", true, 2*day),
		held("h3", "rules#D-1", "rules", false, day),
		held("h4", "rules#D-3", "ui-card", false, day),
		// A pending hold without its done never counts.
		{Write: "h5", Op: tracker.DecisionOpPending, Event: tracker.DecisionHeld, Ref: "rules#D-3", At: at(day), Issue: "ui-card"},
		injected("i1", tracker.BriefingStart, "ui-card", 3*day),
		injected("i2", tracker.BriefingStart, "rules", 2*day),
		injected("i3", tracker.BriefingDispatch, "ui-card", day),
		{Write: "c1", Op: tracker.DecisionOpDone, Event: tracker.DecisionChallenged, Ref: "rules#D-1", Issue: "ui-card", At: at(5 * day)},
		{Write: "c2", Op: tracker.DecisionOpDone, Event: tracker.DecisionResolved, Ref: "rules#D-1", At: at(4 * day)},
		{Write: "c3", Op: tracker.DecisionOpDone, Event: tracker.DecisionChallenged, Ref: "rules#D-4", Issue: "ui-card", At: at(day)},
	}
	if err := tracker.AppendDecisionEvents(proj.IssueDir, evs); err != nil {
		t.Fatal(err)
	}
	return proj
}

func tileValues(c WbStatsDecisions) map[string]string {
	out := map[string]string{}
	for _, tl := range c.Tiles {
		out[tl.Key] = tl.Value
	}
	return out
}

// AC2: the card's numbers equal the counts made by hand from the
// fixture's issues and log; the family's snapshot rows are the same counts.
func TestStatsDecisionsCardCounts(t *testing.T) {
	now := time.Now()
	proj := decisionsFixture(t, now)
	card := loadDecisionsCard(&proj, "/p/test-project", "Last 30 days", now.AddDate(0, 0, -30), now.Add(time.Second), now)
	if card.Empty != nil || card.Err != "" {
		t.Fatalf("card empty/err: %+v %q", card.Empty, card.Err)
	}
	want := map[string]string{"in_force": "3", "hard": "1", "soft": "2", "provisional": "2", "challenges": "1"}
	if got := tileValues(card); len(got) != len(want) {
		t.Fatalf("tiles = %v", got)
	} else {
		for k, v := range want {
			if got[k] != v {
				t.Errorf("tile %s = %q, want %q", k, got[k], v)
			}
		}
	}
	var scopes []string
	for _, s := range card.Scopes {
		scopes = append(scopes, s.Key+"="+strconv.Itoa(s.N))
	}
	if strings.Join(scopes, " ") != "issue=1 system=1 project=1" {
		t.Errorf("scopes = %v", scopes)
	}
	var holds []string
	for _, r := range card.Holds.Rows {
		var cells []string
		for _, c := range r {
			cells = append(cells, c.Text)
		}
		holds = append(holds, strings.Join(cells, "|"))
	}
	if strings.Join(holds, "\n") != "rules#D-1|3|2|1|decisions_cited\nrules#D-3|1|1|0|decisions_cited" {
		t.Errorf("holds:\n%s", strings.Join(holds, "\n"))
	}
	if card.Holds.Rows[0][0].URL != "/p/test-project/issue/rules" {
		t.Errorf("hold link = %q", card.Holds.Rows[0][0].URL)
	}
	var briefs []string
	for _, r := range card.Briefings.Rows {
		briefs = append(briefs, r[0].Text+"="+r[1].Text)
	}
	if strings.Join(briefs, " ") != "issue-cli start=2 worker dispatch=1" || card.Briefings.Sub != "3 briefings, 2 issues briefed" {
		t.Errorf("briefings = %v (%q)", briefs, card.Briefings.Sub)
	}
	if !strings.HasPrefix(card.Sub, "5 decisions recorded") {
		t.Errorf("sub = %q", card.Sub)
	}

	// The health family counts the same ledger (its window: 30 days).
	x, evs, err := decisionstats.Load(&proj)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]float64{}
	for _, m := range decisionstats.Metrics(decisionstats.Measure(decisionstats.Input{Index: x, Events: evs, Today: now, From: now.Add(-decisionstats.Window), To: now.Add(time.Second)})) {
		rows[health.RowID(m.Name, m.Labels)] = m.Value
	}
	for id, v := range map[string]float64{
		"decisions.total": 5, "decisions.in_force": 3, "decisions.hard": 1, "decisions.soft": 2, "decisions.provisional": 2,
		"decisions.challenges_open": 1, "decisions.holds": 4, "decisions.injected": 3,
		"decisions.in_force_by_scope{scope=project}": 1, "decisions.injected_by_briefing{briefing=start}": 2,
	} {
		if rows[id] != v {
			t.Errorf("%s = %v, want %v", id, rows[id], v)
		}
	}
}

// AC4: a project with no decisions and an empty log shows the empty state
// naming decide, and no tile, chip or table: no zeros dressed as data.
func TestStatsDecisionsCardEmpty(t *testing.T) {
	withMockTmuxSessions(t, nil)
	withStubTelemetryReport(t, func(*tracker.Project) ([]byte, error) { return []byte(`{"v":1,"enabled":true}`), nil })
	proj, tmp := setupTestProject(t)
	proj.WorkDir = tmp
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, page := getBody(t, ts.URL+"/p/test-project/stats")
	i := strings.Index(page, `data-testid="stats.decisions.card"`)
	if i < 0 {
		t.Fatal("no Decisions card on the Stats page")
	}
	card := page[i:]
	card = card[:strings.Index(card, "</section>")]
	for _, want := range []string{"No decisions recorded yet", `data-copy="issue-cli --project test-project decide &lt;slug&gt; --text &#34;...&#34;"`} {
		if !strings.Contains(card, want) {
			t.Errorf("empty card lacks %s:\n%s", want, card)
		}
	}
	for _, bad := range []string{"wb-dtile", "stats.decisions.scopes", "stats.table_card", ">0<"} {
		if strings.Contains(card, bad) {
			t.Errorf("empty card shows %s:\n%s", bad, card)
		}
	}
	if strings.Contains(page, `data-testid="stats.chart.decisions"`) {
		t.Error("the Decisions trend is drawn with no decision")
	}
}

// The card renders its tiles, chips and tables on the page.
func TestStatsDecisionsCardRenders(t *testing.T) {
	withMockTmuxSessions(t, nil)
	withStubTelemetryReport(t, func(*tracker.Project) ([]byte, error) { return []byte(`{"v":1,"enabled":true}`), nil })
	proj := decisionsFixture(t, time.Now())
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, page := getBody(t, ts.URL+"/p/test-project/stats")
	for _, want := range []string{
		`data-testid="stats.decisions"`, `data-testid="stats.decisions.in_force"`, `data-testid="stats.decisions.hard"`,
		`data-testid="stats.decisions.provisional"`, `data-testid="stats.decisions.challenges"`, `data-testid="stats.decisions.scope.system"`,
		`data-testid="stats.table_card.decision-holds"`, `data-testid="stats.table_card.decision-briefings"`,
		`<a href="/p/test-project/issue/rules">rules#D-1</a>`, `href="/p/test-project/decisions?state=active"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("Stats page lacks %s", want)
		}
	}
}

// AC3: a snapshot taken by health.Take carries the decisions family, and
// the Decisions in force trend draws one point per snapshot from two.
func TestDecisionsFamilySnapshotAndTrend(t *testing.T) {
	now := time.Now().Truncate(time.Minute)
	proj := decisionsFixture(t, now)
	root := proj.WorkDir
	env := health.Env{Root: root, Project: &proj, Workflow: proj.LoadWorkflow(), Now: now.Add(-24 * time.Hour)}
	first, _, err := health.Take(context.Background(), env, health.TriggerDaily)
	if err != nil {
		t.Fatal(err)
	}
	if res, ok := first.Families[decisionstats.Family]; !ok || res.Error != "" || res.Metrics == 0 {
		t.Fatalf("first snapshot's decisions family = %+v (ok %v)", res, ok)
	}
	if v, _ := first.Value("decisions.hard", nil); v != 1 {
		t.Fatalf("first decisions.hard = %v", v)
	}

	// A second hard decision before the next day's snapshot.
	path := filepath.Join(proj.IssueDir, "rules.md")
	body, _ := os.ReadFile(path)
	body = append(body, []byte(`- **D-6** · 2026-10-06 · chat: Every card has a test id, "ids on cards" {scope: project; hard}`+"\n")...)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	env.Now = now
	if _, _, err := health.Take(context.Background(), env, health.TriggerDaily); err != nil {
		t.Fatal(err)
	}

	series := decisionSeries(health.Range(root, time.Time{}, now.Add(time.Minute)))
	if len(series) != 4 {
		t.Fatalf("series = %+v", series)
	}
	hard := series[0]
	if hard.Name != "hard" || len(hard.Points) != 2 || hard.Points[0].Value != 1 || hard.Points[1].Value != 2 {
		t.Fatalf("hard series = %+v", hard)
	}

	withMockTmuxSessions(t, nil)
	withStubTelemetryReport(t, func(*tracker.Project) ([]byte, error) { return []byte(`{"v":1,"enabled":true}`), nil })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, page := getBody(t, ts.URL+"/p/test-project/stats")
	i := strings.Index(page, `data-testid="stats.chart.decisions"`)
	if i < 0 {
		t.Fatal("the Stats page has no Decisions in force trend")
	}
	if !strings.Contains(page[i:], "<svg") {
		t.Error("the Decisions trend draws no chart")
	}
}
