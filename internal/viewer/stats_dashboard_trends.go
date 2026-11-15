package viewer

import (
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/decisionstats"

	"sort"

	"time"

	"github.com/kacperwerema/dark-factory/internal/chart"
	"github.com/kacperwerema/dark-factory/internal/health"

	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func snapValue(s *health.Snapshot, metric string, labels map[string]string) (float64, bool) {
	if s == nil {
		return 0, false
	}
	return s.Value(metric, labels)
}

// lastPerDay keeps each day's last snapshot in [from, to].
func lastPerDay(snaps []*health.Snapshot, from, to time.Time) []*health.Snapshot {
	var out []*health.Snapshot
	for _, s := range snaps {
		at := s.Time()
		if at.Before(from) || at.After(to) {
			continue
		}
		if n := len(out); n > 0 && sameDate(out[n-1].Time(), at) {
			out[n-1] = s
			continue
		}
		out = append(out, s)
	}
	return out
}

func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// typeOrder puts the known types first in a fixed order, others by name,
// none (no type) last.
func typeOrder(types []string) []string {
	rank := map[string]int{"feature": 0, "bugfix": 1, "tweak": 2, "epic": 3}
	sort.SliceStable(types, func(i, j int) bool {
		ri, iok := rank[types[i]]
		rj, jok := rank[types[j]]
		switch {
		case types[i] == timeline.NoType:
			return false
		case types[j] == timeline.NoType:
			return true
		case iok && jok:
			return ri < rj
		case iok != jok:
			return iok
		}
		return types[i] < types[j]
	})
	return types
}

func pointKey(chartID string, at time.Time, series string) string {
	return "point:" + chartID + ":" + at.Format("2006-01-02") + ":" + series
}

func trends(in dashIn, d *Dashboard, daily []*health.Snapshot, latest *health.Snapshot) []ChartCard {
	var cfg *tracker.HealthConfig
	if in.WF != nil {
		cfg = in.WF.Health
	}
	issuesAt := func(s *health.Snapshot, typ string) float64 {
		v, _ := s.Value("workflow.issues", map[string]string{"type": typ})
		return v
	}
	base := func(id, title, sub string) (ChartCard, chart.Line) {
		return ChartCard{ID: id, Title: title, Sub: sub}, chart.Line{Label: title, From: d.From, To: d.To, Gap: pointGap}
	}
	finish := func(c ChartCard, l chart.Line, family string) ChartCard {
		if latest != nil {
			if r, ok := latest.Families[family]; ok && r.Error != "" {
				c.Error = "The " + family + " family failed in the latest snapshot: " + r.Error
			}
		}
		c.Points = l.Points()
		if l.Empty() {
			c.Empty = "No points in this range"
			return c
		}
		// Mark where the data starts when it starts inside the range.
		first := time.Time{}
		for _, s := range l.Series {
			for _, p := range s.Points {
				if first.IsZero() || p.At.Before(first) {
					first = p.At
				}
			}
		}
		if first.Sub(d.From) > 2*24*time.Hour {
			l.MarkerAt, l.MarkerText = first, "data starts"
		}
		c.SVG, c.Table = l.SVG(), l.Table()
		return c
	}
	selAt, selSeries, selChart := parseDrillPoint(in.Opts.Drill)
	selected := func(id string, l *chart.Line) {
		if selChart == id {
			l.SelectedAt, l.SelectedSeries = selAt, selSeries
		}
	}

	var out []ChartCard

	// Cycle time by type.
	metric := "workflow.cycle.p" + in.Opts.P
	c, l := base("cycle", "Cycle time p"+in.Opts.P+", by type", "Dispatch to done. Click a point for its issues.")
	l.Unit = chart.Seconds
	seen := map[string]bool{}
	var types []string
	for _, s := range daily {
		for _, r := range s.Rows(metric) {
			if t := r.Labels["type"]; t != timeline.All && !seen[t] {
				seen[t] = true
				types = append(types, t)
			}
		}
	}
	types = typeOrder(types)
	for _, typ := range types {
		name := typeName(typ)
		other := append([]string(nil), d.Opts.Hide...)
		item := LegendItem{Name: name, Class: chart.TypeClass(typ, types)}
		if d.Opts.hidden(typ) {
			item.Hidden = true
			item.Href = d.href(func(o *statsOpts) { o.Hide = without(other, typ) }) + "#cycle"
			c.Legend = append(c.Legend, item)
			continue
		}
		item.Href = d.href(func(o *statsOpts) { o.Hide = append(other, typ) }) + "#cycle"
		c.Legend = append(c.Legend, item)
		ser := chart.Series{Name: name, Class: item.Class}
		for _, s := range daily {
			n := issuesAt(s, typ)
			v, ok := s.Value(metric, map[string]string{"type": typ})
			if !ok || n == 0 {
				continue
			}
			ser.Points = append(ser.Points, chart.Point{At: s.Time(), Value: v, Note: plural(int(n), "issue", "issues"), Href: d.drillHref(pointKey("cycle", s.Time(), typ), "cycle")})
		}
		l.Series = append(l.Series, ser)
	}
	for _, p := range []string{"50", "90"} {
		p := p
		c.Toggles = append(c.Toggles, statsOption{Key: p, Label: "p" + p, On: in.Opts.P == p, Href: d.href(func(o *statsOpts) { o.P = p }) + "#cycle"})
	}
	selected("cycle", &l)
	out = append(out, finish(c, l, "workflow"))

	// Waiting on you.
	c, l = base("waiting", "Waiting on you", "Share of issue time in statuses only you can move on, and up to each approval")
	l.Unit = chart.Share
	if limit, ok := cfg.Limit("waiting_share"); ok {
		l.Limit, l.LimitLabel = limit, "limit "+chart.Format(chart.Share, limit)+" (health.limits.waiting_share)"
	}
	ser := chart.Series{Name: "waiting", Class: chart.ClassWaiting}
	for _, s := range daily {
		v, ok := s.Value("workflow.time.share", map[string]string{"kind": "waiting"})
		if !ok || issuesAt(s, timeline.All) == 0 {
			continue
		}
		ser.Points = append(ser.Points, chart.Point{At: s.Time(), Value: v, Href: d.drillHref(pointKey("waiting", s.Time(), timeline.All), "waiting")})
	}
	l.Series = []chart.Series{ser}
	selected("waiting", &l)
	out = append(out, finish(c, l, "workflow"))

	// Agent working time per issue.
	c, l = base("working", "Agent working time per issue", "p50 and p90 across the issues done in the 30 days before each point")
	l.Unit = chart.Seconds
	for _, p := range []string{"p50", "p90"} {
		ser := chart.Series{Name: p, Class: chart.ClassAgent, Faint: p == "p90"}
		for _, s := range daily {
			v, ok := s.Value("workflow.working."+p, map[string]string{"type": timeline.All})
			if !ok || issuesAt(s, timeline.All) == 0 {
				continue
			}
			ser.Points = append(ser.Points, chart.Point{At: s.Time(), Value: v, Href: d.drillHref(pointKey("working", s.Time(), timeline.All), "working")})
		}
		l.Series = append(l.Series, ser)
		c.Legend = append(c.Legend, LegendItem{Name: p, Class: chart.ClassAgent, Hidden: false})
	}
	selected("working", &l)
	out = append(out, finish(c, l, "workflow"))

	// Rework and gate failures.
	c, l = base("rework", "Rework and gate failures", "Rework loops, and refused transition calls that were real attempts (dry runs are in each point's tooltip)")
	l.Unit = chart.Count
	loops := chart.Series{Name: "rework loops", Class: "s-feature"}
	fails := chart.Series{Name: "gate failures", Class: "s-bugfix", Faint: true}
	for _, s := range daily {
		if issuesAt(s, timeline.All) == 0 {
			continue
		}
		href := d.drillHref(pointKey("rework", s.Time(), timeline.All), "rework")
		if v, ok := s.Value("workflow.rework.loops", map[string]string{"type": timeline.All}); ok {
			loops.Points = append(loops.Points, chart.Point{At: s.Time(), Value: v, Href: href})
		}
		real, dry := 0.0, 0.0
		for _, r := range s.Rows("workflow.gate_failures") {
			if r.Labels["dry_run"] == "true" {
				dry += r.Value
			} else {
				real += r.Value
			}
		}
		fails.Points = append(fails.Points, chart.Point{At: s.Time(), Value: real, Note: plural(int(dry), "dry run", "dry runs"), Href: href})
	}
	l.Series = []chart.Series{loops, fails}
	c.Legend = []LegendItem{{Name: loops.Name, Class: loops.Class}, {Name: fails.Name, Class: fails.Class}}
	selected("rework", &l)
	out = append(out, finish(c, l, "workflow"))

	// Lines of code, with the test ratio in the subtitle.
	c, l = base("loc", "Lines of code", "Code and test lines at the branch's commit")
	l.Unit = chart.Count
	ser = chart.Series{Name: "lines", Class: "s-all"}
	var ratio []float64
	for _, s := range daily {
		if v, ok := s.Value("codebase.source_lines", nil); ok {
			ser.Points = append(ser.Points, chart.Point{At: s.Time(), Value: v})
		}
		if v, ok := s.Value("codebase.test_ratio", nil); ok {
			ratio = append(ratio, v)
		}
	}
	l.Series = []chart.Series{ser}
	if n := len(ratio); n > 0 {
		c.Sub += fmt.Sprintf(" · test ratio %.2f", ratio[n-1])
		c.SubSVG = chart.Spark{Label: "Test ratio trend", Values: ratio, Class: "s-tweak"}.SVG()
	}
	out = append(out, finish(c, l, "codebase"))

	// Factory benchmark pass rate, one series per benchmark, once a
	// snapshot has a finished run (internal/bench).
	if series := benchSeries(daily); len(series) > 0 {
		c, l = base("bench", "Factory benchmark pass rate", "Golden tasks passed in each benchmark's newest run, holdout apart (issue-cli bench report)")
		l.Unit = chart.Share
		classes := []string{"s-feature", "s-bugfix", "s-tweak", "s-all"}
		for i, ser := range series {
			ser.Class = classes[i%len(classes)]
			l.Series = append(l.Series, ser)
			c.Legend = append(c.Legend, LegendItem{Name: ser.Name, Class: ser.Class})
		}
		out = append(out, finish(c, l, "bench"))
	}

	// Departments on track, one series per department, once a snapshot
	// has the departments family (internal/deptfamily).
	if series := deptSeries(daily); len(series) > 0 {
		c, l = base("departments", "Departments on track", "Each department's share of goals moving, outputs reviewed in time and jobs on time (/health#departments)")
		l.Unit = chart.Share
		classes := []string{"s-feature", "s-bugfix", "s-tweak", "s-all"}
		for i, ser := range series {
			ser.Class = classes[i%len(classes)]
			l.Series = append(l.Series, ser)
			c.Legend = append(c.Legend, LegendItem{Name: ser.Name, Class: ser.Class})
		}
		out = append(out, finish(c, l, "departments"))
	}

	// The decision ledger, once a snapshot has a decision
	// (internal/decisionstats).
	if series := decisionSeries(daily); len(series) > 0 {
		c, l = base("decisions", "Decisions in force", "Hard, soft and provisional decisions in force, and gate holds by a decision in the 30 days before each point (issue-cli decisions log)")
		l.Unit = chart.Count
		for _, ser := range series {
			l.Series = append(l.Series, ser)
			c.Legend = append(c.Legend, LegendItem{Name: ser.Name, Class: ser.Class})
		}
		out = append(out, finish(c, l, decisionstats.Family))
	}

	// Disk use, once a snapshot has the storage family.
	if c, ok := storageCard(d, daily, latest); ok {
		out = append(out, c)
	}
	return out
}

func without(list []string, x string) []string {
	var out []string
	for _, v := range list {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

// typeName is how a type shows: "none" is "no type".
func typeName(t string) string {
	if t == timeline.NoType {
		return "no type"
	}
	return t
}

// pendingCards are the trend charts whose family no snapshot has yet.
func pendingCards(snaps []*health.Snapshot) []PendingCard {
	has := map[string]bool{}
	for _, s := range snaps {
		for f := range s.Families {
			has[f] = true
		}
	}
	var out []PendingCard
	for _, p := range []PendingCard{
		{Title: "CI and make validate duration", Family: "checks", Need: checksNeed},
		{Title: "ux-test duration", Family: "checks", Need: checksNeed + " Flaky steps are on the Health page (the tests family)."},
		{Title: "Tokens per issue", Family: "context", Need: contextNeed},
		{Title: "Disk use", Family: "storage", Need: "Take a snapshot: issue-cli health snapshot."},
	} {
		if !has[p.Family] {
			out = append(out, p)
		}
	}
	return out
}
