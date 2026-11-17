package spend

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/tokens"
)

// Cost is the cost preview of an experiment run, before the owner spends
// on it: Line is the estimate ("≈ 210M tokens, 9.2 h, prices.yaml unset"),
// Detail says what it covers and where the numbers come from.
type Cost struct {
	Line string
	// Runs is the short form of what it covers, for a one-line display:
	// "baseline + candidate" or "candidate only, baseline reused".
	Runs   string
	Detail []string
}

// ExperimentCost is what experiment run would spend on benchmark: the
// baseline and the candidate, or only the candidate when baselineRun (a
// finished run on main's sha) is reused. The numbers are the newest
// finished run's mean per task (bench.ForecastRun), priced with today's
// prices.yaml; nothing is guessed when a run or a price is missing.
func ExperimentCost(benchmark, baselineRun, mainSHA string) Cost {
	b, err := bench.Load(benchmark)
	if errors.Is(err, bench.ErrNoBenchmark) {
		return Cost{Line: "no estimate: benchmark " + benchmark + " is not set up (issue-cli bench)"}
	}
	if err != nil {
		return Cost{Line: "no estimate: " + err.Error()}
	}
	f, ok := bench.ForecastRun(b, 1, LoadPrices())
	if !ok {
		return Cost{Line: "no estimate yet: " + benchmark + " has no finished run to take the mean per task from"}
	}
	runs, short, what := 2, "baseline + candidate", fmt.Sprintf("2 runs × %d tasks: the baseline on main %s and the candidate", f.Tasks, gitdir.ShortSHA(mainSHA))
	if baselineRun != "" {
		runs, short, what = 1, "candidate only, baseline reused", fmt.Sprintf("1 run × %d tasks: the candidate only, baseline run %s on main %s is reused", f.Tasks, baselineRun, gitdir.ShortSHA(mainSHA))
	}
	all := f.Times(runs)
	c := Cost{
		Runs:   short,
		Line:   fmt.Sprintf("≈ %s, %s, %s", tokensText(all), Hours(all.Wall), usdText(all)),
		Detail: []string{what, fmt.Sprintf("from run %s's mean per task (%s, %s tokens)", f.Run, Hours(f.PerTaskWall), tokens.Format(f.Tokens/int64(max(f.Tasks, 1))))},
	}
	if all.NoUsage > 0 {
		c.Detail = append(c.Detail, fmt.Sprintf("run %s read no usage for %d of its %d tasks: their tokens and cost are unknown, not zero", f.Run, f.NoUsage, f.Sampled))
	}
	if len(all.Unpriced) > 0 {
		c.Detail = append(c.Detail, PricesNote(all.Unpriced))
	}
	return c
}

// tokensText is "105M tokens", "≥ 105M tokens" when some tasks' usage
// wasn't read, "tokens unknown" when none was.
func tokensText(f bench.Forecast) string {
	switch {
	case f.NoUsage >= f.Sampled:
		return "tokens unknown"
	case f.NoUsage > 0:
		return "≥ " + tokens.Format(f.Tokens) + " tokens"
	}
	return tokens.Format(f.Tokens) + " tokens"
}

// usdText is the cost: "$12.40", "≥ $12.40" when some tasks have no
// price, "prices.yaml unset" when there is no prices file, "USD unknown"
// when it names none of the models.
func usdText(f bench.Forecast) string {
	switch {
	case f.NoUsage >= f.Sampled:
		return "USD unknown"
	case f.CostKnown():
		return FormatUSD(f.USD)
	case f.Priced > 0:
		return "≥ " + FormatUSD(f.USD)
	}
	if _, err := os.Stat(PricesPath()); err != nil {
		return "prices.yaml unset"
	}
	return "USD unknown"
}

// Hours is a duration for a cost preview: "40 min", "4.6 h".
func Hours(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Round(time.Minute).Minutes()))
	}
	return fmt.Sprintf("%.1f h", d.Hours())
}
