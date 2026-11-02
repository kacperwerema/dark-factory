package viewer

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/telemetry"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// inlineScript is a <script> without a src: the Workshop Board pages carry
// none (the strict-CSP plan).
var inlineScript = regexp.MustCompile(`<script(\s[^>]*)?>`)

func assertNoInlineScript(t *testing.T, page string) {
	t.Helper()
	for _, tag := range inlineScript.FindAllString(page, -1) {
		if !strings.Contains(tag, `src="/static/`) {
			t.Errorf("script that is not a viewer file: %s", tag)
		}
	}
	if strings.Contains(page, " onclick=") || strings.Contains(page, " onchange=") {
		t.Error("inline handler")
	}
}

// AC1: /p/<slug>/stats is the Workshop Board page with the hall tabs, the
// options as links, Now, Trends, Where the time goes, Token cost, CLI usage
// and the time tables; ?classic=1 is the classic page.
func TestStatsWbPageRenders(t *testing.T) {
	withMockTmuxSessions(t, nil)
	withStubTelemetryReport(t, func(*tracker.Project) ([]byte, error) { return []byte(`{"v":1,"enabled":true}`), nil })
	proj := fixItProject(t)
	overviewSnap(t, proj.WorkDir, time.Now().Add(-time.Hour), map[string]float64{"workflow.issues{type=all}": 1, "codebase.big_files": 3, "codebase.source_lines": 1234, "codebase.test_ratio": 0.42})
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, page := getBody(t, ts.URL+"/p/test-project/stats?range=7d&group=system&drill=status:human-testing")
	for _, want := range []string{
		`data-testid="stats.page"`, `data-project="test-project"`, "/static/workshop.css", "/static/workshop-stats.js", "Product hall",
		`aria-current="page" data-testid="hall.tab.stats"`, `href="/p/test-project/health" data-testid="hall.tab.health"`,
		`aria-current="true" data-testid="seg.stats-range.7d"`, `aria-current="true" data-testid="seg.stats-group.system"`,
		`href="/p/test-project/stats?classic=1&amp;group=system&amp;range=7d" data-testid="stats.classic"`,
		`data-testid="stats.gauge.waiting-on-you"`, `data-testid="stats.gauge.big-files"`, `<a href="/p/test-project/health">Big files</a>`,
		`data-testid="stats.gauge.milestone"`, `data-testid="stats.gauge.ci-green-rate-14-days"`, "wb-gauge--locked",
		`data-testid="stats.backfill_hint"`, `data-copy="issue-cli --project test-project health backfill"`,
		`data-testid="stats.chart.cycle"`, `data-testid="stats.chart.waiting"`, `data-testid="stats.locked"`,
		`data-testid="stats.see_also.code-shape"`, "Latest lines of code: 1.2k", "test ratio 0.42", `href="/p/test-project/health">Open Health`,
		`data-testid="stats.chart.time"`, `data-testid="stats.chart.group"`, `data-testid="stats.chart.weekly"`, "wb-chart--wide",
		`data-testid="stats.drilldown"`, `/issue/fix-it?tab=timeline`, `data-testid="stats.table.time"`, "<summary>Table</summary>",
		`data-testid="stats.cost"`, `data-testid="stats.table_card.dispatch"`, `data-testid="stats.table_card.static"`, `data-testid="stats.table_card.recorded"`,
		`data-testid="stats.cli"`, `data-testid="stats.tables"`, `data-testid="stats.fold.time"`, `data-testid="stats.table_card.approvals"`,
		// Moved from the classic page: the way to every project's stats, the
		// remembered-options note, the time split rules.
		`href="/stats" data-testid="stats.all"`, `data-testid="stats.saved"`, `data-testid="stats.time_rules"`, "less than 15 min apart",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("stats page lacks %s", want)
		}
	}
	// Code shape is Health's: no lines-of-code chart here. No classic
	// sidebar, gauges or dashboard either.
	for _, unwanted := range []string{`data-testid="stats.chart.loc"`, `data-testid="stats.dashboard"`, `data-testid="nav.tab.stats"`, "/static/style.css", "Klasyczny", "Zdrowie"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("stats page has %s", unwanted)
		}
	}
	assertNoInlineScript(t, page)

	_, classic := getBody(t, ts.URL+"/p/test-project/stats?classic=1&range=7d")
	for _, want := range []string{`data-testid="stats.dashboard"`, `href="/p/test-project/stats?group=type&amp;range=7d" data-testid="stats.workshop"`,
		`href="/p/test-project/stats?classic=1&amp;group=type&amp;range=30d"`, `<input type="hidden" name="classic" value="1">`} {
		if !strings.Contains(classic, want) {
			t.Errorf("classic page lacks %s", want)
		}
	}
}

// AC5: a project with no snapshot, no done issue, no recorded transition
// and no CLI call says per block what is missing and what fills it.
func TestStatsWbEmptyProject(t *testing.T) {
	withMockTmuxSessions(t, nil)
	withStubTelemetryReport(t, func(*tracker.Project) ([]byte, error) { return []byte(`{"v":1,"enabled":true}`), nil })
	proj, tmp := setupTestProject(t)
	proj.WorkDir = tmp
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	var page string
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		_, page = getBody(t, ts.URL+"/p/test-project/stats")
		if !strings.Contains(page, "Still counting") || time.Now().After(deadline) {
			break
		}
	}
	for _, want := range []string{
		"No health snapshot yet", `data-testid="stats.backfill_hint"`,
		"No points in this range: a trend point is a day&#39;s snapshot.",
		`data-copy="issue-cli --project test-project health snapshot"`,
		"No issues with CLI calls done in this range. The split reads the issue-cli calls of issues that reached done",
		`>Show all time</a>`,
		"No transition recorded yet", `data-copy="issue-cli transition &lt;slug&gt; --to &lt;status&gt;"`,
		"No issue has a stats sidecar yet",
		"No issue-cli call recorded in the last 30 days", `data-copy="issue-cli --project test-project telemetry report"`,
		"No issue done in the last 30 days with a recorded transition to done.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("empty stats page lacks %s", want)
		}
	}
	// No chart is drawn without data.
	if strings.Contains(page, `class="chart`) && strings.Contains(page, `data-testid="stats.chart.cycle"`) && strings.Contains(page[strings.Index(page, `data-testid="stats.chart.cycle"`):strings.Index(page, `data-testid="stats.chart.waiting"`)], "<svg") {
		t.Error("the empty cycle chart draws an SVG")
	}
}

// AC5: while the cache warms, CLI usage says it is still counting; a report
// lists its tables and the full-report command.
func TestStatsWbCLITables(t *testing.T) {
	sub, notes, tables := cliTables(StatsData{CLIUsageLoading: true}, "x")
	if sub != "" || len(tables) != 0 || len(notes) != 1 || !strings.HasPrefix(notes[0].Text, "Still counting") {
		t.Errorf("loading: %q %+v %d tables", sub, notes, len(tables))
	}
	r := &telemetry.Report{Enabled: true, Summary: telemetry.Summary{Events: 4, Failures: 1, ErrorPct: 25, Callers: map[string]int{"agent": 3, "human": 1}, Assignees: 2},
		Commands:  []telemetry.CommandStat{{Command: "show", Calls: 4, Failures: 1, ErrorPct: 25, P50MS: 12}},
		NeverUsed: telemetry.NeverUsed{Commands: []string{"graph"}, Flags: map[string][]string{"show": {"json"}}}}
	sub, notes, tables = cliTables(StatsData{CLIUsage: r}, "x")
	if sub != "4 calls, 1 failed (25.0%) · callers: agent 3, human 1 · 2 assignees" || len(tables) != 5 || notes[0].Cmd != "issue-cli --project x telemetry report" {
		t.Errorf("report: %q %+v", sub, notes)
	}
	never := tables[4]
	if last := never.Rows[len(never.Rows)-1]; last[0].Text != "Flags of show" || last[1].Text != "--json" {
		t.Errorf("never used flags row = %+v", last)
	}
	if tables[1].Empty == nil || tables[2].Empty == nil || tables[3].Empty == nil {
		t.Error("errors, unknown and retries need an empty state")
	}
}

// AC2: /stats is the Workshop Board overview, one card per project linking
// to its Stats page; ?classic=1 is the classic table.
func TestStatsOverviewWb(t *testing.T) {
	withMockTmuxSessions(t, nil)
	now := time.Now()
	mk := func(name, slug string, rows map[string]float64) tracker.Project {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "issues"), 0o755)
		if rows != nil {
			overviewSnap(t, root, now.Add(-time.Hour), rows)
		}
		return tracker.Project{Name: name, Slug: slug, IssueDir: filepath.Join(root, "issues"), WorkDir: root}
	}
	big := mk("Big", "big", map[string]float64{"workflow.issues{type=all}": 60, "workflow.time.share{kind=waiting}": 0.95, "workflow.cycle.p50{type=all}": 28800, "codebase.source_lines": 237000, "codebase.big_files": 38})
	big.LoadWorkflow()
	ts := newTestServer(t, []tracker.Project{big, mk("Fresh", "fresh", nil)})
	defer ts.Close()

	_, page := getBody(t, ts.URL+"/stats?range=7d")
	for _, want := range []string{
		`data-testid="stats.overview"`, "/static/workshop.css", `aria-current="page">Stats, all projects`,
		`data-testid="stats.overview.row" data-project="big"`, `href="/p/big/stats"`, "over 25%", "95%", "8h", "237k",
		`aria-current="true" data-testid="seg.overview-range.7d"`, `data-testid="seg.overview-sort.done"`,
		`data-testid="stats.overview.none"`, "No snapshot yet: Fresh.", `data-copy="issue-cli --project &lt;slug&gt; health snapshot"`,
		`href="/stats?classic=1&amp;range=7d&amp;sort=done" data-testid="stats.classic"`,
		// The backfill command next to the snapshot one (classic-page-gaps).
		`data-copy="issue-cli --project &lt;slug&gt; health backfill"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("overview lacks %s", want)
		}
	}
	assertNoInlineScript(t, page)

	_, classic := getBody(t, ts.URL+"/stats?classic=1&range=7d")
	for _, want := range []string{`stats-overview-table`, `href="/stats?range=7d" data-testid="stats.workshop"`, `href="/stats?classic=1&amp;range=30d&amp;sort=done"`} {
		if !strings.Contains(classic, want) {
			t.Errorf("classic overview lacks %s", want)
		}
	}
}

// AC3: each new component renders on its own data.
func TestStatsWbComponentsStandAlone(t *testing.T) {
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
	for _, c := range []struct {
		name string
		data any
		want []string
	}{
		{"wb/seg-links", WbSegLinks{Label: "Range", Name: "x-range", Items: []statsOption{{Key: "7d", Label: "7 days", Href: "/a", On: true}, {Key: "all", Label: "All", Href: "/b"}}},
			[]string{`aria-label="Range"`, `aria-current="true" data-testid="seg.x-range.7d"`, `href="/b"`}},
		{"wb/empty", &WbEmpty{Text: "Nothing yet.", Cmd: "issue-cli x", URL: "/u", Link: "Go"}, []string{"Nothing yet.", `data-copy="issue-cli x"`, `href="/u">Go<`}},
		{"wb/empty", (*WbEmpty)(nil), nil},
		{"wb/gauge", WbGauge{Key: "k", Title: "T", Locked: true, Need: "Comes later.", URL: "/p/x/issue/y"}, []string{"wb-gauge--locked", "Comes later.", `href="/p/x/issue/y"`}},
		{"wb/gauge", WbGauge{Key: "k", Title: "T", Href: "/h", Value: "3", Context: "over 500 lines", Verdict: "Over limit", Over: true}, []string{`<a href="/h">T</a>`, "wb-chip--red", "Over limit"}},
		{"wb/data-table", WbTable{RowHead: true, Head: []WbTH{{Text: "A"}, {Text: "B", Num: true}}, Rows: [][]WbCell{{{Text: "r"}, {Text: "2", Num: true}}, {{Text: "s", URL: "/s"}, {Text: "c", Code: true}}}},
			[]string{`<th scope="row">r</th>`, `<td class="wb-num">2</td>`, `<a href="/s">s</a>`, "<code>c</code>"}},
		{"wb/table-card", WbTable{Key: "k", Title: "T", Empty: &WbEmpty{Text: "None."}}, []string{`data-testid="stats.table_card.k"`, "None."}},
		{"wb/fold", WbFold{Key: "f", Title: "Folded", Open: true, Tables: []WbTable{{Key: "a", Title: "A", Empty: &WbEmpty{Text: "None."}}}}, []string{"<details class=\"wb-fold\" open", "<summary>Folded</summary>", "stats.table_card.a"}},
		{"wb/see-also", WbSeeAlso{Key: "k", Title: "T", Text: "There.", URL: "/h", Link: "Open"}, []string{`data-testid="stats.see_also.k"`, `href="/h">Open ›`}},
		{"wb/series-legend", []LegendItem{{Name: "a", Class: "s-agent"}, {Name: "b", Class: "s-idle", Href: "/x", Hidden: true}}, []string{`<span class="s-agent"><i></i>a</span>`, `wb-legend-off`, "Show b"}},
		{"wb/chart-card", WbChartCard{Card: ChartCard{ID: "c", Title: "Chart"}, Empty: &WbEmpty{Text: "No points."}}, []string{`id="c"`, `data-testid="stats.chart.c"`, "No points."}},
		{"wb/drill", &Drill{Title: "Issues", Close: "/close", Issues: []DrillIssue{{Slug: "a", Href: "/a", Date: "1 Jan"}}, More: 2}, []string{`href="/close"`, `href="/a">a<`, "2 more"}},
		{"wb/trend-card", WbTrendCard{Key: "k", Title: "T", Locked: true, Need: "N", Cmd: "x/y", URL: "/p/x/issue/x/y"}, []string{`<a class="wb-trend-cmd" href="/p/x/issue/x/y">x/y</a>`}},
	} {
		out := render(c.name, c.data)
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s lacks %q:\n%s", c.name, w, out)
			}
		}
	}
}

// AC4: the options keep the classic page's query parameters, plus classic.
func TestStatsOptsClassicQuery(t *testing.T) {
	o := parseStatsOpts(url.Values{"classic": {"1"}, "range": {"90d"}, "group": {"epic"}, "p": {"90"}})
	if got := o.query(false); got != "classic=1&group=epic&p=90&range=90d" {
		t.Errorf("classic query = %s", got)
	}
	o.Classic = false
	if got := o.query(false); got != "group=epic&p=90&range=90d" {
		t.Errorf("query = %s", got)
	}
	if slugKey("CI green rate, 14 days") != "ci-green-rate-14-days" || slugKey("Milestone 0.3") != "milestone-0-3" {
		t.Error("slugKey")
	}
}

// Two templates defining one name: the later file silently replaces the
// other (wb/legend once did). Pages on one epic add wb/* components in
// parallel, so a clash must fail here, not in a page that renders the
// wrong component.
func TestTemplateDefinesUnique(t *testing.T) {
	define := regexp.MustCompile(`\{\{-?\s*define\s+"([^"]+)"`)
	files, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, f := range files {
		src, _ := fs.ReadFile(templateFS, f)
		for _, m := range define.FindAllStringSubmatch(string(src), -1) {
			if prev, ok := seen[m[1]]; ok {
				t.Errorf("%q is defined in %s and %s", m[1], prev, f)
			}
			seen[m[1]] = f
		}
	}
}
