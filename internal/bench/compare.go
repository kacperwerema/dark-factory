package bench

import (
	"fmt"
	"slices"
	"sort"
)

// TaskDelta is one task in both runs.
type TaskDelta struct {
	Task        string  `json:"task"`
	A           string  `json:"a"` // outcome in A
	B           string  `json:"b"`
	JudgeA      int     `json:"judge_a"`
	JudgeB      int     `json:"judge_b"`
	TokensDelta int64   `json:"tokens_delta"`
	USDDelta    float64 `json:"usd_delta"`
	WallDelta   float64 `json:"wall_delta_sec"`
}

// Comparison is `bench compare a b`. Holdout tasks show only as HoldoutA and
// HoldoutB.
type Comparison struct {
	A           string      `json:"a"`
	B           string      `json:"b"`
	Comparable  bool        `json:"comparable"`
	Regressions []string    `json:"regressions"`
	Improved    []string    `json:"improved"`
	Tasks       []TaskDelta `json:"tasks"`
	// PassRateA and PassRateB are over every scored task, holdout
	// included (Run.Summary.PassRate leaves the holdout out).
	PassRateA     float64  `json:"pass_rate_a"`
	PassRateB     float64  `json:"pass_rate_b"`
	PassRateDelta float64  `json:"pass_rate_delta"`
	JudgeDelta    float64  `json:"judge_delta"` // 0 when a run has no judge score
	TokensDelta   int64    `json:"tokens_delta"`
	USDDelta      float64  `json:"usd_delta"`
	WallDelta     float64  `json:"wall_delta_sec"`
	HoldoutA      Holdout  `json:"holdout_a"`
	HoldoutB      Holdout  `json:"holdout_b"`
	Warnings      []string `json:"warnings"`
	// Workflows is each workflow's verdict (CompareByWorkflow).
	Workflows []WorkflowVerdict `json:"workflows"`
}

// Regressed reports whether B is worse than A: a task that passed in A fails
// in B, or the pass rate (holdout included) drops.
func (c Comparison) Regressed() bool {
	if len(c.Regressions) > 0 || c.PassRateDelta < 0 {
		return true
	}
	return c.HoldoutA.Total == c.HoldoutB.Total && c.HoldoutB.Passed < c.HoldoutA.Passed
}

// Compare compares run b with run a. Comparable is false when they are of
// different benchmarks, either has not finished, or their task sets differ.
func Compare(a, b *Run) Comparison {
	c := Comparison{A: a.Benchmark + "/" + a.ID, B: b.Benchmark + "/" + b.ID, Comparable: true,
		Regressions: []string{}, Improved: []string{}, Tasks: []TaskDelta{}, Warnings: []string{},
		HoldoutA: a.Holdout, HoldoutB: b.Holdout}
	warn := func(format string, args ...any) { c.Warnings = append(c.Warnings, fmt.Sprintf(format, args...)) }
	if a.Benchmark != b.Benchmark {
		c.Comparable = false
		warn("other benchmarks: %s and %s", a.Benchmark, b.Benchmark)
	}
	for _, r := range []*Run{a, b} {
		if r.Stop != StopDone {
			c.Comparable = false
			warn("%s did not finish (stop: %s)", r.ID, orNone(r.Stop))
		}
	}
	byTask := map[string]TaskRecord{}
	for _, t := range a.Tasks {
		byTask[t.Task] = t
	}
	seen := map[string]bool{}
	for _, tb := range b.Tasks {
		ta, ok := byTask[tb.Task]
		if !ok {
			c.Comparable = false
			warn("%s is only in %s", tb.Task, b.ID)
			continue
		}
		seen[tb.Task] = true
		d := TaskDelta{Task: tb.Task, A: ta.Outcome, B: tb.Outcome, JudgeA: ta.Judge, JudgeB: tb.Judge,
			TokensDelta: tb.Usage.Tokens() - ta.Usage.Tokens(), USDDelta: tb.Usage.USD - ta.Usage.USD,
			WallDelta: tb.WallSec - ta.WallSec}
		c.Tasks = append(c.Tasks, d)
		switch {
		case ta.Outcome == OutcomePass && tb.Outcome != OutcomePass && tb.Outcome != OutcomeBroken:
			c.Regressions = append(c.Regressions, tb.Task)
		case ta.Outcome != OutcomePass && tb.Outcome == OutcomePass:
			c.Improved = append(c.Improved, tb.Task)
		}
		if ta.Usage.Model != "" && tb.Usage.Model != "" && ta.Usage.Model != tb.Usage.Model {
			warn("%s: model %s in %s, %s in %s", tb.Task, ta.Usage.Model, a.ID, tb.Usage.Model, b.ID)
		}
		if ta.Usage.ToolVersion != "" && tb.Usage.ToolVersion != "" && ta.Usage.ToolVersion != tb.Usage.ToolVersion {
			warn("%s: Claude Code %s in %s, %s in %s", tb.Task, ta.Usage.ToolVersion, a.ID, tb.Usage.ToolVersion, b.ID)
		}
	}
	for _, ta := range a.Tasks {
		if !seen[ta.Task] {
			c.Comparable = false
			warn("%s is only in %s", ta.Task, a.ID)
		}
	}
	if a.Holdout.Total != b.Holdout.Total {
		c.Comparable = false
		warn("the holdout differs (%d and %d tasks)", a.Holdout.Total, b.Holdout.Total)
	}
	// Regressions first, then the rest by name.
	sort.SliceStable(c.Tasks, func(i, j int) bool {
		ri, rj := slices.Contains(c.Regressions, c.Tasks[i].Task), slices.Contains(c.Regressions, c.Tasks[j].Task)
		if ri != rj {
			return ri
		}
		return c.Tasks[i].Task < c.Tasks[j].Task
	})
	c.Warnings = dedupe(c.Warnings)
	c.PassRateA, c.PassRateB = withHoldout(a), withHoldout(b)
	c.PassRateDelta = c.PassRateB - c.PassRateA
	if a.Summary.Judge >= 0 && b.Summary.Judge >= 0 {
		c.JudgeDelta = b.Summary.Judge - a.Summary.Judge
	}
	c.TokensDelta = b.SpentTokens - a.SpentTokens
	c.USDDelta = b.SpentUSD - a.SpentUSD
	c.WallDelta = b.Summary.WallSec - a.Summary.WallSec
	c.Workflows = CompareByWorkflow(a, b)
	return c
}

// withHoldout is the pass rate over every scored task, holdout included.
func withHoldout(r *Run) float64 {
	n := r.Summary.Scored + r.Holdout.Total
	if n == 0 {
		return 0
	}
	return float64(r.Summary.Passed+r.Holdout.Passed) / float64(n)
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	out := s[:0]
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
