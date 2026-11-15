package viewer

import (
	"fmt"
	"sort"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/chart"
	"github.com/kacperwerema/dark-factory/internal/health"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Report's Benchmark card (internal/bench): the newest run of every
// benchmark with a task in the project, its pass rate, the holdout as
// passed n/m, cost, and the change against the run before. The viewer never
// starts a run; the card names the command.

// WbBench is the Benchmark card; nil when no benchmark has a task in the
// project (To unlock then offers it).
type WbBench struct {
	Rows []WbBenchRow
}

// WbBenchRow is one benchmark.
type WbBenchRow struct {
	Name  string
	Tasks int
	// NoRun: the benchmark has no run yet; Cmd starts one.
	NoRun bool
	Cmd   string
	RunID string
	When  string
	Label string
	// Pass is "6/8 passed"; Holdout "holdout 2/2" or "".
	Pass, Holdout string
	Cost, Tokens  string
	// Stop is set for a run that did not finish (paused, budget, running).
	Stop string
	// Delta compares with the previous finished run: "+12 points, 1
	// regression"; Tone is green, red or "".
	Delta, Tone string
	// Agents is the newest run on two agents, nil without one.
	Agents *WbBenchAgents
}

// WbBenchAgents is one run by agent and type, and what it says of the
// project's routing: rules. The holdout is not split.
type WbBenchAgents struct {
	RunID   string
	Rows    []bench.AgentRow
	Routing []WbBenchRouting
	// Prices says why a cost is unknown and where to fill it.
	Prices string
}

// WbBenchRouting is one routing line; Tone is red for a rule the numbers
// contradict, green for one they support.
type WbBenchRouting struct {
	Text, Tone string
}

// benchAgents is the card's by-agent block for benchmark name.
func benchAgents(name string, cfg *tracker.RoutingConfig) *WbBenchAgents {
	r := bench.NewestTwoAgents(name)
	if r == nil {
		return nil
	}
	t := bench.ByAgent(r)
	if t == nil {
		return nil
	}
	out := &WbBenchAgents{RunID: r.ID, Rows: t.Rows, Prices: spend.PricesNote(t.Unpriced)}
	for _, c := range bench.CheckRouting(cfg, t) {
		l := WbBenchRouting{Text: c.Line()}
		switch c.Verdict {
		case bench.RoutingContradicted:
			l.Tone = "red"
		case bench.RoutingSupported:
			l.Tone = "green"
		}
		out.Routing = append(out.Routing, l)
	}
	return out
}

// reportBench builds the card from the owner's bench folder.
func reportBench(p *tracker.Project, now time.Time) *WbBench {
	var card WbBench
	var cfg *tracker.RoutingConfig
	if wf := p.LoadWorkflow(); wf != nil {
		cfg = wf.Routing
	}
	for _, name := range bench.List() {
		b, err := bench.Load(name)
		if err != nil {
			continue
		}
		n := 0
		for _, t := range b.Tasks {
			if t.Project == p.Slug {
				n++
			}
		}
		if n == 0 {
			continue
		}
		row := WbBenchRow{Name: name, Tasks: len(b.Tasks), Cmd: "issue-cli bench run " + name}
		runs := bench.Runs(name)
		if len(runs) == 0 {
			row.NoRun = true
			card.Rows = append(card.Rows, row)
			continue
		}
		r := runs[len(runs)-1]
		s := r.Summary
		row.RunID, row.Label = r.ID, r.Setup.Label
		row.Agents = benchAgents(name, cfg)
		row.When = ageText(now.Sub(r.Started)) + " ago"
		row.Pass = fmt.Sprintf("%d/%d passed", s.Passed, s.Scored)
		if r.Holdout.Total > 0 {
			row.Holdout = fmt.Sprintf("holdout %d/%d", r.Holdout.Passed, r.Holdout.Total)
		}
		row.Cost = r.CostText()
		row.Tokens = tokens.Format(r.SpentTokens) + " tokens"
		if !s.TokensKnown {
			row.Tokens += " (some unknown)"
		}
		if !r.Finished() {
			row.Stop = r.Stop
			if row.Stop == "" {
				row.Stop = "running"
			}
		}
		if r.Finished() {
			var prev *bench.Run
			for _, x := range runs[:len(runs)-1] {
				if x.Finished() {
					prev = x
				}
			}
			if prev != nil {
				// The change of the rate the card shows (holdout apart);
				// the verdict is compare's, holdout included.
				c := bench.Compare(prev, r)
				delta := r.Summary.PassRate - prev.Summary.PassRate
				row.Delta = fmt.Sprintf("%+.0f points vs %s", delta*100, prev.ID)
				if n := len(c.Regressions); n > 0 {
					row.Delta += ", " + plural(n, "regression", "regressions")
				}
				switch {
				case !c.Comparable:
					row.Delta += " (not comparable)"
				case c.Regressed():
					row.Tone = "red"
				case delta > 0:
					row.Tone = "green"
				}
			}
		}
		card.Rows = append(card.Rows, row)
	}
	if len(card.Rows) == 0 {
		return nil
	}
	return &card
}

func ageText(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// benchSeries is the bench family's pass rate per benchmark across snaps,
// for the Stats trend; nil when no snapshot has a run.
func benchSeries(snaps []*health.Snapshot) []chart.Series {
	byName := map[string]*chart.Series{}
	var names []string
	for _, s := range snaps {
		for _, r := range s.Rows("bench.pass_rate") {
			name := r.Labels["benchmark"]
			ser := byName[name]
			if ser == nil {
				ser = &chart.Series{Name: name}
				byName[name] = ser
				names = append(names, name)
			}
			note := ""
			if setup := r.Labels["setup"]; setup != "" {
				note = setup
			}
			ser.Points = append(ser.Points, chart.Point{At: s.Time(), Value: r.Value, Note: note})
		}
	}
	sort.Strings(names)
	var out []chart.Series
	for _, n := range names {
		out = append(out, *byName[n])
	}
	return out
}
