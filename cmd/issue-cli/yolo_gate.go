package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/experiment"
	"github.com/michal-franc/issue-viewer/internal/proof"
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
	// OwnerOnly: YOLO is on but the gate is the owner's even in YOLO; the
	// note says why (yoloOwnerReason).
	OwnerOnly string
	// Blocked: YOLO would pass the approval, but these other requirements
	// are still open, so it was held back. A refusal names them, not the
	// approval (blockedRefusal).
	Blocked []transitionProblem
}

// yoloExperimentNote is what an experiment's gate says while YOLO is on.
const yoloExperimentNote = "YOLO is on, but experiments are adopted by the owner, even in YOLO: this gate waits for the owner's approval in the issue viewer."

// yoloOwnerOnlyNote is what an owner_only gate says while YOLO is on.
const yoloOwnerOnlyNote = "YOLO is on, but this gate is the owner's even in YOLO: the step spends money or publishes outside the machine (owner_only in the workflow). It waits for the owner's approval in the issue viewer, where it is in Needs you. Stop here and tell your lead."

// yoloOwnerReason is why the from → to gate is the owner's even with YOLO
// on, "" when it isn't: the one place that says so. An owner_only gate
// (it spends money or publishes outside the machine) and any gate of an
// experiment (the owner adopts experiments, factory/factory-experiment-loop
// D-6). The Human Testing approval is the human's too; yoloEligible keeps
// that rule, as it has no note.
func yoloOwnerReason(wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to string) string {
	switch {
	case wf.OwnerOnlyGate(from, to):
		return yoloOwnerOnlyNote
	case issue != nil && experiment.Is(issue.Labels):
		return yoloExperimentNote
	}
	return ""
}

// yoloEligible reports whether YOLO may pass the from → to gate: a forward
// step on the issue's path, to a status that isn't optional, and never the
// human's own Human Testing approval or a gate that is the owner's even in
// YOLO (yoloOwnerReason).
func yoloEligible(wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to, required string) bool {
	if required == "" || strings.ReplaceAll(strings.ToLower(required), " ", "-") == "human-testing" {
		return false
	}
	if yoloOwnerReason(wf, issue, from, to) != "" {
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
		g.OwnerOnly = yoloOwnerReason(wf, issue, from, to)
		return g, nil
	}
	for _, p := range collectTransitionProblems(ctx, wf, issue, issue.Slug, from, to, fields) {
		if p.Kind != "approval" {
			g.Blocked = append(g.Blocked, p)
		}
	}
	if len(g.Blocked) > 0 {
		return g, nil // the transition fails on its own; don't leave a stray approval
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
	case g.OwnerOnly != "":
		fmt.Fprintln(w, g.OwnerOnly)
	}
}

// yoloRefusal adds why the gate waits to a refused transition or start:
// an owner-only gate's reason goes in the error itself, the other notes
// to w as before.
func yoloRefusal(w io.Writer, g yoloGate, from, to string, now time.Time, err error) error {
	if g.OwnerOnly != "" {
		return fmt.Errorf("%w\n\n%s", err, g.OwnerOnly)
	}
	printYOLOGate(w, g, from, to, now)
	return err
}

// blockedRefusal is the refusal for a transition YOLO would have let
// through its approval gate if the rest were done: it names what is still
// open (a missing assignee after backlog, open boxes) instead of the
// approval, which is not what the agent has to fix. nil when nothing was
// held back or err is not the missing approval.
func blockedRefusal(g yoloGate, prefix string, err error) error {
	if len(g.Blocked) == 0 || !errors.Is(err, tracker.ErrApprovalMissing) {
		return nil
	}
	var b strings.Builder
	for i, p := range g.Blocked {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if p.Message != "" {
			b.WriteString(p.Message)
		} else {
			b.WriteString(p.Requirement)
		}
		for _, fix := range p.Fix {
			b.WriteString("\n  " + fix)
		}
	}
	return fmt.Errorf("%s: %s\n\nYOLO passes the %q approval once this is done.", prefix, b.String(), g.Status)
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

// yoloGuidance is prompts with the ask-the-human and stop-for-approval
// sentences left out, and the YOLO rule that replaces them, while YOLO is
// on; prompts unchanged and "" when it is off.
func yoloGuidance(ctx *Context, slug string, prompts []string) ([]string, string) {
	if _, on, err := yolo.Active(yoloRoot(ctx.Project), ctx.Project.Slug, ctx.now()); err != nil || !on {
		return prompts, ""
	}
	var kept []string
	for _, p := range prompts {
		if k, _ := tracker.YOLOGuidance(p); k != "" {
			kept = append(kept, k)
		}
	}
	return kept, tracker.InjectProject(ctx.Project, tracker.YOLORule(slug))
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
		if reason := yoloOwnerReason(wf, issue, from, next); reason != "" && wf.RequiredHumanApproval(from, next) != "" && yoloIsOn(ctx) {
			return cmd, "(" + reason + ")"
		}
		return cmd, note
	}
	return fmt.Sprintf("issue-cli transition %s --to %q", slug, next),
		fmt.Sprintf("(YOLO is on %s: this gate passes without the owner once everything else is done; run it, no need to stop. The owner reviews it after the fact.)", yoloEndPhrase(st, ctx.now()))
}

// yoloDryRun drops the approval problem YOLO would pass and says so.
func yoloDryRun(ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to string, problems []transitionProblem) ([]transitionProblem, string) {
	ok, st := yoloWouldPass(ctx, wf, issue, from, to)
	if !ok {
		if reason := yoloOwnerReason(wf, issue, from, to); reason != "" && wf.RequiredHumanApproval(from, to) != "" && yoloIsOn(ctx) {
			return problems, reason
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

// yoloIsOn reports whether YOLO is on for the project now.
func yoloIsOn(ctx *Context) bool {
	_, on, err := yolo.Active(yoloRoot(ctx.Project), ctx.Project.Slug, ctx.now())
	return err == nil && on
}

// yoloDefersOwnerProof reports whether criteria only the owner can prove
// wait for them after the fact instead of blocking criteria_proven: YOLO is
// on and the issue is no experiment (the owner adopts those).
func yoloDefersOwnerProof(ctx *Context, issue *tracker.Issue) bool {
	if issue == nil || experiment.Is(issue.Labels) {
		return false
	}
	_, on, err := yolo.Active(yoloRoot(ctx.Project), ctx.Project.Slug, ctx.now())
	return err == nil && on
}

// writeYOLOOwnerChecks puts each box only the owner can check, still
// unticked when the from → to proof, guided-check or approval gate passed
// under YOLO (or the issue reached done), in the after-the-fact queue (a
// yolo-owner-check: comment), once while it waits, and says so. An approval
// gate counts from testing on: before it the work the boxes check isn't done.
func writeYOLOOwnerChecks(ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue, from, to string, approvalPassed bool) error {
	if !wf.DeferOwnerProof {
		return nil
	}
	pastWork := strings.EqualFold(to, "done") || approvalPassed && wf.GetStatusIndex(from) >= wf.GetStatusIndex("testing")
	if !pastWork && !wf.TransitionChecksProof(from, to) && !(strings.EqualFold(to, "human-testing") && usesCheckScripts(wf)) {
		return nil
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	waiting := map[string]bool{}
	for _, p := range yolo.OwnerChecks(issue, comments) {
		if p.Review == "" {
			waiting[strings.ToUpper(p.Check)] = true
		}
	}
	report := proof.Collect(projectRoot(ctx.Project), issue, comments)
	author := commentAuthor(ctx.Project, issue)
	for _, it := range report.OwnerWaiting() {
		if waiting[strings.ToUpper(it.ID)] {
			continue
		}
		c := tracker.YOLOOwnerCheckComment(tracker.YOLOOwnerCheck{ID: it.ID, Why: it.Why, Text: it.Text, From: from})
		if err := tracker.AddCommentBy(issue.FilePath, 0, c, "cli", author); err != nil {
			return err
		}
		if !ctx.JSONOutput {
			fmt.Fprintf(ctx.Stdout, "YOLO: %s: owner check, not done (%s) — left unticked for the owner, in the after-the-fact queue\n", it.ID, it.Why)
		}
	}
	return nil
}
