package main

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/chart"
	"github.com/michal-franc/issue-viewer/internal/timeline"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The hall's Stats page (/p/<project>/stats), L2 of the Workshop Board:
// where the time and the tokens go. It renders the classic page's model
// (buildDashboard, the time report, token cost, CLI usage) as wb/* cards;
// the classic page stays at ?classic=1. Code shape lives on Health, so the
// lines-of-code trend becomes a card that links there. Every block without
// data says what it lacks and the command that fills it. Layout follows
// issues/Factory/designs/workshop-board/L2-health (no Stats design of its
// own, factory/stats-screen#D-1). Components: templates/_workshop_stats.html.

// StatsWbPage is the whole page.
type StatsWbPage struct {
	Header     WbHeader
	Project    string // slug, for the remembered options
	Name       string
	ClassicURL string
	Sub        string
	Range      WbSegLinks
	Group      WbSegLinks
	Gauges     []WbGauge
	RangeLabel string
	Hint       *WbEmpty
	Trends     []WbChartCard
	Locked     []WbTrendCard
	SeeAlso    []WbSeeAlso
	TimeSub    string
	Legend     []LegendItem
	Bars       []WbChartCard
	Weekly     WbChartCard
	Cost       []WbTable
	CostNote   string
	CLISub     string
	CLINotes   []WbEmpty
	CLI        []WbTable
	TimeFolds  []WbFold
}

// WbSegLinks is a row of options as links (range, group by), the one on
// marked aria-current. Name is its testid, seg.<Name>, each option
// seg.<Name>.<Key>; a name has no dots.
type WbSegLinks struct {
	Label, Name string
	Items       []statsOption
}

// WbGauge is one gauge card. Locked: no data source yet; Need says what
// brings it and URL links to the issue that does.
type WbGauge struct {
	Key, Title string
	SVG        template.HTML
	Value      string
	Context    string
	Verdict    string
	Over       bool
	Chip       string
	Href       string
	Locked     bool
	Need, URL  string
	Empty      *WbEmpty
}

// WbChartCard is one chart: its plot, legend, options, a table view that
// works without JavaScript, and the drill-down when it is this chart's.
type WbChartCard struct {
	Card  ChartCard
	Table WbTable
	Empty *WbEmpty
	Drill *Drill
	Wide  bool
}

// WbEmpty is an honest empty state: what is missing, and the command or
// page that fills it.
type WbEmpty struct {
	Text, Cmd, URL, Link string
}

// WbSeeAlso points at the page that owns a number instead of drawing it
// twice.
type WbSeeAlso struct {
	Key, Title, Text, URL, Link string
}

// WbTable is a titled table card. Rows' first cell is the row header when
// RowHead is set; Empty replaces the table when there are no rows.
type WbTable struct {
	Key, Title, Sub string
	Head            []WbTH
	Rows            [][]WbCell
	RowHead         bool
	Empty           *WbEmpty
}

// WbTH is a column head; Num right-aligns the column.
type WbTH struct {
	Text string
	Num  bool
}

// WbCell is one table cell: text, a link when URL is set, monospace when
// Code.
type WbCell struct {
	Text, URL string
	Code, Num bool
}

// WbFold is a folded group of tables.
type WbFold struct {
	Key, Title string
	Open       bool
	Note       *WbEmpty
	Tables     []WbTable
}

type statsWbIn struct {
	Header WbHeader
	Data   StatsData
	Slug   string
}

// renderStatsWb renders the page; ?classic=1 keeps the classic one
// (handleStats).
func (s *Server) renderStatsWb(w http.ResponseWriter, proj *tracker.Project, header WbHeader, data StatsData) {
	page := buildStatsWb(statsWbIn{Header: header, Data: data, Slug: proj.Slug})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "stats_wb.html", page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// statsHeader is the hall's header: Map › project › Product hall, Stats on.
// Everything it shows comes from the project's lot, the same counts
// buildCompany gives the other hall pages (Waiting is the lot's rows), so
// it skips the company's own reads: on 800 issues they cost the page its
// time budget (TestStatsPageFastOnABigProject).
func (s *Server) statsHeader(p *tracker.Project, prefix string, now time.Time) WbHeader {
	lot, rows := s.buildLot(p, s.projectColor(p), now)
	h := WbHeader{WaitingURL: prefix + "/needs-you", Crumbs: wbCrumbs(2, p.Name, prefix+"/company"), Tabs: wbHallTabs(prefix, "stats"), Working: lot.Working, Idle: lot.Idle, AgentWait: lot.AgentWait, DoneWeek: lot.DoneWeek, Waiting: len(rows), HideDone: true}
	h.Crumbs[1].Color = lot.Color
	h.YOLO = headerYOLO(lot.YOLO, prefix)
	h.Crumbs[2] = WbCrumb{Label: "Product hall", URL: prefix + "/hall", Color: companyProductColor}
	if c := wbCIChip(lot.CI, lot.CILine); c != nil {
		h.Chips = append(h.Chips, *c)
	}
	return h
}

func buildStatsWb(in statsWbIn) StatsWbPage {
	data, d := in.Data, in.Data.Dash
	classic := d.Opts
	classic.Classic, classic.Drill = true, ""
	p := StatsWbPage{
		Header: in.Header, Project: in.Slug, Name: data.ProjectName,
		ClassicURL: d.Base + "?" + classic.query(false),
		Range:      WbSegLinks{Label: "Range", Name: "stats-range", Items: d.Ranges},
		Group:      WbSegLinks{Label: "Group by", Name: "stats-group", Items: d.Groups},
		RangeLabel: statsRangeLabel(d.Opts),
	}
	if d.Latest != "" {
		p.Sub = "Last snapshot " + d.Latest + " · " + plural(d.Snapshots, "snapshot", "snapshots")
		if d.Backfilled > 0 {
			p.Sub += fmt.Sprintf(", %d backfilled", d.Backfilled)
		}
	} else {
		p.Sub = "No health snapshot yet"
	}
	p.Sub += " · gauges and bars computed now"

	backfill := "issue-cli --project " + in.Slug + " health backfill"
	if d.Hint != "" {
		p.Hint = &WbEmpty{Text: plural(d.Snapshots, "snapshot", "snapshots") + " so far: a trend needs history. Snapshots are taken daily from now on; draw the past from git history and the issues' recorded transitions (one point per past day, 90 by default).", Cmd: d.Hint}
	}
	for _, g := range d.Gauges {
		p.Gauges = append(p.Gauges, wbGauge(g, data.Prefix, in.Slug))
	}
	for _, c := range d.Trends {
		if c.ID == "loc" {
			p.SeeAlso = append(p.SeeAlso, codeShapeSeeAlso(c, data.Prefix))
			continue
		}
		cc := wbChartCard(c, d)
		if cc.Empty != nil && cc.Empty.Cmd == "" {
			cc.Empty.Text += ": a trend point is a day's snapshot."
			cc.Empty.Cmd = backfill
		}
		p.Trends = append(p.Trends, cc)
	}
	for _, pc := range d.Pending {
		p.Locked = append(p.Locked, WbTrendCard{Key: pc.Family + "-" + slugKey(pc.Title), Title: pc.Title, Locked: true,
			Need: "No snapshot has the " + pc.Family + " family yet. It comes with this issue:", Cmd: pc.Issue, URL: data.Prefix + "/issue/" + pc.Issue})
	}

	p.TimeSub = fmt.Sprintf("%s done %s – %s", plural(d.Done, "issue", "issues"), d.From.Local().Format("2 Jan 2006"), d.To.Local().Format("2 Jan 2006"))
	if d.Split != d.Done {
		p.TimeSub += fmt.Sprintf(", %d with CLI calls", d.Split)
	}
	p.TimeSub += "; each split into agent, waiting and idle exactly as its Timeline tab splits it"
	p.Legend = []LegendItem{{Name: "agent working", Class: chart.ClassAgent}, {Name: "waiting on you", Class: chart.ClassWaiting}, {Name: "idle", Class: "s-idle"}}
	for _, c := range d.Bars {
		p.Bars = append(p.Bars, wbChartCard(c, d))
	}
	p.Weekly = wbChartCard(d.Weekly, d)
	p.Weekly.Wide = true

	p.CostNote = "Approximate tokens of the workflow's own text: static is the scaffolding an agent reads on a transition, dynamic adds the issue body and comments at that moment. Counted as len(text)/4, good for comparing, not exact for a model."
	p.Cost = costTables(data)
	p.CLISub, p.CLINotes, p.CLI = cliTables(data, in.Slug)
	p.TimeFolds = timeFolds(data)
	return p
}

// wbGauge is a gauge card for the page: a pending gauge is locked with the
// issue that brings it; an empty one says what fills it.
func wbGauge(g GaugeCard, prefix, slug string) WbGauge {
	out := WbGauge{Key: slugKey(g.Title), Title: g.Title, SVG: g.SVG, Value: g.Value, Context: g.Context, Verdict: g.Verdict, Over: g.Over, Chip: g.Chip, Href: g.Href}
	milestone := strings.HasPrefix(g.Title, "Milestone") || g.Title == "Current milestone"
	if milestone {
		out.Key = "milestone"
	}
	switch {
	case g.Pending != "":
		out.Locked, out.Need, out.URL = true, "No data yet. It comes with "+g.Pending+".", prefix+"/issue/"+g.Pending
	case g.Title == "Big files":
		out.Href = prefix + "/health"
		if g.Empty != "" {
			out.Empty = &WbEmpty{Text: g.Empty + ".", Cmd: "issue-cli --project " + slug + " health snapshot"}
		}
	case g.Empty == "":
	case milestone:
		out.Empty = &WbEmpty{Text: g.Empty + ". It follows the lowest version that still has open issues: give issues a version: field to pick one."}
	default:
		out.Empty = &WbEmpty{Text: g.Empty + ". The share comes from the issue-cli calls of issues that reached done; a wider range may have some."}
	}
	return out
}

// codeShapeSeeAlso replaces the lines-of-code trend: code shape is the
// Health page's, so this card gives the latest figure and links there.
func codeShapeSeeAlso(c ChartCard, prefix string) WbSeeAlso {
	s := WbSeeAlso{Key: "code-shape", Title: "Code shape is on Health", URL: prefix + "/health", Link: "Open Health",
		Text: "Big files, long functions, hotspots and the test suites are measured and scored there."}
	if n := len(c.Table.Rows); n > 0 && len(c.Table.Rows[n-1]) > 1 {
		row := c.Table.Rows[n-1]
		s.Text = "Latest lines of code: " + row[1] + " (" + row[0] + ")" + testRatioOf(c.Sub) + ". " + s.Text
	}
	return s
}

// testRatioOf is ", test ratio 0.42" from the card's subtitle, or "".
func testRatioOf(sub string) string {
	if i := strings.Index(sub, "test ratio "); i >= 0 {
		return ", " + sub[i:]
	}
	return ""
}

// wbChartCard wraps a chart card with its table and drill-down; an empty
// chart says what it lacks instead of drawing axes.
func wbChartCard(c ChartCard, d *Dashboard) WbChartCard {
	out := WbChartCard{Card: c, Table: chartTable(c)}
	if c.Empty != "" {
		out.Empty = &WbEmpty{Text: c.Empty}
		if strings.HasPrefix(c.Empty, "No issues") {
			out.Empty.Text += ". The split reads the issue-cli calls of issues that reached done; a wider range may have some."
			if all := wideRange(d); all != "" {
				out.Empty.URL, out.Empty.Link = all, "Show all time"
			}
		}
	}
	if d.Drill != nil && d.Drill.Anchor == c.ID {
		out.Drill = d.Drill
	}
	return out
}

// wideRange is the page at "all", or "" when it already is.
func wideRange(d *Dashboard) string {
	if d.Opts.Days == 0 && d.Opts.Range == "all" {
		return ""
	}
	return d.href(func(o *statsOpts) { o.Range, o.Days = "all", 0 })
}

// chartTable is a chart's table view.
func chartTable(c ChartCard) WbTable {
	t := WbTable{Key: c.ID, RowHead: true}
	for i, h := range c.Table.Head {
		t.Head = append(t.Head, WbTH{Text: h, Num: i > 0})
	}
	for _, r := range c.Table.Rows {
		row := make([]WbCell, len(r))
		for i, v := range r {
			row[i] = WbCell{Text: v, Num: i > 0}
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func costTables(data StatsData) []WbTable {
	num := func(n int) WbCell { return WbCell{Text: strconv.Itoa(n), Num: true} }
	txt := func(s string) WbCell { return WbCell{Text: s} }
	dispatch := WbTable{Key: "dispatch", Title: "Dispatch base prompt", Sub: "Sent on every fresh agent dispatch, before the issue body and slug.",
		Head: []WbTH{{Text: "Component"}, {Text: "Static tokens", Num: true}},
		Rows: [][]WbCell{{txt("Agent dispatch prompt template"), num(data.DispatchPromptCost)}}}

	static := WbTable{Key: "static", Title: "By transition", Sub: "The workflow's weight per from → to in workflow.yaml, without the issue.",
		Head: []WbTH{{Text: "From"}, {Text: "To"}, {Text: "Static tokens", Num: true}}}
	for _, r := range data.StaticReference {
		static.Rows = append(static.Rows, []WbCell{txt(r.From), txt(r.To), num(r.Tokens)})
	}
	if len(static.Rows) == 0 {
		static.Empty = &WbEmpty{Text: "This project's workflow declares no transitions.", Cmd: "issue-cli process transitions"}
	}

	recorded := WbTable{Key: "recorded", Title: "Recorded transitions", Sub: "Average over every recorded transition, highest dynamic cost first.",
		Head: []WbTH{{Text: "From"}, {Text: "To"}, {Text: "Count", Num: true}, {Text: "Avg static", Num: true}, {Text: "Avg dynamic", Num: true}}}
	for _, r := range data.RecordedTransitions {
		recorded.Rows = append(recorded.Rows, []WbCell{txt(r.From), txt(r.To), num(r.Count), num(r.AvgStatic), num(r.AvgDynamic)})
	}
	if len(recorded.Rows) == 0 {
		recorded.Empty = &WbEmpty{Text: "No transition recorded yet: each issue-cli transition writes one to the issue's stats sidecar.", Cmd: "issue-cli transition <slug> --to <status>"}
	}

	issues := WbTable{Key: "issues", Title: "Per issue", Sub: "Dynamic tokens summed over each issue's recorded transitions.",
		Head: []WbTH{{Text: "Issue"}, {Text: "Status"}, {Text: "Transitions", Num: true}, {Text: "Total static", Num: true}, {Text: "Total dynamic", Num: true}}}
	for _, r := range data.IssueTotals {
		issues.Rows = append(issues.Rows, []WbCell{{Text: r.Title, URL: data.Prefix + "/issue/" + r.Slug}, txt(r.Status), num(r.TransitionCount), num(r.TotalStaticTokens), num(r.TotalDynamicTokens)})
	}
	if len(issues.Rows) == 0 {
		issues.Empty = &WbEmpty{Text: "No issue has a stats sidecar yet: the first issue-cli transition writes one."}
	}
	return []WbTable{dispatch, static, recorded, issues}
}

// cliTables is the CLI usage section: its subtitle, notes (loading,
// disabled, no issue-cli) and tables.
func cliTables(data StatsData, slug string) (string, []WbEmpty, []WbTable) {
	report := WbEmpty{Text: "The full report, with first and last call:", Cmd: "issue-cli --project " + slug + " telemetry report"}
	var notes []WbEmpty
	if data.CLIUsageLoading {
		notes = append(notes, WbEmpty{Text: "Still counting: the CLI usage log is being read in the background. Reload in a few seconds."})
	}
	if data.CLIUsageNotice != "" {
		notes = append(notes, WbEmpty{Text: data.CLIUsageNotice})
	}
	r := data.CLIUsage
	if r == nil || r.Summary.Events == 0 {
		if !data.CLIUsageLoading && data.CLIUsageNotice == "" {
			notes = append(notes, WbEmpty{Text: "No issue-cli call recorded in the last 30 days. Each call adds a names-only line to the local usage log.", Cmd: report.Cmd})
		}
		return "", notes, nil
	}
	callers := make([]string, 0, len(r.Summary.Callers))
	for k, v := range r.Summary.Callers {
		callers = append(callers, fmt.Sprintf("%s %d", k, v))
	}
	sort.Strings(callers)
	sub := fmt.Sprintf("%d calls, %d failed (%.1f%%) · callers: %s · %s", r.Summary.Events, r.Summary.Failures, r.Summary.ErrorPct, strings.Join(callers, ", "), plural(r.Summary.Assignees, "assignee", "assignees"))
	notes = append(notes, report)

	num := func(n int) WbCell { return WbCell{Text: strconv.Itoa(n), Num: true} }
	code := func(s string) WbCell { return WbCell{Text: s, Code: true} }
	cmds := WbTable{Key: "commands", Title: "Commands", Head: []WbTH{{Text: "Command"}, {Text: "Calls", Num: true}, {Text: "Failed", Num: true}, {Text: "Error %", Num: true}, {Text: "p50 ms", Num: true}}}
	for _, c := range r.Commands {
		cmds.Rows = append(cmds.Rows, []WbCell{code(c.Command), num(c.Calls), num(c.Failures), {Text: fmt.Sprintf("%.1f", c.ErrorPct), Num: true}, {Text: strconv.FormatInt(c.P50MS, 10), Num: true}})
	}
	never := WbTable{Key: "never", Title: "Never used", Sub: "Commands, flags and topics nobody called in 30 days.", RowHead: true, Head: []WbTH{{Text: "Kind"}, {Text: "Names"}}}
	list := func(names []string, pre string) WbCell {
		if len(names) == 0 {
			return WbCell{Text: "—"}
		}
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = pre + n
		}
		return code(strings.Join(out, ", "))
	}
	never.Rows = [][]WbCell{
		{{Text: "Commands"}, list(r.NeverUsed.Commands, "")},
		{{Text: "Aliases"}, list(r.NeverUsed.Aliases, "")},
		{{Text: "Subcommands"}, list(r.NeverUsed.Subcommands, "")},
		{{Text: "Topics"}, list(r.NeverUsed.Topics, "")},
		{{Text: "Global flags"}, list(r.NeverUsed.GlobalFlags, "--")},
	}
	flagCmds := make([]string, 0, len(r.NeverUsed.Flags))
	for k, v := range r.NeverUsed.Flags {
		if len(v) > 0 {
			flagCmds = append(flagCmds, k)
		}
	}
	sort.Strings(flagCmds)
	for _, k := range flagCmds {
		never.Rows = append(never.Rows, []WbCell{{Text: "Flags of " + k}, list(r.NeverUsed.Flags[k], "--")})
	}
	errs := WbTable{Key: "errors", Title: "Errors", Head: []WbTH{{Text: "Class"}, {Text: "Command"}, {Text: "Count", Num: true}}}
	for _, e := range r.Errors {
		errs.Rows = append(errs.Rows, []WbCell{{Text: e.Class}, code(e.Command), num(e.Count)})
	}
	if len(errs.Rows) == 0 {
		errs.Empty = &WbEmpty{Text: "No failed call in 30 days."}
	}
	unknown := WbTable{Key: "unknown", Title: "Unknown commands and flags tried", Head: []WbTH{{Text: "Class"}, {Text: "Token"}, {Text: "Count", Num: true}}}
	for _, u := range r.Unknown {
		unknown.Rows = append(unknown.Rows, []WbCell{{Text: u.Class}, code(u.Token), num(u.Count)})
	}
	if len(unknown.Rows) == 0 {
		unknown.Empty = &WbEmpty{Text: "Nobody tried an unknown command or flag."}
	}
	retries := WbTable{Key: "retries", Title: "Retry sequences", Sub: "A failed call and the next call from the same shell within two minutes.", Head: []WbTH{{Text: "Failed"}, {Text: "Next call"}, {Text: "Count", Num: true}}}
	for _, s := range r.RetrySequences {
		retries.Rows = append(retries.Rows, []WbCell{code(s.Failed), code(s.Next), num(s.Count)})
	}
	if len(retries.Rows) == 0 {
		retries.Empty = &WbEmpty{Text: "No failed call was retried."}
	}
	return sub, notes, []WbTable{cmds, errs, unknown, retries, never}
}

// timeFolds are the time tables behind the bars, folded: per type, per
// status (one table per type), approval waits and gate failures.
func timeFolds(data StatsData) []WbFold {
	rep := data.Time
	f := WbFold{Key: "time", Title: fmt.Sprintf("Time tables · issues done in the last %d days", data.Days()), Open: data.TablesOpen}
	if len(rep.Issues) == 0 {
		f.Note = &WbEmpty{Text: fmt.Sprintf("No issue done in the last %d days with a recorded transition to done.", data.Days())}
		return []WbFold{f}
	}
	var notes []string
	if unsplit := rep.Unsplit(); len(unsplit) > 0 {
		notes = append(notes, "No CLI calls, so status time only: "+strings.Join(unsplit, ", ")+".")
	}
	if n := len(rep.Unread); n > 0 {
		notes = append(notes, fmt.Sprintf("Left out, no transition to done in their stats sidecar: %s.", plural(n, "done issue", "done issues")))
	}
	if len(notes) > 0 {
		f.Note = &WbEmpty{Text: strings.Join(notes, " ")}
	}
	p := func(v timeline.Pct) WbCell { return WbCell{Text: fmtDur(v.P50) + " / " + fmtDur(v.P90), Num: true} }
	num := func(n int) WbCell { return WbCell{Text: strconv.Itoa(n), Num: true} }
	types := WbTable{Key: "types", Title: "By type", Sub: "p50 / p90 across the issues.",
		Head: []WbTH{{Text: "Type"}, {Text: "Issues", Num: true}, {Text: "Cycle p50 / p90", Num: true}, {Text: "Rework loops", Num: true}, {Text: "Sent back", Num: true}, {Text: "Rework share", Num: true}, {Text: "Dispatches p50 / p90", Num: true}}}
	for _, t := range rep.Types {
		types.Rows = append(types.Rows, []WbCell{{Text: t.Type}, num(t.N), p(t.Cycle), num(t.Loops), num(t.Backward),
			{Text: fmt.Sprintf("%.0f%%", t.ReworkShare*100), Num: true}, {Text: fmt.Sprintf("%.0f / %.0f", t.Dispatches[0], t.Dispatches[1]), Num: true}})
	}
	f.Tables = append(f.Tables, types)
	for _, g := range timeGroups(rep) {
		name := g.Type
		if name == timeline.All {
			name = "all types"
		}
		t := WbTable{Key: "status-" + slugKey(g.Type), Title: "Per status · " + name, Sub: plural(g.N, "issue", "issues") + ".",
			Head: []WbTH{{Text: "Status"}, {Text: "Issues", Num: true}, {Text: "In status", Num: true}, {Text: "Agent", Num: true}, {Text: "Waiting on you", Num: true}, {Text: "Idle", Num: true}}}
		for _, s := range g.Statuses {
			n := strconv.Itoa(s.N)
			if s.Split != s.N {
				n += fmt.Sprintf(" (%d with calls)", s.Split)
			}
			row := []WbCell{{Text: s.Status}, {Text: n, Num: true}, p(s.Total)}
			if s.Split > 0 {
				row = append(row, p(s.Working), p(s.Waiting), p(s.Idle))
			} else {
				row = append(row, WbCell{Text: "—", Num: true}, WbCell{Text: "—", Num: true}, WbCell{Text: "—", Num: true})
			}
			t.Rows = append(t.Rows, row)
		}
		f.Tables = append(f.Tables, t)
	}
	approvals := WbTable{Key: "approvals", Title: "Approval waits", Sub: "From the agent's last call in the status to your approval.",
		Head: []WbTH{{Text: "Transition"}, {Text: "Approvals", Num: true}, {Text: "Wait p50 / p90", Num: true}}}
	for _, a := range rep.Approvals {
		approvals.Rows = append(approvals.Rows, []WbCell{{Text: a.Transition}, num(a.N), p(a.Wait)})
	}
	if len(approvals.Rows) == 0 {
		approvals.Empty = &WbEmpty{Text: "No approval recorded in this range."}
	}
	failures := WbTable{Key: "failures", Title: "Gate failures", Sub: "Refused transition calls by status and error class (recorded from v0.54 on); a dry run is the agent checking before it asks.",
		Head: []WbTH{{Text: "Transition"}, {Text: "Error"}, {Text: "Call"}, {Text: "Count", Num: true}}}
	for _, fr := range rep.Failures {
		call := "transition"
		if fr.DryRun {
			call = "dry run"
		}
		failures.Rows = append(failures.Rows, []WbCell{{Text: fr.Transition}, {Text: fr.ErrClass, Code: true}, {Text: call}, num(fr.N)})
	}
	if len(failures.Rows) == 0 {
		failures.Empty = &WbEmpty{Text: "No refused transition recorded in this range."}
	}
	f.Tables = append(f.Tables, approvals, failures)
	return []WbFold{f}
}

// slugKey is a title as a testid key: "CI green rate, 14 days" →
// "ci-green-rate-14-days".
func slugKey(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
