package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// salesGoalsProject is goalsProject plus a Sales department whose
// weekly-funnel job (Mondays 08:30) writes the metrics file its goals read.
func salesGoalsProject(t *testing.T) *tracker.Project {
	t.Helper()
	p := goalsProject(t)
	src := "file:" + goals.JobFileSource("Sales", "weekly-funnel")
	p.Departments = append(p.Departments, tracker.Department{
		Name: "Sales",
		Goals: []tracker.Goal{
			{ID: "visits", Name: "Visits", Target: goalNum(500), Period: "week", Source: src + "#visits"},
			{ID: "signups", Name: "Sign-ups", Target: goalNum(25), Period: "week", Source: src + "#signups"},
		},
		Jobs: []tracker.Job{{ID: "weekly-funnel", Every: "week", On: "mon", At: "08:30"}},
	})
	return p
}

func TestGoalWriteWritesTheJobFile(t *testing.T) {
	p := salesGoalsProject(t)
	ctx, out := goalsCtx(t, p, false)
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "visits=412", "signups=31", "bounce=0.4"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.IssueDir, ".goals", "sales", "weekly-funnel.json")
	for _, want := range []string{
		"✓ sales/weekly-funnel: 3 metric(s) for 2026-10-29",
		"visits = 412 · read by sales/visits",
		"bounce = 0.4 · no goal reads it",
		`⚠ no goal reads bounce: a goal reads one with source: "file:<issues>/.goals/sales/weekly-funnel.json#<id>"`,
		"file: " + path,
	} {
		assertContains(t, out.String(), want)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Date    string `json:"date"`
		Job     string `json:"job"`
		Metrics []struct {
			ID    string  `json:"id"`
			Value float64 `json:"value"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &f); err != nil || f.Date != "2026-10-29" || f.Job != "sales/weekly-funnel" || len(f.Metrics) != 3 {
		t.Fatalf("file = %s (%v)", raw, err)
	}

	// The goals read it: issue-cli goals sales shows the value.
	ctx, out = goalsCtx(t, p, false)
	if err := runGoals(ctx, []string{"sales"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Visits [sales/visits]  412 / 500 · 82% · per week · 2026-10-29 · file:<issues>/.goals/sales/weekly-funnel.json#visits")

	// goal set on it points at goal write.
	ctx, _ = goalsCtx(t, p, false)
	err = runGoal(ctx, []string{"set", "sales/visits", "3"})
	if err == nil || !strings.Contains(err.Error(), "its job writes it (issue-cli goal write sales/weekly-funnel <id>=<value>...)") {
		t.Errorf("goal set on a job-file goal: %v", err)
	}
}

func TestGoalWriteRefuses(t *testing.T) {
	p := salesGoalsProject(t)
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"unknown job": {[]string{"write", "sales/daily", "visits=1"}, `no job "sales/daily"`},
		"not number":  {[]string{"write", "sales/weekly-funnel", "visits=about 400"}, "visits=about 400: the value is not a number"},
		"no equals":   {[]string{"write", "sales/weekly-funnel", "visits"}, `"visits": want <id>=<value>`},
		"bad date":    {[]string{"write", "sales/weekly-funnel", "visits=1", "--date", "monday"}, `--date "monday"`},
		"no metrics":  {[]string{"write", "sales/weekly-funnel"}, "usage: issue-cli goal write"},
	} {
		ctx, _ := goalsCtx(t, p, false)
		err := runGoal(ctx, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", name, err, c.want)
		}
	}
	if _, err := os.Stat(filepath.Join(p.IssueDir, ".goals", "sales")); !os.IsNotExist(err) {
		t.Errorf("a refused write left a file: %v", err)
	}
}

// A job file a whole run older than the job's schedule shows "no value
// since <date>" in issue-cli goals, with the job named.
func TestGoalsShowsNoValueSince(t *testing.T) {
	p := salesGoalsProject(t)
	ctx, _ := goalsCtx(t, p, false)
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "visits=300", "--date", "2026-10-12"}); err != nil {
		t.Fatal(err)
	}
	ctx, out := goalsCtx(t, p, false)
	if err := runGoals(ctx, []string{"sales"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Visits [sales/visits]  no value since 2026-10-12 · per week · 2026-10-12")
	assertContains(t, out.String(), "! job sales/weekly-funnel missed a run since 2026-10-12 (issue-cli jobs shows when it last ran)")
	ctx, out = goalsCtx(t, p, true)
	if err := runGoals(ctx, []string{"sales"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), `"no_value_since": "2026-10-12"`)
}

// goalJobFile is a job's metrics file as goal write leaves it.
type goalJobFile struct {
	Date    string `json:"date"`
	Written string `json:"written"`
	Metrics []struct {
		ID    string  `json:"id"`
		Value float64 `json:"value"`
		Date  string  `json:"date"`
	} `json:"metrics"`
}

func readGoalJobFile(t *testing.T, p *tracker.Project) (goalJobFile, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(p.IssueDir, ".goals", "sales", "weekly-funnel.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f goalJobFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return f, string(raw)
}

// --merge sets the named metrics and keeps the others, each with its own
// date: a kept metric from a missed run still shows "no value since".
func TestGoalWriteMergeKeepsOtherMetrics(t *testing.T) {
	p := salesGoalsProject(t)
	ctx, _ := goalsCtx(t, p, false)
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "visits=1", "--date", "2026-10-12"}); err != nil {
		t.Fatal(err)
	}
	ctx, out := goalsCtx(t, p, false)
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "--merge", "signups=2"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ sales/weekly-funnel: 1 metric(s) for 2026-10-29")
	assertContains(t, out.String(), "kept: visits (2026-10-12)")
	f, raw := readGoalJobFile(t, p)
	got := map[string]string{}
	for _, m := range f.Metrics {
		got[m.ID] = strconv.FormatFloat(m.Value, 'f', -1, 64) + "@" + m.Date
	}
	if len(got) != 2 || got["visits"] != "1@2026-10-12" || got["signups"] != "2@2026-10-29" {
		t.Fatalf("after --merge: %s", raw)
	}

	// The goals date each value by its own metric.
	ctx, out = goalsCtx(t, p, false)
	if err := runGoals(ctx, []string{"sales"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Visits [sales/visits]  no value since 2026-10-12")
	assertContains(t, out.String(), "Sign-ups [sales/signups]  2 / 25")

	// Merging again replaces the named one and keeps the rest.
	ctx, _ = goalsCtx(t, p, false)
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "--merge", "visits=7"}); err != nil {
		t.Fatal(err)
	}
	if f, raw = readGoalJobFile(t, p); len(f.Metrics) != 2 {
		t.Fatalf("second merge: %s", raw)
	}

	// A file --merge can't read is refused and left as it is.
	path := filepath.Join(p.IssueDir, ".goals", "sales", "weekly-funnel.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _ = goalsCtx(t, p, false)
	err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "--merge", "visits=3"})
	if err == nil || !strings.Contains(err.Error(), "rerun without --merge to replace it") {
		t.Fatalf("merge into a broken file: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{not json" {
		t.Errorf("a refused merge changed the file: %s", b)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
}

// Without --merge the file is replaced whole, and the output names the
// ids the old file had that this write dropped.
func TestGoalWriteWithoutMergeReplacesWhole(t *testing.T) {
	p := salesGoalsProject(t)
	ctx, _ := goalsCtx(t, p, false)
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "visits=1", "signups=2"}); err != nil {
		t.Fatal(err)
	}
	ctx, out := goalsCtx(t, p, false)
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "visits=5"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "dropped: signups (the file is replaced whole; goal write --merge keeps the metrics it doesn't name)")
	if f, raw := readGoalJobFile(t, p); len(f.Metrics) != 1 || f.Metrics[0].ID != "visits" || f.Metrics[0].Value != 5 {
		t.Fatalf("whole write: %s", raw)
	}
	ctx, out = goalsCtx(t, p, false)
	if err := runGoals(ctx, []string{"sales"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), `no metric "signups"`)
	// A whole write keeps nothing, so it replaces a file it can't read.
	path := filepath.Join(p.IssueDir, ".goals", "sales", "weekly-funnel.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, _ = goalsCtx(t, p, false)
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "visits=6"}); err != nil {
		t.Fatalf("whole write over a broken file: %v", err)
	}
}

// Every write stamps the day it ran ("written"), not --date, so a gate
// can tell a file this run wrote from last week's.
func TestGoalWriteRecordsRunDate(t *testing.T) {
	p := salesGoalsProject(t)
	ctx, out := goalsCtx(t, p, true)
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "visits=1", "--date", "2026-10-12"}); err != nil {
		t.Fatal(err)
	}
	if f, raw := readGoalJobFile(t, p); f.Written != "2026-10-29" || f.Date != "2026-10-12" {
		t.Fatalf("file: %s", raw)
	}
	var res struct {
		Date    string `json:"date"`
		Written string `json:"written"`
		Merged  bool   `json:"merged"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || res.Written != "2026-10-29" || res.Date != "2026-10-12" || res.Merged {
		t.Fatalf("json = %s (%v)", out, err)
	}
	ctx, _ = goalsCtx(t, p, false)
	ctx.Now = func() time.Time { return time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC) }
	if err := runGoal(ctx, []string{"write", "sales/weekly-funnel", "--merge", "signups=3"}); err != nil {
		t.Fatal(err)
	}
	if f, raw := readGoalJobFile(t, p); f.Written != "2026-11-02" {
		t.Fatalf("a merge keeps the old stamp: %s", raw)
	}
}
