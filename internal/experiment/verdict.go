package experiment

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Run is the part of a benchmark run's run.json the verdict reads
// (factory/factory-benchmark-and-evals: internal/bench Run, whose fields are
// added, never renamed). Holdout tasks are not in Tasks; only their n/m is.
type Run struct {
	ID        string    `json:"id"`
	Benchmark string    `json:"benchmark"`
	Started   time.Time `json:"started"`
	Ended     time.Time `json:"ended"`
	Stop      string    `json:"stop"`
	Setup     struct {
		Ref             string `json:"ref"`
		SHA             string `json:"sha"`
		IssueCLIVersion string `json:"issue_cli_version"`
		Label           string `json:"label"`
	} `json:"setup"`
	Tasks []struct {
		Task    string `json:"task"`
		Outcome string `json:"outcome"`
		Usage   struct {
			Model       string `json:"model"`
			ToolVersion string `json:"claude_code_version"`
		} `json:"usage"`
	} `json:"tasks"`
	Holdout struct {
		Passed int `json:"passed"`
		Total  int `json:"total"`
	} `json:"holdout"`
	Summary struct {
		Scored       int     `json:"scored"`
		Passed       int     `json:"passed"`
		Judge        float64 `json:"judge"`
		TokensKnown  bool    `json:"tokens_known"`
		WallSec      float64 `json:"wall_sec"`
		Backward     int     `json:"rework_backward"`
		FirstTryRate float64 `json:"gates_first_try_rate"`
	} `json:"summary"`
	SpentTokens int64   `json:"spent_tokens"`
	SpentUSD    float64 `json:"spent_usd"`
}

// Outcomes and stops of a run, as internal/bench writes them.
const (
	outcomePass   = "pass"
	outcomeBroken = "broken"
	stopDone      = "done"
)

// RunsDir is benchmark's runs folder under the owner's bench dir.
func RunsDir(benchDir, benchmark string) string { return filepath.Join(benchDir, "runs", benchmark) }

// LoadRun reads run.json of benchmark's run id.
func LoadRun(benchDir, benchmark, id string) (*Run, error) {
	data, err := os.ReadFile(filepath.Join(RunsDir(benchDir, benchmark), id, "run.json"))
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("run %s/%s: %w", benchmark, id, err)
	}
	if r.ID == "" {
		r.ID = id
	}
	return &r, nil
}

// Runs lists benchmark's runs, oldest first.
func Runs(benchDir, benchmark string) []*Run {
	entries, _ := os.ReadDir(RunsDir(benchDir, benchmark))
	var out []*Run
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if r, err := LoadRun(benchDir, benchmark, e.Name()); err == nil {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// Measured reports whether r measured setup sha: its setup's sha, or its ref
// when that is the sha itself.
func (r *Run) Measured(sha string) bool {
	if sha == "" {
		return false
	}
	return r.Setup.SHA == sha || r.Setup.Ref == sha
}

// Finished reports a run that ran to its end.
func (r *Run) Finished() bool { return r.Stop == stopDone }

// Baseline is the newest finished run of benchmark that measured sha, the
// one an experiment reuses instead of running main again; nil when none.
func Baseline(benchDir, benchmark, sha string) *Run {
	runs := Runs(benchDir, benchmark)
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Finished() && runs[i].Measured(sha) {
			return runs[i]
		}
	}
	return nil
}

// Labelled is the newest run of benchmark with setup label; nil when none.
func Labelled(benchDir, benchmark, label string) *Run {
	runs := Runs(benchDir, benchmark)
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Setup.Label == label {
			return runs[i]
		}
	}
	return nil
}

// passRate is the pass rate over every scored task, holdout included, and
// the number of tasks it is over.
func (r *Run) passRate() (float64, int) {
	n := r.Summary.Scored + r.Holdout.Total
	if n == 0 {
		return 0, 0
	}
	return float64(r.Summary.Passed+r.Holdout.Passed) / float64(n), n
}

// metric is r's value of the named metric; ok is false when the run has no
// value for it (no judge, no gates, tokens unknown).
func (r *Run) metric(name string) (float64, bool) {
	switch name {
	case "pass_rate":
		v, n := r.passRate()
		return v, n > 0
	case "gates_first_try":
		return r.Summary.FirstTryRate, r.Summary.FirstTryRate >= 0
	case "rework":
		return float64(r.Summary.Backward), true
	case "judge":
		return r.Summary.Judge, r.Summary.Judge >= 0
	case "tokens":
		return float64(r.SpentTokens), r.Summary.TokensKnown
	case "cost":
		return r.SpentUSD, true
	case "wall":
		return r.Summary.WallSec, true
	}
	return 0, false
}

func holdoutText(r *Run) string {
	if r.Holdout.Total == 0 {
		return ""
	}
	return fmt.Sprintf("passed %d/%d", r.Holdout.Passed, r.Holdout.Total)
}

// Judge gives the verdict on candidate b against baseline a for target t,
// with the reasons and the numbers. The rule (D-4):
//   - broken when the runs can't be compared: either did not finish, other
//     benchmarks or task sets, another holdout size, another model or
//     Claude Code version, or the target metric has no value;
//   - rejected when a task that passed in a fails in b, the pass rate drops,
//     or the target misses its threshold;
//   - promising otherwise.
//
// A pass-rate move smaller than one task (1/n) counts as no change.
func Judge(a, b *Run, t Target) (string, []string, *Result) {
	res := &Result{HoldoutA: holdoutText(a), HoldoutB: holdoutText(b),
		TokensDelta: b.SpentTokens - a.SpentTokens, USDDelta: b.SpentUSD - a.SpentUSD, WallDelta: b.Summary.WallSec - a.Summary.WallSec}
	var n int
	res.PassA, n = a.passRate()
	res.PassB, _ = b.passRate()

	var broken []string
	if a.Benchmark != b.Benchmark {
		broken = append(broken, fmt.Sprintf("other benchmarks: %s and %s", a.Benchmark, b.Benchmark))
	}
	for _, r := range []*Run{a, b} {
		if !r.Finished() {
			broken = append(broken, fmt.Sprintf("run %s did not finish (stop: %s)", r.ID, orNone(r.Stop)))
		}
	}
	if a.Holdout.Total != b.Holdout.Total {
		broken = append(broken, fmt.Sprintf("the holdout differs (%d and %d tasks)", a.Holdout.Total, b.Holdout.Total))
	}
	outA := map[string]string{}
	models, tools := map[string]bool{}, map[string]bool{}
	for _, task := range a.Tasks {
		outA[task.Task] = task.Outcome
	}
	seen := map[string]bool{}
	var regressed []string
	for _, tb := range b.Tasks {
		oa, ok := outA[tb.Task]
		if !ok {
			broken = append(broken, tb.Task+" is only in "+b.ID)
			continue
		}
		seen[tb.Task] = true
		switch {
		case oa == outcomePass && tb.Outcome != outcomePass && tb.Outcome != outcomeBroken:
			regressed = append(regressed, tb.Task)
		case oa != outcomePass && tb.Outcome == outcomePass:
			res.Improved++
		}
	}
	for _, r := range []*Run{a, b} {
		for _, task := range r.Tasks {
			if task.Usage.Model != "" {
				models[task.Usage.Model] = true
			}
			if task.Usage.ToolVersion != "" {
				tools[task.Usage.ToolVersion] = true
			}
		}
	}
	for _, ta := range a.Tasks {
		if !seen[ta.Task] {
			broken = append(broken, ta.Task+" is only in "+a.ID)
		}
	}
	if len(models) > 1 {
		broken = append(broken, "the runs used other models: "+strings.Join(sortedKeys(models), ", "))
	}
	if len(tools) > 1 {
		broken = append(broken, "the runs used other Claude Code versions: "+strings.Join(sortedKeys(tools), ", "))
	}
	res.Regressions = len(regressed)

	m, _ := FindMetric(t.Metric)
	va, okA := a.metric(t.Metric)
	vb, okB := b.metric(t.Metric)
	res.MetricA, res.MetricB = va, vb
	if !okA || !okB {
		broken = append(broken, "no "+t.Metric+" in one of the runs")
	}
	if len(broken) > 0 {
		return Broken, broken, res
	}

	var why []string
	if len(regressed) > 0 {
		why = append(why, fmt.Sprintf("%d task(s) regressed: %s", len(regressed), strings.Join(regressed, ", ")))
	}
	floor := 0.0
	if n > 0 {
		floor = 1/float64(n) - 1e-9
	}
	if d := res.PassB - res.PassA; d < 0 && -d >= floor {
		why = append(why, fmt.Sprintf("pass rate dropped %s → %s", fmtRate(res.PassA), fmtRate(res.PassB)))
	}

	// move is how far b moved in the target's direction, in the units of
	// the threshold.
	move := vb - va
	if t.Direction == "down" {
		move = -move
	}
	if m.Relative {
		if va == 0 {
			move = 0
		} else {
			move /= math.Abs(va)
		}
	}
	if t.Metric == "pass_rate" && math.Abs(vb-va) < floor {
		move = 0
	}
	if move < t.Threshold || move <= 0 {
		why = append(why, fmt.Sprintf("target missed: %s %s → %s, wanted %s", t.Metric, fmtMetric(t.Metric, va), fmtMetric(t.Metric, vb), t))
	}
	if len(why) > 0 {
		return Rejected, why, res
	}
	return Promising, []string{fmt.Sprintf("%s %s → %s (%s), no regressions", t.Metric, fmtMetric(t.Metric, va), fmtMetric(t.Metric, vb), t)}, res
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
