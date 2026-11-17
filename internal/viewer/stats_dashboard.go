package viewer

import (
	"fmt"

	"html/template"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/chart"
	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/milestone"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The stats page's dashboard (docs/workflow-stats.md, "Stats page"): gauges
// for now, trend lines from the health snapshots, and where the time goes,
// from the same timeline.Aggregate the issue Timeline tab uses. Options are
// query parameters, so every view is a link; static/stats.js remembers the
// last one per project.

// statsRange is one range option.
type statsRange struct {
	Key, Label string
	Days       int // 0: all
}

var statsRanges = []statsRange{{"7d", "7 days", 7}, {"30d", "30 days", 30}, {"90d", "90 days", 90}, {"all", "All", 0}}

// statsGroups are the group-by options.
var statsGroups = []struct{ Key, Label string }{{"type", "Type"}, {"system", "System"}, {"epic", "Epic"}, {"version", "Version"}}

// statsOpts is the page's options, from the query.
type statsOpts struct {
	Range string
	Group string
	P     string // cycle chart percentile: 50 or 90
	Scale string // by-status bars: hours or share
	Hide  []string
	Drill string
	// Days is the old ?days=N: a custom range that also opens the time
	// tables, as /stats?days=N did before the dashboard.
	Days int
	// Classic keeps the classic page (?classic=1) across its own links.
	Classic bool
}

func parseStatsOpts(q url.Values) statsOpts {
	o := statsOpts{Range: "30d", Group: "type", P: "50", Scale: "hours"}
	for _, r := range statsRanges {
		if q.Get("range") == r.Key {
			o.Range = r.Key
		}
	}
	for _, g := range statsGroups {
		if q.Get("group") == g.Key {
			o.Group = g.Key
		}
	}
	if q.Get("p") == "90" {
		o.P = "90"
	}
	if q.Get("scale") == "share" {
		o.Scale = "share"
	}
	for _, h := range strings.Split(q.Get("hide"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			o.Hide = append(o.Hide, h)
		}
	}
	o.Drill = q.Get("drill")
	o.Classic = q.Get("classic") == "1"
	if n, err := strconv.Atoi(q.Get("days")); err == nil && n >= 1 && n <= 3650 {
		o.Days = n
	}
	return o
}

// days is the range in days; 0 is all.
func (o statsOpts) days() int {
	if o.Days > 0 {
		return o.Days
	}
	for _, r := range statsRanges {
		if r.Key == o.Range {
			return r.Days
		}
	}
	return 30
}

func (o statsOpts) hidden(name string) bool {
	for _, h := range o.Hide {
		if h == name {
			return true
		}
	}
	return false
}

// query is the options as a query string; drill only when keepDrill.
func (o statsOpts) query(keepDrill bool) string {
	q := url.Values{}
	if o.Classic {
		q.Set("classic", "1")
	}
	if o.Days > 0 {
		q.Set("days", strconv.Itoa(o.Days))
	} else {
		q.Set("range", o.Range)
	}
	q.Set("group", o.Group)
	if o.P != "50" {
		q.Set("p", o.P)
	}
	if o.Scale != "hours" {
		q.Set("scale", o.Scale)
	}
	if len(o.Hide) > 0 {
		q.Set("hide", strings.Join(o.Hide, ","))
	}
	if keepDrill && o.Drill != "" {
		q.Set("drill", o.Drill)
	}
	return q.Encode()
}

// statsCardData is a chart card and its dashboard, for the statsChart
// template.
type statsCardData struct {
	Dash *Dashboard
	Card ChartCard
}

// statsRangeLabel names the range: "30 days", "all time", "45 days".
func statsRangeLabel(o statsOpts) string {
	if o.Days > 0 {
		return plural(o.Days, "day", "days")
	}
	if o.Range == "all" {
		return "all time"
	}
	for _, r := range statsRanges {
		if r.Key == o.Range {
			return r.Label
		}
	}
	return o.Range
}

// Dashboard is the top of the stats page.
type Dashboard struct {
	Opts     statsOpts
	Base     string // the page path, /p/<slug>/stats
	From, To time.Time

	Ranges []statsOption
	Groups []statsOption

	Snapshots  int
	Backfilled int
	Latest     string // "23 Oct 13:17 (daily)"; "" without a snapshot
	AggMS      int64
	// Updating says the numbers are the last aggregate's and a new one is
	// being built; "" when they are current.
	Updating string
	Project  string

	Gauges  []GaugeCard
	Trends  []ChartCard
	Pending []PendingCard
	Bars    []ChartCard
	Weekly  ChartCard
	Drill   *Drill
	// Hint is the backfill hint shown when trends have under two points
	// of history.
	Hint string
	// Done and Split count the issues done in the range, and those of
	// them with CLI calls.
	Done, Split int
}

// statsOption is a range or group choice as a link.
type statsOption struct {
	Key, Label, Href string
	On               bool
}

// GaugeCard is one gauge.
type GaugeCard struct {
	Title   string
	SVG     template.HTML
	Value   string
	Context string
	Verdict string // "Over limit", "Within limit", ""
	Over    bool
	Chip    string
	Href    string
	// Pending: no data yet; what would measure it, one sentence.
	Pending string
	Empty   string
}

// ChartCard is one chart with its table.
type ChartCard struct {
	ID      string
	Title   string
	Sub     string
	SubSVG  template.HTML // a sparkline in the subtitle
	SVG     template.HTML
	Table   chart.Table
	Legend  []LegendItem
	Toggles []statsOption
	Empty   string
	Error   string
	Points  int
	// Big is a headline value over the chart (Disk use: "3.35 GB"); Bars
	// list its parts, each with a bar scaled to the largest.
	Big  string
	Bars []AreaBar
}

// LegendItem is one series in a legend; Href toggles it when set.
type LegendItem struct {
	Name, Class, Href string
	Hidden            bool
}

// PendingCard is a chart whose metric family no snapshot has yet; Need is
// what would bring it, one sentence.
type PendingCard struct {
	Title, Family, Need string
}

// Drill lists the issues behind a point or a bar.
type Drill struct {
	Title  string
	Issues []DrillIssue
	More   int
	Close  string
	Anchor string
}

// DrillIssue is one issue in a drill-down.
type DrillIssue struct {
	Slug, Href, Date, Detail string
}

// What would bring the families this page waits for. No issue on the board
// brings the checks or context families now, so the cards say what is
// missing instead of linking one (TestNoDeadIssueRefsInScore).
const (
	checksNeed  = "No health family records local CI's durations or green rate yet."
	contextNeed = "No gauge reads the context family yet: its floor is the Context floor trend (issue-cli spend --context)."
)

// drillLimit is how many issues a drill-down lists before "N more".
const drillLimit = 12

// pointGap breaks a trend line across a missing day.
const pointGap = 36 * time.Hour

type dashIn struct {
	Proj     *tracker.Project
	Base     string // /p/<slug>/stats
	Prefix   string // /p/<slug>
	WF       *tracker.WorkflowConfig
	Issues   []*tracker.Issue
	Opts     statsOpts
	Now      time.Time
	Wide     timeline.Report // covers [From - timeline.Window, Now]
	AggMS    int64
	Updating string
	Snaps    []*health.Snapshot // every snapshot, oldest first
	Released string
}

// statsWindow is the aggregate window a request needs: its range plus the
// 30 days a trend point looks back. all is ten years.
func (o statsOpts) window() time.Duration {
	d := o.days()
	if d == 0 {
		return 3650 * 24 * time.Hour
	}
	return time.Duration(d)*24*time.Hour + timeline.Window
}

func buildDashboard(in dashIn) *Dashboard {
	d := &Dashboard{Opts: in.Opts, Base: in.Base, To: in.Now, AggMS: in.AggMS, Updating: in.Updating, Project: in.Proj.Slug}
	if days := in.Opts.days(); days > 0 {
		d.From = in.Now.AddDate(0, 0, -days)
	} else {
		d.From = in.Now.AddDate(0, 0, -30)
		if len(in.Snaps) > 0 && in.Snaps[0].Time().Before(d.From) {
			d.From = in.Snaps[0].Time()
		}
		for _, s := range in.Wide.Issues {
			if s.DoneAt.Before(d.From) {
				d.From = s.DoneAt
			}
		}
	}
	for _, r := range statsRanges {
		o := in.Opts
		o.Range, o.Days, o.Drill = r.Key, 0, ""
		d.Ranges = append(d.Ranges, statsOption{Key: r.Key, Label: r.Label, Href: in.Base + "?" + o.query(false), On: in.Opts.Days == 0 && in.Opts.Range == r.Key})
	}
	for _, g := range statsGroups {
		o := in.Opts
		o.Group, o.Drill = g.Key, ""
		d.Groups = append(d.Groups, statsOption{Key: g.Key, Label: g.Label, Href: in.Base + "?" + o.query(false), On: in.Opts.Group == g.Key})
	}

	d.Snapshots = len(in.Snaps)
	for _, s := range in.Snaps {
		if s.Run.Trigger == health.TriggerBackfill {
			d.Backfilled++
		}
	}
	var latest *health.Snapshot
	if n := len(in.Snaps); n > 0 {
		latest = in.Snaps[n-1]
		d.Latest = latest.Time().Format("2 Jan 15:04") + " (" + latest.Run.Trigger + ")"
	}

	rep := in.Wide.Filter(d.From, d.To)
	d.Done = len(rep.Issues)
	for _, s := range rep.Issues {
		if s.Known {
			d.Split++
		}
	}
	d.Gauges = gauges(in, rep, latest)

	daily := lastPerDay(in.Snaps, d.From, d.To)
	if len(daily) < 2 {
		d.Hint = "issue-cli --project " + in.Proj.Slug + " health backfill"
	}
	d.Trends = trends(in, d, daily, latest)
	d.Pending = pendingCards(in.Snaps)
	d.Bars = timeBars(in, d, rep)
	d.Weekly = weeklyColumns(in, d, rep)
	d.Drill = drill(in, d, rep)
	return d
}

// WorkshopURL is the page with the same options and without classic, for
// the classic page's link to the Workshop Board page.
func (d *Dashboard) WorkshopURL() string {
	o := d.Opts
	o.Classic, o.Drill = false, ""
	return d.Base + "?" + o.query(false)
}

// href is the page with the options, one changed by change.
func (d *Dashboard) href(change func(*statsOpts)) string {
	o := d.Opts
	o.Drill = ""
	if change != nil {
		change(&o)
	}
	return d.Base + "?" + o.query(true)
}

// drillHref opens the issues behind key, anchored at the chart.
func (d *Dashboard) drillHref(key, anchor string) string {
	return d.href(func(o *statsOpts) { o.Drill = key }) + "#" + anchor
}

func gauges(in dashIn, rep timeline.Report, latest *health.Snapshot) []GaugeCard {
	var cfg *tracker.HealthConfig
	if in.WF != nil {
		cfg = in.WF.Health
	}
	var out []GaugeCard

	// Waiting on you, live over the range.
	g := GaugeCard{Title: "Waiting on you", Href: "#time"}
	var all timeline.TypeRow
	for _, t := range rep.Types {
		if t.Type == timeline.All {
			all = t
		}
	}
	if all.Working+all.Waiting+all.Idle == 0 {
		g.Empty = "No issues with CLI calls done in this range"
	} else {
		share := all.WaitingShare()
		limit, hasLimit := cfg.Limit("waiting_share")
		gg := chart.Gauge{Label: "Waiting on you: " + chart.Format(chart.Share, share) + " of issue time", Value: share, Max: 1, Class: chart.ClassWaiting}
		g.Value, g.Context = chart.Format(chart.Share, share), "of issue time"
		if hasLimit {
			gg.Limit = limit
			gg.Label += ", limit " + chart.Format(chart.Share, limit)
			g.Context += " · limit " + chart.Format(chart.Share, limit)
			g.Over = share > limit
			g.Verdict = verdict(g.Over)
		}
		g.SVG = gg.SVG()
	}
	out = append(out, g)

	// The current milestone, live.
	m := GaugeCard{Title: "Current milestone", Href: in.Prefix + "/status"}
	if v, done, total, items := milestone.Pick(milestone.ByVersion(in.Issues), milestone.StatusesOf(in.WF), in.Released); v != "" {
		m.Title = "Milestone " + v
		m.Value = fmt.Sprintf("%d / %d", done, total)
		m.Context = fmt.Sprintf("done · %d open", total-done)
		m.SVG = chart.Gauge{Label: fmt.Sprintf("Milestone %s: %d of %d issues done", v, done, total), Value: float64(done), Max: float64(total), Class: chart.ClassAgent}.SVG()
		m.Chip = openByStatus(in.WF, items)
	} else {
		m.Empty = "No version with open issues"
	}
	out = append(out, m)

	// Big files, from the latest snapshot.
	b := GaugeCard{Title: "Big files"}
	if v, ok := snapValue(latest, "codebase.big_files", nil); ok {
		limit, hasLimit := cfg.Limit("big_files")
		max := v
		if hasLimit && limit*2 > max {
			max = limit * 2
		}
		gg := chart.Gauge{Label: fmt.Sprintf("Big files: %d over %d lines", int(v), cfg.BigFileLimit()), Value: v, Max: max, Class: chart.ClassAgent}
		b.Value, b.Context = strconv.Itoa(int(v)), fmt.Sprintf("over %d lines", cfg.BigFileLimit())
		if hasLimit {
			gg.Limit = limit
			gg.Label += fmt.Sprintf(", limit %d", int(limit))
			b.Context += fmt.Sprintf(" · limit %d", int(limit))
			b.Over = v > limit
			b.Verdict = verdict(b.Over)
		}
		b.SVG = gg.SVG()
	} else {
		b.Empty = "No codebase snapshot yet"
	}
	out = append(out, b)

	out = append(out,
		GaugeCard{Title: "CI green rate, 14 days", Pending: checksNeed},
		GaugeCard{Title: "Static context tokens", Pending: contextNeed},
	)
	return out
}

func verdict(over bool) string {
	if over {
		return "Over limit"
	}
	return "Within limit"
}

// openByStatus is "1 shipping, 1 in design, 12 idea" for the milestone's
// open issues, furthest along first.
func openByStatus(wf *tracker.WorkflowConfig, items []*tracker.Issue) string {
	st := milestone.StatusesOf(wf)
	n := map[string]int{}
	for _, is := range items {
		k := strings.ToLower(is.Status)
		if k == st.Final || st.Side[k] {
			continue
		}
		n[is.Status]++
	}
	keys := make([]string, 0, len(n))
	for k := range n {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := statusIndex(wf, keys[i]), statusIndex(wf, keys[j])
		if a != b {
			return a > b
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%d %s", n[k], k)
	}
	return strings.Join(parts, ", ")
}

func statusIndex(wf *tracker.WorkflowConfig, status string) int {
	if wf == nil {
		return -1
	}
	return wf.GetStatusIndex(status)
}
