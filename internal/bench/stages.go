package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A golden task carries a reference per stage, so a stage can be scored on
// its own (docs/benchmark.md "A reference per stage"): the design the issue
// settled on, the code's hidden tests and check, and the docs' rubric with
// the docs the branch wrote. The references sit beside the snapshot in the
// owner's benchmark folder, never in a sandbox. Scoring a stage is the evals
// per stage step's: stage_score.go.

// Stage names, in the order a task's path meets them.
const (
	StageDesign = tracker.EvalStageDesign
	StageCode   = tracker.EvalStageCode
	StageDocs   = tracker.EvalStageDocs
)

// StageNames is every stage, in path order.
var StageNames = tracker.EvalStageNames

// DesignReferenceFile and DocsReferenceFile are the stage references' file
// names beside the snapshot (TaskDir).
const (
	DesignReferenceFile = "design.md"
	DocsReferenceFile   = "docs.diff"
)

// MaxStageReference caps a reference file, as the judge's diff is cut.
const MaxStageReference = 60 * 1024

// DefaultDocsRubric are the docs stage's rubric lines every task gets;
// --docs-rubric adds the task's own.
var DefaultDocsRubric = []string{
	"The changed docs say what the issue asks for, in the reader's words",
	"What the docs claim matches the code the branch wrote",
	"Every new command, flag or config key the change adds is documented",
}

// Stages are a task's references per stage.
type Stages struct {
	// Design is the issue's ## Design section as it stood when the issue
	// left its design status (Reference: design.md).
	Design *StageRef `yaml:"design,omitempty"`
	// Code is scored by the task's hidden tests and check; it has no file of
	// its own.
	Code *StageRef `yaml:"code,omitempty"`
	// Docs is the docs the branch changed (Reference: docs.diff) and the
	// rubric that grades a docs stage.
	Docs *StageRef `yaml:"docs,omitempty"`
	// Missing names a stage with no reference and why.
	Missing map[string]string `yaml:"missing,omitempty"`
}

// StageRef is one stage's reference: a file beside the snapshot, rubric
// lines, or both.
type StageRef struct {
	Reference string   `yaml:"reference,omitempty"`
	Rubric    []string `yaml:"rubric,omitempty"`
}

// StagePath is a stage reference's file, "" when it has none.
func StagePath(t Task, ref *StageRef) string {
	if ref == nil || ref.Reference == "" {
		return ""
	}
	return filepath.Join(TaskDir(t.Project, t.Slug), filepath.Base(ref.Reference))
}

// StagesLine is the task's stages in words for bench show and candidates:
// "design ✓ · code 3 tests + check · docs rubric 3, reference 2 files".
func StagesLine(t Task) string {
	if t.Stages == nil {
		return "no stage references (added before them: bench task add again to get them)"
	}
	var parts []string
	for _, name := range StageNames {
		if why, ok := t.Stages.Missing[name]; ok {
			parts = append(parts, name+" missing: "+why)
			continue
		}
		switch name {
		case StageDesign:
			if t.Stages.Design != nil {
				parts = append(parts, "design ✓")
			}
		case StageCode:
			s := "code " + plural(len(t.HiddenTests), "test")
			if t.Check != "" {
				s += " + check"
			}
			parts = append(parts, s)
		case StageDocs:
			if d := t.Stages.Docs; d != nil {
				s := fmt.Sprintf("docs rubric %d", len(d.Rubric))
				if d.Reference != "" {
					s += ", reference " + plural(len(t.DocsFiles), "file")
				}
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, " · ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// DocsFiles are the range's docs: the markdown files the branch's own
// commits changed, outside the work records (the issue folder, retros and
// session notes). issueDir is the issue folder relative to the repo, "" when
// it is outside it.
func DocsFiles(r Range, issueDir string) []string {
	var out []string
	for _, f := range r.Files {
		if !strings.HasSuffix(strings.ToLower(f), ".md") {
			continue
		}
		if under(f, issueDir) || under(f, "retros") || under(f, "sessions") {
			continue
		}
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func under(f, dir string) bool {
	dir = strings.Trim(filepath.ToSlash(dir), "/")
	return dir != "" && dir != "." && (f == dir || strings.HasPrefix(f, dir+"/"))
}

// StageReferences builds a task's stages and writes their files beside the
// snapshot: design.md from the issue's tracker history, docs.diff from the
// branch's docs. rubric is added to DefaultDocsRubric. A stage that can't
// have a reference is in Missing with why; nothing is invented.
func StageReferences(repo string, p *tracker.Project, issue *tracker.Issue, stats tracker.StatsStore, r Range, t *Task, rubric []string) error {
	st := &Stages{Code: &StageRef{}}
	dir := TaskDir(t.Project, t.Slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	missing := func(stage, why string) {
		if st.Missing == nil {
			st.Missing = map[string]string{}
		}
		st.Missing[stage] = why
	}

	design, why := designReference(p.LoadWorkflow(), issue, stats)
	path := filepath.Join(dir, DesignReferenceFile)
	if why != "" {
		missing(StageDesign, why)
		_ = os.Remove(path)
	} else {
		if err := os.WriteFile(path, []byte(cutRef(design)+"\n"), 0o644); err != nil {
			return err
		}
		st.Design = &StageRef{Reference: DesignReferenceFile}
	}

	issueDir := ""
	if rel, err := filepath.Rel(repo, p.IssueDir); err == nil && !strings.HasPrefix(rel, "..") {
		issueDir = rel
	}
	t.DocsFiles = DocsFiles(r, issueDir)
	docs := &StageRef{Rubric: append(append([]string{}, DefaultDocsRubric...), rubric...)}
	path = filepath.Join(dir, DocsReferenceFile)
	_ = os.Remove(path)
	if len(t.DocsFiles) > 0 {
		out, err := git(repo, append([]string{"diff", r.Base, r.tip(), "--"}, t.DocsFiles...)...)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(cutRef(out)), 0o644); err != nil {
			return err
		}
		docs.Reference = DocsReferenceFile
	}
	st.Docs = docs
	t.Stages = st
	return nil
}

// cutRef keeps a reference under MaxStageReference, marking the cut.
func cutRef(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= MaxStageReference {
		return s
	}
	return s[:MaxStageReference] + "\n… (cut at 60 kB)"
}

// designReference is the issue's ## Design section just after it left its
// first design status (role design), read from the tracker's git history;
// why says why there is none.
func designReference(wf *tracker.WorkflowConfig, issue *tracker.Issue, stats tracker.StatsStore) (string, string) {
	var left time.Time
	var from string
	for _, tr := range stats.Transitions {
		if wf.HasRole(tr.From, "design") {
			left, from = tr.TS, tr.From
			break
		}
	}
	if left.IsZero() {
		return "", "never went through a design status"
	}
	data, err := issueFileAt(issue, left)
	if err != nil {
		return "", err.Error()
	}
	text, ok := tracker.SectionText(string(data), "Design")
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Sprintf("no ## Design when it left %q", from)
	}
	return "## Design\n\n" + text, ""
}
