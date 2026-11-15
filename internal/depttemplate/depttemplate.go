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
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/cmd/issue-cli/templates"
	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/partner"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
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
	// Optional are a group's departments that can join it later (template
	// keys): not installed with the group, named after its add, and a
	// department added beside the group without --parent hears it can go
	// under it.
	Optional []string `yaml:"optional" json:"optional,omitempty"`
	// Connections are the handoffs the department usually has, by box key
	// (the template's own department is its name's key): printed by
	// department add for the owner to declare under connections: in
	// projects.yaml, never written there (declaring any requests: makes
	// routing refuse every route not declared).
	Connections *tracker.Connections `yaml:"connections" json:"connections,omitempty"`
	// Partner is the outside program the department drives (Video's
	// OpenMontage): installed by the user, never by the engine. The entry
	// records its name, adapter, path and pinned commit only.
	Partner *TemplatePartner `yaml:"partner" json:"partner,omitempty"`
	// Connectors are the department's outside tools, config only: secrets
	// are references, every value the owner's. {{path}} in a command or
	// check is the partner's checkout.
	Connectors []tracker.Connector `yaml:"connectors" json:"connectors,omitempty"`
	// ConnectorExamples is a file in the template's folder holding a
	// connectors: list the department can use but doesn't get: department
	// add writes it into the entry commented out, for the owner to
	// uncomment (Marketing's openseo rankings source). Nothing in it runs or
	// costs anything until then.
	ConnectorExamples string `yaml:"connector_examples" json:"-"`
	// Install is a file in the template's folder: the steps department add
	// prints (and never runs) to install the partner, with {{path}},
	// {{commit}} and {{repo}} filled in.
	Install string `yaml:"install" json:"-"`
	// PartnerSlots are the partners catalog's slots this department fills
	// (analytics, email, …): department add names the catalog's rows for
	// them, and installs none (partner.Catalog, issue-cli department
	// partners).
	PartnerSlots []string `yaml:"partner_slots" json:"partner_slots,omitempty"`
	// Files are starter files in the template's folder the owner edits
	// (Marketing's subreddits.md), each installed as
	// departments/<key>/<file> like role.md, never overwriting one: a plain
	// file name, not role.md and not under jobs/.
	Files []string `yaml:"files" json:"files,omitempty"`
	// FileTexts are the Files' contents, by file name.
	FileTexts map[string][]byte `yaml:"-" json:"-"`
	// RolePrompt is role.md's content.
	RolePrompt []byte `yaml:"-" json:"-"`
	// ExamplesText is the connector_examples: file's content.
	ExamplesText []byte `yaml:"-" json:"-"`
	// JobBriefs are the jobs' brief files' content, by job id.
	JobBriefs map[string][]byte `yaml:"-" json:"-"`
	// InstallText is the Install file's content.
	InstallText []byte `yaml:"-" json:"-"`
}

// TemplatePartner is a template's partner: block: tracker.Partner's
// fields, plus where the user clones it from.
type TemplatePartner struct {
	Name    string `yaml:"name" json:"name"`
	Adapter string `yaml:"adapter" json:"adapter"`
	// Path is the default checkout path; department add --partner-path
	// picks another.
	Path   string `yaml:"path" json:"path"`
	Commit string `yaml:"commit" json:"commit"`
	// Repo is the clone URL the install steps name; not recorded.
	Repo string `yaml:"repo" json:"repo,omitempty"`
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
	if len(t.Optional) > 0 && !t.Group {
		return nil, fmt.Errorf("department template %s: optional: needs group: true", key)
	}
	for _, c := range t.Optional {
		if slices.Contains(t.Children, c) {
			return nil, fmt.Errorf("department template %s: %s is in both children: and optional:", key, c)
		}
		ct, err := Load(c)
		switch {
		case err != nil:
			return nil, fmt.Errorf("department template %s: optional %s: %w", key, c, err)
		case ct.Group:
			return nil, fmt.Errorf("department template %s: optional %s is a group: groups are one level", key, c)
		}
	}
	if err := t.checkConnections(); err != nil {
		return nil, err
	}
	if err := checkPartnerSlots(key, t.PartnerSlots); err != nil {
		return nil, err
	}
	if t.RolePrompt, err = fs.ReadFile(templates.FS, path.Join(templatesDir, key, "role.md")); err != nil {
		return nil, fmt.Errorf("department template %s: no role.md", key)
	}
	if err := t.loadPartner(); err != nil {
		return nil, err
	}
	if err := t.loadExamples(); err != nil {
		return nil, err
	}
	if err := t.loadFiles(); err != nil {
		return nil, err
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

// fileNameRe is what a template's files: entry may be: one plain file name.
var fileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// loadFiles reads the files: entries, each a plain file name in the
// template's folder, once, never role.md.
func (t *Template) loadFiles() error {
	t.FileTexts = map[string][]byte{}
	for _, f := range t.Files {
		switch {
		case !fileNameRe.MatchString(f) || strings.Contains(f, ".."):
			return fmt.Errorf("department template %s: files: %q is not a plain file name in the template's folder", t.Key, f)
		case f == "role.md":
			return fmt.Errorf("department template %s: files: role.md is installed already", t.Key)
		case t.FileTexts[f] != nil:
			return fmt.Errorf("department template %s: files: %s twice", t.Key, f)
		}
		data, err := fs.ReadFile(templates.FS, path.Join(templatesDir, t.Key, f))
		if err != nil {
			return fmt.Errorf("department template %s: files: no %s in the template's folder", t.Key, f)
		}
		t.FileTexts[f] = data
	}
	return nil
}

// checkPartnerSlots: each slot is one of the partners catalog's, once.
func checkPartnerSlots(key string, slots []string) error {
	for i, s := range slots {
		if !slices.Contains(partner.Slots, s) {
			return fmt.Errorf("department template %s: partner_slots: %q is not a slot of the partners catalog (%s)", key, s, strings.Join(partner.Slots, ", "))
		}
		if slices.Contains(slots[:i], s) {
			return fmt.Errorf("department template %s: partner_slots: %s twice", key, s)
		}
	}
	return nil
}

// checkConnections: every route has both ends, by box key, and one of them
// is the template's own department; via: is for requests only.
func (t *Template) checkConnections() error {
	if t.Connections == nil {
		return nil
	}
	own := tracker.DepartmentKey(t.Name)
	check := func(kind string, rs []tracker.Route) error {
		for _, r := range rs {
			from, to := strings.TrimSpace(r.From), strings.TrimSpace(r.To)
			switch {
			case from == "" || to == "" || from == to:
				return fmt.Errorf("department template %s: connections %s: a route needs from: and to:, two boxes", t.Key, kind)
			case from != own && to != own:
				return fmt.Errorf("department template %s: connections %s: %s → %s doesn't touch %s", t.Key, kind, from, to, own)
			case kind == "feeds" && strings.TrimSpace(r.Via) != "":
				return fmt.Errorf("department template %s: connections feeds: via: is for requests only", t.Key)
			}
		}
		return nil
	}
	if err := check("requests", t.Connections.Requests); err != nil {
		return err
	}
	return check("feeds", t.Connections.Feeds)
}

// loadExamples reads the connector_examples: file and checks it is a
// connectors: list of named connectors of a known kind. It is decoded
// loosely: the example may name a field newer than this build.
func (t *Template) loadExamples() error {
	if t.ConnectorExamples == "" {
		return nil
	}
	if len(t.Goals) == 0 && len(t.Jobs) == 0 && t.Partner == nil && len(t.Connectors) == 0 {
		return fmt.Errorf("department template %s: connector_examples: is written before the entry's goals:, jobs:, partner: or connectors:, and it has none", t.Key)
	}
	raw, err := fs.ReadFile(templates.FS, path.Join(templatesDir, t.Key, t.ConnectorExamples))
	if err != nil {
		return fmt.Errorf("department template %s: no connector_examples file %s", t.Key, t.ConnectorExamples)
	}
	var ex struct {
		Connectors []tracker.Connector `yaml:"connectors"`
	}
	if err := yaml.Unmarshal(raw, &ex); err != nil {
		return fmt.Errorf("department template %s: connector_examples %s: %w", t.Key, t.ConnectorExamples, err)
	}
	if len(ex.Connectors) == 0 {
		return fmt.Errorf("department template %s: connector_examples %s has no connectors:", t.Key, t.ConnectorExamples)
	}
	for _, c := range ex.Connectors {
		if !tracker.ConnectorNameRe.MatchString(c.Name) || !slices.Contains(tracker.ConnectorKinds, c.Kind) {
			return fmt.Errorf("department template %s: connector_examples %s: connector %q needs a name and kind %s", t.Key, t.ConnectorExamples, c.Name, strings.Join(tracker.ConnectorKinds, ", "))
		}
		if slices.ContainsFunc(t.Connectors, func(o tracker.Connector) bool { return o.Name == c.Name }) {
			return fmt.Errorf("department template %s: connector_examples %s: %s is also in connectors:", t.Key, t.ConnectorExamples, c.Name)
		}
	}
	t.ExamplesText = raw
	return nil
}

// loadPartner checks the partner:, connectors: and install: a template
// carries, and reads the install file.
func (t *Template) loadPartner() error {
	if pt := t.Partner; pt != nil {
		if _, err := partner.For(tracker.Partner{Name: pt.Name, Adapter: pt.Adapter}); err != nil {
			return fmt.Errorf("department template %s: %w", t.Key, err)
		}
		if strings.TrimSpace(pt.Path) == "" || strings.TrimSpace(pt.Commit) == "" {
			return fmt.Errorf("department template %s: partner: needs a default path: and a pinned commit:", t.Key)
		}
	}
	for _, c := range t.Connectors {
		if !tracker.ConnectorNameRe.MatchString(c.Name) {
			return fmt.Errorf("department template %s: connector name %q", t.Key, c.Name)
		}
		if !slices.Contains(tracker.ConnectorKinds, c.Kind) {
			return fmt.Errorf("department template %s: connector %s: kind %q (one of %s)", t.Key, c.Name, c.Kind, strings.Join(tracker.ConnectorKinds, ", "))
		}
		if t.Partner == nil && (strings.Contains(c.Command, "{{path}}") || strings.Contains(c.Check, "{{path}}")) {
			return fmt.Errorf("department template %s: connector %s names {{path}} but the template has no partner:", t.Key, c.Name)
		}
	}
	if t.Install == "" {
		return nil
	}
	if t.Partner == nil {
		return fmt.Errorf("department template %s: install: is the partner's steps, and there is no partner:", t.Key)
	}
	raw, err := fs.ReadFile(templates.FS, path.Join(templatesDir, t.Key, t.Install))
	if err != nil {
		return fmt.Errorf("department template %s: no install file %s", t.Key, t.Install)
	}
	t.InstallText = raw
	return nil
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
	// PartnerPath is the partner's checkout path the entry records ("" for
	// a template without a partner); Install is the steps to install it
	// there, printed and never run.
	PartnerPath string `json:"partner_path,omitempty"`
	Install     string `json:"install,omitempty"`
	// Connections is the template's handoffs as projects.yaml spells them
	// under connections: ("" for none): printed for the owner to declare,
	// never written.
	Connections string `json:"connections,omitempty"`
	// Optional are the group's departments that can join it later and
	// aren't in the project yet: "Sales (issue-cli department add
	// sales-b2c --parent gtm)".
	Optional []OptionalChild `json:"optional,omitempty"`
}

// OptionalChild is a department that can join a group later.
type OptionalChild struct {
	Template string `json:"template"`
	Name     string `json:"name"`
}

// Options are NewPlanWith's choices.
type Options struct {
	// Parent is the group the department goes under ("" for none).
	Parent string
	// PartnerPath is where the user installs the template's partner (""
	// for the template's default).
	PartnerPath string
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
	return NewPlanWith(proj, t, Options{Parent: parent})
}

// NewPlanWith is NewPlanUnder with the partner's path chosen too.
func NewPlanWith(proj *tracker.Project, t *Template, opts Options) (*Plan, error) {
	parent := opts.Parent
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
	p, err := newPlan(proj, t, parent, opts.PartnerPath)
	if err != nil {
		return nil, err
	}
	if !t.Group {
		if parent == "" {
			p.Notes = append(p.Notes, groupsTaking(proj, t)...)
		}
		return p, nil
	}
	for _, c := range t.Optional {
		ct, err := Load(c)
		if err != nil {
			return nil, err
		}
		if !ct.Installed(proj) {
			p.Optional = append(p.Optional, OptionalChild{Template: ct.Key, Name: ct.Name})
		}
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
		cp, err := newPlan(proj, ct, p.Key, "")
		if err != nil {
			return nil, fmt.Errorf("child %s: %w", c, err)
		}
		p.Children = append(p.Children, cp)
	}
	return p, nil
}

// groupsTaking are notes for t added with no parent: each installed group
// whose template lists t as an optional child, and how to put it there.
func groupsTaking(proj *tracker.Project, t *Template) []string {
	var out []string
	for _, g := range List() {
		if !g.Group || !slices.Contains(g.Optional, t.Key) {
			continue
		}
		d := g.InstalledAs(proj)
		if d == nil {
			continue
		}
		key := tracker.DepartmentKey(d.Name)
		out = append(out, fmt.Sprintf("%s is a group here and takes %s: add --parent %s to put it under %s (%s)", d.Name, t.Name, key, d.Name,
			tracker.InjectProject(proj, "issue-cli department add "+t.Key+" --parent "+key)))
	}
	return out
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

func newPlan(proj *tracker.Project, t *Template, parent, partnerPath string) (*Plan, error) {
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
	for _, f := range t.Files {
		files = append(files, struct {
			rel  string
			data []byte
		}{"departments/" + key + "/" + f, t.FileTexts[f]})
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
	var pt *tracker.Partner
	if t.Partner != nil {
		if partnerPath = strings.TrimSpace(partnerPath); partnerPath == "" {
			partnerPath = t.Partner.Path
		}
		pt = &tracker.Partner{Name: t.Partner.Name, Adapter: t.Partner.Adapter, Path: partnerPath, Commit: t.Partner.Commit}
		p.PartnerPath = partnerPath
		p.Install = fillPartner(string(t.InstallText), t.Partner, pt.Dir(p.Root))
	} else if strings.TrimSpace(partnerPath) != "" {
		return nil, fmt.Errorf("template %s has no partner: --partner-path is only for a template with one", t.Key)
	}
	entry, err := renderEntry(t, name, parent, workflowRel, promptRel, briefs, pt, p.Root)
	if err != nil {
		return nil, err
	}
	p.Entry = entry
	if p.Connections, err = renderConnections(proj, t.Connections); err != nil {
		return nil, err
	}
	return p, nil
}

// route is a declared connection as projects.yaml spells it, one flow line.
type route struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
	Via  string `yaml:"via,omitempty"`
	What string `yaml:"what,omitempty"`
}

// renderConnections is the connections: block for the template's handoffs
// proj doesn't declare yet, "" when there are none left.
func renderConnections(proj *tracker.Project, c *tracker.Connections) (string, error) {
	if c == nil {
		return "", nil
	}
	declared := func(have []tracker.Route, r tracker.Route) bool {
		for _, h := range have {
			if tracker.DepartmentKey(h.From) == r.From && tracker.DepartmentKey(h.To) == r.To {
				return true
			}
		}
		return false
	}
	var have tracker.Connections
	if proj.Connections != nil {
		have = *proj.Connections
	}
	type block struct {
		Requests []route `yaml:"requests,omitempty"`
		Feeds    []route `yaml:"feeds,omitempty"`
	}
	var b block
	for _, r := range c.Requests {
		if !declared(have.Requests, r) {
			b.Requests = append(b.Requests, route{From: r.From, To: r.To, Via: r.Via, What: r.What})
		}
	}
	for _, r := range c.Feeds {
		if !declared(have.Feeds, r) {
			b.Feeds = append(b.Feeds, route{From: r.From, To: r.To, What: r.What})
		}
	}
	if len(b.Requests)+len(b.Feeds) == 0 {
		return "", nil
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(struct {
		Connections block `yaml:"connections"`
	}{b}); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
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
	// Partner and Connectors come last: the outside program and tools.
	Partner    *entryPartner    `yaml:"partner,omitempty"`
	Connectors []entryConnector `yaml:"connectors,omitempty"`
}

// entryPartner is what the entry records of a partner: never more than
// its name, adapter, the path the user chose and the pinned commit.
type entryPartner struct {
	Name    string `yaml:"name,omitempty"`
	Adapter string `yaml:"adapter"`
	Path    string `yaml:"path"`
	Commit  string `yaml:"commit,omitempty"`
}

type entryConnector struct {
	Name     string            `yaml:"name"`
	Kind     string            `yaml:"kind"`
	Command  string            `yaml:"command,omitempty"`
	Args     []string          `yaml:"args,omitempty"`
	URL      string            `yaml:"url,omitempty"`
	Headers  map[string]string `yaml:"headers,omitempty"`
	Secrets  map[string]string `yaml:"secrets,omitempty"`
	Optional []string          `yaml:"optional,flow,omitempty"`
	Check    string            `yaml:"check,omitempty"`
	Sends    bool              `yaml:"sends,omitempty"`
	Note     string            `yaml:"note,omitempty"`
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
func renderEntry(t *Template, name, parent, workflow, prompt string, briefs map[string]string, pt *tracker.Partner, root string) (string, error) {
	e := entry{Name: name, Parent: parent, Color: t.Color, Role: t.Role, Mission: t.Mission, Systems: t.Systems, Prompt: prompt, Workflow: workflow}
	for _, g := range t.Goals {
		e.Goals = append(e.Goals, entryGoal{ID: g.ID, Name: g.Name, Target: g.Target, Unit: g.Unit, Period: g.Period, Direction: g.Direction, Source: g.Source, RollsUp: g.RollsUp})
	}
	for _, j := range t.Jobs {
		e.Jobs = append(e.Jobs, entryJob{ID: j.ID, Name: j.Name, Every: j.Every, On: j.On, At: j.At, Brief: briefs[j.ID], Priority: j.Priority, Type: j.Type, Overlap: j.Overlap})
	}
	dir := ""
	if pt != nil {
		e.Partner = &entryPartner{Name: pt.Name, Adapter: pt.Adapter, Path: pt.Path, Commit: pt.Commit}
		dir = pt.Dir(root)
	}
	for _, c := range t.Connectors {
		fill := func(s string) string { return strings.ReplaceAll(s, "{{path}}", dir) }
		ec := entryConnector{Name: c.Name, Kind: c.Kind, Command: fill(c.Command), URL: c.URL, Headers: c.Headers, Secrets: c.Secrets, Optional: c.Optional, Check: fill(c.Check), Sends: c.Sends, Note: c.Note}
		for _, a := range c.Args {
			ec.Args = append(ec.Args, fill(a))
		}
		e.Connectors = append(e.Connectors, ec)
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
	return withExamples(b.String(), t.ExamplesText), nil
}

// withExamples writes examples into the rendered entry, every line
// commented, right before its goals:, jobs:, partner: or connectors: key:
// inside the item, so adding another department after it or removing it
// (both by the item's last line) keeps the comments with the entry.
func withExamples(item string, examples []byte) string {
	if len(bytes.TrimSpace(examples)) == 0 {
		return item
	}
	var block strings.Builder
	for _, l := range strings.Split(strings.TrimRight(string(examples), "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			block.WriteString("  #\n")
			continue
		}
		block.WriteString("  # " + l + "\n")
	}
	lines := strings.SplitAfter(item, "\n")
	for i, l := range lines {
		for _, k := range []string{"goals:", "jobs:", "partner:", "connectors:"} {
			if strings.HasPrefix(l, "  "+k) {
				return strings.Join(lines[:i], "") + block.String() + strings.Join(lines[i:], "")
			}
		}
	}
	return item
}

// chainNames is group g and the groups above it, "Outbound → GTM".
func chainNames(g *tracker.Department, above []*tracker.Department) string {
	names := []string{g.Name}
	for _, a := range above {
		names = append(names, a.Name)
	}
	return strings.Join(names, " → ")
}

// fillPartner is an install file with {{path}} (the checkout, absolute),
// {{commit}} and {{repo}} filled in.
func fillPartner(text string, pt *TemplatePartner, dir string) string {
	return strings.NewReplacer("{{path}}", dir, "{{commit}}", pt.Commit, "{{repo}}", pt.Repo).Replace(text)
}
