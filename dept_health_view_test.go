package main

import (
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/deptfamily"
	"github.com/michal-franc/issue-viewer/internal/health"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// deptSnap is a snapshot with the departments family's rows for
// marketing (on track 0.62) and sales (nothing to measure).
func deptSnap(at time.Time, onTrack float64) *health.Snapshot {
	s := healthSnap(at, map[string]float64{
		"departments.on_track{department=marketing}":              onTrack,
		"departments.on_track":                                    onTrack,
		"departments.count":                                       2,
		"departments.goals.progress{department=marketing}":        55,
		"departments.goals.moving{department=marketing}":          0.5,
		"departments.goals.delta{department=marketing}":           4,
		"departments.outputs.waiting{department=marketing}":       3,
		"departments.outputs.reviewed{department=marketing}":      0.75,
		"departments.jobs.on_time{department=marketing}":          0.5,
		"departments.jobs.due{department=marketing}":              1,
		"departments.jobs.missed{department=marketing}":           2,
		"departments.handoffs.open{department=marketing,dir=in}":  1,
		"departments.handoffs.open{department=marketing,dir=out}": 0,
		"departments.handoffs.oldest_days{department=marketing}":  9,
	})
	s.Families[deptfamily.Family] = health.FamilyResult{Metrics: len(s.Metrics)}
	return s
}

func deptProject() *tracker.Project {
	return &tracker.Project{Slug: "acme", Departments: []tracker.Department{{Name: "Marketing"}, {Name: "Sales"}}}
}

func TestDeptHealthRows(t *testing.T) {
	p := deptProject()
	d := deptHealth(p, deptSnap(time.Now(), 0.62), nil)
	if d == nil || len(d.Rows) != 2 {
		t.Fatalf("deptHealth = %+v", d)
	}
	m, s := d.Rows[0], d.Rows[1]
	for _, c := range []struct{ got, want string }{
		{m.OnTrack, "62%"}, {m.Tone, "medium"},
		{m.Goals, "55% to target · 50% moving (+4 in 7 days)"},
		{m.Outputs, "75% reviewed within 7 days · 3 waiting"},
		{m.Jobs, "50% on time · 1 due · 2 periods missed"},
		{m.Handoff, "1 in · 0 out open · oldest 9 days"},
		{s.OnTrack, ""}, {s.Goals, "no goal progress"}, {s.Jobs, "no jobs"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if !m.Old {
		t.Error("a 9-day handoff is past the 7-day limit")
	}
	// The limit is configurable; 0 turns it off.
	if deptHealthRow(deptSnap(time.Now(), 0.62), p.Departments[0], &tracker.HealthConfig{Limits: map[string]float64{"handoff_age_days": 0}}).Old {
		t.Error("handoff_age_days 0 turns the mark off")
	}

	// No departments: no section. Departments, no family yet: the empty state.
	if deptHealth(&tracker.Project{}, deptSnap(time.Now(), 1), nil) != nil {
		t.Error("section without departments")
	}
	if e := deptHealth(p, healthSnap(time.Now(), nil), nil); e == nil || e.Empty == "" || !strings.Contains(e.Command, "--project acme health snapshot") {
		t.Errorf("empty state = %+v", e)
	}
}

func TestDeptFootLineAndSeries(t *testing.T) {
	s := deptSnap(time.Now(), 0.85)
	f := deptFootItem(s, "/p/acme", "marketing")
	if f == nil || f.Text != "♥ 85% on track" || f.Tone != "green" || f.URL != "/p/acme/health#departments" ||
		!strings.Contains(f.Title, "goals moving 50% · outputs reviewed in time 75% · jobs on time 50%") {
		t.Errorf("foot = %+v", f)
	}
	if deptFootItem(s, "/p/acme", "sales") != nil || deptFootItem(nil, "", "marketing") != nil {
		t.Error("foot item without a row")
	}
	if got := deptHealthLine(s); got != "Departments: 85% on track · 3 outputs waiting" {
		t.Errorf("line = %q", got)
	}
	if deptHealthLine(healthSnap(time.Now(), map[string]float64{"lint.warnings": 1})) != "" {
		t.Error("line without the family")
	}
	now := time.Now()
	series := deptSeries([]*health.Snapshot{deptSnap(now.AddDate(0, 0, -1), 0.5), healthSnap(now, nil), deptSnap(now, 0.7)})
	if len(series) != 1 || series[0].Name != "marketing" || len(series[0].Points) != 2 || series[0].Points[1].Value != 0.7 {
		t.Errorf("series = %+v", series)
	}
}

// The pages: /health's table, the room foot, the Report's line and the
// Stats card, from a snapshot with the family. Without departments none
// of them shows.
func TestDeptHealthOnThePages(t *testing.T) {
	withMockTmuxSessions(t, nil)
	withReviewLive(t, false)
	stubLeadPane(t, "")
	now := time.Now()
	proj, root := healthProject(t, now)
	plain := proj
	if _, err := health.Write(root, deptSnap(now.Add(-time.Minute), 0.62), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	ts := newTestServer(t, []tracker.Project{plain})
	_, page := getBody(t, ts.URL+"/p/test-project/health")
	ts.Close()
	if strings.Contains(page, `data-testid="health.departments"`) {
		t.Error("no departments, yet the health page has the Departments table")
	}

	proj.Departments = []tracker.Department{{Name: "Marketing"}, {Name: "Sales"}}
	ts = newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, page = getBody(t, ts.URL+"/p/test-project/health")
	assertContains(t, page, `id="departments"`, `data-testid="health.departments.row.marketing"`, `data-testid="health.departments.row.sales"`,
		"62%", "75% reviewed within 7 days · 3 waiting", "nothing to measure", `class="wb-dept-health--old"`)
	_, company := getBody(t, ts.URL+"/p/test-project/company")
	assertContains(t, company, "♥ 62% on track", `href="/p/test-project/health#departments"`, `class="wb-foot--amber"`)
	_, report := getBody(t, ts.URL+"/p/test-project/status")
	assertContains(t, report, `data-testid="report.health.departments"`, "Departments: 62% on track · 3 outputs waiting")
	_, md := getBody(t, ts.URL+"/p/test-project/status.md")
	assertContains(t, md, "- Departments: 62% on track")
	_, stats := getBody(t, ts.URL+"/p/test-project/stats?classic=1")
	assertContains(t, stats, "Departments on track")
}
