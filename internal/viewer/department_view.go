package viewer

import (
	"net/http"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// viewWorkflow is the workflow a project view draws columns from: ?department=
// names a department with its own workflow file, else Product's. keep says
// which issues belong on that view; without department workflow files it
// keeps every issue, as before. A file that doesn't load comes back as an
// empty workflow with err set.
func viewWorkflow(proj *tracker.Project, r *http.Request) (dept string, wf *tracker.WorkflowConfig, keep func(*tracker.Issue) bool, err error) {
	if d := proj.DepartmentNamed(r.URL.Query().Get("department")); d != nil && proj.DepartmentWorkflowPath(d.Name) != "" {
		dept = d.Name
	}
	keep = func(*tracker.Issue) bool { return true }
	if len(proj.WorkflowDepartments()) > 0 {
		keep = func(issue *tracker.Issue) bool { return proj.WorkflowDepartment(issue) == dept }
	}
	wf, err = proj.WorkflowFor(dept)
	if err != nil {
		wf = &tracker.WorkflowConfig{}
	}
	return dept, wf, keep, err
}

// keepIssues is the issues keep says yes to.
func keepIssues(issues []*tracker.Issue, keep func(*tracker.Issue) bool) []*tracker.Issue {
	var out []*tracker.Issue
	for _, issue := range issues {
		if keep(issue) {
			out = append(out, issue)
		}
	}
	return out
}
