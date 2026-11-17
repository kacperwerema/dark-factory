package health

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/period"
	"github.com/kacperwerema/dark-factory/internal/testresults"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The tests family: unit and e2e test counts and runtimes from the newest
// local CI run that wrote results for each kind (ci.run[].results), plus the
// playtest sessions in the tree and which systems' work they cover. It runs
// no tests. See docs/workbench-files.md, "The tests family".
func init() {
	Register("tests", testsFamily)
	Describe("tests", "slowest tests and uncovered systems: issue-cli health --family tests")
}

// TestRun is the newest local CI run that wrote results of one kind: when it
// finished, its summary and, when it kept the list, every test.
type TestRun struct {
	Finished time.Time
	Summary  testresults.Summary
	Tests    []testresults.Test
}

// TestResults finds the newest n TestRuns of kind at or before now under
// the main checkout root, newest first; only the newest needs its Tests.
// internal/ci sets it from init() (SetTestResults); unset, the tests family
// has no CI rows and the review pack no test list.
type TestResults func(root, kind string, now time.Time, n int) []TestRun

var (
	testResultsMu sync.Mutex
	testResults   TestResults
)

// SetTestResults plugs in where test results come from (internal/ci).
func SetTestResults(f TestResults) {
	testResultsMu.Lock()
	defer testResultsMu.Unlock()
	testResults = f
}

func recentTestRuns(root, kind string, now time.Time, n int) []TestRun {
	testResultsMu.Lock()
	f := testResults
	testResultsMu.Unlock()
	if f == nil {
		return nil
	}
	return f(root, kind, now, n)
}

func latestTestRun(root, kind string, now time.Time) (TestRun, bool) {
	runs := recentTestRuns(root, kind, now, 1)
	if len(runs) == 0 {
		return TestRun{}, false
	}
	return runs[0], true
}

// BestOfRuns is how many recent CI runs the best wall time is taken from:
// the fastest of them is the suite's cost on the quietest machine it saw,
// while a real slowdown lifts every run and shows within BestOfRuns runs.
const BestOfRuns = 5

// E2ERuns is what the ux-test runs started in [from, to] under the main
// checkout root say about flaky steps: how many runs, how many distinct steps
// passed or failed, and the steps that both failed and passed on one build.
// ok is false when there was no run. internal/ci sets it from init()
// (SetE2ERuns, read by internal/playtestruns); unset, the tests family has no
// flaky rows.
type E2ERuns func(root string, from, to time.Time) (runs, steps int, flaky []string, ok bool)

var e2eRuns E2ERuns

// SetE2ERuns plugs in where the ux-test runs are read (internal/ci).
func SetE2ERuns(f E2ERuns) {
	testResultsMu.Lock()
	defer testResultsMu.Unlock()
	e2eRuns = f
}

// FlakyDays is the flaky component's window, in calendar days.
const FlakyDays = 14

// flakyTop is how many flaky steps the family names.
const flakyTop = 10

// flakyMetrics: the share of the steps run in the last FlakyDays days that
// failed and passed on one build. No run in the window is no rows, so the
// score says "no data" instead of a 0.
func flakyMetrics(env Env) []Metric {
	testResultsMu.Lock()
	f := e2eRuns
	testResultsMu.Unlock()
	if f == nil {
		return nil
	}
	runs, steps, flaky, ok := f(env.Root, period.DaysBack(env.Now, FlakyDays), env.Now)
	if !ok || runs == 0 || steps == 0 {
		return nil
	}
	w := map[string]string{"window": "14d"}
	out := []Metric{
		{Name: "tests.e2e.flaky_share", Labels: w, Value: shareOf(len(flaky), steps), Unit: UnitShare, Help: "ux-test steps that failed and passed on one build / steps run, 14 days"},
		{Name: "tests.e2e.flaky_steps", Labels: w, Value: float64(len(flaky)), Unit: UnitCount, Help: "ux-test steps that failed and passed on one build, 14 days"},
		{Name: "tests.e2e.steps_run", Labels: w, Value: float64(steps), Unit: UnitCount, Help: "Distinct ux-test steps that passed or failed, 14 days"},
		{Name: "tests.e2e.runs", Labels: w, Value: float64(runs), Unit: UnitCount, Help: "ux-test runs (main checkout and local CI), 14 days"},
	}
	for i, name := range flaky {
		if i == flakyTop {
			break
		}
		out = append(out, Metric{Name: "tests.e2e.flaky", Labels: map[string]string{"step": name}, Value: 1, Unit: UnitCount, Help: "A flaky step: <session>/<step>"})
	}
	return out
}

// TestKinds are the kinds a results: entry may name, in output order.
var TestKinds = []string{"unit", "e2e"}

const (
	slowestTop   = 10
	coverageDays = 90
)

// slowFrom is when one test counts as slow, per kind.
var slowFrom = map[string]float64{"unit": 1, "e2e": 30}

func testsFamily(ctx context.Context, env Env) ([]Metric, error) {
	var out []Metric
	for _, kind := range TestKinds {
		runs := recentTestRuns(env.Root, kind, env.Now, BestOfRuns)
		if len(runs) == 0 {
			continue
		}
		run := runs[0]
		out = append(out, resultsMetrics(kind, run.Finished, run.Summary, run.Tests, env.Now)...)
		out = append(out, bestWall(kind, runs)...)
	}
	out = append(out, flakyMetrics(env)...)
	e2e, err := sessionMetrics(ctx, env)
	if err != nil {
		return nil, err
	}
	return append(out, e2e...), ctx.Err()
}

func resultsMetrics(kind string, finished time.Time, s testresults.Summary, tests []testresults.Test, now time.Time) []Metric {
	k := map[string]string{"kind": kind}
	with := func(key, v string) map[string]string { return map[string]string{"kind": kind, key: v} }
	out := []Metric{
		{Name: "tests.count", Labels: with("result", "all"), Value: float64(s.Tests), Unit: UnitCount, Help: "Tests in the newest CI run with results (top-level; e2e: steps)"},
		{Name: "tests.count", Labels: with("result", "pass"), Value: float64(s.Passed), Unit: UnitCount},
		{Name: "tests.count", Labels: with("result", "fail"), Value: float64(s.Failed), Unit: UnitCount},
		{Name: "tests.count", Labels: with("result", "skip"), Value: float64(s.Skipped), Unit: UnitCount},
		{Name: "tests.subtests", Labels: k, Value: float64(s.Subtests), Unit: UnitCount, Help: "Subtests (t.Run, test cases)"},
		{Name: "tests.wall_seconds", Labels: k, Value: s.WallS, Unit: UnitSeconds, Help: "The suite's own wall time"},
		{Name: "tests.sum_seconds", Labels: k, Value: s.SumS, Unit: UnitSeconds, Help: "Per-test time summed (more than the wall when tests run in parallel)"},
		{Name: "tests.per_test_ms", Labels: with("stat", "mean"), Value: s.MeanMS, Help: "Per-test runtime, ms"},
		{Name: "tests.per_test_ms", Labels: with("stat", "p50"), Value: s.P50MS},
		{Name: "tests.per_test_ms", Labels: with("stat", "p90"), Value: s.P90MS},
		{Name: "tests.run_age_days", Labels: k, Value: math.Round(now.Sub(finished).Hours()/24*10) / 10, Help: "Age of the CI run the numbers come from, in days"},
	}
	if s.Cores > 0 {
		out = append(out,
			Metric{Name: "tests.load", Labels: k, Value: s.Load, Help: "The machine's 1-minute load average around the run (above tests.cores: times inflated)"},
			Metric{Name: "tests.cores", Labels: k, Value: float64(s.Cores), Unit: UnitCount, Help: "The machine's cores when the run's load was read"})
	}
	for _, p := range s.Parts {
		out = append(out, Metric{Name: "tests.part.seconds", Labels: with("part", p.Name), Value: p.Seconds, Unit: UnitSeconds, Help: "The slowest parts (package, test class, session): their own time"})
	}
	if tests == nil {
		return out
	}
	slow := 0
	areas := map[string]int{}
	for _, t := range tests {
		if t.Seconds > slowFrom[kind] {
			slow++
		}
		areas[t.Area]++
	}
	out = append(out, Metric{Name: "tests.slow", Labels: k, Value: float64(slow), Unit: UnitCount, Help: "Tests over 1 s (unit) or steps over 30 s (e2e)"})
	for _, a := range sortedCounts(areas) {
		out = append(out, Metric{Name: "tests.count_by_area", Labels: with("area", a), Value: float64(areas[a]), Unit: UnitCount, Help: "Tests per area (top folder or namespace), next to codebase.lines{area}"})
	}
	sorted := append([]testresults.Test(nil), tests...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Seconds > sorted[j].Seconds })
	for i, t := range sorted {
		if i == slowestTop || t.Seconds == 0 {
			break
		}
		name := t.Name
		if t.Part != "" && t.Part != "." && kind == "unit" {
			name = t.Part + "." + t.Name
		}
		out = append(out, Metric{Name: "tests.slowest.seconds", Labels: with("test", name), Value: t.Seconds, Unit: UnitSeconds, Help: "The slowest tests" + LoadNote(s)})
	}
	return out
}

// bestWall is the lowest wall time of the recent runs (newest first) and
// how many runs it was taken from: unit_wait scores it, so one run under a
// loaded machine doesn't move the score.
func bestWall(kind string, runs []TestRun) []Metric {
	best := runs[0].Summary.WallS
	for _, r := range runs[1:] {
		if w := r.Summary.WallS; w > 0 && (best <= 0 || w < best) {
			best = w
		}
	}
	return []Metric{
		{Name: "tests.wall_seconds_best", Labels: map[string]string{"kind": kind, "window": bestWindow}, Value: best, Unit: UnitSeconds, Help: "The lowest suite wall time of the last 5 CI runs with results (unit_wait scores it)"},
		{Name: "tests.wall_runs", Labels: map[string]string{"kind": kind}, Value: float64(len(runs)), Unit: UnitCount, Help: "CI runs the best wall time was taken from (at most 5)"},
	}
}

// bestWindow labels the best-wall row.
const bestWindow = "5runs"

// LoadNote is " (run under load 15.2 on 14 cores: times inflated)" when the
// run's load average was above its core count, else "".
func LoadNote(s testresults.Summary) string {
	if !s.Loaded() {
		return ""
	}
	return " (" + loadText(s.Load, s.Cores) + ")"
}

func loadText(load float64, cores int) string {
	return fmt.Sprintf("run under load %.1f on %d cores: times inflated", load, cores)
}

// RunLoad is "run under load 15.2 on 14 cores: times inflated" when the
// snapshot's run of kind had a load above its cores (tests.load,
// tests.cores), else "": the Health view and the review pack put it beside
// the slowest tests.
func (s *Snapshot) RunLoad(kind string) string {
	k := map[string]string{"kind": kind}
	load, ok1 := s.Value("tests.load", k)
	cores, ok2 := s.Value("tests.cores", k)
	if !ok1 || !ok2 || cores <= 0 || load <= cores {
		return ""
	}
	return loadText(load, int(cores))
}

// session is the part of a playtest session file the family reads.
type session struct {
	Session string `yaml:"session"`
	Issue   string `yaml:"issue"`
	Steps   []struct {
		ID string `yaml:"id"`
	} `yaml:"steps"`
}

// sessionMetrics counts the playtest sessions and steps at the commit, per
// system, and the share of recent done work in systems that have one.
func sessionMetrics(ctx context.Context, env Env) ([]Metric, error) {
	globs := env.Config.E2ESessionGlobs()
	var sessions []session
	matched := 0
	_, err := walkBlobs(ctx, env.Root, env.Commit, func(p string) bool { return !matchAny(globs, p) }, func(p string, data []byte) {
		matched++
		var s session
		if yaml.Unmarshal(data, &s) == nil && len(s.Steps) > 0 {
			sessions = append(sessions, s)
		}
	})
	if err == nil && matched == 0 {
		Warn(ctx, "%s matched no file, so no e2e session rows", globNote("e2e_sessions", env.Config != nil && len(env.Config.E2ESessions) > 0, globs))
	}
	if err != nil || len(sessions) == 0 {
		return nil, err
	}
	var issues []*tracker.Issue
	if env.Project != nil && env.Project.IssueDir != "" {
		issues, _ = env.Project.LoadIssues()
	}
	system := map[string]string{}
	for _, is := range issues {
		system[is.Slug] = is.System
	}
	bySys, steps := map[string]int{}, map[string]int{}
	total := 0
	for _, s := range sessions {
		sys := system[s.Issue]
		if sys == "" {
			sys = "none"
		}
		bySys[sys]++
		steps[sys] += len(s.Steps)
		total += len(s.Steps)
	}
	out := []Metric{
		{Name: "tests.e2e.sessions", Labels: map[string]string{"system": "all"}, Value: float64(len(sessions)), Unit: UnitCount, Help: "Playtest sessions in the tree, per system of their issue"},
		{Name: "tests.e2e.steps", Labels: map[string]string{"system": "all"}, Value: float64(total), Unit: UnitCount, Help: "Playtest steps in the tree"},
	}
	for _, sys := range sortedCounts(bySys) {
		l := map[string]string{"system": sys}
		out = append(out,
			Metric{Name: "tests.e2e.sessions", Labels: l, Value: float64(bySys[sys]), Unit: UnitCount},
			Metric{Name: "tests.e2e.steps", Labels: l, Value: float64(steps[sys]), Unit: UnitCount})
	}
	return append(out, coverageMetrics(env, issues, bySys)...), nil
}

// coverageMetrics: of the issues done in the last coverageDays (with a
// system), the share in systems with at least one session, and how many were
// done in each system that has none.
func coverageMetrics(env Env, issues []*tracker.Issue, sessions map[string]int) []Metric {
	now := env.Now
	from := now.AddDate(0, 0, -coverageDays)
	done := map[string]int{}
	total, covered := 0, 0
	for _, is := range issues {
		if !env.Project.IssueDone(is) || is.System == "" {
			continue
		}
		at, ok := doneAt(is.FilePath, env.Workflow)
		if !ok || at.Before(from) || at.After(now) {
			continue
		}
		total++
		done[is.System]++
		if sessions[is.System] > 0 {
			covered++
		}
	}
	if total == 0 {
		return nil
	}
	out := []Metric{{Name: "tests.e2e.coverage", Value: shareOf(covered, total), Unit: UnitShare, Help: "Issues done in 90 days whose system has a playtest session / issues done with a system"}}
	for _, sys := range sortedCounts(done) {
		if sessions[sys] == 0 {
			out = append(out, Metric{Name: "tests.e2e.uncovered", Labels: map[string]string{"system": sys}, Value: float64(done[sys]), Unit: UnitCount, Help: "Issues done in 90 days in a system with no playtest session"})
		}
	}
	return out
}

// doneAt is the last recorded transition to done in the issue's stats.
func doneAt(issuePath string, wf *tracker.WorkflowConfig) (time.Time, bool) {
	st, err := tracker.LoadStats(issuePath)
	if err != nil {
		return time.Time{}, false
	}
	for i := len(st.Transitions) - 1; i >= 0; i-- {
		if wf.IsLastStatus(st.Transitions[i].To) {
			return st.Transitions[i].TS, true
		}
	}
	return time.Time{}, false
}
