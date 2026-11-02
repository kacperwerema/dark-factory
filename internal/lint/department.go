package lint

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checkDepartment checks each department's own workflow file: that it
// loads, that it leaves the project-wide keys to Product's file
// (factory/department-workflow-files#D-1), and that every issue walking it
// has a status it knows.
func checkDepartment(env *Env) []Finding {
	var out []Finding
	for _, d := range env.Project.WorkflowDepartments() {
		path := env.Project.DepartmentWorkflowPath(d.Name)
		where := filepath.Base(path)
		wf, err := env.Project.WorkflowFor(d.Name)
		if err != nil {
			out = append(out, Finding{Where: where, Message: err.Error(),
				Fix: fmt.Sprintf("issue-cli workflow init --template content --out %s (or fix the file, or remove workflow: from %s in projects.yaml)", d.Workflow, d.Name)})
			continue
		}
		if raw, err := os.ReadFile(path); err == nil {
			var doc map[string]any
			if yaml.Unmarshal(raw, &doc) == nil {
				for _, key := range tracker.ProjectWideKeys {
					if _, ok := doc[key]; ok {
						out = append(out, Finding{Where: where, Message: fmt.Sprintf("%s: is project-wide: department %s takes it from Product's workflow file, so this one is ignored", key, d.Name),
							Fix: fmt.Sprintf("move %s: to Product's workflow.yaml, or delete it here", key)})
					}
				}
			}
		}
		for _, issue := range env.Issues {
			if env.Project.WorkflowDepartment(issue) != d.Name || issue.Status == "" || wf.GetStatus(issue.Status) != nil {
				continue
			}
			out = append(out, Finding{Where: issue.Slug, Message: fmt.Sprintf("status %q is not in %s's workflow %s (%s)", issue.Status, d.Name, where, wf.StatusNames()),
				Fix: fmt.Sprintf("issue-cli set-meta %s --key department --value %s --status <one of them>", issue.Slug, d.Name)})
		}
	}
	return out
}
