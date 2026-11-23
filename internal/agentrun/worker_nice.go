package agentrun

import (
	"fmt"
	"os/exec"
	"strconv"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// niceLookPath finds nice(1); tests stand in for a machine without it.
var niceLookPath = exec.LookPath

// WorkerNiceCommand is argv, the pane command of a worker's session, run
// under nice -n <workers: nice:> so the agent and every process it starts
// inherit the level from the first instruction (no renice after the start
// to race). A lead, a level of 0 and a machine without nice keep argv as
// it is. The step says what the launch did; nil when nothing was asked.
func WorkerNiceCommand(proj *tracker.Project, spec Spec, argv []string) ([]string, *Step) {
	if spec.LeadScope != "" {
		return argv, nil
	}
	wf := spec.Workflow
	if wf == nil && proj != nil {
		wf = proj.LoadWorkflow()
	}
	level := wf.WorkerNice()
	if level == 0 {
		return argv, nil
	}
	p, err := niceLookPath("nice")
	if err != nil {
		return argv, &Step{Name: "Worker priority (workers: nice:)", Status: "warning",
			Detail: fmt.Sprintf("nice %d asked, but nice is not on PATH: started at the dispatcher's priority", level)}
	}
	out := append([]string{p, "-n", strconv.Itoa(level)}, argv...)
	return out, &Step{Name: "Worker priority (workers: nice:)", Status: "ok",
		Detail: fmt.Sprintf("nice %d: the worker and everything it runs yield the cores to the lead", level)}
}
