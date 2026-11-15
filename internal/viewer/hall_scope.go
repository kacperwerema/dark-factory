package viewer

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Which workflow a hall page draws: Product's workflow.yaml, or with
// ?department=<name> a department's own file (projects.yaml departments:
// workflow:). The hall, its drill-ins and the Edit form all take one scope,
// and every URL they build carries its query, so a Marketing station links
// to Marketing's stations and Edit.
// Design: issues/UI/the-hall-shows-and-edits-a-department-workflow-like-the-software-one-marketing-first-every-types-path-and-gates.md.

// hallScope is one hall's workflow and the issues on it.
type hallScope struct {
	prefix string
	// Dept is the department's name, "" for Product.
	Dept string
	WF   *tracker.WorkflowConfig
	// File is the workflow file the stations come from, "" when Product
	// runs on the built-in workflow.
	File string
	// Label is the hall's name ("Product hall", "Marketing hall"); Color
	// its crumb's colour.
	Label, Color string
	// Query is "" or "?department=<name>", added to every hall URL.
	Query string
	keep  func(*tracker.Issue) bool
}

// errNoHall is a ?department= that names no department with a file.
type errNoHall struct{ msg string }

func (e errNoHall) Error() string { return e.msg }

// hallScopeOf is the hall r asks for. An unknown department, or one that
// runs on Product's workflow, is errNoHall (a 404); a department file that
// does not load is any other error.
func hallScopeOf(p *tracker.Project, prefix string, r *http.Request) (*hallScope, error) {
	name := strings.TrimSpace(r.URL.Query().Get("department"))
	if name == "" {
		return productHall(p, prefix), nil
	}
	d := p.DepartmentNamed(name)
	if d == nil {
		return nil, errNoHall{fmt.Sprintf("no department %q in this project", name)}
	}
	if p.DepartmentWorkflowPath(d.Name) == "" {
		return nil, errNoHall{fmt.Sprintf("%s runs on Product's workflow: its stations are in the Product hall (%s/hall)", d.Name, prefix)}
	}
	wf, err := p.WorkflowFor(d.Name)
	if err != nil {
		return nil, err
	}
	dept := d.Name
	sc := &hallScope{prefix: prefix, Dept: dept, WF: wf, File: p.DepartmentWorkflowPath(dept), Label: dept + " hall", Color: d.Color,
		Query: hallDeptQuery(dept), keep: func(issue *tracker.Issue) bool { return p.WorkflowDepartment(issue) == dept }}
	return sc, nil
}

// productHall is Product's hall, as it was before department halls.
func productHall(p *tracker.Project, prefix string) *hallScope {
	return &hallScope{prefix: prefix, WF: p.LoadWorkflow(), File: p.WorkflowPath(), Label: "Product hall", Color: companyProductColor,
		keep: nil}
}

// hallDeptQuery is the query naming department dept's hall.
func hallDeptQuery(dept string) string {
	return "?department=" + url.QueryEscape(strings.ToLower(dept))
}

// hallURLOf is department dept's hall ("" is Product's).
func hallURLOf(prefix, dept string) string {
	if dept == "" {
		return prefix + "/hall"
	}
	return prefix + "/hall" + hallDeptQuery(dept)
}

// url is the scope's hall (kind "") or a station or gate of it.
func (sc *hallScope) url(kind, status string) string {
	if kind == "" {
		return sc.prefix + "/hall" + sc.Query
	}
	return hallPartURL(sc.prefix, kind, status) + sc.Query
}

// editURL is the station's Edit form, or a POST under it (action
// "preview" or "save").
func (sc *hallScope) editURL(status, action string) string {
	u := hallPartURL(sc.prefix, "station", status) + "/edit"
	if action != "" {
		u += "/" + action
	}
	return u + sc.Query
}

// issues is the scope's issues out of all of them.
func (sc *hallScope) issues(p *tracker.Project, all []*tracker.Issue) []*tracker.Issue {
	if sc.keep == nil {
		return productIssues(p, all)
	}
	return keepIssues(all, sc.keep)
}

// status is the scope's status the URL key names, or nil.
func (sc *hallScope) status(key string) *tracker.WorkflowStatus {
	for i := range sc.WF.Statuses {
		if hallPartKey(sc.WF.Statuses[i].Name) == key {
			return &sc.WF.Statuses[i]
		}
	}
	return nil
}

// hallScopeError answers a scope that could not be made.
func hallScopeError(w http.ResponseWriter, err error) {
	if _, ok := err.(errNoHall); ok {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// WbHallSwitch is the hall's switcher between Product's workflow and each
// department's own: drawn only when the project has a department file.
type WbHallSwitch struct {
	Items []WbHallSwitchItem
}

// WbHallSwitchItem is one workflow in the switcher.
type WbHallSwitchItem struct {
	Label, URL, Color string
	Current           bool
}

// hallSwitch is the switcher with sc's hall current, nil without
// department workflows.
func hallSwitch(p *tracker.Project, sc *hallScope) *WbHallSwitch {
	depts := p.WorkflowDepartments()
	if len(depts) == 0 {
		return nil
	}
	sw := &WbHallSwitch{Items: []WbHallSwitchItem{{Label: tracker.ProductName, URL: hallURLOf(sc.prefix, ""), Color: companyProductColor, Current: sc.Dept == ""}}}
	for _, d := range depts {
		sw.Items = append(sw.Items, WbHallSwitchItem{Label: d.Name, URL: hallURLOf(sc.prefix, d.Name), Color: d.Color, Current: strings.EqualFold(sc.Dept, d.Name)})
	}
	return sw
}

// WbHallType is one type of work's path on the hall: its statuses in
// order and the gates between them.
type WbHallType struct {
	Name, Description string
	Default           bool
	Steps             []WbHallTypeStep
}

// WbHallTypeStep is one status on a type's path. Gate: entering it needs
// the owner's approval; OwnerOnly: not even YOLO passes that gate.
type WbHallTypeStep struct {
	Status, URL     string
	Gate, OwnerOnly bool
}

// hallTypes is every type's path in sc's workflow, the default first. A
// type's overlay counts: its own gates show on its path.
func hallTypes(sc *hallScope) []WbHallType {
	wf := sc.WF
	def, _ := wf.ResolveType("")
	var out []WbHallType
	add := func(name string) {
		tw := wf.ForType(name)
		t := WbHallType{Name: name, Description: wf.Types[name].Description, Default: name == def}
		prev := ""
		for _, st := range tw.Statuses {
			if tracker.IsParkedStatus(st.Name) {
				continue
			}
			step := WbHallTypeStep{Status: st.Name, URL: sc.url("station", st.Name)}
			if prev != "" && tw.RequiredHumanApproval(prev, st.Name) != "" {
				step.Gate, step.OwnerOnly = true, tw.OwnerOnlyGate(prev, st.Name)
			}
			t.Steps = append(t.Steps, step)
			prev = st.Name
		}
		out = append(out, t)
	}
	if def != "" {
		add(def)
	}
	for _, name := range wf.TypeNames() {
		if name != def {
			add(name)
		}
	}
	return out
}
