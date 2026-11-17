package viewer

import (
	"github.com/kacperwerema/dark-factory/internal/experiment"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// stationExperimentCost is the Fields card's "Cost preview" row on an
// issue labelled experiment that hasn't run yet: what experiment run would
// spend (tokens, hours, USD or prices.yaml unset) and whether the baseline
// on main is reused, the line experiment run --dry-run prints. Nil on any
// other issue, or once the block has a verdict.
func stationExperimentCost(p *tracker.Project, issue *tracker.Issue) *WbFieldRow {
	if !experiment.Is(issue.Labels) {
		return nil
	}
	b, err := experiment.Load(issue.FilePath)
	if err != nil || b == nil || b.Verdict != "" {
		return nil
	}
	benchDir := experiment.BenchDir()
	name, err := experiment.PickBenchmark("", b, benchDir)
	if err != nil {
		return &WbFieldRow{Label: "Cost preview", Value: "no estimate: " + err.Error()}
	}
	mainSHA, err := experiment.SHA(projectRoot(p), "main")
	if err != nil {
		return &WbFieldRow{Label: "Cost preview", Value: "no estimate: the project has no main branch to measure"}
	}
	var reused string
	if r := experiment.Baseline(benchDir, name, mainSHA); r != nil {
		reused = r.ID
	}
	c := spend.ExperimentCost(name, reused, mainSHA)
	v := c.Line
	if c.Runs != "" {
		v += " · " + c.Runs
	}
	return &WbFieldRow{Label: "Cost preview", Value: v, Detail: c.Detail}
}
