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
	// Branch is the ref whose files marked the workflows (MarkChanged),
	// NotMapped its files no workflow reads; empty without --branch.
	Branch    string   `json:"branch,omitempty"`
	NotMapped []string `json:"not_mapped,omitempty"`
	// ByAgent is the table by agent and type when the runs hold two agents
	// between them; Routing what it says of the project's routing: rules,
	// set by the caller, which knows the project (CheckRouting).
	ByAgent *AgentTable    `json:"by_agent,omitempty"`
	Routing []RoutingCheck `json:"routing,omitempty"`
	// Stages is the runs by stage (CompareByStage), from the tasks' traces;
	// empty when no task is traced in both.
	Stages []StageDelta `json:"stages,omitempty"`
	// StageScores is each stage's score in both runs (CompareStageScores);
	// empty when no task has stage records in both.
	StageScores []StageScoreDelta `json:"stage_scores,omitempty"`
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
		byTask[t.Key()] = t
	}
	seen := map[string]bool{}
	for _, tb := range b.Tasks {
		key := tb.Key()
		ta, ok := byTask[key]
		if !ok {
			c.Comparable = false
			warn("%s is only in %s", key, b.ID)
			continue
		}
		seen[key] = true
		d := TaskDelta{Task: key, A: ta.Outcome, B: tb.Outcome, JudgeA: ta.Judge, JudgeB: tb.Judge,
			TokensDelta: tb.Usage.Tokens() - ta.Usage.Tokens(), USDDelta: tb.Usage.USD - ta.Usage.USD,
			WallDelta: tb.WallSec - ta.WallSec}
		c.Tasks = append(c.Tasks, d)
		switch {
		case ta.Outcome == OutcomePass && tb.Outcome != OutcomePass && tb.Outcome != OutcomeBroken:
			c.Regressions = append(c.Regressions, key)
		case ta.Outcome != OutcomePass && tb.Outcome == OutcomePass:
			c.Improved = append(c.Improved, key)
		}
		if ta.Usage.Model != "" && tb.Usage.Model != "" && ta.Usage.Model != tb.Usage.Model {
			warn("%s: model %s in %s, %s in %s", key, ta.Usage.Model, a.ID, tb.Usage.Model, b.ID)
		}
		if ta.Usage.ToolVersion != "" && tb.Usage.ToolVersion != "" && ta.Usage.ToolVersion != tb.Usage.ToolVersion {
			warn("%s: Claude Code %s in %s, %s in %s", key, ta.Usage.ToolVersion, a.ID, tb.Usage.ToolVersion, b.ID)
		}
	}
	for _, ta := range a.Tasks {
		if !seen[ta.Key()] {
			c.Comparable = false
			warn("%s is only in %s", ta.Key(), a.ID)
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
	c.ByAgent = ByAgent(a, b)
	c.Stages = CompareByStage(a, b)
	c.StageScores = CompareStageScores(a, b)
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
