package viewer

import (
	"fmt"

	"github.com/kacperwerema/dark-factory/internal/period"

	"sort"

	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/chart"

	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// issueMeta indexes issues by slug.
func issueMeta(issues []*tracker.Issue) map[string]*tracker.Issue {
	m := make(map[string]*tracker.Issue, len(issues))
	for _, is := range issues {
		m[is.Slug] = is
	}
	return m
}

// groupOf is an issue's group for the group-by option.
func groupOf(group string, s timeline.IssueStat, meta map[string]*tracker.Issue) string {
	is := meta[s.Slug]
	switch group {
	case "system":
		if is != nil && is.System != "" {
			return is.System
		}
		return "(no system)"
	case "epic":
		if is != nil {
			if p := tracker.ParentSlug(is); p != "" {
				return p
			}
		}
		return "(no epic)"
	case "version":
		if is != nil && is.Version != "" {
			return is.Version
		}
		return "(no version)"
	}
	return typeName(s.Type)
}

// statusNames is the workflow's statuses in order, then any other status
// the issues spent time in.
func statusNames(wf *tracker.WorkflowConfig, issues []timeline.IssueStat) []string {
	var out []string
	seen := map[string]bool{}
	if wf != nil {
		for _, s := range wf.Statuses {
			if !seen[s.Name] {
				seen[s.Name] = true
				out = append(out, s.Name)
			}
		}
	}
	var extra []string
	for _, is := range issues {
		for st := range is.Statuses {
			if !seen[st] {
				seen[st] = true
				extra = append(extra, st)
			}
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func timeSegments(working, waiting, idle time.Duration) []chart.Segment {
	return []chart.Segment{
		{Name: "agent working", Class: chart.ClassAgent, Value: working.Seconds()},
		{Name: "waiting on you", Class: chart.ClassWaiting, Value: waiting.Seconds()},
		{Name: "idle", Class: chart.ClassIdle, Value: idle.Seconds()},
	}
}

// maxGroups is how many group rows the by-group chart draws; the rest are
// summed into one row.
const maxGroups = 12

func timeBars(in dashIn, d *Dashboard, rep timeline.Report) []ChartCard {
	share := in.Opts.Scale == "share"
	unit := chart.Hours
	if share {
		unit = chart.Share
	}
	drillKind, drillVal, _ := strings.Cut(in.Opts.Drill, ":")

	// By status.
	type sums struct{ working, waiting, idle time.Duration }
	byStatus := map[string]*sums{}
	var total sums
	for _, s := range rep.Issues {
		if !s.Known {
			continue
		}
		for st, t := range s.Statuses {
			b := byStatus[st]
			if b == nil {
				b = &sums{}
				byStatus[st] = b
			}
			b.working += t.Working
			b.waiting += t.Waiting
			b.idle += t.Idle
			total.working += t.Working
			total.waiting += t.Waiting
			total.idle += t.Idle
		}
	}
	status := ChartCard{ID: "time", Title: "By status", Sub: fmt.Sprintf("Summed over the issues: agent working %s of %s", chart.Format(chart.Hours, total.working.Seconds()), chart.Format(chart.Hours, (total.working+total.waiting+total.idle).Seconds()))}
	for _, sc := range []struct{ key, label string }{{"hours", "Hours"}, {"share", "Share"}} {
		key := sc.key
		status.Toggles = append(status.Toggles, statsOption{Key: key, Label: sc.label, On: in.Opts.Scale == key, Href: d.href(func(o *statsOpts) { o.Scale = key }) + "#time"})
	}
	h := chart.HBar{Label: "Time by status: agent working, waiting on you and idle", Unit: unit}
	for _, st := range statusNames(in.WF, rep.Issues) {
		b := byStatus[st]
		if b == nil || b.working+b.waiting+b.idle == 0 {
			continue
		}
		bar := chart.Bar{Label: st, Segments: timeSegments(b.working, b.waiting, b.idle), Href: d.drillHref("status:"+st, "time"), Selected: drillKind == "status" && drillVal == st}
		if share {
			bar = bar.AsShare()
		}
		h.Bars = append(h.Bars, bar)
	}
	if len(h.Bars) == 0 {
		status.Empty = "No issues with CLI calls done in this range"
	} else {
		status.SVG, status.Table = h.SVG(), h.Table()
		status.Table.Head[0] = "Status"
	}

	// By the group-by option.
	meta := issueMeta(in.Issues)
	groupLabel := "type"
	for _, g := range statsGroups {
		if g.Key == in.Opts.Group {
			groupLabel = strings.ToLower(g.Label)
		}
	}
	byGroup := map[string]*sums{}
	for _, s := range rep.Issues {
		if !s.Known {
			continue
		}
		k := groupOf(in.Opts.Group, s, meta)
		b := byGroup[k]
		if b == nil {
			b = &sums{}
			byGroup[k] = b
		}
		for _, t := range s.Statuses {
			b.working += t.Working
			b.waiting += t.Waiting
			b.idle += t.Idle
		}
	}
	keys := make([]string, 0, len(byGroup))
	for k := range byGroup {
		keys = append(keys, k)
	}
	tot := func(k string) time.Duration { b := byGroup[k]; return b.working + b.waiting + b.idle }
	sort.Slice(keys, func(i, j int) bool {
		if tot(keys[i]) != tot(keys[j]) {
			return tot(keys[i]) > tot(keys[j])
		}
		return keys[i] < keys[j]
	})
	group := ChartCard{ID: "group", Title: "By " + groupLabel, Sub: "Group by: " + groupLabel + " · click a bar for its issues"}
	g := chart.HBar{Label: "Time by " + groupLabel + ": agent working, waiting on you and idle", Unit: unit}
	var rest sums
	restN := 0
	for i, k := range keys {
		b := byGroup[k]
		if i >= maxGroups {
			rest.working += b.working
			rest.waiting += b.waiting
			rest.idle += b.idle
			restN++
			continue
		}
		bar := chart.Bar{Label: shortGroup(k), Segments: timeSegments(b.working, b.waiting, b.idle), Href: d.drillHref("group:"+k, "group"), Selected: drillKind == "group" && drillVal == k}
		if share {
			bar = bar.AsShare()
		}
		g.Bars = append(g.Bars, bar)
	}
	if restN > 0 {
		bar := chart.Bar{Label: plural(restN, "other", "others"), Segments: timeSegments(rest.working, rest.waiting, rest.idle)}
		if share {
			bar = bar.AsShare()
		}
		g.Bars = append(g.Bars, bar)
	}
	if len(g.Bars) == 0 {
		group.Empty = "No issues with CLI calls done in this range"
	} else {
		group.SVG, group.Table = g.SVG(), g.Table()
		group.Table.Head[0] = strings.ToUpper(groupLabel[:1]) + groupLabel[1:]
	}
	return []ChartCard{status, group}
}

// shortGroup keeps a group label to the chart's label column: the last part
// of an epic slug, clipped.
func shortGroup(k string) string {
	if i := strings.LastIndex(k, "/"); i >= 0 && i < len(k)-1 {
		k = k[i+1:]
	}
	return clip(k, 16)
}

// periodOf is the column an issue done at t falls in: its ISO week, or its
// month for ranges over half a year.
func periodOf(t time.Time, monthly bool) (key, label string) {
	if monthly {
		return period.MonthKey(t), t.Format("Jan")
	}
	return period.WeekKey(t), period.WeekLabel(t)
}

func weeklyColumns(in dashIn, d *Dashboard, rep timeline.Report) ChartCard {
	monthly := d.To.Sub(d.From) > 182*24*time.Hour
	title := "Issues done per week, by type"
	if monthly {
		title = "Issues done per month, by type"
	}
	c := ChartCard{ID: "weekly", Title: title, Sub: "Click a segment for its issues"}
	var keys []string
	labels := map[string]string{}
	for t := d.From; !t.After(d.To); t = t.AddDate(0, 0, 1) {
		k, l := periodOf(t, monthly)
		if _, ok := labels[k]; !ok {
			keys = append(keys, k)
			labels[k] = l
		}
	}
	count := map[string]map[string]int{}
	seen := map[string]bool{}
	var types []string
	for _, s := range rep.Issues {
		k, _ := periodOf(s.DoneAt, monthly)
		if count[k] == nil {
			count[k] = map[string]int{}
		}
		count[k][s.Type]++
		if !seen[s.Type] {
			seen[s.Type] = true
			types = append(types, s.Type)
		}
	}
	types = typeOrder(types)
	for _, t := range types {
		c.Legend = append(c.Legend, LegendItem{Name: typeName(t), Class: chart.TypeClass(t, types)})
	}
	drillKind, rest, _ := strings.Cut(in.Opts.Drill, ":")
	selKey, selType, _ := strings.Cut(rest, ":")
	cols := chart.Columns{Label: title, Unit: chart.Count}
	for _, k := range keys {
		bar := chart.Bar{Label: labels[k], SegmentHref: map[string]string{}}
		for _, t := range types {
			n := count[k][t]
			if n == 0 {
				continue
			}
			bar.Segments = append(bar.Segments, chart.Segment{Name: typeName(t), Class: chart.TypeClass(t, types), Value: float64(n)})
			bar.SegmentHref[typeName(t)] = d.drillHref("week:"+k+":"+t, "weekly")
		}
		if drillKind == "week" && selKey == k {
			bar.SelectedSegment = typeName(selType)
		}
		cols.Bars = append(cols.Bars, bar)
	}
	if len(rep.Issues) == 0 {
		c.Empty = "No issues done in this range"
		return c
	}
	c.SVG, c.Table = cols.SVG(), cols.Table()
	c.Table.Head[0] = "Week"
	if monthly {
		c.Table.Head[0] = "Month"
	}
	return c
}

// parseDrillPoint reads point:<chart>:<date>:<series>.
func parseDrillPoint(drill string) (at time.Time, series, chartID string) {
	parts := strings.SplitN(drill, ":", 4)
	if len(parts) != 4 || parts[0] != "point" {
		return time.Time{}, "", ""
	}
	day, err := time.ParseInLocation("2006-01-02", parts[2], time.Local)
	if err != nil {
		return time.Time{}, "", ""
	}
	return day, parts[3], parts[1]
}

// drill lists the issues behind the drill option.
func drill(in dashIn, d *Dashboard, rep timeline.Report) *Drill {
	if in.Opts.Drill == "" {
		return nil
	}
	meta := issueMeta(in.Issues)
	var list []timeline.IssueStat
	var title, anchor string
	kind, rest, _ := strings.Cut(in.Opts.Drill, ":")
	switch kind {
	case "point":
		day, series, chartID := parseDrillPoint(in.Opts.Drill)
		if day.IsZero() {
			return nil
		}
		// A point covers the issues done in the window before its
		// snapshot, as the workflow family counted them.
		end := day.Add(24*time.Hour - time.Nanosecond)
		for _, s := range in.Wide.Filter(end.Add(-timeline.Window), end).Issues {
			if series == timeline.All || s.Type == series {
				list = append(list, s)
			}
		}
		anchor = chartID
		what := "all types"
		if series != timeline.All {
			what = typeName(series)
		}
		title = fmt.Sprintf("%s · %s · %s done in the 30 days before", day.Format("2 Jan"), what, plural(len(list), "issue", "issues"))
		sort.SliceStable(list, func(i, j int) bool { return list[i].Cycle > list[j].Cycle })
	case "status":
		for _, s := range rep.Issues {
			if t, ok := s.Statuses[rest]; ok && s.Known && t.Total > 0 {
				list = append(list, s)
			}
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].Statuses[rest].Total > list[j].Statuses[rest].Total })
		anchor, title = "time", fmt.Sprintf("%s · %s", rest, plural(len(list), "issue", "issues"))
	case "group":
		for _, s := range rep.Issues {
			if s.Known && groupOf(in.Opts.Group, s, meta) == rest {
				list = append(list, s)
			}
		}
		anchor, title = "group", fmt.Sprintf("%s · %s", rest, plural(len(list), "issue", "issues"))
	case "week":
		key, typ, _ := strings.Cut(rest, ":")
		monthly := d.To.Sub(d.From) > 182*24*time.Hour
		label := key
		for _, s := range rep.Issues {
			if k, l := periodOf(s.DoneAt, monthly); k == key && s.Type == typ {
				list = append(list, s)
				label = l
			}
		}
		anchor, title = "weekly", fmt.Sprintf("%s · %s · %s", label, typeName(typ), plural(len(list), "issue", "issues"))
	default:
		return nil
	}
	if kind != "point" && kind != "status" {
		sort.SliceStable(list, func(i, j int) bool { return list[i].DoneAt.After(list[j].DoneAt) })
	}
	dr := &Drill{Title: title, Anchor: anchor, Close: d.href(nil) + "#" + anchor}
	for i, s := range list {
		if i == drillLimit {
			dr.More = len(list) - i
			break
		}
		detail := "cycle " + chart.Duration(s.Cycle)
		if kind == "status" {
			t := s.Statuses[rest]
			detail = fmt.Sprintf("in status %s · agent %s · waiting %s · idle %s", chart.Duration(t.Total), chart.Duration(t.Working), chart.Duration(t.Waiting), chart.Duration(t.Idle))
		} else if s.Known {
			detail += " · agent " + chart.Duration(s.Working)
		}
		if s.Loops > 0 {
			detail += " · " + plural(s.Loops, "loop", "loops")
		}
		dr.Issues = append(dr.Issues, DrillIssue{Slug: s.Slug, Href: in.Prefix + "/issue/" + s.Slug + "?tab=timeline", Date: s.DoneAt.Local().Format("Mon 2 Jan"), Detail: detail})
	}
	return dr
}
