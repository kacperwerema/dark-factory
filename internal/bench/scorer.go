package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The scorer grades what an output or diff eval produced (docs/benchmark.md,
// Evals). It wraps promptfoo (D-1 of
// factory/benchmarks-and-evals-for-every-agentic-workflow-is-a-workflow-change-a-net-positive-or-negative):
// the engine writes a promptfoo config whose "model" is promptfoo's echo
// provider over the produced text, so the eval's assertions grade that text,
// runs `<scorer> eval`, and reads the JSON result. Rubric lines are
// llm-rubric assertions graded by the benchmark's grader: command through a
// promptfoo exec: provider; without a grader they are left out and named.

// PromptfooVersion is the promptfoo release the default scorer runs, pinned
// so a run's scoring doesn't change under it.
const PromptfooVersion = "0.124.1"

// DefaultScorer runs the pinned promptfoo through npx.
const DefaultScorer = "npx --yes promptfoo@" + PromptfooVersion

// ScorerWall caps one scoring.
const ScorerWall = 10 * time.Minute

// scorerEnv keeps promptfoo on the machine: no telemetry, no update check,
// its config and cache in the scoring folder (not ~/.promptfoo).
func scorerEnv(dir string, base []string) []string {
	return append(base,
		"PROMPTFOO_DISABLE_TELEMETRY=1",
		"PROMPTFOO_DISABLE_UPDATE=1",
		"PROMPTFOO_DISABLE_SHARING=1",
		"PROMPTFOO_CONFIG_DIR="+filepath.Join(dir, ".promptfoo"),
		"PROMPTFOO_CACHE_PATH="+filepath.Join(dir, ".promptfoo", "cache"),
	)
}

// ScoreInput is what one scoring grades.
type ScoreInput struct {
	Eval tracker.WorkflowEval
	// Output is the produced text (the output's file, or the diff);
	// Reference the owner-approved text the task came from ("" none); Brief
	// the issue as the worker got it.
	Output, Reference, Brief string
	// Grader is the command that answers a rubric prompt on stdin; "" leaves
	// the rubric out.
	Grader string
}

// EvalResult is one scoring's outcome.
type EvalResult struct {
	// Score is the mean over the assertions that ran, 0-1.
	Score float64
	// Passed of Total assertions passed.
	Passed, Total int
	// Failed are the failing assertions with promptfoo's reason, cut short.
	Failed []string
	// Skipped names what was left out (rubric lines without a grader).
	Skipped []string
	Tokens  int64
}

// WriteScorerConfig writes promptfooconfig.yaml and the vars' files into dir
// and returns what it left out.
func WriteScorerConfig(dir string, in ScoreInput) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	files := map[string]string{"output.txt": in.Output, "reference.txt": in.Reference, "brief.md": in.Brief}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			return nil, err
		}
	}
	var asserts []map[string]any
	for _, a := range in.Eval.Assert {
		m := map[string]any{"type": a.Type}
		if a.Value != nil {
			m["value"] = a.Value
		}
		asserts = append(asserts, m)
	}
	var skipped []string
	grader := strings.TrimSpace(in.Grader)
	for _, line := range in.Eval.Rubric {
		if grader == "" {
			skipped = append(skipped, "rubric: "+line)
			continue
		}
		asserts = append(asserts, map[string]any{"type": "llm-rubric", "value": line})
	}
	cfg := map[string]any{
		"description": "bench eval " + in.Eval.ID,
		"prompts":     []string{"{{output}}"},
		"providers":   []string{"echo"},
		"tests": []map[string]any{{
			"vars":   map[string]string{"output": "file://output.txt", "reference": "file://reference.txt", "brief": "file://brief.md"},
			"assert": asserts,
		}},
	}
	if grader != "" {
		// promptfoo's exec: provider passes the grading prompt as $1; the
		// grader reads it on stdin, like the judge.
		script := "#!/bin/sh\nprintf '%s' \"$1\" | " + grader + "\n"
		if err := os.WriteFile(filepath.Join(dir, "grader.sh"), []byte(script), 0o755); err != nil {
			return nil, err
		}
		cfg["defaultTest"] = map[string]any{"options": map[string]any{"provider": "exec: ./grader.sh"}}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return skipped, os.WriteFile(filepath.Join(dir, "promptfooconfig.yaml"), data, 0o644)
}

// ErrNothingToScore is returned when an eval has no assertion that can run.
var ErrNothingToScore = errors.New("nothing to score: no assert: items, and the rubric needs the benchmark's grader:")

// Score grades in with the scorer command (default DefaultScorer) in dir.
func Score(ctx context.Context, dir, scorer string, in ScoreInput, env []string) (EvalResult, error) {
	skipped, err := WriteScorerConfig(dir, in)
	res := EvalResult{Skipped: skipped}
	if err != nil {
		return res, err
	}
	if len(in.Eval.Assert) == 0 && (len(in.Eval.Rubric) == 0 || strings.TrimSpace(in.Grader) == "") {
		return res, ErrNothingToScore
	}
	if strings.TrimSpace(scorer) == "" {
		scorer = DefaultScorer
	}
	out := filepath.Join(dir, "result.json")
	_ = os.Remove(out)
	sctx, cancel := context.WithTimeout(ctx, ScorerWall)
	defer cancel()
	cmd := exec.CommandContext(sctx, "sh", "-c", scorer+" eval -c promptfooconfig.yaml -o result.json --no-cache --no-table --no-write")
	cmd.Dir = dir
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = scorerEnv(dir, env)
	ownGroup(cmd)
	cmd.WaitDelay = 10 * time.Second
	log, runErr := cmd.CombinedOutput()
	data, err := os.ReadFile(out)
	if err != nil {
		// promptfoo exits 100 when an assertion fails, but still writes the
		// result; no result at all is the scorer failing.
		why := lastLine(string(log))
		if runErr != nil {
			why = runErr.Error() + ": " + why
		}
		if strings.Contains(string(log), "not found") || errors.Is(runErr, exec.ErrNotFound) {
			why += " (the scorer needs Node and npx on PATH, or set scorer: in the benchmark)"
		}
		return res, fmt.Errorf("scorer: %s", strings.TrimSpace(why))
	}
	parsed, err := ParseScorerResult(data)
	if err != nil {
		return res, err
	}
	parsed.Skipped = skipped
	return parsed, nil
}

// ParseScorerResult reads promptfoo's `-o result.json`: the first result's
// gradingResult and its componentResults.
func ParseScorerResult(data []byte) (EvalResult, error) {
	var doc struct {
		Results struct {
			Results []struct {
				Error         string `json:"error"`
				GradingResult *struct {
					Score      float64 `json:"score"`
					Components []struct {
						Pass      bool    `json:"pass"`
						Score     float64 `json:"score"`
						Reason    string  `json:"reason"`
						Assertion struct {
							Type  string `json:"type"`
							Value any    `json:"value"`
						} `json:"assertion"`
					} `json:"componentResults"`
				} `json:"gradingResult"`
			} `json:"results"`
			Stats struct {
				TokenUsage struct {
					Assertions struct {
						Total int64 `json:"total"`
					} `json:"assertions"`
				} `json:"tokenUsage"`
			} `json:"stats"`
		} `json:"results"`
	}
	var res EvalResult
	if err := json.Unmarshal(data, &doc); err != nil {
		return res, fmt.Errorf("scorer result: %w", err)
	}
	if len(doc.Results.Results) == 0 {
		return res, errors.New("scorer result has no results")
	}
	r := doc.Results.Results[0]
	if r.GradingResult == nil {
		return res, fmt.Errorf("scorer result has no grading: %s", orNone(r.Error))
	}
	sum := 0.0
	for _, c := range r.GradingResult.Components {
		res.Total++
		sum += c.Score
		if c.Pass {
			res.Passed++
			continue
		}
		what := c.Assertion.Type
		if v := fmt.Sprint(c.Assertion.Value); c.Assertion.Value != nil && v != "" {
			what += " " + cut(v, 60)
		}
		res.Failed = append(res.Failed, what+": "+cut(c.Reason, 160))
	}
	switch {
	case res.Total > 0:
		res.Score = sum / float64(res.Total)
	default:
		res.Score = r.GradingResult.Score
	}
	res.Tokens = doc.Results.Stats.TokenUsage.Assertions.Total
	return res, nil
}

func cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
