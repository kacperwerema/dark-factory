package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/autostart"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Thursday 2026-10-29 12:00, ISO week 44 (Mon 10-26).
var now = time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func mustParse(t *testing.T, j tracker.Job) Schedule {
	t.Helper()
	s, err := Parse(j)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseRefusesBadSchedules(t *testing.T) {
	for _, j := range []tracker.Job{
		{Every: ""}, {Every: "hourly"}, {Every: "day", On: "mon"}, {Every: "week", On: "someday"},
		{Every: "month", On: "31"}, {Every: "month", On: "0"}, {Every: "week", At: "7am"},
	} {
		if _, err := Parse(j); err == nil {
			t.Errorf("Parse(%+v) = nil error", j)
		}
	}
	s := mustParse(t, tracker.Job{Every: "Week", On: "Friday", At: "07:30"})
	if s.Weekday != time.Friday || s.Hour != 7 || s.Minute != 30 {
		t.Errorf("schedule = %+v", s)
	}
}

func TestLatestNextAndKeys(t *testing.T) {
	cases := []struct {
		job        tracker.Job
		key        string
		due, next  string
		labelMatch string
	}{
		// Monday 07:00 of week 44 has passed by Thursday.
		{tracker.Job{Every: "week", On: "mon", At: "07:00"}, "2026-W44", "2026-10-26 07:00", "2026-11-02 07:00", "every Mon 07:00"},
		// Friday 09:00 of week 44 hasn't come: the latest due is week 43's.
		{tracker.Job{Every: "week", On: "fri", At: "09:00"}, "2026-W43", "2026-10-23 09:00", "2026-10-30 09:00", "every Fri 09:00"},
		// Daily at 13:00: today's isn't due until 13:00.
		{tracker.Job{Every: "day", At: "13:00"}, "2026-10-28", "2026-10-28 13:00", "2026-10-29 13:00", "daily 13:00"},
		{tracker.Job{Every: "day"}, "2026-10-29", "2026-10-29 00:00", "2026-10-30 00:00", "daily 00:00"},
		{tracker.Job{Every: "month", On: "1", At: "09:00"}, "2026-10", "2026-10-01 09:00", "2026-11-01 09:00", "monthly on the 1st 09:00"},
		// Last day of October is the 31st, still to come: September's.
		{tracker.Job{Every: "month", On: "last"}, "2026-09", "2026-09-30 00:00", "2026-10-31 00:00", "monthly, last day 00:00"},
	}
	for _, c := range cases {
		s := mustParse(t, c.job)
		key, due := s.Latest(now)
		if key != c.key || !due.Equal(at(c.due)) {
			t.Errorf("%+v: Latest = %s %s, want %s %s", c.job, key, due, c.key, c.due)
		}
		if next := s.Next(now); !next.Equal(at(c.next)) {
			t.Errorf("%+v: Next = %s, want %s", c.job, next, c.next)
		}
		if s.Label() != c.labelMatch {
			t.Errorf("%+v: Label = %q", c.job, s.Label())
		}
	}
}

func TestISOWeekKeyAcrossTheYear(t *testing.T) {
	s := mustParse(t, tracker.Job{Every: "week"})
	// Thursday 2027-01-07: week 1 of 2027 starts Monday 2027-01-04.
	if key, _ := s.Latest(at("2027-01-07 10:00")); key != "2027-W01" {
		t.Errorf("key = %s", key)
	}
	// Friday 2027-01-01 is in ISO week 53 of 2026.
	if key, _ := s.Latest(at("2027-01-01 10:00")); key != "2026-W53" {
		t.Errorf("key = %s", key)
	}
}

func TestMissed(t *testing.T) {
	s := mustParse(t, tracker.Job{Every: "week", On: "mon", At: "07:00"})
	for last, want := range map[string]int{"": 0, "2026-W44": 0, "2026-W43": 0, "2026-W42": 1, "2026-W40": 3} {
		if got := s.Missed(last, now); got != want {
			t.Errorf("Missed(%q) = %d, want %d", last, got, want)
		}
	}
	d := mustParse(t, tracker.Job{Every: "day"})
	if got := d.Missed("2026-10-25", now); got != 3 {
		t.Errorf("daily Missed = %d, want 3", got)
	}
}

// project is a temp project with a Marketing department on the content
// workflow and one weekly job with a brief.
func project(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	content, err := os.ReadFile("../../cmd/issue-cli/templates/workflow/content.yaml")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "workflows/marketing.yaml"), string(content))
	writeFile(t, filepath.Join(root, "departments/marketing/jobs/weekly.md"), "Refresh every client site for {{period}} ({{date}}); last: {{last_run}}.")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{
		Slug: "acme", IssueDir: issues, WorkDir: root,
		Departments: []tracker.Department{{
			Name: "Marketing", Systems: []string{"SEO"}, Workflow: "workflows/marketing.yaml",
			Jobs: []tracker.Job{{ID: "weekly-seo-pass", Name: "Weekly SEO pass", Every: "week", On: "mon", At: "07:00", Brief: "departments/marketing/jobs/weekly.md", Priority: "medium"}},
		}},
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadIssues(t *testing.T, p *tracker.Project) []*tracker.Issue {
	t.Helper()
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

// createRun plans and writes a run for period, as issue-cli job run does.
func createRun(t *testing.T, p *tracker.Project, period string, again bool) *NewRun {
	t.Helper()
	r, err := Find(p, "Marketing/Weekly-SEO-Pass")
	if err != nil {
		t.Fatal(err)
	}
	n, err := PlanRun(p, r, Runs(p, loadIssues(t, p), r.Key), period, again)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := Brief(p, r, n, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteRun(p, r, n, brief, now); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRunIssueAndOnePerPeriod(t *testing.T) {
	p := project(t)
	first := createRun(t, p, "2026-W43", false)
	if first.Status != "brief" || first.System != "SEO" || first.Slug != "weekly-seo-pass-2026-w43" {
		t.Fatalf("run = %+v", first)
	}
	n := createRun(t, p, "2026-W44", false)
	if n.Previous != "seo/weekly-seo-pass-2026-w43" {
		t.Errorf("previous = %q", n.Previous)
	}
	issues := loadIssues(t, p)
	runs := Runs(p, issues, "marketing/weekly-seo-pass")
	if len(runs) != 2 || runs[1].Period != "2026-W44" || !runs[1].Open {
		t.Fatalf("runs = %+v", runs)
	}
	is := runs[1].Issue
	if is.Slug != "seo/weekly-seo-pass-2026-w44" || p.DepartmentOf(is) != "Marketing" || is.Priority != "medium" || is.Title != "Weekly SEO pass · 2026-W44" {
		t.Errorf("issue = %s %q %s %q", is.Slug, p.DepartmentOf(is), is.Priority, is.Title)
	}
	for _, want := range []string{"for 2026-W44 (2026-10-29); last: seo/weekly-seo-pass-2026-w43.", "## Run", "Previous run: seo/weekly-seo-pass-2026-w43."} {
		if !strings.Contains(is.BodyRaw, want) {
			t.Errorf("body misses %q:\n%s", want, is.BodyRaw)
		}
	}

	r, _ := Find(p, "marketing/weekly-seo-pass")
	_, err := PlanRun(p, r, runs, "2026-W44", false)
	var exists *ExistsError
	if err == nil || !asExists(err, &exists) || exists.Slug != "seo/weekly-seo-pass-2026-w44" {
		t.Fatalf("second run in the period: %v", err)
	}
	again := createRun(t, p, "2026-W44", true)
	if again.Slug != "weekly-seo-pass-2026-w44-2" || again.Title != "Weekly SEO pass · 2026-W44 (2)" {
		t.Errorf("again = %+v", again)
	}
}

func asExists(err error, target **ExistsError) bool {
	e, ok := err.(*ExistsError)
	if ok {
		*target = e
	}
	return ok
}

func lowMemory() autostart.Memory { return autostart.Memory{Found: true, Used: 20, CanStart: true} }

func evaluate(t *testing.T, p *tracker.Project, in Input) Status {
	t.Helper()
	if in.Now.IsZero() {
		in.Now = now
	}
	if in.Limits == (Limits{}) {
		in.Limits = DefaultLimits
	}
	all := Evaluate(p, loadIssues(t, p), in)
	if len(all) != 1 {
		t.Fatalf("statuses = %+v", all)
	}
	return all[0]
}

func TestVerdicts(t *testing.T) {
	p := project(t)
	auto := &State{Auto: map[string]Auto{AllJobs: {Words: "for now all", By: "lead-acme"}}}

	// Never ran, no standing start: the owner starts it.
	s := evaluate(t, p, Input{State: &State{}, Memory: lowMemory()})
	if !s.Due || s.Period != "2026-W44" || s.Verdict != VerdictOwner || s.Auto != nil {
		t.Fatalf("no standing start: %+v", s)
	}
	// With every job's standing start and room: start.
	if s := evaluate(t, p, Input{State: auto, Memory: lowMemory()}); s.Verdict != VerdictStart || s.Auto.Words != "for now all" {
		t.Fatalf("standing start: %+v", s)
	}
	holds := map[string]Input{
		"mem-guard is not on PATH":                           {State: auto},
		"mem-guard check refuses: BLOCKED":                   {State: auto, Memory: autostart.Memory{Found: true, Used: 80, Note: "BLOCKED"}},
		"memory 62% ≥ 50%":                                   {State: auto, Memory: autostart.Memory{Found: true, Used: 62, CanStart: true}},
		"2/2 workers live":                                   {State: auto, Memory: lowMemory(), Workers: 2},
		"golf started golf/x 2 min ago (one start per 5 min": {State: auto, Memory: lowMemory(), Last: &autostart.Start{Project: "golf", Slug: "golf/x", At: now.Add(-2 * time.Minute)}},
	}
	for want, in := range holds {
		if s := evaluate(t, p, in); s.Verdict != VerdictHold || !strings.Contains(s.Reason, want) {
			t.Errorf("want hold %q, got %+v", want, s)
		}
	}

	// This week's run exists: not due; next is next Monday.
	createRun(t, p, "2026-W44", false)
	s = evaluate(t, p, Input{State: auto, Memory: lowMemory(), Now: at("2026-10-31 10:00")})
	if s.Due || s.Verdict != VerdictNone || !s.Next.Equal(at("2026-11-02 07:00")) || s.LastPeriod != "2026-W44" || !s.LastOpen {
		t.Fatalf("after the run: %+v", s)
	}
	// Two weeks on, the run still open: held by it, one week missed.
	s = evaluate(t, p, Input{State: auto, Memory: lowMemory(), Now: at("2026-11-10 10:00")})
	if s.Verdict != VerdictHold || s.Period != "2026-W46" || s.Missed != 1 || !strings.Contains(s.Reason, "still in brief") {
		t.Fatalf("open last run: %+v", s)
	}
	// overlap: true lets it start anyway.
	p.Departments[0].Jobs[0].Overlap = true
	if s := evaluate(t, p, Input{State: auto, Memory: lowMemory(), Now: at("2026-11-10 10:00")}); s.Verdict != VerdictStart {
		t.Fatalf("overlap: %+v", s)
	}
}

func TestBrokenJobIsAnError(t *testing.T) {
	p := project(t)
	p.Departments[0].Jobs[0].Every = "hourly"
	if s := evaluate(t, p, Input{State: &State{}}); s.Verdict != VerdictError || !strings.Contains(s.Error, "hourly") {
		t.Fatalf("status = %+v", s)
	}
}

func TestStandingStartOnAndOff(t *testing.T) {
	root := t.TempDir()
	if err := TurnOn(root, "acme", "marketing/weekly-seo-pass", Auto{}); err == nil {
		t.Fatal("TurnOn without words: nil error")
	}
	if err := TurnOn(root, "acme", "Marketing/Weekly-SEO-Pass", Auto{Words: "run it weekly", By: "lead-acme", Since: now}); err != nil {
		t.Fatal(err)
	}
	if err := TurnOn(root, "acme", AllJobs, Auto{Words: "for now all", By: "lead-acme", Since: now}); err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(root, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := st.For("marketing/weekly-seo-pass"); !ok || a.Words != "run it weekly" {
		t.Errorf("own start = %+v %v", a, ok)
	}
	if a, ok := st.For("sales/leads"); !ok || a.Words != "for now all" {
		t.Errorf("every job's start = %+v %v", a, ok)
	}
	if b, err := os.ReadFile(filepath.Join(Dir(root), ".gitignore")); err != nil || !strings.Contains(string(b), "*") {
		t.Errorf(".gitignore: %q %v", b, err)
	}
	if on, err := TurnOff(root, "acme", "sales/leads", now, "x"); err != nil || on {
		t.Errorf("off of a job without its own start: %v %v", on, err)
	}
	if on, err := TurnOff(root, "acme", AllJobs, now, "done by owner"); err != nil || !on {
		t.Fatalf("off all: %v %v", on, err)
	}
	st, _ = LoadState(root, "acme")
	if _, ok := st.For("marketing/weekly-seo-pass"); ok {
		t.Error("still on after off --all")
	}
	b, _ := os.ReadFile(OffLogPath(root, "acme"))
	if n := strings.Count(string(b), "\n"); n != 2 || !strings.Contains(string(b), "done by owner") {
		t.Errorf("off log:\n%s", b)
	}
}

func TestLogNotesOnce(t *testing.T) {
	p := project(t)
	note := func(e Event) {
		t.Helper()
		if err := Note(p, ReadLog(p), e); err != nil {
			t.Fatal(err)
		}
	}
	note(Event{Op: OpDue, Job: "marketing/weekly-seo-pass", Run: "2026-W44", Missed: 2})
	note(Event{Op: OpDue, Job: "marketing/weekly-seo-pass", Run: "2026-W44"})
	note(Event{Op: OpHold, Job: "marketing/weekly-seo-pass", Run: "2026-W44", Reason: "memory 60% ≥ 50%"})
	note(Event{Op: OpHold, Job: "marketing/weekly-seo-pass", Run: "2026-W44", Reason: "memory 60% ≥ 50%"})
	note(Event{Op: OpHold, Job: "marketing/weekly-seo-pass", Run: "2026-W44", Reason: "2/2 workers live"})
	if err := Append(p, Event{Op: OpStart, Job: "marketing/weekly-seo-pass", Run: "2026-W44", How: HowUnattended}); err != nil {
		t.Fatal(err)
	}
	log := ReadLog(p)
	var ops []string
	for _, e := range log {
		ops = append(ops, e.Op)
	}
	if strings.Join(ops, ",") != "due,hold,hold,start" || log[0].Missed != 2 {
		t.Errorf("log = %+v", log)
	}
}

func TestLastRunTokensFromItsStats(t *testing.T) {
	p := project(t)
	n := createRun(t, p, "2026-W44", false)
	actual := 500
	if err := tracker.SaveStats(n.File, tracker.StatsStore{Transitions: []tracker.TransitionStat{
		{From: "brief", To: "draft", StaticTokens: 100, DynamicTokens: 20},
		{From: "draft", To: "owner review", StaticTokens: 9, DynamicTokens: 9, ActualTokens: &actual},
	}}); err != nil {
		t.Fatal(err)
	}
	if s := evaluate(t, p, Input{State: &State{}}); s.LastTokens != 620 {
		t.Fatalf("last tokens = %d", s.LastTokens)
	}
}
