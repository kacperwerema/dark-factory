// Package experiment is the factory's experiment loop (factory/
// factory-experiment-loop): a change to prompts, workflow.yaml or a tool,
// made on an issue's branch and measured on the benchmark against main.
//
// An experiment is an ordinary issue labelled "experiment". Its block lives
// in the issue's data sidecar (tracker.DataStore.Experiment): the
// hypothesis, the retros it came from, the target metric, the two benchmark
// runs and the verdict. The verdict is advice; adopting the change is the
// owner's, even in YOLO (cmd/issue-cli yolo_gate.go).
package experiment

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Label marks an issue as an experiment.
const Label = "experiment"

// Is reports whether labels carry the experiment label.
func Is(labels []string) bool {
	return slices.ContainsFunc(labels, func(l string) bool { return strings.EqualFold(strings.TrimSpace(l), Label) })
}

// Verdicts.
const (
	// Promising: no task regressed, the pass rate did not drop and the
	// target moved by its threshold. The owner decides whether to adopt.
	Promising = "promising"
	// Rejected: a task regressed, the pass rate dropped or the target missed.
	Rejected = "rejected"
	// Broken: the two runs can't be compared (a run stopped, other tasks,
	// another model or Claude Code version, the metric unknown).
	Broken = "broken"
)

// Metric is a number of a benchmark run an experiment can target.
type Metric struct {
	Name string
	// Higher reports whether a higher value is better (the usual direction).
	Higher bool
	// Relative: the threshold is a fraction of the baseline (tokens, cost,
	// wall); otherwise it is in the metric's own units.
	Relative bool
	Text     string
}

// Metrics are the targets init accepts, in the order help lists them.
var Metrics = []Metric{
	{Name: "pass_rate", Higher: true, Text: "tasks passed / tasks scored, holdout included (0..1)"},
	{Name: "gates_first_try", Higher: true, Text: "transitions that passed with no refused try first (0..1)"},
	{Name: "rework", Text: "backward transitions over the run (count)"},
	{Name: "judge", Higher: true, Text: "mean judge score (0..5)"},
	{Name: "tokens", Relative: true, Text: "tokens spent by the run (fraction of the baseline)"},
	{Name: "cost", Relative: true, Text: "USD spent by the run (fraction of the baseline)"},
	{Name: "wall", Relative: true, Text: "wall time of the scored tasks (fraction of the baseline)"},
}

// FindMetric is the metric named name.
func FindMetric(name string) (Metric, bool) {
	for _, m := range Metrics {
		if m.Name == name {
			return m, true
		}
	}
	return Metric{}, false
}

// MetricNames lists the metric names.
func MetricNames() []string {
	var out []string
	for _, m := range Metrics {
		out = append(out, m.Name)
	}
	return out
}

// Target is what the experiment means to move.
type Target struct {
	Metric string `json:"metric"`
	// Direction is "up" or "down".
	Direction string  `json:"direction"`
	Threshold float64 `json:"threshold"`
}

func (t Target) String() string {
	return fmt.Sprintf("%s %s by %s", t.Metric, t.Direction, strconv.FormatFloat(t.Threshold, 'g', -1, 64))
}

// ParseTarget reads "<metric>:<up|down>:<threshold>".
func ParseTarget(s string) (Target, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return Target{}, fmt.Errorf("target %q: want <metric>:<up|down>:<threshold>, e.g. gates_first_try:up:0.05", s)
	}
	t := Target{Metric: parts[0], Direction: strings.ToLower(parts[1])}
	if _, ok := FindMetric(t.Metric); !ok {
		return Target{}, fmt.Errorf("unknown metric %q (metrics: %s)", t.Metric, strings.Join(MetricNames(), ", "))
	}
	if t.Direction != "up" && t.Direction != "down" {
		return Target{}, fmt.Errorf("target direction %q: up or down", parts[1])
	}
	v, err := strconv.ParseFloat(parts[2], 64)
	if err != nil || v < 0 {
		return Target{}, fmt.Errorf("target threshold %q: a number ≥ 0", parts[2])
	}
	t.Threshold = v
	return t, nil
}

// RunRef is one side of the experiment: the setup measured and its run.
type RunRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha,omitempty"`
	// Run is the benchmark run id ("" until it ran).
	Run string `json:"run,omitempty"`
}

// Result is what the compare said, kept next to the verdict.
type Result struct {
	PassA       float64 `json:"pass_rate_a"`
	PassB       float64 `json:"pass_rate_b"`
	MetricA     float64 `json:"metric_a"`
	MetricB     float64 `json:"metric_b"`
	Regressions int     `json:"regressions"`
	Improved    int     `json:"improved"`
	TokensDelta int64   `json:"tokens_delta"`
	USDDelta    float64 `json:"usd_delta"`
	WallDelta   float64 `json:"wall_delta_sec"`
	HoldoutA    string  `json:"holdout_a,omitempty"` // "passed n/m"
	HoldoutB    string  `json:"holdout_b,omitempty"`
}

// Adopted is set when the change reached main.
type Adopted struct {
	Commit     string `json:"commit,omitempty"`
	Date       string `json:"date,omitempty"`
	RolledBack string `json:"rolled_back,omitempty"` // the revert branch, once prepared
}

// Block is the experiment block in the sidecar.
type Block struct {
	Hypothesis string   `json:"hypothesis"`
	Source     []string `json:"source,omitempty"` // retros/<file> the findings came from
	// Change globs the branch may touch (empty: anything not protected).
	Change    []string `json:"change,omitempty"`
	Target    Target   `json:"target"`
	Benchmark string   `json:"benchmark,omitempty"`
	Baseline  *RunRef  `json:"baseline,omitempty"`
	Candidate *RunRef  `json:"candidate,omitempty"`
	Verdict   string   `json:"verdict,omitempty"`
	Reasons   []string `json:"reasons,omitempty"`
	Result    *Result  `json:"result,omitempty"`
	Adopted   *Adopted `json:"adopted,omitempty"`
}

// Load reads the block of the issue at issuePath; nil when it has none.
func Load(issuePath string) (*Block, error) {
	store, err := tracker.LoadData(issuePath)
	if err != nil {
		return nil, err
	}
	if len(store.Experiment) == 0 || string(store.Experiment) == "null" {
		return nil, nil
	}
	var b Block
	if err := json.Unmarshal(store.Experiment, &b); err != nil {
		return nil, fmt.Errorf("experiment block of %s: %w", tracker.SidecarPath(issuePath), err)
	}
	return &b, nil
}

// Save writes b into the issue's sidecar, keeping the rest of it.
func Save(issuePath string, b *Block) error {
	store, err := tracker.LoadData(issuePath)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	store.Experiment = raw
	return tracker.SaveData(issuePath, store)
}

// VerdictLine is the one-line summary the comment, list and the Updates row
// show: "promising — gates_first_try 0.62→0.71 (up by 0.05), pass 6/8→6/8".
func (b *Block) VerdictLine() string {
	if b == nil || b.Verdict == "" {
		return "not run yet"
	}
	if b.Result == nil {
		return b.Verdict
	}
	r := b.Result
	line := fmt.Sprintf("%s — %s %s→%s (%s), pass %s→%s, regressions %d",
		b.Verdict, b.Target.Metric, fmtMetric(b.Target.Metric, r.MetricA), fmtMetric(b.Target.Metric, r.MetricB),
		b.Target.Direction+" by "+strconv.FormatFloat(b.Target.Threshold, 'g', -1, 64),
		fmtRate(r.PassA), fmtRate(r.PassB), r.Regressions)
	if r.HoldoutB != "" {
		line += ", holdout " + r.HoldoutA + "→" + r.HoldoutB
	}
	return line
}

func fmtRate(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func fmtMetric(name string, v float64) string {
	switch name {
	case "tokens", "rework":
		return strconv.FormatFloat(v, 'f', 0, 64)
	case "cost":
		return "$" + strconv.FormatFloat(v, 'f', 2, 64)
	case "wall":
		return strconv.FormatFloat(v/60, 'f', 1, 64) + "m"
	}
	return fmtRate(v)
}
