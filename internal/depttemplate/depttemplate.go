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
	// Group marks a group template: it installs the group, then each of
	// Children (template keys) under it with parent: <group>. A group may
	// have no workflow: (its own issues then run on Product's); GTM's runs
	// the planning workflow (its weekly plan).
	Group    bool     `yaml:"group" json:"group,omitempty"`
	Children []string `yaml:"children" json:"children,omitempty"`
	// RolePrompt is role.md's content.
	RolePrompt []byte `yaml:"-" json:"-"`
	// JobBriefs are the jobs' brief files' content, by job id.
	JobBriefs map[string][]byte `yaml:"-" json:"-"`
}

// List is every bundled template, by key: departments, then groups.
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
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return !out[i].Group
		}
		return out[i].Key < out[j].Key
	})
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
	// A group may have no workflow file: its own issues run on Product's.
	if !t.Group || t.Workflow != "" {
		if _, err := workflowupgrade.ReadTemplate(t.Workflow); err != nil {
			return nil, fmt.Errorf("department template %s: workflow %q is not a bundled workflow template", key, t.Workflow)
		}
	}
	if len(t.Children) > 0 && !t.Group {
		return nil, fmt.Errorf("department template %s: children: needs group: true", key)
	}
	for _, c := range t.Children {
		ct, err := Load(c)
		switch {
		case err != nil:
			return nil, fmt.Errorf("department template %s: child %s: %w", key, c, err)
		case ct.Group:
			return nil, fmt.Errorf("department template %s: child %s is a group: groups are one level", key, c)
		}
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

// InstalledAs is proj's department installed from t, nil when there is
// none: one by t's name (any case), or one whose workflow file is stamped
// with t's workflow (installed under another name, which --name allowed
// before a template installed once).
func (t *Template) InstalledAs(proj *tracker.Project) *tracker.Department {
	if d := proj.DepartmentByKey(t.Name); d != nil {
		return d
	}
	// A group is found by its name only: another group may run the same
	// workflow template (planning).
	if t.Workflow == "" || t.Group {
		return nil
	}
	for i := range proj.Departments {
		d := &proj.Departments[i]
		if d.Workflow == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proj.Root(), filepath.FromSlash(d.Workflow)))
		if err != nil {
			continue
		}
		if name, _, ok := workflowupgrade.ParseStamp(workflowupgrade.ReadStamp(raw)); ok && name == t.Workflow {
			return d
		}
	}
	return nil
}

// Installed reports whether proj has a department installed from t.
func (t *Template) Installed(proj *tracker.Project) bool {
	return t.InstalledAs(proj) != nil
}

// InstalledError is NewPlan's refusal of a template proj already has: a
// template installs once, under its own name.
type InstalledError struct {
	Template *Template
	Dept     *tracker.Department
	Project  *tracker.Project
}

func (e *InstalledError) Error() string {
	as := ""
	if !strings.EqualFold(e.Dept.Name, e.Template.Name) {
		as = " as " + e.Dept.Name
	}
	return fmt.Sprintf("%s is already a department of %s%s (%s): a template installs once. To add it again, remove it first: %s",
		e.Template.Name, e.Project.Name, as, e.Project.ConfigPath,
		tracker.InjectProject(e.Project, "issue-cli department remove "+tracker.DepartmentKey(e.Dept.Name)))
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
	// Parent is the group the department goes under ("" when none).
	Parent string `json:"parent,omitempty"`
	// Children are a group's departments to install with it; Reparent the
	// ones already in projects.yaml with no parent:, which get parent:
	// <group>; Notes say what is left as it is.
	Children []*Plan  `json:"children,omitempty"`
	Reparent []string `json:"reparent,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

// NewPlan plans installing t into proj under t's own name. It refuses HQ,
// Product and a template proj already has (*InstalledError). A group
// template's plan carries its children's.
func NewPlan(proj *tracker.Project, t *Template) (*Plan, error) {
	return NewPlanUnder(proj, t, "")
}

// NewPlanUnder is NewPlan with the department under group parent (a
// department proj has, "" for none). A group may go under a group too,
// while its departments stay within tracker.MaxGroupDepth.
func NewPlanUnder(proj *tracker.Project, t *Template, parent string) (*Plan, error) {
	if d := t.InstalledAs(proj); d != nil {
		return nil, &InstalledError{Template: t, Dept: d, Project: proj}
	}
	if parent = strings.TrimSpace(parent); parent != "" {
		g := proj.DepartmentByKey(parent)
		switch {
		case tracker.DepartmentKey(parent) == "hq" || tracker.DepartmentKey(parent) == strings.ToLower(tracker.ProductName):
			return nil, fmt.Errorf("%s is built in and never a group; groups: %s", parent, groupNames(proj))
		case g == nil:
			return nil, fmt.Errorf("no department %q to put it under; groups: %s", parent, groupNames(proj))
		}
		// Groups nest, at most tracker.MaxGroupDepth groups above a
		// department (factory/nested-groups-…#D-1).
		above, problem := proj.ParentChain(g.Name)
		if problem != "" {
			return nil, fmt.Errorf("%s's own parent: chain is broken (issue-cli health lint --check departments says why); fix it first", g.Name)
		}
		depth := len(above) + 1 // groups above the new department
		if t.Group {
			depth++ // its departments sit one deeper
		}
		if depth > tracker.MaxGroupDepth {
			where := "the department"
			if t.Group {
				where = t.Name + "'s departments"
			}
			return nil, fmt.Errorf("under %s, %s would sit %d groups deep: groups nest at most %d deep; put it under a group higher up (%s)", g.Name, where, depth, tracker.MaxGroupDepth, chainNames(g, above))
		}
		parent = tracker.DepartmentKey(g.Name)
	}
	p, err := newPlan(proj, t, parent)
	if err != nil || !t.Group {
		return p, err
	}
	for _, c := range t.Children {
		ct, err := Load(c)
		if err != nil {
			return nil, err
		}
		if d := ct.InstalledAs(proj); d != nil {
			switch have := strings.TrimSpace(d.Parent); {
			case have == "":
				p.Reparent = append(p.Reparent, d.Name)
			case tracker.DepartmentKey(have) != p.Key:
				p.Notes = append(p.Notes, fmt.Sprintf("%s is already under %s: left there (change its parent: to move it)", d.Name, have))
			default:
				p.Notes = append(p.Notes, fmt.Sprintf("%s is already under %s", d.Name, p.Name))
			}
			continue
		}
		cp, err := newPlan(proj, ct, p.Key)
		if err != nil {
			return nil, fmt.Errorf("child %s: %w", c, err)
		}
		p.Children = append(p.Children, cp)
	}
	return p, nil
}

// groupNames lists proj's groups for an error, "none yet" without one.
func groupNames(proj *tracker.Project) string {
	var out []string
	for _, g := range proj.Groups() {
		out = append(out, tracker.DepartmentKey(g.Name))
	}
	if len(out) == 0 {
		return "none yet (add one: issue-cli department add gtm)"
	}
	return strings.Join(out, ", ")
}

func newPlan(proj *tracker.Project, t *Template, parent string) (*Plan, error) {
	name := t.Name
	key := tracker.DepartmentKey(name)
	switch {
	case key == "":
		return nil, fmt.Errorf("department name %q has no letters or digits", name)
	case key == "hq" || key == strings.ToLower(tracker.ProductName):
		return nil, fmt.Errorf("%s is built in: every project has HQ and Product (Product runs the development workflow). Give its mission, prompt and goals in projects.yaml (docs/departments.md)", name)
	}
	if proj.ConfigPath == "" {
		return nil, fmt.Errorf("this project has no projects.yaml entry to add the department to; run with --project <name> and a projects config")
	}
	p := &Plan{Template: t, Name: name, Key: key, Root: proj.Root(), Config: proj.ConfigPath, Parent: parent}
	workflowRel := "workflows/" + key + ".yaml"
	promptRel := "departments/" + key + "/role.md"
	files := []struct {
		rel  string
		data []byte
	}{{promptRel, t.RolePrompt}}
	if t.Workflow == "" {
		workflowRel = ""
	} else {
		wf, err := workflowupgrade.ReadTemplate(t.Workflow)
		if err != nil {
			return nil, err
		}
		files = append([]struct {
			rel  string
			data []byte
		}{{workflowRel, wf}}, files...)
	}
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
	entry, err := renderEntry(t, name, parent, workflowRel, promptRel, briefs)
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
	if err := AddEntry(p.Config, proj.Slug, p.Name, p.Entry); err != nil {
		return err
	}
	for _, c := range p.Children {
		if err := c.Apply(proj); err != nil {
			return fmt.Errorf("%s added, but its department %s: %w", p.Name, c.Name, err)
		}
	}
	for _, name := range p.Reparent {
		if err := SetParent(p.Config, proj.Slug, name, p.Key); err != nil {
			return fmt.Errorf("%s added, but moving %s under it: %w", p.Name, name, err)
		}
	}
	return nil
}

// entry is a departments: item as projects.yaml spells it.
type entry struct {
	Name     string      `yaml:"name"`
	Parent   string      `yaml:"parent,omitempty"`
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
	Type     string `yaml:"type,omitempty"`
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
func renderEntry(t *Template, name, parent, workflow, prompt string, briefs map[string]string) (string, error) {
	e := entry{Name: name, Parent: parent, Color: t.Color, Role: t.Role, Mission: t.Mission, Systems: t.Systems, Prompt: prompt, Workflow: workflow}
	for _, g := range t.Goals {
		e.Goals = append(e.Goals, entryGoal{ID: g.ID, Name: g.Name, Target: g.Target, Unit: g.Unit, Period: g.Period, Direction: g.Direction, Source: g.Source, RollsUp: g.RollsUp})
	}
	for _, j := range t.Jobs {
		e.Jobs = append(e.Jobs, entryJob{ID: j.ID, Name: j.Name, Every: j.Every, On: j.On, At: j.At, Brief: briefs[j.ID], Priority: j.Priority, Type: j.Type, Overlap: j.Overlap})
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

// chainNames is group g and the groups above it, "Outbound → GTM".
func chainNames(g *tracker.Department, above []*tracker.Department) string {
	names := []string{g.Name}
	for _, a := range above {
		names = append(names, a.Name)
	}
	return strings.Join(names, " → ")
}
