package bench

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Outcomes of a task.
const (
	// OutcomePass: the hidden tests and the task's check are green.
	OutcomePass = "pass"
	// OutcomeFail: the worker finished but a hidden test or the check is red.
	OutcomeFail = "fail"
	// OutcomeBroken: the base itself does not build or pass its check; left
	// out of the pass rate.
	OutcomeBroken = "broken"
	// OutcomeTimeout: the worker hit the task's wall-time cap.
	OutcomeTimeout = "timeout"
	// OutcomeBudget: the run's token, cost or wall budget was spent.
	OutcomeBudget = "budget"
	// OutcomeError: the sandbox or the worker could not run.
	OutcomeError = "error"
)

// Run stop reasons.
const (
	StopDone   = "done"
	StopBudget = "budget"
	StopPaused = "paused" // memory stayed BLOCKED; resume with --resume
	StopError  = "error"
)

// Usage is what a worker spent. Known is false when no usage could be read
// (no JSON result, no transcript): the counts are then unknown, not zero.
type Usage struct {
	Known       bool    `json:"known"`
	Input       int64   `json:"input_tokens"`
	Output      int64   `json:"output_tokens"`
	CacheRead   int64   `json:"cache_read_tokens"`
	CacheWrite  int64   `json:"cache_write_tokens"`
	USD         float64 `json:"usd"`
	USDKnown    bool    `json:"usd_known"`
	Model       string  `json:"model,omitempty"`
	SessionID   string  `json:"session_id,omitempty"`
	Turns       int     `json:"turns,omitempty"`
	Source      string  `json:"source,omitempty"` // "result" or "transcript"
	ToolVersion string  `json:"claude_code_version,omitempty"`
}

// Tokens is every token the worker used.
func (u Usage) Tokens() int64 { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

// TaskRecord is one task of a run. The experiment loop reads these fields;
// keep them stable (add, never rename).
type TaskRecord struct {
	Task    string `json:"task"` // <project>/<slug>
	Project string `json:"project"`
	Type    string `json:"type,omitempty"`
	Holdout bool   `json:"holdout,omitempty"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`

	HiddenPassed int  `json:"hidden_passed"`
	HiddenTotal  int  `json:"hidden_total"`
	CheckOK      bool `json:"check_ok"`
	// Judge is the judge's 0-5 score; -1 when there was no judge.
	Judge       int    `json:"judge"`
	JudgeReason string `json:"judge_reason,omitempty"`

	Started time.Time `json:"started"`
	WallSec float64   `json:"wall_sec"`
	Usage   Usage     `json:"usage"`

	// From the sandbox's clilog: issue-cli calls, the refused ones,
	// transitions that passed with no refused try before them, backward
	// transitions, and the statuses the issue went through.
	Calls          int      `json:"cli_calls"`
	Refused        int      `json:"cli_refused"`
	FirstTryGates  int      `json:"gates_first_try"`
	Gates          int      `json:"gates"`
	Backward       int      `json:"rework_backward"`
	Statuses       []string `json:"statuses,omitempty"`
	FinalStatus    string   `json:"final_status,omitempty"`
	OutsideTouched []string `json:"outside_touched,omitempty"`
	DiffLines      int      `json:"diff_lines"`

	// Workflow is the task's workflow box key (product, a department);
	// Eval the eval that scored it ("" scored by hidden tests alone).
	Workflow string `json:"workflow,omitempty"`
	Eval     string `json:"eval,omitempty"`
	// EvalScore is the eval's mean score 0-1, -1 when no eval graded the
	// task; EvalPassed of EvalTotal assertions passed; EvalFailed the
	// failing ones with why, EvalSkipped what was left out.
	EvalScore   float64  `json:"eval_score"`
	EvalPassed  int      `json:"eval_passed,omitempty"`
	EvalTotal   int      `json:"eval_total,omitempty"`
	EvalFailed  []string `json:"eval_failed,omitempty"`
	EvalSkipped []string `json:"eval_skipped,omitempty"`
	// ScoreWallSec is how long scoring the eval took (the grader's time);
	// a task's whole time is WallSec + ScoreWallSec.
	ScoreWallSec float64 `json:"score_wall_sec,omitempty"`
}

// Setup is what a run measured: the issue-cli and workflow under test.
type Setup struct {
	// Ref is the engine commit issue-cli was built from ("" = the installed
	// one).
	Ref string `json:"ref,omitempty"`
	// SHA is the engine commit the issue-cli under test was built from: the
	// resolved ref, or the installed binary's own revision (its build info;
	// "" when it carries none). experiment.Run reads it to find a baseline.
	SHA             string `json:"sha,omitempty"`
	IssueCLI        string `json:"issue_cli"`
	IssueCLIVersion string `json:"issue_cli_version,omitempty"`
	Workflow        string `json:"workflow,omitempty"`
	Label           string `json:"label,omitempty"`
	// WorkerHome is the benchmark's worker_home: for this run (sandbox or
	// owner; "" in runs from before it existed: owner).
	WorkerHome string `json:"worker_home,omitempty"`
	// Agent is the worker command's CLI (its first word: claude, codex),
	// so runs of the same tasks on two agents can be compared (routing).
	Agent string `json:"agent,omitempty"`
}

// WorkerAgent is the agent CLI a worker command runs: its first word, the
// path's base name.
func WorkerAgent(worker string) string {
	f := strings.Fields(worker)
	if len(f) == 0 {
		return ""
	}
	return filepath.Base(f[0])
}

// Holdout is the holdout's aggregate, the only part of it a Run shows.
type Holdout struct {
	Passed int `json:"passed"`
	Total  int `json:"total"`
}

// Summary is a run's aggregate over its non-holdout, non-broken tasks.
type Summary struct {
	Tasks    int     `json:"tasks"`
	Scored   int     `json:"scored"` // tasks not broken
	Passed   int     `json:"passed"`
	PassRate float64 `json:"pass_rate"` // Passed / Scored, 0..1
	// Judge is the mean judge score over scored tasks with one; -1 none.
	Judge        float64 `json:"judge"`
	Tokens       int64   `json:"tokens"`
	TokensKnown  bool    `json:"tokens_known"` // every task's usage was read
	USD          float64 `json:"usd"`
	WallSec      float64 `json:"wall_sec"`
	Refused      int     `json:"cli_refused"`
	Backward     int     `json:"rework_backward"`
	FirstTryRate float64 `json:"gates_first_try_rate"` // 0..1, -1 no gates
}

// Run is run.json. Tasks never holds a holdout task.
type Run struct {
	ID        string    `json:"id"`
	Benchmark string    `json:"benchmark"`
	Started   time.Time `json:"started"`
	Ended     time.Time `json:"ended,omitzero"`
	Stop      string    `json:"stop,omitempty"`
	StopNote  string    `json:"stop_note,omitempty"`
	Setup     Setup     `json:"setup"`
	// StartedBy is "owner" for a run started from the owner's shell, else
	// the agent session's name; Quote the owner's words an agent started it
	// with.
	StartedBy string       `json:"started_by,omitempty"`
	Quote     string       `json:"quote,omitempty"`
	Budget    Budget       `json:"budget"`
	Tasks     []TaskRecord `json:"tasks"`
	Holdout   Holdout      `json:"holdout"`
	Summary   Summary      `json:"summary"`
	// Spent is what the run used across every task, holdout included,
	// checked against Budget.
	SpentTokens int64   `json:"spent_tokens"`
	SpentUSD    float64 `json:"spent_usd"`
}

// Finished reports whether the run ended (any stop but paused).
func (r *Run) Finished() bool { return !r.Ended.IsZero() && r.Stop != StopPaused }

// RunsDir is benchmark's runs folder.
func RunsDir(benchmark string) string { return filepath.Join(Dir(), "runs", benchmark) }

// RunDir is one run's folder.
func RunDir(benchmark, id string) string { return filepath.Join(RunsDir(benchmark), id) }

// Summarize fills r.Summary and r.Holdout from records (holdout included).
func Summarize(r *Run, records []TaskRecord) {
	var s Summary
	var h Holdout
	judged, judgeSum := 0, 0
	gates, first := 0, 0
	s.TokensKnown = true
	for _, t := range records {
		if t.Holdout {
			if t.Outcome != OutcomeBroken {
				h.Total++
				if t.Outcome == OutcomePass {
					h.Passed++
				}
			}
			continue
		}
		s.Tasks++
		if t.Outcome == OutcomeBroken {
			continue
		}
		s.Scored++
		if t.Outcome == OutcomePass {
			s.Passed++
		}
		if t.Judge >= 0 {
			judged++
			judgeSum += t.Judge
		}
		if t.Usage.Known {
			s.Tokens += t.Usage.Tokens()
		} else {
			s.TokensKnown = false
		}
		s.USD += t.Usage.USD
		s.WallSec += t.WallSec
		s.Refused += t.Refused
		s.Backward += t.Backward
		gates += t.Gates
		first += t.FirstTryGates
	}
	if s.Scored > 0 {
		s.PassRate = float64(s.Passed) / float64(s.Scored)
	}
	s.Judge = -1
	if judged > 0 {
		s.Judge = float64(judgeSum) / float64(judged)
	}
	s.FirstTryRate = -1
	if gates > 0 {
		s.FirstTryRate = float64(first) / float64(gates)
	}
	r.Summary, r.Holdout = s, h
}

// Save writes run.json (records without the holdout) and holdout.json.
func Save(r *Run, records []TaskRecord) error {
	dir := RunDir(r.Benchmark, r.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	Summarize(r, records)
	var hold []TaskRecord
	r.Tasks = r.Tasks[:0]
	for _, t := range records {
		if t.Holdout {
			hold = append(hold, t)
		} else {
			r.Tasks = append(r.Tasks, t)
		}
	}
	if err := writeJSON(filepath.Join(dir, "run.json"), r); err != nil {
		return err
	}
	if len(hold) == 0 {
		return nil
	}
	return writeJSON(filepath.Join(dir, "holdout.json"), hold)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadRun reads run.json of benchmark's run id.
func LoadRun(benchmark, id string) (*Run, error) {
	data, err := os.ReadFile(filepath.Join(RunDir(benchmark, id), "run.json"))
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// LoadRecords is every task record of a run, holdout.json included: for the
// run itself (resume), never for output.
func LoadRecords(benchmark, id string) ([]TaskRecord, error) {
	r, err := LoadRun(benchmark, id)
	if err != nil {
		return nil, err
	}
	out := append([]TaskRecord(nil), r.Tasks...)
	data, err := os.ReadFile(filepath.Join(RunDir(benchmark, id), "holdout.json"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var hold []TaskRecord
	if err := json.Unmarshal(data, &hold); err != nil {
		return nil, err
	}
	return append(out, hold...), nil
}

// Runs lists benchmark's runs, oldest first.
func Runs(benchmark string) []*Run {
	entries, _ := os.ReadDir(RunsDir(benchmark))
	var out []*Run
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if r, err := LoadRun(benchmark, e.Name()); err == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Started.Equal(out[j].Started) {
			return out[i].Started.Before(out[j].Started)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// FindRun resolves id across benchmarks: "<benchmark>/<run>", a run id, or
// "latest" (newest run of benchmark, which must then be given).
func FindRun(benchmark, id string) (*Run, error) {
	if b, rid, ok := cutRunID(id); ok {
		benchmark, id = b, rid
	}
	if id == "latest" || id == "" {
		if benchmark == "" {
			return nil, errors.New("latest needs a benchmark (<benchmark>/latest)")
		}
		runs := Runs(benchmark)
		if len(runs) == 0 {
			return nil, errors.New("benchmark " + benchmark + " has no runs yet")
		}
		return runs[len(runs)-1], nil
	}
	if benchmark != "" {
		return LoadRun(benchmark, id)
	}
	for _, b := range List() {
		if r, err := LoadRun(b, id); err == nil {
			return r, nil
		}
	}
	return nil, errors.New("no run " + id)
}

func cutRunID(s string) (string, string, bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}
