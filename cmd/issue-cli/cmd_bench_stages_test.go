package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/bench"
)

func TestBenchPrintsStageScores(t *testing.T) {
	c := bench.Comparison{A: "t/a", B: "t/b", Comparable: true, StageScores: []bench.StageScoreDelta{
		{Stage: bench.StageDesign, Status: "in design", Tasks: 2,
			A: bench.StageScoreSide{Scored: 2, Score: 0.7, Passed: 1, Tokens: -1, WallSec: -1},
			B: bench.StageScoreSide{Scored: 2, Score: 0.9, Passed: 2, Tokens: 12000, WallSec: 90}},
		{Stage: bench.StageDocs, Tasks: 2, A: bench.StageScoreSide{Score: -1, Tokens: -1, WallSec: -1}, B: bench.StageScoreSide{Score: -1, Tokens: -1, WallSec: -1}},
	}}
	var out bytes.Buffer
	printComparison(&out, c)
	got := out.String()
	for _, want := range []string{"By stage, scores", "design  in design", "0.70 → 0.90", "1/2 → 2/2", "— → 12k", "docs    —"} {
		if !strings.Contains(got, want) {
			t.Errorf("compare lacks %q:\n%s", want, got)
		}
	}
	var rep bytes.Buffer
	printRun(&rep, &bench.Run{ID: "r", Benchmark: "t", Tasks: []bench.TaskRecord{{Task: "p/one", Outcome: bench.OutcomePass, Judge: -1,
		Stages: []bench.StageRecord{{Stage: bench.StageDesign, Score: 0.9, Pass: true}, {Stage: bench.StageDocs, Score: -1, Reason: "nothing to score"}}}}})
	if !strings.Contains(rep.String(), "stages: design 0.90 ✓ · docs — (nothing to score)") {
		t.Errorf("report lacks the stages line:\n%s", rep.String())
	}
}
