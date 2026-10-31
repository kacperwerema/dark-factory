package tracker

import (
	"slices"
	"strings"
)

// How an eval scores a benchmark task (WorkflowEval.Score).
const (
	// EvalScoreTests: the task's hidden Go tests and its check (the
	// development workflow; the factory benchmark's original scoring).
	EvalScoreTests = "tests"
	// EvalScoreOutput: the newest output the run put on the shelf for the
	// issue, its file's text graded by the rubric and assertions.
	EvalScoreOutput = "output"
	// EvalScoreDiff: the worker's diff against the task's base, graded by
	// the rubric and assertions (a change in files with no hidden tests,
	// such as docs).
	EvalScoreDiff = "diff"
)

// EvalScores are the values Score takes.
var EvalScores = []string{EvalScoreTests, EvalScoreOutput, EvalScoreDiff}

// EvalAssertTypes are the promptfoo assertion types an eval's assert:
// passes through: the ones that grade text without a model, a network
// call or code (read from promptfoo 0.124.1's BaseAssertionTypesSchema).
// Each also takes promptfoo's not- prefix.
var EvalAssertTypes = []string{
	"equals", "contains", "icontains", "contains-all", "contains-any", "icontains-all", "icontains-any",
	"starts-with", "regex", "is-json", "contains-json", "is-html", "contains-html", "is-sql", "contains-sql",
	"is-xml", "contains-xml", "levenshtein", "word-count", "rouge-n", "bleu", "gleu",
}

// EvalAssertModelTypes are promptfoo assertion types that need a model:
// they belong under rubric:, graded by the benchmark's grader.
var EvalAssertModelTypes = []string{
	"llm-rubric", "g-eval", "agent-rubric", "search-rubric", "similar", "similar:cosine", "similar:dot", "similar:euclidean",
	"factuality", "model-graded-closedqa", "model-graded-factuality", "answer-relevance", "context-faithfulness",
	"context-recall", "context-relevance", "conversation-relevance", "classifier", "moderation", "is-refusal", "select-best", "pi", "guardrails",
}

// EvalAssertCodeTypes are promptfoo assertion types that run code or call
// out; an eval never passes them through.
var EvalAssertCodeTypes = []string{"javascript", "python", "ruby", "webhook"}

// Graded is true for evals the scorer grades (output, diff), false for
// hidden tests.
func (e WorkflowEval) Graded() bool {
	k := e.ScoreKind()
	return k == EvalScoreOutput || k == EvalScoreDiff
}

// DefaultEvalPass is the mean score (0-1) a task needs when an eval sets
// no pass:.
const DefaultEvalPass = 0.7

// WorkflowEval is one entry of a workflow file's evals: what a good run of
// some of its types looks like and how the factory benchmark scores it
// (docs/benchmark.md, Evals). Evals are per file and project-wide within it:
// type and system overlays don't change them.
type WorkflowEval struct {
	ID          string       `yaml:"id" json:"id" desc:"Unique name of the eval in this file; a benchmark task names it with eval:"`
	Description string       `yaml:"description,omitempty" json:"description,omitempty" desc:"What a good run looks like, one line"`
	Types       []string     `yaml:"types,omitempty" json:"types,omitempty" desc:"Issue types this eval scores; empty = every type no other eval lists"`
	Score       string       `yaml:"score,omitempty" json:"score,omitempty" desc:"How a run is scored: tests (hidden Go tests + checks, the default), output (the run's output on the shelf) or diff (the worker's diff against the base), the last two by the rubric and assertions"`
	Rubric      []string     `yaml:"rubric,omitempty" json:"rubric,omitempty" desc:"Lines a grader model checks the output against (promptfoo llm-rubric, graded by the benchmark's judge: command)"`
	Assert      []EvalAssert `yaml:"assert,omitempty" json:"assert,omitempty" desc:"promptfoo assertions without a model, passed through: contains, icontains, not-contains, regex, is-json, … (type, value)"`
	Checks      []string     `yaml:"checks,omitempty" json:"checks,omitempty" desc:"Shell commands that must pass in the task's checkout"`
	Pass        float64      `yaml:"pass,omitempty" json:"pass,omitempty" desc:"Mean score 0-1 a task needs to pass (default 0.7); tests evals pass on every hidden test and check"`
}

// EvalAssert is one promptfoo assertion an eval passes through.
type EvalAssert struct {
	Type  string `yaml:"type" json:"type" desc:"promptfoo assertion type without a model (contains, icontains, contains-any, regex, starts-with, is-json, contains-json, equals, levenshtein, word-count, …; each with not-)"`
	Value any    `yaml:"value,omitempty" json:"value,omitempty" desc:"The assertion's value: text, a pattern or a number"`
}

// ScoreKind is Score with its default.
func (e WorkflowEval) ScoreKind() string {
	if s := strings.TrimSpace(e.Score); s != "" {
		return s
	}
	return EvalScoreTests
}

// PassBar is Pass with its default.
func (e WorkflowEval) PassBar() float64 {
	if e.Pass > 0 {
		return e.Pass
	}
	return DefaultEvalPass
}

// EvalByID is the eval named id; nil when none.
func (w *WorkflowConfig) EvalByID(id string) *WorkflowEval {
	if w == nil {
		return nil
	}
	for i := range w.Evals {
		if w.Evals[i].ID == id {
			return &w.Evals[i]
		}
	}
	return nil
}

// EvalFor is the eval that scores issueType: the first eval listing it,
// else the first with no types; nil when the file declares none that fit.
func (w *WorkflowConfig) EvalFor(issueType string) *WorkflowEval {
	if w == nil {
		return nil
	}
	for i := range w.Evals {
		if slices.Contains(w.Evals[i].Types, issueType) {
			return &w.Evals[i]
		}
	}
	for i := range w.Evals {
		if len(w.Evals[i].Types) == 0 {
			return &w.Evals[i]
		}
	}
	return nil
}
