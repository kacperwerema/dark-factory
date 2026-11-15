package bench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/trace"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// stagedTask is the fixture's task with a reference per stage: design.md
// and docs.diff beside its snapshot.
func stagedTask(t *testing.T, f *fixture) Task {
	t.Helper()
	task := f.task
	task.Stages = &Stages{
		Design: &StageRef{Reference: DesignReferenceFile},
		Code:   &StageRef{},
		Docs:   &StageRef{Reference: DocsReferenceFile, Rubric: DefaultDocsRubric},
	}
	write(t, filepath.Join(TaskDir(task.Project, task.Slug), DesignReferenceFile), "## Design\n\nFlip the sign in Add.\n")
	write(t, filepath.Join(TaskDir(task.Project, task.Slug), DocsReferenceFile), "+Add adds.\n")
	return task
}

// stagedWorker fixes Add, writes a ## Design into the sandbox issue and a
// doc, each saying GOOD (what stubScorer grades) or not.
func stagedWorker(word string) string {
	return `cat > /dev/null; printf 'package calc\n\nfunc Add(a, b int) int { return a + b }\n' > calc.go; ` +
		`printf '\n## Design\n\nPLANMARKER ` + word + ` flip the sign.\n' >> ../tracker/issues/calc/fix-add.md; ` +
		`mkdir -p docs; printf 'DOCSMARKER ` + word + ` Add adds.\n' > docs/calc.md; ` +
		`echo '{"type":"result","total_cost_usd":0.25,"usage":{"input_tokens":1000,"output_tokens":200},"modelUsage":{"claude-opus-5-5":{}}}'`
}

// stageJudge answers the design prompt with 4 and the whole-task one with 3.
const stageJudge = `if grep -q 'WORKER DESIGN'; then printf 'SCORE: 4\nREASON: same plan\n'; else printf 'SCORE: 3\nREASON: same fix\n'; fi`

func TestRunWritesStages(t *testing.T) {
	f := newFixture(t)
	task := stagedTask(t, f)
	b := f.benchmark(stagedWorker("GOOD"), task)
	b.Judge, b.Grader, b.Scorer = stageJudge, "true", stubScorer(t)
	run, err := Execute(context.Background(), b, f.env(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if len(r.Stages) != 3 {
		t.Fatalf("stages = %+v", r.Stages)
	}
	d, c, docs := r.Stages[0], r.Stages[1], r.Stages[2]
	if d.Stage != StageDesign || d.Judge != 4 || d.Score < 0.89 || d.Score > 0.91 || !d.Pass || !strings.Contains(d.Reason, "same plan") {
		t.Errorf("design = %+v", d)
	}
	if c.Stage != StageCode || c.Status != "in progress" || c.Score != 1 || !c.Pass {
		t.Errorf("code = %+v", c)
	}
	if docs.Stage != StageDocs || docs.Score != 1 || !docs.Pass || docs.Status != "" {
		t.Errorf("docs = %+v", docs)
	}
	for _, s := range r.Stages {
		if s.TokensKnown {
			t.Errorf("%s: tokens known without a trace", s.Stage)
		}
	}
	// No graded text in run.json: scores and reasons only.
	data, _ := json.Marshal(run)
	for _, marker := range []string{"PLANMARKER", "DOCSMARKER", "Flip the sign"} {
		if strings.Contains(string(data), marker) {
			t.Errorf("run.json holds %s", marker)
		}
	}
}

func TestStagesLeaveOutcome(t *testing.T) {
	f := newFixture(t)
	task := stagedTask(t, f)
	b := f.benchmark(stagedWorker("BAD"), task)
	b.Grader, b.Scorer = "true", stubScorer(t)
	run, err := Execute(context.Background(), b, f.env(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := run.Tasks[0]
	if r.Outcome != OutcomePass || r.Judge != -1 {
		t.Fatalf("a failed stage changed the task: %+v", r)
	}
	if r.Stages[0].Pass || r.Stages[0].Score != 0 || r.Stages[2].Pass {
		t.Errorf("stages = %+v", r.Stages)
	}
	// A task without references gets no stages.
	plain, err := Execute(context.Background(), f.benchmark(fixWorker, f.task), f.env(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Tasks[0].Stages) != 0 {
		t.Errorf("a task without references has stages %+v", plain.Tasks[0].Stages)
	}
}

func TestScoreDesignStage(t *testing.T) {
	f := newFixture(t)
	task := stagedTask(t, f)
	in := stageInput{task: task, b: New("t"), dir: t.TempDir(), design: "a plan"}
	// Neither judge nor grader: unknown, and the reason names both.
	var s StageRecord
	s.Score, s.Judge = -1, -1
	scoreDesign(context.Background(), in, &TaskRecord{}, &s)
	if s.Known() || !strings.Contains(s.Reason, "no judge: and no grader:") {
		t.Fatalf("ungraded design = %+v", s)
	}
	// The judge alone: its score over 5.
	in.b.Judge = stageJudge
	s = StageRecord{Score: -1, Judge: -1}
	scoreDesign(context.Background(), in, &TaskRecord{}, &s)
	if s.Judge != 4 || s.Score != 0.8 {
		t.Fatalf("judged design = %+v", s)
	}
	// No ## Design written: 0, said so.
	in.design = ""
	s = StageRecord{Score: -1, Judge: -1}
	scoreDesign(context.Background(), in, &TaskRecord{}, &s)
	if s.Score != 0 || !strings.Contains(s.Reason, "no ## Design") {
		t.Fatalf("missing design = %+v", s)
	}
	// A missing reference is unknown with its reason, whatever is set.
	task.Stages.Missing = map[string]string{StageDesign: "never went through a design status"}
	rec := &TaskRecord{}
	scoreStages(context.Background(), stageInput{task: task, b: New("t"), dir: t.TempDir()}, rec)
	if rec.Stages[0].Known() || !strings.Contains(rec.Stages[0].Reason, "never went through") {
		t.Fatalf("missing reference = %+v", rec.Stages[0])
	}
}

func TestScoreCodeStage(t *testing.T) {
	cases := []struct {
		rec   TaskRecord
		check string
		score float64
		pass  bool
	}{
		{TaskRecord{HiddenPassed: 3, HiddenTotal: 3, CheckOK: true}, "", 1, true},
		{TaskRecord{HiddenPassed: 2, HiddenTotal: 4, CheckOK: true}, "", 0.5, false},
		{TaskRecord{HiddenPassed: 3, HiddenTotal: 3, CheckOK: false}, "make check", 0, false},
		{TaskRecord{CheckOK: true}, "make check", 1, true},
		{TaskRecord{CheckOK: true}, "", -1, false},
	}
	for i, c := range cases {
		task := Task{Check: c.check, Stages: &Stages{Code: &StageRef{}}}
		rec := c.rec
		scoreStages(context.Background(), stageInput{task: task, b: New("t"), dir: t.TempDir()}, &rec)
		s := rec.Stages[1]
		if s.Score != c.score || s.Pass != c.pass {
			t.Errorf("case %d: code = %+v, want %v %v", i, s, c.score, c.pass)
		}
	}
}

func TestScoreDocsStage(t *testing.T) {
	f := newFixture(t)
	task := f.task
	task.Stages = &Stages{Docs: &StageRef{Rubric: DefaultDocsRubric}}
	b := New("t")
	b.Grader, b.Scorer = "true", stubScorer(t)
	in := stageInput{task: task, b: b, dir: t.TempDir()}
	s := StageRecord{Score: -1}
	scoreDocs(context.Background(), in, &TaskRecord{}, &s)
	if s.Known() || !strings.Contains(s.Reason, "nothing to score") {
		t.Fatalf("no docs on either side = %+v", s)
	}
	in.docs = "+GOOD docs\n"
	s = StageRecord{Score: -1}
	scoreDocs(context.Background(), in, &TaskRecord{}, &s)
	if s.Score != 1 {
		t.Fatalf("graded docs = %+v", s)
	}
	cfg, _ := os.ReadFile(filepath.Join(in.dir, "stage-docs", "promptfooconfig.yaml"))
	if !strings.Contains(string(cfg), DefaultDocsRubric[0]) {
		t.Errorf("the docs rubric is not in the scorer's config:\n%s", cfg)
	}
	in.b = New("t")
	s = StageRecord{Score: -1}
	scoreDocs(context.Background(), in, &TaskRecord{}, &s)
	if s.Known() || !strings.Contains(s.Reason, "no grader:") {
		t.Fatalf("docs without a grader = %+v", s)
	}
}

func TestStageStatusAndSpend(t *testing.T) {
	wf := &tracker.WorkflowConfig{Statuses: []tracker.WorkflowStatus{{Name: "drafting", Role: "design"}, {Name: "building", Role: "work"}, {Name: "writing"}}}
	if got := StageStatus(wf, nil, StageDesign); got != "drafting" {
		t.Errorf("design = %q", got)
	}
	if got := StageStatus(wf, nil, StageCode); got != "building" {
		t.Errorf("code = %q", got)
	}
	if got := StageStatus(wf, nil, StageDocs); got != "" {
		t.Errorf("docs without an eval = %q", got)
	}
	ev := &tracker.WorkflowEval{ID: "f", Stages: map[string]tracker.EvalStage{StageDocs: {Status: "writing"}, StageDesign: {Status: "building"}}}
	if got := StageStatus(wf, ev, StageDocs); got != "writing" {
		t.Errorf("docs from the eval = %q", got)
	}
	if got := StageStatus(wf, ev, StageDesign); got != "building" {
		t.Errorf("the eval's status wins: %q", got)
	}
	if tok, known, wall := stageSpend(TaskRecord{}, "building"); known || tok != 0 || wall != 0 {
		t.Errorf("spend without a trace = %d %v %v", tok, known, wall)
	}
	traced := TaskRecord{Trace: []trace.Span{
		{Status: "building", WallSec: 60, Tokens: trace.Tokens{Known: true, In: 100, Out: 20, CacheRead: 5}},
		{Status: "testing", WallSec: 30, Tokens: trace.Tokens{Known: true, In: 999}},
		{Status: "building", WallSec: 15},
	}}
	if tok, known, wall := stageSpend(traced, "building"); !known || tok != 125 || wall != 75 {
		t.Errorf("spend from the trace = %d %v %v, want 125 true 75", tok, known, wall)
	}
	if _, known, wall := stageSpend(traced, ""); known || wall != 0 {
		t.Errorf("a stage with no status has no spend: %v %v", known, wall)
	}
}

func TestCompareStageScores(t *testing.T) {
	rec := func(task string, design, docs float64) TaskRecord {
		return TaskRecord{Task: task, Outcome: OutcomePass, Judge: -1, Stages: []StageRecord{
			{Stage: StageDesign, Status: "in design", Score: design, Pass: design >= 0.7, Judge: -1},
			{Stage: StageCode, Status: "in progress", Score: 1, Pass: true, Judge: -1},
			{Stage: StageDocs, Score: docs, Pass: docs >= 0.7, Judge: -1},
		}}
	}
	a := &Run{ID: "a", Benchmark: "t", Stop: StopDone, Tasks: []TaskRecord{rec("p/one", 0.5, -1), rec("p/two", 0.9, 0.8)}}
	b := &Run{ID: "b", Benchmark: "t", Stop: StopDone, Tasks: []TaskRecord{rec("p/one", 0.8, 0.6), rec("p/two", 1, 0.9)}}
	Summarize(a, a.Tasks)
	Summarize(b, b.Tasks)
	c := Compare(a, b)
	if len(c.StageScores) != 3 {
		t.Fatalf("stage scores = %+v", c.StageScores)
	}
	d := c.StageScores[0]
	if d.Stage != StageDesign || d.Status != "in design" || d.Tasks != 2 || d.A.Passed != 1 || d.B.Passed != 2 || d.A.Score != 0.7 || d.B.Score != 0.9 {
		t.Errorf("design = %+v", d)
	}
	docs := c.StageScores[2]
	if docs.A.Scored != 1 || docs.B.Scored != 2 || docs.A.Tokens != -1 {
		t.Errorf("docs = %+v", docs)
	}
	data, _ := json.Marshal(c)
	if !strings.Contains(string(data), `"stage_scores"`) {
		t.Errorf("--json lacks stage_scores: %s", data)
	}
	// Runs without stage records compare as before.
	a.Tasks[0].Stages, a.Tasks[1].Stages = nil, nil
	if c := Compare(a, b); c.StageScores != nil {
		t.Errorf("old runs got stage scores %+v", c.StageScores)
	}
}
