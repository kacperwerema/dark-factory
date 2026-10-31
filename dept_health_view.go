package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/chart"
	"github.com/michal-franc/issue-viewer/internal/deptfamily"
	"github.com/michal-franc/issue-viewer/internal/health"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The departments health family on the pages (internal/deptfamily): the
// Departments table on /health, the "♥ N% on track" item on each room's
// foot, the Report Health card's line and the Stats trend. Everything reads
// the newest snapshot; nothing is measured live.

// DeptHealthRow is one department's measures, as words.
type DeptHealthRow struct {
	Key, Name string
	// OnTrack is "72%", "" when the department has nothing to measure.
	OnTrack string
	Pct     int
	Tone    string
	Goals   string
	Outputs string
	Jobs    string
	Handoff string
	// Old: its oldest open inbox handoff is past health.limits.handoff_age_days.
	Old bool
}

// DeptHealth is /health's Departments section; nil without departments.
type DeptHealth struct {
	Rows []DeptHealthRow
	// Empty says why there are no rows and what fills them.
	Empty   string
	Command string
}

func deptHealthCommand(p *tracker.Project) string {
	if p.Slug != "" {
		return "issue-cli --project " + p.Slug + " health snapshot"
	}
	return "issue-cli health snapshot"
}

// deptHealth builds the section from the snapshot s (nil: none yet).
func deptHealth(p *tracker.Project, s *health.Snapshot, cfg *tracker.HealthConfig) *DeptHealth {
	if p == nil || len(p.Departments) == 0 {
		return nil
	}
	d := &DeptHealth{Command: deptHealthCommand(p)}
	if s == nil || s.Families[deptfamily.Family].Metrics == 0 {
		d.Empty = "No department measures yet: the next snapshot takes them."
		return d
	}
	for _, dept := range p.Departments {
		d.Rows = append(d.Rows, deptHealthRow(s, dept, cfg))
	}
	return d
}

func deptHealthRow(s *health.Snapshot, dept tracker.Department, cfg *tracker.HealthConfig) DeptHealthRow {
	key := tracker.DepartmentKey(dept.Name)
	l := func(kv ...string) map[string]string {
		m := map[string]string{"department": key}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	get := func(name string, kv ...string) (float64, bool) { return s.Value(name, l(kv...)) }
	r := DeptHealthRow{Key: key, Name: dept.Name, Goals: "no goal progress", Outputs: "no outputs", Jobs: "no jobs", Handoff: "no handoffs"}
	if v, ok := get(deptfamily.OnTrack); ok {
		r.Pct = int(math.Round(v * 100))
		r.OnTrack, r.Tone = fmt.Sprintf("%d%%", r.Pct), shareTone(v)
	}
	if v, ok := get(deptfamily.GoalsProgress); ok {
		r.Goals = fmt.Sprintf("%.0f%% to target", v)
		if m, ok := get(deptfamily.GoalsMoving); ok {
			delta, _ := get(deptfamily.GoalsDelta)
			r.Goals += fmt.Sprintf(" · %.0f%% moving (%+.0f in 7 days)", m*100, delta)
		} else {
			r.Goals += " · moving: needs a week of snapshots"
		}
	}
	if w, ok := get(deptfamily.OutputsWaiting); ok {
		r.Outputs = plural(int(w), "waiting", "waiting")
		if v, ok := get(deptfamily.OutputsReview); ok {
			days, _ := cfg.Limit("outputs_review_days")
			r.Outputs = fmt.Sprintf("%.0f%% reviewed within %s · %s", v*100, plural(int(days), "day", "days"), r.Outputs)
		}
	}
	if v, ok := get(deptfamily.JobsOnTime); ok {
		missed, _ := get(deptfamily.JobsMissed)
		due, _ := get(deptfamily.JobsDue)
		r.Jobs = fmt.Sprintf("%.0f%% on time · %d due · %s", v*100, int(due), plural(int(missed), "period missed", "periods missed"))
	}
	if in, ok := get(deptfamily.HandoffsOpen, "dir", "in"); ok {
		out, _ := get(deptfamily.HandoffsOpen, "dir", "out")
		r.Handoff = fmt.Sprintf("%d in · %d out open", int(in), int(out))
		if age, ok := get(deptfamily.HandoffsOldest); ok {
			r.Handoff += " · oldest " + plural(int(age), "day", "days")
			if limit, on := cfg.Limit("handoff_age_days"); on && age > limit {
				r.Old = true
			}
		}
	}
	return r
}

// shareTone is an on-track share's colour, as the score's sub-scores: good
// from 0.7, bad below 0.4.
func shareTone(v float64) string {
	return healthTone(v * 100)
}

// deptFootItem is a room's "♥ N% on track" foot item from the snapshot;
// nil when the snapshot has no row for the box.
func deptFootItem(s *health.Snapshot, prefix, box string) *WbFootItem {
	if s == nil {
		return nil
	}
	v, ok := s.Value(deptfamily.OnTrack, map[string]string{"department": box})
	if !ok {
		return nil
	}
	tone := map[string]string{"good": "green", "medium": "amber", "bad": "red"}[shareTone(v)]
	var parts []string
	for _, m := range []struct{ name, word string }{
		{deptfamily.GoalsMoving, "goals moving"}, {deptfamily.OutputsReview, "outputs reviewed in time"}, {deptfamily.JobsOnTime, "jobs on time"},
	} {
		if x, ok := s.Value(m.name, map[string]string{"department": box}); ok {
			parts = append(parts, fmt.Sprintf("%s %.0f%%", m.word, x*100))
		}
	}
	return &WbFootItem{Text: fmt.Sprintf("♥ %.0f%% on track", v*100), Tone: tone, URL: prefix + "/health#departments",
		Title: "From the last health snapshot: " + strings.Join(parts, " · ")}
}

// addDeptFoot appends the on-track item to a room's foot and its children's.
func addDeptFoot(room *WbRoomDept, s *health.Snapshot, prefix string) {
	if f := deptFootItem(s, prefix, room.Box); f != nil {
		room.Foot = append(room.Foot, *f)
	}
	for i := range room.Children {
		addDeptFoot(&room.Children[i], s, prefix)
	}
}

// deptHealthLine is the Report Health card's line: "Departments: 72% on
// track · 2 outputs waiting"; "" without the family's project row.
func deptHealthLine(s *health.Snapshot) string {
	if s == nil {
		return ""
	}
	v, ok := s.Value(deptfamily.OnTrack, nil)
	if !ok {
		return ""
	}
	waiting := 0.0
	for _, r := range s.Rows(deptfamily.OutputsWaiting) {
		waiting += r.Value
	}
	line := fmt.Sprintf("Departments: %.0f%% on track", v*100)
	if waiting > 0 {
		line += " · " + plural(int(waiting), "output waiting", "outputs waiting")
	}
	return line
}

// deptSeries is the Stats trend: each department's on-track share per
// snapshot, departments in name order.
func deptSeries(snaps []*health.Snapshot) []chart.Series {
	byKey := map[string]*chart.Series{}
	var keys []string
	for _, s := range snaps {
		for _, r := range s.Rows(deptfamily.OnTrack) {
			k := r.Labels["department"]
			if k == "" {
				continue
			}
			ser := byKey[k]
			if ser == nil {
				ser = &chart.Series{Name: k}
				byKey[k] = ser
				keys = append(keys, k)
			}
			ser.Points = append(ser.Points, chart.Point{At: s.Time(), Value: r.Value})
		}
	}
	out := make([]chart.Series, 0, len(keys))
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, *byKey[k])
	}
	return out
}

// newestHealth is the newest snapshot at or before now, read alone (the
// company page shouldn't read every snapshot).
func newestHealth(p *tracker.Project, now time.Time) *health.Snapshot {
	return health.Before(ciRootFor(p), now)
}
