package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/autostart"
	"github.com/michal-franc/issue-viewer/internal/jobs"
	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const jobWords = "1. It can be a lead per department. 2. Inside company/project secrets for now 3. pilot projects on Orchard 4. for now all."

// jobNow is Thursday 2026-10-29 12:00: week 44's Monday 07:00 run is due.
var jobNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)

// makeJobProject is a project with a Marketing department on the content
// workflow and a weekly job, run as the CEO with no live sessions,
// mem-guard at 31% and the start budget in a temp dir.
func makeJobProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj := makeAutostartProject(t)
	root := proj.WorkDir
	content, err := os.ReadFile("templates/workflow/content.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"workflows/marketing.yaml":        string(content),
		"departments/marketing/weekly.md": "Refresh the client pages for {{period}}.",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj.Departments = []tracker.Department{{
		Name: "Marketing", Systems: []string{"SEO"}, Workflow: "workflows/marketing.yaml",
		Jobs: []tracker.Job{{ID: "weekly-seo-pass", Name: "Weekly SEO pass", Every: "week", On: "mon", At: "07:00", Brief: "departments/marketing/weekly.md"}},
	}}
	return proj
}

func runJobCmd(t *testing.T, proj *tracker.Project, args ...string) (string, error) {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return jobNow }
	err := runJob(ctx, args)
	return stdout.String(), err
}

func TestJob_DueWaitsForTheOwnerWithoutAStandingStart(t *testing.T) {
	proj := makeJobProject(t)
	started := stubStart(t)
	out, err := runJobCmd(t, proj, "due")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"marketing/weekly-seo-pass · 2026-W44 (due Mon 2026-10-26 07:00): waits for the owner",
		"in Needs you:",
		`issue-cli --project proj job run marketing/weekly-seo-pass --quote "<owner's words>"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("due misses %q:\n%s", want, out)
		}
	}
	_, err = runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--scheduled")
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "has no standing start") || !strings.Contains(err.Error(), "job auto on marketing/weekly-seo-pass") || len(*started) != 0 {
		t.Fatalf("scheduled without a standing start: %v, started %v", err, *started)
	}
	if log := jobs.ReadLog(proj); len(log) != 1 || log[0].Op != jobs.OpDue || log[0].Run != "2026-W44" {
		t.Fatalf("log = %+v", log)
	}
}

func TestJob_RunOnTheOwnersWordsCreatesOneRunPerPeriod(t *testing.T) {
	proj := makeJobProject(t)
	started := stubStart(t)
	out, err := runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--quote", "run the SEO pass now")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(*started) != 1 || (*started)[0] != "seo/weekly-seo-pass-2026-w44" {
		t.Fatalf("started = %v\n%s", *started, out)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	run := tracker.FindIssue(issues, "seo/weekly-seo-pass-2026-w44")
	if run == nil || run.Status != "brief" || proj.DepartmentOf(run) != "Marketing" || run.Field("job") != "marketing/weekly-seo-pass" || run.Field("run") != "2026-W44" {
		t.Fatalf("run issue = %+v", run)
	}
	comments, _ := tracker.LoadComments(run.FilePath)
	if len(comments) != 1 || !strings.Contains(comments[0].Text, `dispatch: "run the SEO pass now" (CEO chat; recurring job marketing/weekly-seo-pass, run 2026-W44, 2026-10-29)`) {
		t.Fatalf("comments = %+v", comments)
	}
	if log := jobs.ReadLog(proj); len(log) != 1 || log[0].Op != jobs.OpStart || log[0].How != jobs.HowOwner || log[0].Issue != run.Slug {
		t.Fatalf("log = %+v", log)
	}

	_, err = runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--quote", "again")
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "already there: seo/weekly-seo-pass-2026-w44") || len(*started) != 1 {
		t.Fatalf("second run: %v", err)
	}
	if _, err := runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--quote", "one more", "--again"); err != nil || len(*started) != 2 || (*started)[1] != "seo/weekly-seo-pass-2026-w44-2" {
		t.Fatalf("--again: %v %v", err, *started)
	}
	// Run there: nothing is due any more.
	out, _ = runJobCmd(t, proj, "due")
	if !strings.HasPrefix(out, "No job due (1 in your scope). Next: marketing/weekly-seo-pass Mon 07:00 (in 3d).") {
		t.Fatalf("due after the run = %q", out)
	}
}

func TestJob_StandingStartRunsUnattendedWithinTheBudget(t *testing.T) {
	proj := makeJobProject(t)
	started := stubStart(t)
	if _, err := runJobCmd(t, proj, "auto", "on", "--all"); exitOf(err) != 2 {
		t.Fatalf("on without --quote: %v", err)
	}
	if out, err := runJobCmd(t, proj, "auto", "on", "--all", "--quote", jobWords); err != nil || !strings.Contains(out, "Standing start on for every job") {
		t.Fatalf("auto on: %v\n%s", err, out)
	}
	st, _ := jobs.LoadState(yoloRoot(proj), "proj")
	if a, ok := st.For("marketing/weekly-seo-pass"); !ok || a.Words != jobWords || a.By != "lead-proj" {
		t.Fatalf("state = %+v", st)
	}

	// Memory too high: held, logged, nothing started.
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 62, CanStart: true} }
	out, _ := runJobCmd(t, proj, "due")
	if !strings.Contains(out, "hold: memory 62% ≥ 50%") {
		t.Fatalf("due = %s", out)
	}
	_, err := runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--scheduled")
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "hold: memory 62% ≥ 50%") || len(*started) != 0 {
		t.Fatalf("scheduled at 62%%: %v", err)
	}

	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 31, CanStart: true} }
	out, _ = runJobCmd(t, proj, "due")
	if !strings.Contains(out, "start it now") || !strings.Contains(out, "issue-cli --project proj job run marketing/weekly-seo-pass --scheduled") {
		t.Fatalf("due = %s", out)
	}
	if out, err := runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--scheduled"); err != nil || len(*started) != 1 {
		t.Fatalf("scheduled: %v %v\n%s", err, *started, out)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	run := tracker.FindIssue(issues, "seo/weekly-seo-pass-2026-w44")
	comments, _ := tracker.LoadComments(run.FilePath)
	if len(comments) != 1 || !strings.Contains(comments[0].Text, `dispatch: "`+jobWords+`" (recurring job marketing/weekly-seo-pass, run 2026-W44, unattended, turned on by lead-proj in the lead chat 2026-10-29; memory 31%, 2026-10-29)`) {
		t.Fatalf("comment = %+v", comments)
	}
	if last, _ := autostart.LastStart(autostart.SharedDir()); last == nil || last.Slug != run.Slug {
		t.Fatalf("budget's last start = %+v", last)
	}
	var ops []string
	for _, e := range jobs.ReadLog(proj) {
		ops = append(ops, e.Op+":"+e.How)
	}
	if strings.Join(ops, ",") != "due:,hold:,start:unattended" {
		t.Fatalf("log = %v", ops)
	}

	if out, err := runJobCmd(t, proj, "auto", "off", "--all", "--reason", "owner said stop"); err != nil || !strings.Contains(out, "owner said stop by lead-proj") {
		t.Fatalf("auto off: %v %s", err, out)
	}
}

func TestJob_ScheduledHoldsInsideTheMachineGap(t *testing.T) {
	proj := makeJobProject(t)
	started := stubStart(t)
	if _, err := runJobCmd(t, proj, "auto", "on", "marketing/weekly-seo-pass", "--quote", jobWords); err != nil {
		t.Fatal(err)
	}
	// Another project auto-started a worker a minute before jobNow.
	if err := autostart.WithBudget(autostart.SharedDir(), func(*autostart.Start) (*autostart.Start, error) {
		return &autostart.Start{Project: "golf", Slug: "golf/x", At: jobNow.Add(-time.Minute)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--scheduled")
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "golf started golf/x 1 min ago") || len(*started) != 0 {
		t.Fatalf("scheduled in the gap: %v", err)
	}
}

func TestJob_WorkersAndOtherDepartmentsAreRefused(t *testing.T) {
	proj := makeJobProject(t)
	stubStart(t)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "high")
	for _, args := range [][]string{
		{"auto", "on", "--all", "--quote", "x"}, {"auto", "off", "--all"}, {"due"},
		{"run", "marketing/weekly-seo-pass", "--quote", "x"},
	} {
		if _, err := runJobCmd(t, proj, args...); err == nil || !strings.Contains(err.Error(), "workers") {
			t.Errorf("worker %v: %v", args, err)
		}
	}
	proj.Departments = append(proj.Departments, tracker.Department{Name: "Sales"})
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:sales")
	if _, err := runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--quote", "x"); err == nil || !strings.Contains(err.Error(), "only its own department's jobs") {
		t.Errorf("sales lead: %v", err)
	}
	// The Sales lead's job due shows none of Marketing's.
	out, _ := runJobCmd(t, proj, "due")
	if !strings.HasPrefix(out, "No job due (0 in your scope).") {
		t.Errorf("sales lead due = %q", out)
	}
}

func TestJob_CEOLeavesJobsToALiveDepartmentLead(t *testing.T) {
	proj := makeJobProject(t)
	agentrun.HasSession = func(name string) bool { return name == "lead-proj-marketing" }
	orig := sessionLive
	sessionLive = func(*tracker.Project) func(string) bool {
		return func(name string) bool { return name == "lead-proj-marketing" }
	}
	t.Cleanup(func() { sessionLive = orig })
	out, _ := runJobCmd(t, proj, "due")
	if !strings.Contains(out, "No job due (0 in your scope).") || !strings.Contains(out, "(1 more job belongs to a live department lead: lead-proj-marketing)") {
		t.Errorf("CEO due = %q", out)
	}
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:marketing")
	if out, _ := runJobCmd(t, proj, "due"); !strings.Contains(out, "waits for the owner") {
		t.Errorf("marketing lead due = %q", out)
	}
}

func TestJobs_ListAndJSON(t *testing.T) {
	proj := makeJobProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return jobNow }
	if err := runJobs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Marketing:\n  marketing/weekly-seo-pass — Weekly SEO pass\n",
		"schedule: every Mon 07:00 · next Mon 07:00 (in 3d)",
		"last run: none yet",
		"due: 2026-W44 — waits for the owner",
		"standing start: off — the owner turns it on: issue-cli --project proj job auto on marketing/weekly-seo-pass",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("jobs misses %q:\n%s", want, stdout.String())
		}
	}
	ctx, stdout, _ = newTestContext(proj, true)
	ctx.Now = func() time.Time { return jobNow }
	if err := runJobs(ctx, []string{"marketing"}); err != nil {
		t.Fatal(err)
	}
	var got jobsListJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || len(got.Jobs) != 1 || got.Jobs[0].Verdict != jobs.VerdictOwner || got.Jobs[0].Period != "2026-W44" {
		t.Fatalf("json = %s (%v)", stdout.String(), err)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runJobs(ctx, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "departments: marketing") {
		t.Errorf("unknown department: %v", err)
	}

	proj.Departments[0].Jobs = nil
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runJobs(ctx, nil); err != nil || !strings.Contains(stdout.String(), "No recurring jobs. Add them to a department") {
		t.Errorf("no jobs: %v %s", err, stdout.String())
	}
}

func TestJob_DryRunCreatesNothing(t *testing.T) {
	proj := makeJobProject(t)
	started := stubStart(t)
	out, err := runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--quote", "try it", "--dry-run")
	if err != nil || !strings.Contains(out, `Would create seo/weekly-seo-pass-2026-w44 ("Weekly SEO pass · 2026-W44") at brief in Marketing`) || len(*started) != 0 {
		t.Fatalf("dry run: %v %v\n%s", err, *started, out)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	if tracker.FindIssue(issues, "seo/weekly-seo-pass-2026-w44") != nil || len(jobs.ReadLog(proj)) != 0 {
		t.Fatal("dry run wrote something")
	}
}

func TestLeadWhoami_PrintsTheJobsLine(t *testing.T) {
	proj := makeJobProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return jobNow }
	if err := runLeadWhoami(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Jobs: 1 jobs, 1 due (1 waits for the owner). At each check-in: issue-cli --project proj job due") {
		t.Fatalf("whoami = %s", stdout.String())
	}
}

func TestDepartmentShow_ListsJobs(t *testing.T) {
	proj := makeJobProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return jobNow }
	if err := runDepartment(ctx, []string{"show", "marketing"}); err != nil {
		t.Fatal(err)
	}
	want := "Jobs:\n  marketing/weekly-seo-pass — every Mon 07:00 · next Mon 07:00 (in 3d) · no run yet · standing start off\n  issue-cli --project proj jobs marketing\n"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("department show =\n%s\nwant\n%s", stdout.String(), want)
	}
	proj.Departments[0].Jobs = nil
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runDepartment(ctx, []string{"show", "marketing"}); err != nil || !strings.Contains(stdout.String(), "Jobs: none (add jobs: to Marketing in projects.yaml; issue-cli help jobs)") {
		t.Fatalf("no jobs: %v\n%s", err, stdout.String())
	}
}

func TestJobs_ShowsTheLastRunsOutputs(t *testing.T) {
	proj := makeJobProject(t)
	stubStart(t)
	if _, err := runJobCmd(t, proj, "run", "marketing/weekly-seo-pass", "--quote", "run it"); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Acme FAQ", "Bistro hours"} {
		if _, err := outputs.Add(proj, outputs.AddInput{Department: "marketing", Title: title, Kind: "page", URL: "https://example.com/" + title, Issue: "seo/weekly-seo-pass-2026-w44"}, jobNow); err != nil {
			t.Fatal(err)
		}
	}
	ctx, stdout, _ := newTestContext(proj, false)
	ctx.Now = func() time.Time { return jobNow }
	if err := runJobs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if want := "last run: seo/weekly-seo-pass-2026-w44 (2026-W44) brief · 2 outputs (2 to review) · 1 runs"; !strings.Contains(stdout.String(), want) {
		t.Fatalf("jobs misses %q:\n%s", want, stdout.String())
	}
}
