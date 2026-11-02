package bench

import (
	"context"
	"errors"

	"github.com/kacperwerema/dark-factory/internal/health"
)

// The `bench` health family: the newest finished run of every benchmark
// with a task in the project, so Health and Stats draw a run-by-run trend.
// `bench run` takes a snapshot when a run ends. Holdout only as passed and
// total. Names are documented in docs/workbench-files.md, "Health
// snapshots"; keep them stable.
func init() {
	health.Register("bench", benchFamily)
	health.Describe("bench", "runs: issue-cli bench run")
}

func benchFamily(_ context.Context, env health.Env) ([]health.Metric, error) {
	if env.Backfill {
		return nil, health.ErrNoPast
	}
	if env.Project == nil {
		return nil, errors.New("no project")
	}
	var out []health.Metric
	for _, name := range List() {
		b, err := Load(name)
		if err != nil || !hasProject(b, env.Project.Slug) {
			continue
		}
		r := LatestFinished(name)
		if r == nil {
			continue
		}
		out = append(out, Metrics(r)...)
	}
	return out, nil
}

func hasProject(b *Benchmark, slug string) bool {
	for _, t := range b.Tasks {
		if t.Project == slug {
			return true
		}
	}
	return false
}

// LatestFinished is benchmark's newest run that ended, nil without one.
func LatestFinished(benchmark string) *Run {
	runs := Runs(benchmark)
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Finished() {
			return runs[i]
		}
	}
	return nil
}

// Metrics are a run's rows in the bench family.
func Metrics(r *Run) []health.Metric {
	l := map[string]string{"benchmark": r.Benchmark}
	if r.Setup.Label != "" {
		l["setup"] = r.Setup.Label
	}
	s := r.Summary
	out := []health.Metric{
		{Name: "bench.pass_rate", Labels: l, Value: s.PassRate, Unit: health.UnitShare, Help: "Benchmark tasks passed, share of scored tasks (holdout apart)"},
		{Name: "bench.tasks", Labels: l, Value: float64(s.Scored), Unit: health.UnitCount, Help: "Benchmark tasks scored (not broken, holdout apart)"},
		{Name: "bench.holdout.passed", Labels: l, Value: float64(r.Holdout.Passed), Unit: health.UnitCount, Help: "Holdout tasks passed"},
		{Name: "bench.holdout.total", Labels: l, Value: float64(r.Holdout.Total), Unit: health.UnitCount, Help: "Holdout tasks scored"},
		{Name: "bench.wall", Labels: l, Value: s.WallSec, Unit: health.UnitSeconds, Help: "Benchmark run wall time, summed over the tasks"},
		{Name: "bench.cli_refused", Labels: l, Value: float64(s.Refused), Unit: health.UnitCount, Help: "Refused issue-cli calls in the run"},
		{Name: "bench.rework", Labels: l, Value: float64(s.Backward), Unit: health.UnitCount, Help: "Backward transitions in the run"},
	}
	if s.Judge >= 0 {
		out = append(out, health.Metric{Name: "bench.judge", Labels: l, Value: s.Judge, Unit: health.UnitCount, Help: "Mean judge score, 0-5"})
	}
	if s.FirstTryRate >= 0 {
		out = append(out, health.Metric{Name: "bench.gates_first_try", Labels: l, Value: s.FirstTryRate, Unit: health.UnitShare, Help: "Transitions that passed on the first try"})
	}
	if s.TokensKnown {
		out = append(out, health.Metric{Name: "bench.tokens", Labels: l, Value: float64(r.SpentTokens), Unit: health.UnitCount, Help: "Tokens the run spent"})
	}
	out = append(out, health.Metric{Name: "bench.cost", Labels: l, Value: r.SpentUSD, Unit: "usd", Help: "Cost of the run, USD"})
	return out
}
