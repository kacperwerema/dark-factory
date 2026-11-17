package bench

import (
	"sort"
	"time"
)

// Forecast is what a run of a benchmark will likely take: the newest
// finished run's mean per task times the tasks to run. Estimate words it
// for bench run; the experiment cost preview scales it by the runs it
// needs (internal/experiment CostPreview).
type Forecast struct {
	// Run is the finished run the means come from.
	Run string
	// Tasks is the tasks it covers (the benchmark's tasks × agents).
	Tasks  int
	Tokens int64
	Wall   time.Duration
	// USD is the cost of the tasks that have a price: the one the run
	// recorded, else today's price of the task's model. Unpriced names the
	// models with neither ("unknown model" for a task whose model wasn't
	// read); USD is then a floor, or nothing when Priced is 0.
	USD      float64
	Priced   int
	Unpriced []string
	// NoUsage counts the source run's tasks whose usage wasn't read, of
	// Sampled: their tokens and cost are unknown, not zero.
	NoUsage, Sampled int
	// PerTask are the source run's means per task.
	PerTaskWall time.Duration
	PerTaskUSD  float64
}

// CostKnown reports whether every task of the source run had a price.
func (f Forecast) CostKnown() bool { return len(f.Unpriced) == 0 && f.NoUsage == 0 }

// Times is f for n runs of the same tasks.
func (f Forecast) Times(n int) Forecast {
	k := float64(n)
	f.Tasks *= n
	f.Tokens = int64(float64(f.Tokens) * k)
	f.Wall = time.Duration(float64(f.Wall) * k)
	f.USD *= k
	return f
}

// ForecastRun is b's forecast for agents agents from its newest finished
// run with tasks; ok is false when it has none. prices fill in a task whose
// run recorded no cost (nil: only the recorded costs count).
func ForecastRun(b *Benchmark, agents int, prices map[string]Price) (f Forecast, ok bool) {
	runs := Runs(b.Name)
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if !r.Finished() || len(r.Tasks) == 0 {
			continue
		}
		var wall, usd float64
		var spent int64
		unpriced := map[string]bool{}
		priced, noUsage := 0, 0
		for _, t := range r.Tasks {
			wall += t.WallSec + t.ScoreWallSec
			spent += t.Usage.Tokens()
			switch u := t.Usage; {
			case u.USDKnown || u.USD > 0: // a record from before usd_known has only USD
				usd += u.USD
				priced++
			case !u.Known:
				noUsage++
			default:
				if p, ok := PriceFor(u.Model, prices); ok {
					usd += p.USD(u.Input, u.Output, u.CacheRead, u.CacheWrite)
					priced++
				} else if u.Model == "" {
					unpriced["unknown model"] = true
				} else {
					unpriced[u.Model] = true
				}
			}
		}
		n := float64(len(r.Tasks))
		tasks := len(b.Tasks) * max(agents, 1)
		k := float64(tasks)
		f = Forecast{
			Run: r.ID, Tasks: tasks,
			Tokens:  int64(float64(spent) / n * k),
			Wall:    time.Duration(wall/n*k) * time.Second,
			USD:     usd / n * k,
			Priced:  priced,
			NoUsage: noUsage, Sampled: len(r.Tasks),
			PerTaskWall: time.Duration(wall/n) * time.Second,
			PerTaskUSD:  usd / n,
		}
		for m := range unpriced {
			f.Unpriced = append(f.Unpriced, m)
		}
		sort.Strings(f.Unpriced)
		return f, true
	}
	return Forecast{}, false
}
