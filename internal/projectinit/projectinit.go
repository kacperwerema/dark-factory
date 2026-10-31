// Package projectinit plans and makes a new project: its folder with
// issues/, docs/ and workflow.yaml from a bundled template, and its
// projects.yaml entry, printed or added to a config the caller names
// (issue-cli project init). It never overwrites a file: what is already
// there is reported and left alone, so a second run says what it found.
package projectinit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/internal/depttemplate"
	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

// DefaultTemplate is the workflow template a project gets without --template.
const DefaultTemplate = "development"

// States of a file or folder in the plan.
const (
	StateCreate = "create" // a folder init makes
	StateWrite  = "write"  // a file init writes
	StateThere  = "there"  // a folder already there
	StateSame   = "same"   // a file already there, the same as the template
	StateKeep   = "keep"   // a file already there that differs: left alone
)

// States of the config step.
const (
	ConfigPrint  = "print"  // no config given: the entry is printed only
	ConfigCreate = "create" // the given config doesn't exist: a new file
	ConfigAdd    = "add"    // the entry goes into the given config
	ConfigThere  = "there"  // the given config has this project already
)

// Options are what the caller asked for.
type Options struct {
	Dir      string
	Name     string
	Slug     string
	Template string
	// Config is the projects.yaml to add the entry to; "" prints it only.
	Config string
}

// Action is one folder or file of the project.
type Action struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"` // dir | file
	State string `json:"state"`
}

// ConfigAction is the projects.yaml step.
type ConfigAction struct {
	Path  string `json:"path,omitempty"`
	State string `json:"state"`
	// Entry is the "- name: …" item for the projects: list.
	Entry string `json:"entry"`
}

// Plan is everything init does, before it does it.
type Plan struct {
	Dir      string       `json:"dir"`
	Name     string       `json:"name"`
	Slug     string       `json:"slug"`
	Template string       `json:"template"`
	Files    []Action     `json:"files"`
	Config   ConfigAction `json:"config"`
	// GitRepo says whether Dir is inside a git work tree (workers need one).
	GitRepo bool `json:"git_repo"`

	workflow []byte
}

// entry is the projects.yaml item init writes.
type entry struct {
	Name     string `yaml:"name"`
	Slug     string `yaml:"slug"`
	Issues   string `yaml:"issues"`
	Docs     string `yaml:"docs"`
	Workflow string `yaml:"workflow"`
	WorkDir  string `yaml:"workdir"`
}

// IssueDir, DocsDir and WorkflowFile are the project's paths.
func (p *Plan) IssueDir() string     { return filepath.Join(p.Dir, "issues") }
func (p *Plan) DocsDir() string      { return filepath.Join(p.Dir, "docs") }
func (p *Plan) WorkflowFile() string { return filepath.Join(p.Dir, "workflow.yaml") }

// NewPlan looks at the folder and the config and says what init would do.
func NewPlan(o Options) (*Plan, error) {
	if strings.TrimSpace(o.Dir) == "" {
		return nil, errors.New("a folder is needed: issue-cli project init <dir>")
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = filepath.Base(dir)
	}
	slug := strings.TrimSpace(o.Slug)
	if slug == "" {
		slug = tracker.Slugify(name)
	}
	if slug == "" || slug != tracker.Slugify(slug) {
		return nil, fmt.Errorf("slug %q is not usable: lowercase letters, digits and dashes (pass --slug)", slug)
	}
	tmpl := o.Template
	if tmpl == "" {
		tmpl = DefaultTemplate
	}
	data, err := workflowupgrade.ReadTemplate(tmpl)
	if err != nil {
		return nil, fmt.Errorf("unknown template %q; templates: %s", tmpl, strings.Join(workflowupgrade.Templates(), ", "))
	}
	p := &Plan{Dir: dir, Name: name, Slug: slug, Template: tmpl, workflow: data}

	for _, d := range []string{dir, p.IssueDir(), p.DocsDir()} {
		state, err := dirState(d)
		if err != nil {
			return nil, err
		}
		p.Files = append(p.Files, Action{Path: d, Kind: "dir", State: state})
	}
	state, err := fileState(p.WorkflowFile(), data)
	if err != nil {
		return nil, err
	}
	p.Files = append(p.Files, Action{Path: p.WorkflowFile(), Kind: "file", State: state})
	p.GitRepo = inGitRepo(dir)

	p.Config.Entry, err = p.renderEntry()
	if err != nil {
		return nil, err
	}
	if err := p.planConfig(o.Config); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Plan) renderEntry() (string, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	e := entry{Name: p.Name, Slug: p.Slug, Issues: p.IssueDir(), Docs: p.DocsDir(), Workflow: p.WorkflowFile(), WorkDir: p.Dir}
	if err := enc.Encode([]entry{e}); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// planConfig decides the config step. A project with this slug already in
// the config is fine when it points at this folder, refused otherwise.
func (p *Plan) planConfig(config string) error {
	if config == "" {
		p.Config.State = ConfigPrint
		return nil
	}
	abs, err := filepath.Abs(config)
	if err != nil {
		return err
	}
	p.Config.Path = abs
	info, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		p.Config.State = ConfigCreate
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a folder; --config names a projects.yaml file", config)
	}
	cfg, err := tracker.LoadProjectsConfig(abs)
	if err != nil {
		return fmt.Errorf("%v; fix it or name another config with --config", err)
	}
	for _, q := range cfg.Projects {
		if q.Slug != p.Slug {
			continue
		}
		if samePath(q.IssueDir, p.IssueDir()) {
			p.Config.State = ConfigThere
			return nil
		}
		return fmt.Errorf("%s already has a project %s (issues: %s); pick another --slug", config, p.Slug, q.IssueDir)
	}
	p.Config.State = ConfigAdd
	return nil
}

// Apply makes the plan: folders, workflow.yaml, then the config. Nothing
// that is there already is written again.
func (p *Plan) Apply() error {
	for _, a := range p.Files {
		switch {
		case a.Kind == "dir" && a.State == StateCreate:
			if err := os.MkdirAll(a.Path, 0o755); err != nil {
				return err
			}
		case a.Kind == "file" && a.State == StateWrite:
			if err := writeNew(a.Path, p.workflow); err != nil {
				return err
			}
		}
	}
	check := func(q *tracker.Project) error {
		if !samePath(q.IssueDir, p.IssueDir()) {
			return fmt.Errorf("project %s in the result points at %s, not %s", p.Slug, q.IssueDir, p.IssueDir())
		}
		return nil
	}
	switch p.Config.State {
	case ConfigCreate:
		if err := os.MkdirAll(filepath.Dir(p.Config.Path), 0o755); err != nil {
			return err
		}
		if err := writeNew(p.Config.Path, []byte("projects:\n"+indent(p.Config.Entry, "  "))); err != nil {
			return err
		}
	case ConfigAdd:
		if err := depttemplate.AddProject(p.Config.Path, p.Slug, p.Config.Entry, check); err != nil {
			return err
		}
	}
	return nil
}

// writeNew writes a file that must not exist: never an overwrite, even
// when one appeared since the plan was made.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s appeared since the plan was made; left alone, run again", path)
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func dirState(path string) (string, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return StateCreate, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is a file, not a folder; left alone", path)
	}
	return StateThere, nil
}

func fileState(path string, want []byte) (string, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return StateWrite, nil
	}
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a folder, not a file; left alone", path)
	}
	have, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if bytes.Equal(have, want) {
		return StateSame, nil
	}
	return StateKeep, nil
}

// inGitRepo reports whether dir or a folder above it has a .git.
func inGitRepo(dir string) bool {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		up := filepath.Dir(d)
		if up == d {
			return false
		}
		d = up
	}
}

func samePath(a, b string) bool {
	clean := func(s string) string {
		s = filepath.Clean(s)
		if real, err := filepath.EvalSymlinks(s); err == nil {
			return real
		}
		return s
	}
	return clean(a) == clean(b)
}

func indent(s, pad string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(strings.TrimRight(s, "\n")+"\n", "\n") {
		if l != "" {
			b.WriteString(pad + l)
		}
	}
	return b.String()
}
