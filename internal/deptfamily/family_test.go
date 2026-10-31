package deptfamily

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/health"
	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func num(v float64) *float64 { return &v }

// Thursday 2026-10-29, noon.
var now = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func project(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{
		Slug: "acme", IssueDir: issues, WorkDir: root,
		Company: &tracker.Company{Mission: "Grow"},
		Departments: []tracker.Department{
			{Name: "Marketing", Systems: []string{"SEO"}, Goals: []tracker.Goal{
				{ID: "pages", Name: "Pages", Target: num(40)},
				{ID: "posts", Name: "Posts", Target: num(10)},
			}, Jobs: []tracker.Job{
				{ID: "daily", Every: "day", Brief: "b.md"},
				{ID: "weekly", Every: "week", On: "mon", Brief: "b.md"},
			}},
			{Name: "Sales"},
		},
	}
}

func issue(t *testing.T, slug, front string) *tracker.Issue {
	t.Helper()
	i, err := tracker.ParseIssue(strings.ReplaceAll(slug, "/", "-")+".md", []byte("---\ntitle: \""+slug+"\"\n"+front+"\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	i.Slug = slug
	return i
}

func rows(ms []health.Metric) map[string]float64 {
	out := map[string]float64{}
	for _, m := range ms {
		out[health.RowID(m.Name, m.Labels)] = m.Value
	}
	return out
}

func TestMeasure(t *testing.T) {
	p := project(t)
	for _, e := range []goals.Entry{{Goal: "marketing/pages", Value: 20, Date: "2026-10-28"}, {Goal: "marketing/posts", Value: 5, Date: "2026-10-20"}} {
		if err := goals.Append(p, e); err != nil {
			t.Fatal(err)
		}
	}
	// Outputs: one reviewed in 2 days, one in 10 (late), one waiting 3 days
	// (not late yet), one waiting 9 days (late).
	day := func(n int) string { return now.AddDate(0, 0, -n).Format(time.RFC3339) }
	for _, e := range []outputs.Event{
		{Op: outputs.OpAdd, ID: "o1", Date: day(20), Department: "marketing", Title: "a", Kind: "post", URL: "https://example.com/a"},
		{Op: outputs.OpReview, ID: "o1", Date: day(18), State: outputs.StateApproved, Quote: "ok"},
		{Op: outputs.OpAdd, ID: "o2", Date: day(15), Department: "marketing", Title: "b", Kind: "post", URL: "https://example.com/b"},
		{Op: outputs.OpReview, ID: "o2", Date: day(5), State: outputs.StateApproved, Quote: "ok"},
		{Op: outputs.OpAdd, ID: "o3", Date: day(3), Department: "marketing", Title: "c", Kind: "post", URL: "https://example.com/c"},
		{Op: outputs.OpAdd, ID: "o4", Date: day(9), Department: "marketing", Title: "d", Kind: "post", URL: "https://example.com/d"},
	} {
		if err := outputs.Append(p, e); err != nil {
			t.Fatal(err)
		}
	}
	issues := []*tracker.Issue{
		// The daily job ran today; the weekly one last ran two weeks ago.
		issue(t, "seo/daily-run", "status: \"done\"\njob: \"marketing/daily\"\nrun: \"2026-10-29\"\ndepartment: \"Marketing\""),
		issue(t, "seo/weekly-run", "status: \"done\"\njob: \"marketing/weekly\"\nrun: \"2026-W42\"\ndepartment: \"Marketing\""),
		// An open handoff from HQ to Marketing, 5 days old; one from Marketing to Product.
		issue(t, "seo/asked", "status: \"idea\"\ndepartment: \"Marketing\"\ncreated: \"2026-10-24\"\nrequested_by: \"hq\"\nhandoff_route: \"hq → marketing\""),
		issue(t, "factory/x", "status: \"idea\"\ncreated: \"2026-10-28\"\nrequested_by: \"Marketing\"\nhandoff_route: \"marketing → product\""),
	}
	past := &health.Snapshot{Metrics: []health.Row{
		{ID: health.RowID(GoalProgress, map[string]string{"department": "marketing", "goal": "pages"}), Metric: GoalProgress, Value: 40},
		{ID: health.RowID(GoalProgress, map[string]string{"department": "marketing", "goal": "posts"}), Metric: GoalProgress, Value: 50},
	}}
	got := rows(Measure(Input{Project: p, Issues: issues, Past: past, Now: now}))
	m := func(name string, kv ...string) string {
		l := map[string]string{"department": "marketing"}
		for i := 0; i+1 < len(kv); i += 2 {
			l[kv[i]] = kv[i+1]
		}
		return health.RowID(name, l)
	}
	want := map[string]float64{
		m(GoalProgress, "goal", "pages"): 50, m(GoalProgress, "goal", "posts"): 50,
		m(GoalsProgress): 50, m(GoalsMoving): 0.5, m(GoalsDelta): 5,
		m(OutputsWaiting): 2, m(OutputsReview): 0.333,
		m(JobsOnTime): 0.5, m(JobsDue): 1,
		m(HandoffsOpen, "dir", "in"): 1, m(HandoffsOpen, "dir", "out"): 1, m(HandoffsOldest): 5,
		m(OnTrack): 0.444, Count: 2, OnTrack: 0.444,
	}
	for id, v := range want {
		if g, ok := got[id]; !ok || g != v {
			t.Errorf("%s = %v (present %v), want %v", id, g, ok, v)
		}
	}
	// Sales has nothing to measure: no rows of its own.
	for id := range got {
		if strings.Contains(id, "department=sales") {
			t.Errorf("sales has a row: %s", id)
		}
	}
	if got[m(JobsMissed)] < 1 {
		t.Errorf("weekly job missed periods = %v", got[m(JobsMissed)])
	}
}

func TestNoPastNoMoving(t *testing.T) {
	p := project(t)
	if err := goals.Append(p, goals.Entry{Goal: "marketing/pages", Value: 20, Date: "2026-10-28"}); err != nil {
		t.Fatal(err)
	}
	got := rows(Measure(Input{Project: p, Now: now}))
	l := map[string]string{"department": "marketing"}
	if _, ok := got[health.RowID(GoalsMoving, l)]; ok {
		t.Error("goals moving without a past snapshot")
	}
	if got[health.RowID(GoalsProgress, l)] != 50 {
		t.Errorf("progress = %v", got[health.RowID(GoalsProgress, l)])
	}
}

// AC3: a project without departments gets no rows, and its score is the
// same with the departments component as without it.
func TestNoDepartmentsUnaffected(t *testing.T) {
	p := &tracker.Project{Slug: "solo", IssueDir: t.TempDir()}
	if got := Measure(Input{Project: p, Now: now}); got != nil {
		t.Fatalf("rows for a project without departments: %v", got)
	}
	got, err := family(t.Context(), health.Env{Root: t.TempDir(), Project: p, Now: now})
	if err != nil || got != nil {
		t.Fatalf("family = %v, %v", got, err)
	}
	cur := &health.Snapshot{Metrics: []health.Row{
		{ID: "lint.warnings", Metric: "lint.warnings", Value: 4},
		{ID: "shape.big_lines.share", Metric: "shape.big_lines.share", Value: 0.1},
	}}
	var without []health.Component
	for _, c := range health.DefaultComponents() {
		if c.Name != "departments" {
			without = append(without, c)
		}
	}
	a, b := rows(health.Derive(cur, nil, health.DefaultComponents())), rows(health.Derive(cur, nil, without))
	if len(a) != len(b) {
		t.Fatalf("rows differ: %v vs %v", a, b)
	}
	for id, v := range b {
		if a[id] != v {
			t.Errorf("%s = %v with departments component, %v without", id, a[id], v)
		}
	}
	// With departments it counts.
	cur.Metrics = append(cur.Metrics, health.Row{ID: OnTrack, Metric: OnTrack, Value: 0.5})
	if c := rows(health.Derive(cur, nil, health.DefaultComponents())); c["score.component{component=departments}"] != 0 || c["score.component.weight{component=departments}"] != 10 {
		t.Errorf("departments component: %v", c)
	}
}

// A department whose only measure is a job that has never run has nothing
// to measure yet: no on-track row, so it is not off track (AC1). The
// project share counts only measured departments.
func TestNotMeasured(t *testing.T) {
	p := project(t)
	p.Departments = append(p.Departments, tracker.Department{Name: "GTM", Jobs: []tracker.Job{{ID: "plan", Every: "week", On: "mon", Brief: "b.md"}}})
	// Marketing: one output reviewed in time; its two jobs never ran.
	day := func(n int) string { return now.AddDate(0, 0, -n).Format(time.RFC3339) }
	for _, e := range []outputs.Event{
		{Op: outputs.OpAdd, ID: "o1", Date: day(4), Department: "marketing", Title: "a", Kind: "post", URL: "https://example.com/a"},
		{Op: outputs.OpReview, ID: "o1", Date: day(3), State: outputs.StateApproved, Quote: "ok"},
	} {
		if err := outputs.Append(p, e); err != nil {
			t.Fatal(err)
		}
	}
	got := rows(Measure(Input{Project: p, Now: now}))
	mk := func(name string) string { return health.RowID(name, map[string]string{"department": "marketing"}) }
	gk := func(name string) string { return health.RowID(name, map[string]string{"department": "gtm"}) }
	if _, ok := got[gk(OnTrack)]; ok {
		t.Errorf("gtm (a job with no run yet) has an on-track row: %v", got[gk(OnTrack)])
	}
	if _, ok := got[gk(JobsOnTime)]; ok {
		t.Error("a job with no run yet counts in jobs on time")
	}
	if got[gk(JobsDue)] != 1 {
		t.Errorf("gtm jobs due = %v, want 1 (still shown)", got[gk(JobsDue)])
	}
	if got[mk(OnTrack)] != 1 || got[OnTrack] != 1 || got[Measured] != 1 || got[Count] != 3 {
		t.Errorf("marketing on track %v, project %v, measured %v, count %v", got[mk(OnTrack)], got[OnTrack], got[Measured], got[Count])
	}

	// Once the job has run, a period due over a day with no run is late.
	ran := []*tracker.Issue{issue(t, "gtm/plan-run", "status: \"done\"\njob: \"gtm/plan\"\nrun: \"2026-W42\"\ndepartment: \"GTM\"")}
	got = rows(Measure(Input{Project: p, Issues: ran, Now: now}))
	if v, ok := got[gk(JobsOnTime)]; !ok || v != 0 {
		t.Errorf("gtm jobs on time after a run = %v (present %v), want 0", v, ok)
	}
}

// With no department measured the family has no project on-track row and
// the derived score leaves the departments component out (AC2).
func TestNoneMeasuredLeavesScoreOut(t *testing.T) {
	p := project(t) // Marketing's jobs never ran, no goal values, no outputs; Sales has nothing.
	ms := Measure(Input{Project: p, Now: now})
	got := rows(ms)
	if _, ok := got[OnTrack]; ok {
		t.Fatalf("project on-track row with nothing measured: %v", got[OnTrack])
	}
	if got[Measured] != 0 || got[Count] != 2 {
		t.Errorf("measured %v, count %v", got[Measured], got[Count])
	}
	base := []health.Row{
		{ID: "lint.warnings", Metric: "lint.warnings", Value: 4},
		{ID: "shape.big_lines.share", Metric: "shape.big_lines.share", Value: 0.1},
	}
	cur := &health.Snapshot{Metrics: base}
	for _, m := range ms {
		cur.Metrics = append(cur.Metrics, health.Row{ID: health.RowID(m.Name, m.Labels), Metric: m.Name, Labels: m.Labels, Value: m.Value})
	}
	score := rows(health.Derive(cur, nil, health.DefaultComponents()))
	for id := range score {
		if strings.Contains(id, "component=departments") {
			t.Errorf("departments component in the score: %s", id)
		}
	}
	plain := rows(health.Derive(&health.Snapshot{Metrics: base}, nil, health.DefaultComponents()))
	if len(score) != len(plain) || score["score.coverage"] != plain["score.coverage"] {
		t.Errorf("score %v with unmeasured departments, %v without", score, plain)
	}
}
