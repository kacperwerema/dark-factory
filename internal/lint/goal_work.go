package lint

import (
	"github.com/kacperwerema/dark-factory/internal/goalwork"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checkGoalWithoutWork names each goal and open epic with no open issue,
// active queue item or job behind it (internal/goalwork), and a project
// with no goals once, as a note: a new project starts with no warning.
func checkGoalWithoutWork(env *Env) []Finding {
	var items []queue.Item
	if env.Project.IssueDir != "" {
		if q, err := queue.Load(env.Project.IssueDir); err == nil {
			items = q.Items
		}
	}
	rep := goalwork.Find(env.Project, env.Issues, items, env.Now)
	var out []Finding
	if rep.NoGoals {
		out = append(out, Finding{
			Where: "projects.yaml", Note: true,
			Message: "no goals in projects.yaml (company: or departments: goals:): nothing says what the project should do next, so nothing can be queued from it",
			Fix:     tracker.InjectProject(env.Project, `issue-cli department update hq --goal "<id>=<name>/<target>/<period>"`) + " (the owner's call)",
		})
	}
	for _, g := range rep.Gaps {
		f := Finding{Fix: g.Fix}
		if g.Kind == goalwork.KindEpic {
			f.Where = g.Key
			f.Message = "open epic with no open child issue or active queue item behind it"
		} else {
			f.Where = "projects.yaml"
			f.Message = "goal " + g.Name() + " has no open issue, active queue item or job behind it"
		}
		out = append(out, f)
	}
	return out
}
