package viewer

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/depttemplate"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Adding a department from the viewer: the company page's New department
// tile (a form over the bundled templates), the preview page
// (GET …/departments/new: what `issue-cli department add --dry-run` prints)
// and Add (POST …/departments/add: Plan.Apply, then the project is read
// again from projects.yaml so the new room shows without a restart). The
// installing is internal/depttemplate, the same as the CLI's.
// Design: issues/UI/the-company-pages-new-department-tile-adds-a-department-from-a-template.md.

// WbNewDeptTile is the company page's New department tile. With no
// FormURL (the project has no projects.yaml) or no Templates (every one is
// added) it is a static tile saying so.
type WbNewDeptTile struct {
	Tile      WbNewTile
	FormURL   string
	Templates []WbDeptChoice
	// Groups are the project's groups: with one, the form asks which a
	// new department goes under.
	Groups []WbDeptGroup
}

// WbDeptGroup is one group the tile's "Under which group?" offers.
type WbDeptGroup struct{ Key, Name string }

// WbDeptChoice is one bundled template the project hasn't added, in the
// tile's form.
type WbDeptChoice struct {
	Key, Name, Description, Workflow string
	Goals, Jobs                      int
	Checked                          bool
	// Children are a group template's departments (their names, joined);
	// "" for a department template.
	Children string
}

// DeptNewData is the preview page.
type DeptNewData struct {
	Header                 WbHeader
	Project                string
	CompanyURL, AddURL     string
	Template, TemplateName string
	// Parent is the group the department goes under ("" none).
	Parent string
	// Error is why the department can't be added as asked; Plan is then nil.
	Error string
	Plan  *DeptNewPlan
}

// DeptNewPlan is a depttemplate.Plan as the page shows it.
type DeptNewPlan struct {
	Name, Key, Root, Config, Entry string
	Files                          []DeptNewFile
	// Parent is the group it goes under; Children a group's departments
	// installed with it; Reparent the ones already there moved under it;
	// Notes what is left as it is.
	Parent          string
	Children        []DeptNewPlan
	Reparent, Notes []string
}

// DeptNewFile is one file of the plan; Word says what Add does with it.
type DeptNewFile struct {
	Path, State, Word string
}

// deptFileWords are the plan's file states in the page's words, as
// `department add --dry-run` says them.
var deptFileWords = map[string]string{
	depttemplate.FileWrite: "written",
	depttemplate.FileSame:  "already there, same as the template",
	depttemplate.FileKeep:  "your edited file: kept, not overwritten",
}

// newDeptTile is the tile for p: the form when p has a projects.yaml.
func newDeptTile(p *tracker.Project, prefix string) WbNewDeptTile {
	t := WbNewDeptTile{Tile: WbNewTile{Title: "New department", TestID: "new_department"}}
	if p.ConfigPath == "" {
		t.Tile.Sub = "From a template: " + strings.Join(templateNames(), " · ")
		t.Tile.Note = "This project has no projects.yaml entry to add a department to. Register it in projects.yaml, then add one here or with issue-cli department add <template>."
		return t
	}
	// A template installs once: only the ones p hasn't added are offered.
	var left []string
	for _, tm := range depttemplate.List() {
		if tm.Installed(p) {
			continue
		}
		c := WbDeptChoice{Key: tm.Key, Name: tm.Name, Description: tm.Description, Workflow: tm.Workflow, Goals: len(tm.Goals), Jobs: len(tm.Jobs)}
		var kids []string
		for _, k := range tm.Children {
			if ct, err := depttemplate.Load(k); err == nil {
				kids = append(kids, ct.Name)
			}
		}
		c.Children = strings.Join(kids, ", ")
		t.Templates = append(t.Templates, c)
		left = append(left, tm.Name)
	}
	if len(t.Templates) == 0 {
		t.Tile.Sub = "Every department template is added"
		t.Tile.Note = "A template installs once. To add one again, remove it first with issue-cli department remove <name>."
		return t
	}
	t.FormURL = prefix + "/departments/new"
	for _, g := range p.Groups() {
		t.Groups = append(t.Groups, WbDeptGroup{Key: tracker.DepartmentKey(g.Name), Name: g.Name})
	}
	t.Templates[0].Checked = true
	t.Tile.Sub = "From a template: " + strings.Join(left, " · ")
	return t
}

// templateNames are the bundled templates' names: the only departments
// the viewer adds.
func templateNames() []string {
	var out []string
	for _, tm := range depttemplate.List() {
		out = append(out, tm.Name)
	}
	return out
}

// planDepartment is NewPlanUnder for template key: it refuses a template p
// already has (*depttemplate.InstalledError), as `department add` does. A
// group template ignores parent: the form asks for a group whichever
// template is picked.
func planDepartment(p *tracker.Project, key, parent string) (*depttemplate.Template, *depttemplate.Plan, error) {
	t, err := depttemplate.Load(key)
	if err != nil {
		return nil, nil, fmt.Errorf("%v; templates: %s", err, strings.Join(depttemplate.Keys(), ", "))
	}
	if t.Group {
		parent = ""
	}
	plan, err := depttemplate.NewPlanUnder(p, t, parent)
	return t, plan, err
}

// deptNewPlan is plan as the preview page shows it.
func deptNewPlan(plan *depttemplate.Plan) *DeptNewPlan {
	d := &DeptNewPlan{Name: plan.Name, Key: plan.Key, Root: plan.Root, Config: plan.Config, Entry: plan.Entry, Parent: plan.Parent, Reparent: plan.Reparent, Notes: plan.Notes}
	for _, f := range plan.Files {
		d.Files = append(d.Files, DeptNewFile{Path: f.Path, State: f.State, Word: deptFileWords[f.State]})
	}
	for _, c := range plan.Children {
		d.Children = append(d.Children, *deptNewPlan(c))
	}
	return d
}

// buildDeptNew is the preview page for template key under group parent.
func (s *Server) buildDeptNew(p *tracker.Project, prefix, key, parent string) DeptNewData {
	company := s.buildCompany(p, prefix, time.Now())
	d := DeptNewData{Header: company.Header, Project: p.Name, CompanyURL: prefix + "/company", AddURL: prefix + "/departments/add",
		Template: key, Parent: strings.TrimSpace(parent)}
	d.Header.Crumbs[1].Current, d.Header.Crumbs[1].URL = false, prefix+"/company"
	d.Header.Crumbs[2] = WbCrumb{Label: "New department", Current: true}
	t, plan, err := planDepartment(p, key, parent)
	if t != nil {
		d.TemplateName = t.Name
		if t.Group {
			d.Parent = ""
		}
	}
	if err != nil {
		d.Error = err.Error()
		return d
	}
	d.Plan = deptNewPlan(plan)
	return d
}

func (s *Server) handleDeptNew(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	q := r.URL.Query()
	s.renderDeptNew(w, http.StatusOK, s.buildDeptNew(proj, prefix, q.Get("template"), q.Get("parent")))
}

func (s *Server) renderDeptNew(w http.ResponseWriter, code int, d DeptNewData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := s.tmpl.ExecuteTemplate(w, "dept_new.html", d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleDeptAdd(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	if !s.fromOwner(r) {
		http.Error(w, "only the owner adds a department, from the company page in the browser; an agent asks the CEO on its issue trail (issue-cli comment <slug> \"question: …\")", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key, parent := r.PostForm.Get("template"), r.PostForm.Get("parent")
	// Re-planned here: the preview the form came from is never trusted.
	_, plan, err := planDepartment(proj, key, parent)
	code := http.StatusBadRequest
	var installed *depttemplate.InstalledError
	if errors.As(err, &installed) {
		code = http.StatusConflict
	}
	if err == nil {
		err = plan.Apply(proj)
	}
	if err != nil {
		d := s.buildDeptNew(proj, prefix, key, parent)
		d.Plan, d.Error = nil, err.Error()
		s.renderDeptNew(w, code, d)
		return
	}
	if err := s.reloadProject(proj.Slug); err != nil {
		http.Error(w, fmt.Sprintf("added %s to %s, but reading it back failed: %v; restart the viewer", plan.Name, plan.Config, err), http.StatusInternalServerError)
		return
	}
	wrote, kept := 0, 0
	for _, pl := range append([]*depttemplate.Plan{plan}, plan.Children...) {
		for _, f := range pl.Files {
			switch f.State {
			case depttemplate.FileWrite:
				wrote++
			case depttemplate.FileKeep:
				kept++
			}
		}
	}
	v := url.Values{"added": {plan.Name}, "wrote": {strconv.Itoa(wrote)}, "kept": {strconv.Itoa(kept)}}
	http.Redirect(w, r, prefix+"/company?"+v.Encode(), http.StatusSeeOther)
}

// reloadProject reads project slug again from its projects.yaml, so a
// department added from the viewer shows without a restart.
func (s *Server) reloadProject(slug string) error {
	p := s.findProject(slug)
	if p == nil || p.ConfigPath == "" {
		return fmt.Errorf("no project %q from a projects.yaml", slug)
	}
	cfg, err := tracker.LoadProjectsConfig(p.ConfigPath)
	if err != nil {
		return err
	}
	for _, fresh := range cfg.Projects {
		if fresh.Slug == slug {
			*p = fresh
			return nil
		}
	}
	return fmt.Errorf("project %q is no longer in %s", slug, p.ConfigPath)
}

// addedFlash is the company page's line after Add, from its query.
func addedFlash(q url.Values) string {
	name := q.Get("added")
	if name == "" {
		return ""
	}
	wrote, _ := strconv.Atoi(q.Get("wrote"))
	kept, _ := strconv.Atoi(q.Get("kept"))
	text := fmt.Sprintf("✓ Added %s: %s written", name, plural(wrote, "file", "files"))
	if kept > 0 {
		text += fmt.Sprintf(", %s of yours kept", plural(kept, "edited file", "edited files"))
	}
	return text + ". Start its lead from the room."
}
