package bench

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// Marks of a workflow in a comparison with a branch (WorkflowVerdict.Mark).
const (
	MarkChanged = "changed"
	MarkControl = "control"
)

// templateDir is where the bundled workflow templates live in the engine
// repo; a change to <name>.yaml there reaches every workflow stamped
// template: <name>@….
const templateDir = "cmd/issue-cli/templates/workflow/"

// engineCode are the paths whose change reaches every workflow: the CLI the
// agents run, its packages, the module and the agents' instructions.
var engineCode = []string{"cmd/issue-cli/", "internal/", "go.mod", "go.sum", "CLAUDE.md", "AGENTS.md"}

// WorkflowSource is one workflow of a project, as a branch's files reach it.
type WorkflowSource struct {
	// Key is the box key: product, or the department's key.
	Key string
	// File is the workflow file, relative to the repository root ("" when
	// it is outside the repo or the built-in default).
	File string
	// Template is the name in the file's template: stamp ("" unstamped).
	Template string
	// Prompt is the department's prompt: file, repo-relative.
	Prompt string
	// Folder is the department's departments/<key>/ folder, repo-relative.
	Folder string
}

// WorkflowSources are p's workflows as seen from the repository at top:
// Product's file, then each department with a workflow: of its own (one
// without runs on Product's file, so it is not a workflow of its own).
// read gives a repo-relative file's content as the branch has it (nil, nil
// when missing); a stamp is read from it.
func WorkflowSources(p *tracker.Project, top string, read func(rel string) ([]byte, error)) []WorkflowSource {
	rel := func(path string) string {
		if path == "" {
			return ""
		}
		if !filepath.IsAbs(path) {
			if root := p.TelemetryRoot(); root != "" {
				path = filepath.Join(root, path)
			}
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return ""
		}
		if r, err := filepath.Rel(resolve(top), resolve(abs)); err == nil && r != "." && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
		return ""
	}
	stamp := func(file string) string {
		if file == "" || read == nil {
			return ""
		}
		data, err := read(file)
		if err != nil || data == nil {
			return ""
		}
		name, _, ok := workflowupgrade.ParseStamp(workflowupgrade.ReadStamp(data))
		if !ok {
			return ""
		}
		return name
	}
	// A project on the built-in default, or with no project at all, would
	// read workflow.yaml at the root once the branch adds one.
	product := "workflow.yaml"
	if p != nil {
		if path := p.WorkflowPath(); path != "" {
			product = rel(path)
		}
	}
	out := []WorkflowSource{{Key: ProductWorkflow, File: product, Template: stamp(product)}}
	if p == nil {
		return out
	}
	for _, d := range p.WorkflowDepartments() {
		key := tracker.DepartmentKey(d.Name)
		f := rel(p.DepartmentWorkflowPath(d.Name))
		s := WorkflowSource{Key: key, File: f, Template: stamp(f), Folder: "departments/" + key + "/"}
		if pr := strings.TrimSpace(d.Prompt); pr != "" {
			s.Prompt = rel(pr)
		}
		out = append(out, s)
	}
	return out
}

// Affected is what a branch's files reach: each changed workflow with the
// files that changed it, the files that reach every workflow, and the files
// no workflow reads.
type Affected struct {
	Ref       string              `json:"ref"`
	Changed   map[string][]string `json:"changed"`
	All       []string            `json:"all"`
	NotMapped []string            `json:"not_mapped"`
}

// Mark is workflow key's mark and the files behind it: changed with the
// files when a file reached it (its own or one reaching all), else control.
func (a Affected) Mark(key string) (string, []string) {
	files := append(append([]string{}, a.Changed[key]...), a.All...)
	if len(files) == 0 {
		return MarkControl, nil
	}
	sort.Strings(files)
	return MarkChanged, files
}

// Files is the number of files mapped and not.
func (a Affected) Files() int {
	n := len(a.All) + len(a.NotMapped)
	seen := map[string]bool{}
	for _, fs := range a.Changed {
		for _, f := range fs {
			if !seen[f] {
				seen[f] = true
				n++
			}
		}
	}
	return n
}

// AffectedWorkflows maps changed (repo-relative files) to the workflows of
// sources by the first rule that matches (D-1): a workflow's own file; a
// department's prompt or departments/<key>/ folder; a bundled template to
// every workflow stamped with its name; engine code to every workflow; else
// not mapped. Work records are the caller's to drop first
// (experiment.WorkRecords).
func AffectedWorkflows(changed []string, sources []WorkflowSource) Affected {
	a := Affected{Changed: map[string][]string{}, All: []string{}, NotMapped: []string{}}
	add := func(key, f string) { a.Changed[key] = append(a.Changed[key], f) }
	for _, f := range changed {
		f = strings.TrimSpace(filepath.ToSlash(f))
		if f == "" {
			continue
		}
		hit := false
		for _, s := range sources {
			if s.File != "" && f == s.File {
				add(s.Key, f)
				hit = true
			}
		}
		if !hit {
			for _, s := range sources {
				if (s.Prompt != "" && f == s.Prompt) || (s.Folder != "" && strings.HasPrefix(f, s.Folder)) {
					add(s.Key, f)
					hit = true
				}
			}
		}
		if !hit && strings.HasPrefix(f, templateDir) && strings.HasSuffix(f, ".yaml") {
			name := strings.TrimSuffix(strings.TrimPrefix(f, templateDir), ".yaml")
			for _, s := range sources {
				if s.Template != "" && s.Template == name {
					add(s.Key, f)
					hit = true
				}
			}
			if !hit {
				// A template no workflow of the project is stamped with.
				a.NotMapped = append(a.NotMapped, f)
				continue
			}
		}
		if !hit && isEngineCode(f) {
			a.All = append(a.All, f)
			hit = true
		}
		if !hit {
			a.NotMapped = append(a.NotMapped, f)
		}
	}
	return a
}

// resolve follows the links in path's longest existing prefix (a file the
// branch adds needn't exist here; on macOS /var is a link to /private/var).
func resolve(path string) string {
	rest := ""
	for p := path; ; {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return path
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

func isEngineCode(f string) bool {
	for _, e := range engineCode {
		if strings.HasSuffix(e, "/") {
			if strings.HasPrefix(f, e) {
				return true
			}
		} else if f == e {
			return true
		}
	}
	return false
}

// MarkChanged marks each of c's workflows changed or control by a, and
// records the branch and its unmapped files.
func (c *Comparison) MarkChanged(a Affected) {
	c.Branch = a.Ref
	c.NotMapped = a.NotMapped
	if c.NotMapped == nil {
		c.NotMapped = []string{}
	}
	MarkWorkflows(c.Workflows, a)
}

// MarkWorkflows marks each verdict changed or control by a.
func MarkWorkflows(vs []WorkflowVerdict, a Affected) {
	for i := range vs {
		vs[i].Mark, vs[i].ChangedBy = a.Mark(vs[i].Workflow)
	}
}

// NoisyControls names the control workflows whose verdict is net negative:
// the change wasn't meant to reach them, so the runs are noisy or it does.
func NoisyControls(vs []WorkflowVerdict) []string {
	var out []string
	for _, v := range vs {
		if v.Mark == MarkControl && v.Verdict == NetNegative {
			out = append(out, fmt.Sprintf("control %s is net negative: the runs are noisy or the change reaches it", v.Workflow))
		}
	}
	return out
}

// FilesText is files for one line: up to five, then "+N more".
func FilesText(files []string) string {
	if len(files) <= 5 {
		return strings.Join(files, ", ")
	}
	return strings.Join(files[:5], ", ") + fmt.Sprintf(" +%d more", len(files)-5)
}
