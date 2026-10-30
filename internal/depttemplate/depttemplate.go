// Package depttemplate is the library of department templates bundled in
// cmd/issue-cli/templates/departments: a department's workflow file, role
// prompt and goals skeleton, installed into a project by `issue-cli
// department add` (Plan, Apply) and taken out again by `department remove`
// (RemoveEntry). The viewer's Updates tab offers the templates a project
// hasn't installed. See docs/departments.md#templates.
package depttemplate

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/michal-franc/issue-viewer/cmd/issue-cli/templates"
	"github.com/michal-franc/issue-viewer/internal/jobs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/workflowupgrade"
)

const templatesDir = "departments"

// Template is one bundled department template: departments/<key>/
// department.yaml and its role.md.
type Template struct {
	// Key is the template's folder name: what `department add` takes.
	Key         string         `yaml:"-" json:"key"`
	Name        string         `yaml:"name" json:"name"`
	Description string         `yaml:"description" json:"description"`
	Color       string         `yaml:"color" json:"color,omitempty"`
	Role        string         `yaml:"role" json:"role,omitempty"`
	Mission     string         `yaml:"mission" json:"mission,omitempty"`
	Systems     []string       `yaml:"systems" json:"systems,omitempty"`
	Workflow    string         `yaml:"workflow" json:"workflow"`
	Goals       []tracker.Goal `yaml:"goals" json:"-"`
	// Jobs are the department's recurring jobs, off until the owner turns
	// a standing start on. Each brief: is a file in the template's folder
	// (jobs/<id>.md), installed as departments/<key>/jobs/<id>.md.
	Jobs []tracker.Job `yaml:"jobs" json:"jobs,omitempty"`
	// RolePrompt is role.md's content.
	RolePrompt []byte `yaml:"-" json:"-"`
	// JobBriefs are the jobs' brief files' content, by job id.
	JobBriefs map[string][]byte `yaml:"-" json:"-"`
}

// List is every bundled template, by key.
func List() []*Template {
	entries, err := fs.ReadDir(templates.FS, templatesDir)
	if err != nil {
		return nil
	}
	var out []*Template
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if t, err := Load(e.Name()); err == nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Keys are the bundled templates' keys.
func Keys() []string {
	var out []string
	for _, t := range List() {
		out = append(out, t.Key)
	}
	return out
}

// Load reads template key. Its department.yaml is decoded strictly: a key
// the loader doesn't know is an error (connectors: come with
// factory/department-connectors). Every job must parse and have its brief.
func Load(key string) (*Template, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" || strings.ContainsAny(key, "/\\.") {
		return nil, fmt.Errorf("no department template %q", key)
	}
	raw, err := fs.ReadFile(templates.FS, path.Join(templatesDir, key, "department.yaml"))
	if err != nil {
		return nil, fmt.Errorf("no department template %q", key)
	}
	t := &Template{Key: key}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(t); err != nil {
		return nil, fmt.Errorf("department template %s: %w", key, err)
	}
	if strings.TrimSpace(t.Name) == "" {
		return nil, fmt.Errorf("department template %s: no name", key)
	}
	if _, err := workflowupgrade.ReadTemplate(t.Workflow); err != nil {
		return nil, fmt.Errorf("department template %s: workflow %q is not a bundled workflow template", key, t.Workflow)
	}
	if t.RolePrompt, err = fs.ReadFile(templates.FS, path.Join(templatesDir, key, "role.md")); err != nil {
		return nil, fmt.Errorf("department template %s: no role.md", key)
	}
	t.JobBriefs = map[string][]byte{}
	for _, j := range t.Jobs {
		if _, err := jobs.Parse(j); err != nil {
			return nil, fmt.Errorf("department template %s: job %s: %w", key, j.ID, err)
		}
		brief, err := fs.ReadFile(templates.FS, path.Join(templatesDir, key, j.Brief))
		if err != nil || strings.TrimSpace(j.ID) == "" {
			return nil, fmt.Errorf("department template %s: job %q: no brief %s", key, j.ID, j.Brief)
		}
		t.JobBriefs[j.ID] = brief
	}
	return t, nil
}

// Installed reports whether proj has a department by t's name (any case).
func (t *Template) Installed(proj *tracker.Project) bool {
	return proj.DepartmentByKey(t.Name) != nil
}

// File states in a plan.
const (
	FileWrite = "write" // missing: Apply writes it
	FileSame  = "same"  // already there with the template's content
	FileKeep  = "keep"  // already there and different: kept as it is
)

// FileAction is one file a plan writes or leaves alone.
type FileAction struct {
	// Path is relative to the project root, with forward slashes.
	Path  string `json:"path"`
	State string `json:"state"`
	abs   string
	data  []byte
}

// Plan is what installing a template into a project does.
type Plan struct {
	Template *Template    `json:"template"`
	Name     string       `json:"name"`
	Key      string       `json:"key"`
	Root     string       `json:"root"`
	Config   string       `json:"config"`
	Files    []FileAction `json:"files"`
	// Entry is the departments: item added to the project in projects.yaml.
	Entry string `json:"entry"`
}

// NewPlan plans installing t into proj as department name ("" is the
// template's own name). It refuses HQ, Product and a department proj
// already has.
func NewPlan(proj *tracker.Project, t *Template, name string) (*Plan, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = t.Name
	}
	key := tracker.DepartmentKey(name)
	switch {
	case key == "":
		return nil, fmt.Errorf("department name %q has no letters or digits", name)
	case key == "hq" || key == strings.ToLower(tracker.ProductName):
		return nil, fmt.Errorf("%s is built in: every project has HQ and Product (Product runs the development workflow). Give its mission, prompt and goals in projects.yaml (docs/departments.md)", name)
	case proj.DepartmentByKey(name) != nil:
		return nil, fmt.Errorf("department %s is already in projects.yaml (%s); pick another name with --name, or remove it first: issue-cli department remove %s", proj.DepartmentByKey(name).Name, proj.ConfigPath, key)
	}
	if proj.ConfigPath == "" {
		return nil, fmt.Errorf("this project has no projects.yaml entry to add the department to; run with --project <name> and a projects config")
	}
	wf, err := workflowupgrade.ReadTemplate(t.Workflow)
	if err != nil {
		return nil, err
	}
	p := &Plan{Template: t, Name: name, Key: key, Root: proj.Root(), Config: proj.ConfigPath}
	workflowRel := "workflows/" + key + ".yaml"
	promptRel := "departments/" + key + "/role.md"
	files := []struct {
		rel  string
		data []byte
	}{{workflowRel, wf}, {promptRel, t.RolePrompt}}
	briefs := map[string]string{}
	for _, j := range t.Jobs {
		briefs[j.ID] = "departments/" + key + "/jobs/" + j.ID + ".md"
		files = append(files, struct {
			rel  string
			data []byte
		}{briefs[j.ID], t.JobBriefs[j.ID]})
	}
	for _, f := range files {
		a := FileAction{Path: f.rel, abs: filepath.Join(p.Root, filepath.FromSlash(f.rel)), data: f.data, State: FileWrite}
		if have, err := os.ReadFile(a.abs); err == nil {
			a.State = FileKeep
			if bytes.Equal(have, f.data) {
				a.State = FileSame
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading %s: %w", a.abs, err)
		}
		p.Files = append(p.Files, a)
	}
	entry, err := renderEntry(t, name, workflowRel, promptRel, briefs)
	if err != nil {
		return nil, err
	}
	p.Entry = entry
	return p, nil
}

// Apply writes the plan's missing files, then adds the entry to the
// project in projects.yaml. A file that is already there is never touched.
func (p *Plan) Apply(proj *tracker.Project) error {
	for _, f := range p.Files {
		if f.State != FileWrite {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.abs), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(f.abs), err)
		}
		// O_EXCL: a file that appeared since the plan is still never
		// overwritten.
		fh, err := os.OpenFile(f.abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("writing %s: %w", f.abs, err)
		}
		_, werr := fh.Write(f.data)
		if cerr := fh.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fmt.Errorf("writing %s: %w", f.abs, werr)
		}
	}
	return AddEntry(p.Config, proj.Slug, p.Name, p.Entry)
}

// entry is a departments: item as projects.yaml spells it.
type entry struct {
	Name     string      `yaml:"name"`
	Color    string      `yaml:"color,omitempty"`
	Role     string      `yaml:"role,omitempty"`
	Mission  string      `yaml:"mission,omitempty"`
	Systems  []string    `yaml:"systems,flow,omitempty"`
	Prompt   string      `yaml:"prompt,omitempty"`
	Workflow string      `yaml:"workflow,omitempty"`
	Goals    []entryGoal `yaml:"goals,omitempty"`
	Jobs     []entryJob  `yaml:"jobs,omitempty"`
}

type entryJob struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name,omitempty"`
	Every    string `yaml:"every"`
	On       string `yaml:"on,omitempty"`
	At       string `yaml:"at,omitempty"`
	Brief    string `yaml:"brief"`
	Priority string `yaml:"priority,omitempty"`
	Overlap  bool   `yaml:"overlap,omitempty"`
}

type entryGoal struct {
	ID        string   `yaml:"id"`
	Name      string   `yaml:"name,omitempty"`
	Target    *float64 `yaml:"target,omitempty"`
	Unit      string   `yaml:"unit,omitempty"`
	Period    string   `yaml:"period,omitempty"`
	Direction string   `yaml:"direction,omitempty"`
	Source    string   `yaml:"source,omitempty"`
	RollsUp   string   `yaml:"rolls_up,omitempty"`
}

// renderEntry is the department's departments: item, starting "- name:",
// indented by two spaces per level. briefs are the jobs' installed brief
// paths, by job id.
func renderEntry(t *Template, name, workflow, prompt string, briefs map[string]string) (string, error) {
	e := entry{Name: name, Color: t.Color, Role: t.Role, Mission: t.Mission, Systems: t.Systems, Prompt: prompt, Workflow: workflow}
	for _, g := range t.Goals {
		e.Goals = append(e.Goals, entryGoal{ID: g.ID, Name: g.Name, Target: g.Target, Unit: g.Unit, Period: g.Period, Direction: g.Direction, Source: g.Source, RollsUp: g.RollsUp})
	}
	for _, j := range t.Jobs {
		e.Jobs = append(e.Jobs, entryJob{ID: j.ID, Name: j.Name, Every: j.Every, On: j.On, At: j.At, Brief: briefs[j.ID], Priority: j.Priority, Overlap: j.Overlap})
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode([]entry{e}); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}
