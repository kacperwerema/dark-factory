package jobs

import (
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The lead tick's queue-low sweep (cmd/issue-cli/cmd_lead_tick_sweep.go):
// a run of the bundled retro sweep the tick files itself at the queue's
// refill line. It is a run of the project's own job on that brief when it
// has one, else of QueueSweepKey, an implicit job no projects.yaml lists.

// QueueSweepBrief is the bundled brief the tick's sweep runs.
const QueueSweepBrief = BuiltinPrefix + "retro-sweep"

// QueueSweepID is the implicit job's id: its runs are queue-sweep-<date>.
const QueueSweepID = "queue-sweep"

// everyRefill is the implicit job's schedule: none, the queue's level.
const everyRefill = "refill"

// QueueSweepKey is the implicit job's key.
var QueueSweepKey = KeyOf(tracker.ProductName, QueueSweepID)

// IsImplicit reports a job key the engine runs without a projects.yaml
// entry (lint's open-run check leaves its runs alone).
func IsImplicit(key string) bool { return strings.EqualFold(strings.TrimSpace(key), QueueSweepKey) }

// QueueSweepRef is the job the tick's sweep is a run of: the project's own
// job on QueueSweepBrief when it has one, else the implicit
// product/queue-sweep (type report when the workflow has it). The project
// returned is p, or a copy with an empty Product entry so the run files
// under Product/ on a project that configures none.
func QueueSweepRef(p *tracker.Project) (*tracker.Project, *Ref) {
	for _, r := range List(p) {
		if strings.TrimSpace(r.Job.Brief) == QueueSweepBrief {
			return p, r
		}
	}
	typ := ""
	if wf := p.LoadWorkflow(); wf != nil && wf.HasTypes() {
		if _, ok := wf.Types["report"]; ok {
			typ = "report"
		}
	}
	ref := &Ref{
		Key:        QueueSweepKey,
		Department: tracker.ProductName,
		Job:        tracker.Job{ID: QueueSweepID, Name: "Queue-low sweep", Brief: QueueSweepBrief, Type: typ},
		Schedule:   Schedule{Every: everyRefill},
	}
	if p.Product == nil {
		cp := *p
		cp.Product = &tracker.Department{Name: tracker.ProductName}
		p = &cp
	}
	return p, ref
}
