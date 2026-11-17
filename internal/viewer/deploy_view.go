package viewer

import (
	"fmt"
	"time"

	"github.com/kacperwerema/dark-factory/internal/deploy"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// WbDeploy is the Report's Last deploy card: the newest record under the
// issues dir's .deploys/ (issue-cli deploy, docs/workflow.md#deploy).
type WbDeploy struct {
	// Empty: deploy: is set but nothing ran yet; Cmd is the dry run.
	Empty bool
	Cmd   string
	// When, By, Took and State describe the run; Tone colours State.
	When, By, Took, State, Tone string
	// Scope is "only <step>" or "from <step>" for a partial run.
	Scope string
	Steps []WbDeployStep
	// Resume is the command that goes on from the red step.
	Resume string
}

// WbDeployStep is one step of the run.
type WbDeployStep struct {
	N                 int
	Name, State, Tone string
	Why, Took         string
}

// reportDeploy is the card, nil when the project names no deploy: steps.
func reportDeploy(p *tracker.Project, wf *tracker.WorkflowConfig, now time.Time) *WbDeploy {
	if wf == nil || len(wf.Deploy) == 0 {
		return nil
	}
	rec := deploy.Last(p.IssueDir)
	if rec == nil {
		return &WbDeploy{Empty: true, Cmd: tracker.InjectProject(p, "issue-cli deploy --dry-run")}
	}
	card := &WbDeploy{
		When:  shortAgo(rec.Started, now),
		By:    rec.By,
		Took:  rec.Finished.Sub(rec.Started).Round(time.Second).String(),
		State: rec.State,
		Tone:  deployTone(rec.State),
	}
	switch {
	case rec.Only != "":
		card.Scope = "only " + rec.Only
	case rec.From != "":
		card.Scope = "from " + rec.From
	}
	for i, s := range rec.Steps {
		row := WbDeployStep{N: i + 1, Name: s.Name, State: s.State, Tone: deployTone(s.State), Why: s.Why}
		if d := (time.Duration(s.Seconds * float64(time.Second))).Round(time.Second); d > 0 {
			row.Took = d.String()
		}
		card.Steps = append(card.Steps, row)
	}
	if rec.Failed != "" {
		card.Resume = tracker.InjectProject(p, "issue-cli deploy --from "+rec.Failed)
	}
	return card
}

func deployTone(state string) string {
	switch state {
	case deploy.Green:
		return "green"
	case deploy.Red, deploy.Refused:
		return "red"
	}
	return "idle"
}

// shortAgo is t as a date and time with how long ago it was.
func shortAgo(t, now time.Time) string {
	d := now.Sub(t)
	var ago string
	switch {
	case d < time.Minute:
		ago = "just now"
	case d < time.Hour:
		ago = fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		ago = fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		ago = fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("Mon 2 Jan 15:04") + " · " + ago
}

// deployMarkdownLine is status.md's Last deploy line, "" without deploy:.
func deployMarkdownLine(p *tracker.Project, wf *tracker.WorkflowConfig) string {
	if wf == nil || len(wf.Deploy) == 0 {
		return ""
	}
	rec := deploy.Last(p.IssueDir)
	if rec == nil {
		return "Last deploy: none yet (" + tracker.InjectProject(p, "issue-cli deploy --dry-run") + ")"
	}
	line := fmt.Sprintf("Last deploy: %s %s by %s", rec.Started.Format("2006-01-02 15:04"), rec.State, valueOrDash(rec.By))
	if rec.Failed != "" {
		line += " at " + rec.Failed
	}
	return line
}
