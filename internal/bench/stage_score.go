package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Scores per stage (docs/benchmark.md, Scores per stage): after the worker
// stops, a task with stage references (Task.Stages, stages.go) has each
// stage scored against its reference: design by the judge and the rubric,
// code by the hidden tests and check, docs by the rubric. The records sit
// beside the task's outcome and never change it.

// StageRecord is one stage's score on a TaskRecord (run.json's stages).
type StageRecord struct {
	Stage string `json:"stage"`
	// Status is the workflow status the stage is (StageStatus), "" when
	// none names it.
	Status string `json:"status,omitempty"`
	// Score is 0-1, -1 when the stage could not be graded (Reason says why).
	Score float64 `json:"score"`
	// Pass is true only when the score is known and at the stage's bar.
	Pass bool `json:"pass"`
	// Reason is one line: why the score is unknown, what failed, or the
	// judge's reason. Never the graded text.
	Reason string `json:"reason,omitempty"`
	// Judge is the design judge's 0-5, -1 when no judge graded the stage.
	Judge int `json:"judge"`
	// Tokens and WallSec are the stage's spend: the task's trace spans of
	// its status. TokensKnown is false when no span of it had known tokens.
	Tokens      int64   `json:"tokens"`
	TokensKnown bool    `json:"tokens_known"`
	WallSec     float64 `json:"wall_sec"`
}

// Known reports whether the stage was graded.
func (s StageRecord) Known() bool { return s.Score >= 0 }

// DefaultDesignRubric grades a design when the task names no design
// rubric of its own; the eval's stage rubric adds to it.
var DefaultDesignRubric = []string{
	"The design does what the issue asks, as the REFERENCE design does",
	"It names the files, packages or commands it changes",
	"It says what is not included and how the change is tested",
}

// StageStatus is the workflow status stage is: the eval's
// stages.<stage>.status, else the design role's status for design and the
// work role's for code; docs has none without an eval entry.
func StageStatus(wf *tracker.WorkflowConfig, ev *tracker.WorkflowEval, stage string) string {
	if s := strings.TrimSpace(ev.Stage(stage).Status); s != "" {
		return s
	}
	switch stage {
	case StageDesign:
		return wf.RoleStatus(tracker.StatusRoleDesign)
	case StageCode:
		return wf.RoleStatus(tracker.StatusRoleWork)
	}
	return ""
}

// stageInput is what scoring the stages reads, gathered by runTask.
type stageInput struct {
	task  Task
	wf    *tracker.WorkflowConfig
	ev    *tracker.WorkflowEval
	b     *Benchmark
	dir   string // scoring folder (the sandbox)
	env   []string
	brief string
	// design is the worker's ## Design ("" none written); docs the
	// worker's markdown diff against the base.
	design, docs string
}

// scoreStages fills rec.Stages for a task with stage references; nothing
// for one without. rec's outcome is not touched.
func scoreStages(ctx context.Context, in stageInput, rec *TaskRecord) {
	st := in.task.Stages
	if st == nil {
		return
	}
	started := time.Now()
	for _, name := range StageNames {
		s := StageRecord{Stage: name, Status: StageStatus(in.wf, in.ev, name), Score: -1, Judge: -1}
		if why, ok := st.Missing[name]; ok {
			s.Reason = "no reference: " + why
		} else {
			switch name {
			case StageDesign:
				scoreDesign(ctx, in, rec, &s)
			case StageCode:
				scoreCode(in, rec, &s)
			case StageDocs:
				scoreDocs(ctx, in, rec, &s)
			}
		}
		if s.Known() {
			s.Pass = s.Score+1e-9 >= stageBar(in.ev, name)
		}
		s.Tokens, s.TokensKnown, s.WallSec = stageSpend(*rec, s.Status)
		rec.Stages = append(rec.Stages, s)
	}
	rec.ScoreWallSec += time.Since(started).Seconds()
}

// stageBar is a stage's pass bar: code passes only when everything does.
func stageBar(ev *tracker.WorkflowEval, stage string) float64 {
	if stage == StageCode {
		return 1
	}
	return ev.Stage(stage).PassBar()
}

// stageSpend is the stage's tokens and wall time: the sum over the task's
// trace spans of status. Tokens are known when any of those spans had them;
// without a trace (or a span of status) both are unknown, reported as 0.
func stageSpend(rec TaskRecord, status string) (int64, bool, float64) {
	var tok int64
	var known bool
	var wall float64
	for _, s := range rec.Trace {
		if status == "" || s.Status != status {
			continue
		}
		wall += s.WallSec
		if s.Tokens.Known {
			known = true
			tok += s.Tokens.Total()
		}
	}
	return tok, known, wall
}

func scoreCode(in stageInput, rec *TaskRecord, s *StageRecord) {
	switch {
	case rec.HiddenTotal == 0 && in.task.Check == "":
		s.Reason = "no hidden tests and no check"
		return
	case !rec.CheckOK:
		s.Score, s.Reason = 0, "the check fails"
		return
	case rec.HiddenTotal == 0:
		s.Score = 1
		return
	}
	s.Score = float64(rec.HiddenPassed) / float64(rec.HiddenTotal)
	if rec.HiddenPassed < rec.HiddenTotal {
		s.Reason = fmt.Sprintf("hidden tests %d/%d", rec.HiddenPassed, rec.HiddenTotal)
	}
}

func scoreDesign(ctx context.Context, in stageInput, rec *TaskRecord, s *StageRecord) {
	ref := readStageRef(in.task, in.task.Stages.Design)
	if strings.TrimSpace(in.design) == "" {
		s.Score, s.Reason = 0, "the run wrote no ## Design"
		return
	}
	var scores []float64
	var why []string
	if strings.TrimSpace(in.b.Judge) != "" {
		score, reason, u := runJudge(ctx, in.b, in.dir, DesignJudgePrompt(in.brief, ref, in.design))
		addUsage(&rec.Usage, u)
		if score >= 0 {
			s.Judge = score
			scores = append(scores, float64(score)/5)
			if reason != "" {
				why = append(why, "judge: "+reason)
			}
		} else {
			why = append(why, reason)
		}
	}
	rubric := in.task.Stages.Design.Rubric
	if len(rubric) == 0 {
		rubric = DefaultDesignRubric
	}
	if score, reason, ok := stageRubric(ctx, in, StageDesign, rubric, in.design, ref); ok {
		scores = append(scores, score)
		if reason != "" {
			why = append(why, reason)
		}
	} else if reason != "" {
		why = append(why, reason)
	}
	if len(scores) == 0 {
		if strings.TrimSpace(in.b.Judge) == "" && strings.TrimSpace(in.b.Grader) == "" {
			s.Reason = "not graded: the benchmark file has no judge: and no grader: (add one to score the design)"
		} else {
			s.Reason = "not graded: " + oneLine(strings.Join(why, "; "))
		}
		return
	}
	sum := 0.0
	for _, v := range scores {
		sum += v
	}
	s.Score = sum / float64(len(scores))
	s.Reason = oneLine(strings.Join(why, "; "))
}

func scoreDocs(ctx context.Context, in stageInput, rec *TaskRecord, s *StageRecord) {
	ref := readStageRef(in.task, in.task.Stages.Docs)
	if strings.TrimSpace(ref) == "" && strings.TrimSpace(in.docs) == "" {
		s.Reason = "nothing to score: the reference changed no docs and the run wrote none"
		return
	}
	if strings.TrimSpace(in.docs) == "" {
		s.Score, s.Reason = 0, "the run changed no docs"
		return
	}
	var rubric []string
	if d := in.task.Stages.Docs; d != nil {
		rubric = d.Rubric
	}
	if len(rubric) == 0 {
		rubric = DefaultDocsRubric
	}
	score, reason, ok := stageRubric(ctx, in, StageDocs, rubric, in.docs, ref)
	if !ok {
		s.Reason = reason
		return
	}
	s.Score, s.Reason = score, oneLine(reason)
}

// stageRubric grades text by the stage's rubric (the task's lines plus the
// eval's) through the scorer and the benchmark's grader. ok is false when
// nothing graded it, with why.
func stageRubric(ctx context.Context, in stageInput, stage string, rubric []string, text, ref string) (float64, string, bool) {
	if strings.TrimSpace(in.b.Grader) == "" {
		return 0, "rubric not graded: the benchmark file has no grader: (add one to score the " + stage + " rubric)", false
	}
	lines := append(append([]string{}, rubric...), in.ev.Stage(stage).Rubric...)
	id := stage
	if in.ev != nil && in.ev.ID != "" {
		id = in.ev.ID + "/" + stage
	}
	ev := tracker.WorkflowEval{ID: id, Score: tracker.EvalScoreDiff, Rubric: lines}
	res, err := Score(ctx, filepath.Join(in.dir, "stage-"+stage), in.b.Scorer,
		ScoreInput{Eval: ev, Output: cutDiff(text), Reference: cutDiff(ref), Brief: in.brief, Grader: in.b.Grader}, in.env)
	if err != nil {
		return 0, "rubric: " + oneLine(err.Error()), false
	}
	why := ""
	if len(res.Failed) > 0 {
		why = fmt.Sprintf("rubric %d/%d, failed: %s", res.Passed, res.Total, res.Failed[0])
	}
	return res.Score, why, true
}

// readStageRef is a stage reference's text, "" when it has no file.
func readStageRef(t Task, ref *StageRef) string {
	p := StagePath(t, ref)
	if p == "" {
		return ""
	}
	data, _ := os.ReadFile(p)
	return string(data)
}

// DesignJudgePrompt is what the design judge reads: the issue, the design
// the issue settled on and the run's design, each cut.
func DesignJudgePrompt(issue, reference, design string) string {
	cut := cutDiff
	return "You judge the design stage of one benchmark task. Compare the WORKER DESIGN with the REFERENCE DESIGN for the ISSUE: does it settle the approach the issue needs, as well as the reference? A different approach that meets the issue is fine.\n" +
		"Answer with exactly two lines:\nSCORE: <0-5>  (5 = as good or better, 0 = nothing useful)\nREASON: <one line>\n\n" +
		"## ISSUE\n\n" + cut(issue) + "\n\n## REFERENCE DESIGN\n\n" + cut(reference) + "\n\n## WORKER DESIGN\n\n" + cut(design) + "\n"
}

// runJudge runs the benchmark's judge on prompt: its 0-5 score (-1 none)
// with the reason (or why there is none) and its usage.
func runJudge(ctx context.Context, b *Benchmark, dir, prompt string) (int, string, Usage) {
	jctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, _, err := runWorker(jctx, dir, b.Judge, prompt, gitEnv(os.Environ()))
	if err != nil && len(out) == 0 {
		return -1, "judge: " + err.Error(), Usage{}
	}
	text, u := judgeText(b, out)
	m := judgeScore.FindStringSubmatch(text)
	if m == nil {
		return -1, "judge gave no SCORE line", u
	}
	score, _ := strconv.Atoi(m[1])
	reason := ""
	if r := judgeReason.FindStringSubmatch(text); r != nil {
		reason = strings.TrimSpace(r[1])
	}
	return score, reason, u
}

func addUsage(dst *Usage, u Usage) {
	dst.Input += u.Input
	dst.Output += u.Output
	dst.CacheRead += u.CacheRead
	dst.CacheWrite += u.CacheWrite
	dst.USD += u.USD
}

// oneLine keeps the first line of s, cut to 200 bytes.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// workerDocs is the worker's diff of markdown docs against base in work:
// the .md files it changed outside the work records (retros, sessions).
func workerDocs(work, base string) string {
	_, _ = git(work, "add", "-A", "-N")
	out, err := git(work, "diff", "--name-only", base)
	if err != nil {
		return ""
	}
	files := DocsFiles(Range{Files: strings.Fields(out)}, "issues")
	if len(files) == 0 {
		return ""
	}
	diff, _ := git(work, append([]string{"diff", base, "--"}, files...)...)
	return diff
}

// workerDesign is the sandbox issue's ## Design at the end of the run.
func workerDesign(issueDir string) string {
	issues, err := tracker.LoadIssues(issueDir)
	if err != nil || len(issues) != 1 {
		return ""
	}
	text, ok := tracker.SectionText(issues[0].BodyRaw, "Design")
	if !ok {
		return ""
	}
	return text
}

// StagesText is a record's stages in one line for bench report:
// "design 0.80 ✓ · code 3/3 ✓ · docs — (nothing to score)".
func StagesText(r TaskRecord) string {
	var parts []string
	for _, s := range r.Stages {
		switch {
		case !s.Known():
			parts = append(parts, s.Stage+" — ("+s.Reason+")")
		default:
			mark := "✗"
			if s.Pass {
				mark = "✓"
			}
			parts = append(parts, fmt.Sprintf("%s %.2f %s", s.Stage, s.Score, mark))
		}
	}
	return strings.Join(parts, " · ")
}

// StageScoreSide is one run's side of a stage in compare.
type StageScoreSide struct {
	// Scored of the tasks had a known score; Score is their mean (-1 none);
	// Passed of them passed.
	Scored int     `json:"scored"`
	Score  float64 `json:"score"`
	Passed int     `json:"passed"`
	// Tokens and WallSec are the mean per task with known spend (-1 none).
	Tokens  float64 `json:"tokens"`
	WallSec float64 `json:"wall_sec"`
}

// StageScoreDelta is one stage in both runs, over the tasks both scored
// stages for.
type StageScoreDelta struct {
	Stage  string         `json:"stage"`
	Status string         `json:"status,omitempty"`
	Tasks  int            `json:"tasks"`
	A      StageScoreSide `json:"a"`
	B      StageScoreSide `json:"b"`
}

// CompareStageScores is compare's scores per stage over the tasks with
// stage records in both runs; nil when there are none.
func CompareStageScores(a, b *Run) []StageScoreDelta {
	inA := map[string]TaskRecord{}
	for _, t := range a.Tasks {
		if len(t.Stages) > 0 {
			inA[t.Key()] = t
		}
	}
	type pair struct{ a, b []StageRecord }
	byStage := map[string]*pair{}
	status := map[string]string{}
	for _, tb := range b.Tasks {
		ta, ok := inA[tb.Key()]
		if !ok || len(tb.Stages) == 0 {
			continue
		}
		for _, s := range ta.Stages {
			if byStage[s.Stage] == nil {
				byStage[s.Stage] = &pair{}
			}
			byStage[s.Stage].a = append(byStage[s.Stage].a, s)
			if status[s.Stage] == "" {
				status[s.Stage] = s.Status
			}
		}
		for _, s := range tb.Stages {
			if byStage[s.Stage] == nil {
				byStage[s.Stage] = &pair{}
			}
			byStage[s.Stage].b = append(byStage[s.Stage].b, s)
			if s.Status != "" {
				status[s.Stage] = s.Status
			}
		}
	}
	var out []StageScoreDelta
	for _, name := range StageNames {
		p := byStage[name]
		if p == nil {
			continue
		}
		out = append(out, StageScoreDelta{Stage: name, Status: status[name], Tasks: max(len(p.a), len(p.b)),
			A: stageSide(p.a), B: stageSide(p.b)})
	}
	return out
}

func stageSide(recs []StageRecord) StageScoreSide {
	side := StageScoreSide{Score: -1, Tokens: -1, WallSec: -1}
	var sum, tok, wall float64
	spent := 0
	for _, s := range recs {
		if s.Known() {
			side.Scored++
			sum += s.Score
			if s.Pass {
				side.Passed++
			}
		}
		if s.TokensKnown {
			spent++
			tok += float64(s.Tokens)
			wall += s.WallSec
		}
	}
	if side.Scored > 0 {
		side.Score = sum / float64(side.Scored)
	}
	if spent > 0 {
		side.Tokens, side.WallSec = tok/float64(spent), wall/float64(spent)
	}
	return side
}
