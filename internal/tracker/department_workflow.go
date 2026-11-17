package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProjectWideKeys are the workflow.yaml keys only Product's file sets: a
// department file takes them from it, and lint warns when one shows up in a
// department file (factory/department-workflow-files#D-1).
var ProjectWideKeys = []string{"workbench", "ci", "deploy", "health", "privacy", "leads", "project_actions", "allow_shell", "max_red_rounds", "rules"}

// DepartmentNamed is the department called name (any case), or nil.
func (p *Project) DepartmentNamed(name string) *Department {
	if p == nil || strings.TrimSpace(name) == "" {
		return nil
	}
	for i := range p.Departments {
		if strings.EqualFold(p.Departments[i].Name, strings.TrimSpace(name)) {
			return &p.Departments[i]
		}
	}
	return nil
}

// DepartmentWorkflowPath is the file department dept runs on: its
// workflow: resolved against the project root, or "" when it runs on
// Product's workflow (dept "" is Product).
func (p *Project) DepartmentWorkflowPath(dept string) string {
	d := p.DepartmentNamed(dept)
	if d == nil || strings.TrimSpace(d.Workflow) == "" {
		return ""
	}
	path := strings.TrimSpace(d.Workflow)
	if filepath.IsAbs(path) {
		return path
	}
	if root := p.TelemetryRoot(); root != "" {
		return filepath.Join(root, path)
	}
	return path
}

// WorkflowDepartments are the departments with their own workflow file, in
// projects.yaml order.
func (p *Project) WorkflowDepartments() []Department {
	if p == nil {
		return nil
	}
	var out []Department
	for _, d := range p.Departments {
		if strings.TrimSpace(d.Workflow) != "" {
			out = append(out, d)
		}
	}
	return out
}

// WorkflowFor is the workflow department dept runs on. "" and a department
// without workflow: get Product's (LoadWorkflow). A department file gets
// the project-wide blocks from Product's file and never falls back to it:
// a missing or broken file is an error naming the file.
func (p *Project) WorkflowFor(dept string) (*WorkflowConfig, error) {
	path := p.DepartmentWorkflowPath(dept)
	if path == "" {
		return p.LoadWorkflow(), nil
	}
	name := p.DepartmentNamed(dept).Name
	own, err := loadDepartmentFile(path)
	if err != nil {
		return nil, fmt.Errorf("department %s: %w", name, err)
	}
	if len(own.Statuses) == 0 {
		return nil, fmt.Errorf("department %s: workflow %s has no statuses", name, path)
	}
	wf := departmentWorkflow(own, p.LoadWorkflow())
	wf.Department, wf.File = name, path
	p.attachRuntime(wf)
	return wf, nil
}

// departmentWorkflow is a department's own file with the project-wide
// blocks (ProjectWideKeys) taken from Product's, and the playtest runner
// and worktree settings from Product's when the file names none. Every
// other field is the department file's (TestDepartmentWorkflowCopiesEveryField).
func departmentWorkflow(own, product *WorkflowConfig) *WorkflowConfig {
	wf := own.Clone()
	wf.Workbench = product.Workbench
	wf.CI = product.CI
	wf.Deploy = product.Deploy
	wf.Health = product.Health
	wf.Privacy = product.Privacy
	wf.Leads = product.Leads
	wf.ProjectActions = product.ProjectActions
	wf.AllowShell = product.AllowShell
	wf.MaxRedRounds = product.MaxRedRounds
	wf.Rules = product.Rules
	// The playtest runner is the repository's: a department file may name its
	// own, else it uses Product's.
	if own.PlaytestCommand == "" {
		wf.PlaytestCommand = product.PlaytestCommand
	}
	if own.PlaytestReport == "" {
		wf.PlaytestReport = product.PlaytestReport
	}
	if own.Worktree == nil {
		wf.Worktree, wf.WorktreeSetup, wf.WorktreeSparseExclude = product.Worktree, product.WorktreeSetup, product.WorktreeSparseExclude
	}
	return wf
}

// WorkflowForIssue is the workflow issue walks: its department's file
// (DepartmentOf), scoped to its type and system.
func (p *Project) WorkflowForIssue(issue *Issue) (*WorkflowConfig, error) {
	if issue == nil {
		return p.LoadWorkflow(), nil
	}
	wf, err := p.WorkflowFor(p.DepartmentOf(issue))
	if err != nil {
		return nil, err
	}
	return wf.ForIssue(issue), nil
}

// brokenWorkflow stands in for a department file that didn't load: no
// statuses, so nothing can move, and LoadErr says why.
func (p *Project) brokenWorkflow(dept string, err error) *WorkflowConfig {
	wf := &WorkflowConfig{Department: dept, File: p.DepartmentWorkflowPath(dept), LoadErr: err}
	if d := p.DepartmentNamed(dept); d != nil {
		wf.Department = d.Name
	}
	p.attachRuntime(wf)
	return wf
}

// WorkflowFile is the file this workflow came from when it is a
// department's, else "" (Product's file).
func (w *WorkflowConfig) WorkflowFile() string {
	if w == nil {
		return ""
	}
	return w.File
}

// StatusNames is the workflow's statuses in order, comma separated, for
// error messages that list what a file allows.
func (w *WorkflowConfig) StatusNames() string {
	if w == nil {
		return ""
	}
	return strings.Join(w.GetStatusOrder(), ", ")
}

// AllStatuses is every status of the project's workflow and its departments'
// own files, first spelling kept, Product's first: what a project-wide
// reference to a status (a memory's --at) can name.
func (p *Project) AllStatuses() []string {
	var out []string
	seen := map[string]bool{}
	add := func(wf *WorkflowConfig) {
		if wf == nil {
			return
		}
		for _, s := range wf.GetStatusOrder() {
			if k := strings.ToLower(s); !seen[k] {
				seen[k] = true
				out = append(out, s)
			}
		}
	}
	add(p.LoadWorkflow())
	for _, d := range p.WorkflowDepartments() {
		if wf, err := p.WorkflowFor(d.Name); err == nil {
			add(wf)
		}
	}
	return out
}

// CheckDepartmentMove is nil when issue may move to department to with its
// status; otherwise the error lists to's statuses and the --status retry
// (factory/department-workflow-files#D-4). to "" is Product.
func (p *Project) CheckDepartmentMove(issue *Issue, to string) error {
	if issue == nil || strings.TrimSpace(issue.Status) == "" {
		return nil
	}
	wf, err := p.WorkflowFor(to)
	if err != nil {
		return err
	}
	if wf.GetStatus(issue.Status) != nil {
		return nil
	}
	where := "Product"
	if d := p.DepartmentNamed(to); d != nil {
		where = d.Name
	}
	return fmt.Errorf("status %q is not in %s's workflow (%s); retry with --status <one of them>", issue.Status, where, wf.StatusNames())
}

// DepartmentsOf lists the department names issues fall in, Product ("")
// first, then projects.yaml order; for views that build columns per
// department.
func (p *Project) DepartmentsOf(issues []*Issue) []string {
	seen := map[string]bool{}
	for _, issue := range issues {
		seen[p.DepartmentOf(issue)] = true
	}
	var out []string
	if seen[""] {
		out = append(out, "")
	}
	for _, d := range p.Departments {
		if seen[d.Name] {
			out = append(out, d.Name)
		}
	}
	return out
}

// deptFiles caches department files by path and modification time, so a
// board render that resolves every issue reads each file once.
var deptFiles = struct {
	sync.Mutex
	m map[string]deptFile
}{m: map[string]deptFile{}}

type deptFile struct {
	mod  time.Time
	size int64
	wf   *WorkflowConfig
}

func loadDepartmentFile(path string) (*WorkflowConfig, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading workflow %s: %w", path, err)
	}
	deptFiles.Lock()
	c, ok := deptFiles.m[path]
	deptFiles.Unlock()
	if ok && c.mod.Equal(st.ModTime()) && c.size == st.Size() {
		return c.wf, nil
	}
	wf, err := LoadWorkflow(path)
	if err != nil {
		return nil, err
	}
	deptFiles.Lock()
	deptFiles.m[path] = deptFile{mod: st.ModTime(), size: st.Size(), wf: wf}
	deptFiles.Unlock()
	return wf, nil
}

// WithField is a copy of issue with frontmatter key set to value ("" clears
// it), to ask which department it would belong to after the change.
func (i *Issue) WithField(key, value string) *Issue {
	c := *i
	if key == "system" {
		c.System = value
		return &c
	}
	c.ExtraFields = nil
	for _, f := range i.ExtraFields {
		if f.Key != key {
			c.ExtraFields = append(c.ExtraFields, f)
		}
	}
	if value != "" {
		c.ExtraFields = append(c.ExtraFields, ExtraField{Key: key, Value: value})
	}
	return &c
}

// WorkflowDepartment is the department whose own workflow file issue walks,
// or "" when it walks Product's (Product's issues, and those of a department
// without workflow:). Views that build columns per workflow group by it.
func (p *Project) WorkflowDepartment(issue *Issue) string {
	if p.DepartmentWorkflowPath(p.DepartmentOf(issue)) == "" {
		return ""
	}
	return p.DepartmentOf(issue)
}

// UnfiledDepartment is the department issue's system: names although the
// issue isn't in it: no department: field, and that department's systems:
// don't list the system, so DepartmentOf falls through to Product and the
// issue walks Product's workflow. "" for anything else, an issue another
// department claims included. Lint and create warn on it (the system: of a
// department's work is easy to take for the department).
func (p *Project) UnfiledDepartment(issue *Issue) string {
	if p == nil || strings.TrimSpace(issue.System) == "" || strings.TrimSpace(issue.Field("department")) != "" {
		return ""
	}
	if p.DepartmentOf(issue) != "" {
		return ""
	}
	if d := p.DepartmentByKey(issue.System); d != nil {
		return d.Name
	}
	return ""
}

// IssueDone reports whether issue has finished its own workflow: done for
// Product's, the end of its department's file otherwise. Parked is closed
// but not finished. Epic progress counts children with it, so a child in
// another department counts.
func (p *Project) IssueDone(issue *Issue) bool {
	if p == nil || p.WorkflowDepartment(issue) == "" {
		return strings.EqualFold(strings.TrimSpace(issue.Status), statusDone)
	}
	wf, err := p.WorkflowFor(p.DepartmentOf(issue))
	if err != nil {
		return strings.EqualFold(strings.TrimSpace(issue.Status), statusDone)
	}
	return wf.IsLastStatus(issue.Status)
}

// WorkflowMemo derives each workflow once for a pass over many issues:
// each department's file is parsed once (LoadWorkflowForIssue per issue
// parsed workflow.yaml again for every one), and each (department, type,
// system) scope is derived once through that department's ScopeMemo.
type WorkflowMemo struct {
	p  *Project
	mu sync.Mutex
	m  map[string]*ScopeMemo
}

// WorkflowMemo starts an empty memo for p.
func (p *Project) WorkflowMemo() *WorkflowMemo {
	return &WorkflowMemo{p: p, m: map[string]*ScopeMemo{}}
}

// ForIssue is LoadWorkflowForIssue with the department's file parsed once
// and each scope derived once.
func (m *WorkflowMemo) ForIssue(issue *Issue) *WorkflowConfig {
	if m == nil {
		return nil
	}
	if issue == nil {
		return m.p.LoadWorkflowForIssue(nil)
	}
	dept := m.p.DepartmentOf(issue)
	key := strings.ToLower(dept)
	m.mu.Lock()
	scopes, ok := m.m[key]
	m.mu.Unlock()
	if !ok {
		wf, err := m.p.WorkflowFor(dept)
		if err != nil {
			return m.p.brokenWorkflow(dept, err)
		}
		scopes = wf.ScopeMemo()
		m.mu.Lock()
		m.m[key] = scopes
		m.mu.Unlock()
	}
	return scopes.ForIssue(issue)
}

// ScopeMemo is ForIssue on one workflow for a pass over many issues: each
// (type, system) scope is derived once instead of once per issue (a Clone
// each: ~35% of the health page's time on 520 issues). An issue on a status
// off its path gets its own rescued copy.
//
// What ForIssue hands out shares its slices and maps with the other issues
// of that scope: callers read it and never change it
// (TestWorkflowMemoCallersDoNotMutate). Each gets its own decision runtime,
// so holds and warnings stay per config.
type ScopeMemo struct {
	w  *WorkflowConfig
	mu sync.Mutex
	m  map[scopeKey]*WorkflowConfig
}

// scopeKey is one scope: the resolved type and the system.
type scopeKey struct{ typ, system string }

// ScopeMemo starts an empty scope memo over w.
func (w *WorkflowConfig) ScopeMemo() *ScopeMemo {
	return &ScopeMemo{w: w, m: map[scopeKey]*WorkflowConfig{}}
}

// ForIssue is w.ForIssue(issue) with the issue's scope derived once.
func (s *ScopeMemo) ForIssue(issue *Issue) *WorkflowConfig {
	if s == nil || s.w == nil {
		return nil
	}
	if issue == nil {
		return s.w.ForIssue(nil)
	}
	typ, _ := s.w.ResolveType(issue.Type)
	key := scopeKey{typ: typ, system: issue.System}
	s.mu.Lock()
	scoped, ok := s.m[key]
	s.mu.Unlock()
	if !ok {
		scoped = s.w.ForType(issue.Type)
		scoped.mergeSystem(issue.System)
		s.mu.Lock()
		s.m[key] = scoped
		s.mu.Unlock()
	}
	if s.w.offPath(scoped, issue) {
		rescued := scoped.Clone()
		s.w.rescueOffPath(rescued, issue)
		return rescued
	}
	cp := *scoped
	cp.decisionState = nil
	return &cp
}
