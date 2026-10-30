package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/experiment"
	"github.com/michal-franc/issue-viewer/internal/tracker"
	"github.com/michal-franc/issue-viewer/internal/yolo"
)

// yoloRoot is where the project's .yolo/ lives: the main checkout, as .ci/.
func yoloRoot(proj *tracker.Project) string {
	return ci.RepoRoot(projectRoot(proj))
}

// yoloRecentEnd is how long after YOLO ends a gate refusal still says why.
const yoloRecentEnd = 12 * time.Hour

// yoloGate is what YOLO mode did at a transition's approval gate.
type yoloGate struct {
	Passed bool
	Status string // the approval the gate needed
	To     string // the status start advances to (set by start only)
	State  *yolo.State
	// Ended: YOLO closed recently; the gate waits and the refusal says why.
	Ended string
	// Experiment: YOLO is on but the issue is an experiment, whose gates
	// are the owner's (factory/factory-experiment-loop D-6).
	Experiment bool
}

// yoloExperimentNote is what an experiment's gate says while YOLO is on.
const yoloExperimentNote = "YOLO is on, but experiments are adopted by the owner, even in YOLO: this gate waits for the owner's approval in the issue viewer."

// yoloEligible reports whether YOLO may pass the from → to gate: a forward
// step on the issue's path, to a status that isn't optional, and never the
// human's own Human Testing approval or any gate of an issue labelled
// experiment (the owner adopts experiments).
func yoloEligible(wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to, required string) bool {
	if required == "" || strings.ReplaceAll(strings.ToLower(required), " ", "-") == "human-testing" {
		return false
	}
	if issue != nil && experiment.Is(issue.Labels) {
		return false
	}
	fi, ti := wf.GetStatusIndex(from), wf.GetStatusIndex(to)
	if fi < 0 || ti <= fi {
		return false
	}
	if s := wf.GetStatus(to); s == nil || s.Optional {
		return false
	}
	return true
}

// passYOLOGate lets issue through its from → to approval gate when YOLO is
// on and that approval is all the transition still lacks: it writes the
// YOLO approval comment (the marker the owner reviews after the fact) and
// sets human_approval, so the transition runs as if approved.
func passYOLOGate(ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue, issues []*tracker.Issue, to string, fields map[string]string) (yoloGate, error) {
	from := issue.Status
	required := wf.RequiredHumanApproval(from, to)
	if required == "" || strings.EqualFold(issue.HumanApproval, required) {
		return yoloGate{}, nil
	}
	g := yoloGate{Status: required}
	st, on, err := yolo.Active(yoloRoot(ctx.Project), ctx.Project.Slug, ctx.now())
	if err != nil {
		return g, err
	}
	g.State = st
	if !on {
		if st != nil && st.EndReason != "" && ctx.now().Sub(st.Ended) < yoloRecentEnd {
			g.Ended = fmt.Sprintf("YOLO turned off at %s (%s): the gate waits for the owner again", st.Ended.Format("15:04"), st.EndReason)
		}
		return g, nil
	}
	if !yoloEligible(wf, issue, from, to, required) {
		g.Experiment = experiment.Is(issue.Labels)
		return g, nil
	}
	for _, p := range collectTransitionProblems(ctx, wf, issue, issue.Slug, from, to, fields) {
		if p.Kind != "approval" {
			return g, nil // the transition fails on its own; don't leave a stray approval
		}
	}
	me := whoAmI(ctx.Project, issues)
	comment := tracker.ApprovalComment("YOLO", tracker.YOLOHow(me.Name, from), ctx.now().Format("2006-01-02"), required)
	if err := writeApproval(ctx, issue, comment, required); err != nil {
		return g, err
	}
	issue.HumanApproval = required
	g.Passed = true
	return g, nil
}

// printYOLOGate tells the agent the gate passed without the owner, or why
// it waits after YOLO just ended.
func printYOLOGate(w io.Writer, g yoloGate, from, to string, now time.Time) {
	switch {
	case g.Passed:
		fmt.Fprintf(w, "YOLO: gate %s → %s passed without the owner, marked for review after the fact (YOLO %s)\n", from, to, yoloEndPhrase(g.State, now))
	case g.Ended != "":
		fmt.Fprintln(w, g.Ended)
	case g.Experiment:
		fmt.Fprintln(w, yoloExperimentNote)
	}
}

// yoloEndPhrase is "until 08:00" or "until turned off".
func yoloEndPhrase(st *yolo.State, now time.Time) string {
	label := st.EndLabel(now)
	if strings.HasPrefix(label, "until") {
		return label
	}
	return "until " + label
}

// yoloHeaderLine is the "YOLO:" line start, show and context print while
// YOLO is on; "" when off.
func yoloHeaderLine(proj *tracker.Project, now time.Time) string {
	st, on, err := yolo.Active(yoloRoot(proj), proj.Slug, now)
	if err != nil || !on {
		return ""
	}
	return fmt.Sprintf("YOLO: on %s (gates pass without the owner and are reviewed after the fact; at human-testing run the guided checks yourself: %s)",
		yoloEndPhrase(st, now), tracker.InjectProject(proj, "issue-cli check-script <slug> --yes"))
}

// yoloWouldPass reports whether YOLO is on and would pass the from → to
// gate, with the state for the end phrase.
func yoloWouldPass(ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to string) (bool, *yolo.State) {
	required := wf.RequiredHumanApproval(from, to)
	if !yoloEligible(wf, issue, from, to, required) {
		return false, nil
	}
	st, on, err := yolo.Active(yoloRoot(ctx.Project), ctx.Project.Slug, ctx.now())
	if err != nil || !on {
		return false, nil
	}
	return true, st
}

// yoloNextStep rewrites the gated next step while YOLO is on: run the
// transition, don't stop for the owner.
func yoloNextStep(ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue, from, next, cmd, note string) (string, string) {
	if note == "" {
		return cmd, note
	}
	slug := issue.Slug
	ok, st := yoloWouldPass(ctx, wf, issue, from, next)
	if !ok {
		return cmd, note
	}
	return fmt.Sprintf("issue-cli transition %s --to %q", slug, next),
		fmt.Sprintf("(YOLO is on %s: this gate passes without the owner once everything else is done; run it, no need to stop. The owner reviews it after the fact.)", yoloEndPhrase(st, ctx.now()))
}

// yoloDryRun drops the approval problem YOLO would pass and says so.
func yoloDryRun(ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to string, problems []transitionProblem) ([]transitionProblem, string) {
	ok, st := yoloWouldPass(ctx, wf, issue, from, to)
	if !ok {
		if _, on, _ := yolo.Active(yoloRoot(ctx.Project), ctx.Project.Slug, ctx.now()); on && issue != nil && experiment.Is(issue.Labels) && wf.RequiredHumanApproval(from, to) != "" {
			return problems, yoloExperimentNote
		}
		return problems, ""
	}
	kept := problems[:0:0]
	dropped := false
	for _, p := range problems {
		if p.Kind == "approval" {
			dropped = true
			continue
		}
		kept = append(kept, p)
	}
	if !dropped {
		return problems, ""
	}
	return kept, fmt.Sprintf("YOLO is on %s: the human-approval gate passes without the owner and is marked for review after the fact.", yoloEndPhrase(st, ctx.now()))
}
