package lint

import (
	"context"
	"fmt"

	"github.com/kacperwerema/dark-factory/internal/health"
)

// The lint health family: issue-cli health lint's warnings over
// workflow.yaml and the board, in total (the lint score component) and per
// check. It reads the present files: backfill leaves it out. Lint registers
// it itself, so health never imports lint. See docs/workbench-files.md,
// "The lint family".
func init() {
	health.Register("lint", lintFamily)
	health.Describe("lint", "the warnings and their fixes: issue-cli health lint")
}

func lintFamily(_ context.Context, env health.Env) ([]health.Metric, error) {
	if env.Backfill {
		return nil, health.ErrNoPast
	}
	if env.Project == nil {
		return nil, fmt.Errorf("no project to lint")
	}
	le, err := Load(env.Project, env.Now)
	if err != nil {
		return nil, err
	}
	rep, err := Run(le)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, f := range rep.Findings {
		counts[f.Check]++
	}
	out := []health.Metric{{Name: "lint.warnings", Value: float64(rep.Warnings), Unit: health.UnitCount, Help: "issue-cli health lint warnings over workflow.yaml and the board (the lint score component)"}}
	for _, c := range rep.Checks {
		if n := counts[c]; n > 0 {
			out = append(out, health.Metric{Name: "lint.findings", Labels: map[string]string{"check": c}, Value: float64(n), Unit: health.UnitCount, Help: "issue-cli health lint warnings by check"})
		}
	}
	return out, nil
}
