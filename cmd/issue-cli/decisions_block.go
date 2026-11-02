package main

import (
	"fmt"
	"io"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// issueDecisionBlock is the decisions-in-force block for issue at status,
// built from the same InForce list decisions --in-force prints.
func issueDecisionBlock(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue, status string, wf *tracker.WorkflowConfig) tracker.DecisionBlock {
	return tracker.IssueDecisionBlock(ctx.Project, issues, issue, status, wf, ctx.now())
}

// printDecisionBlock prints == Decisions in force == for slug.
func printDecisionBlock(w io.Writer, proj *tracker.Project, b tracker.DecisionBlock, slug string) {
	fmt.Fprintln(w, "== Decisions in force ==")
	for _, l := range b.Lines(tracker.InForceCommand(slug), "this issue") {
		fmt.Fprintln(w, tracker.InjectProject(proj, l))
	}
}

// printStartDecisions is start's block: it is a real briefing, so it
// writes one injected event when anything is in force. A log it can't
// write is a warning, never a failed start.
func printStartDecisions(ctx *Context, issue *tracker.Issue, issues []*tracker.Issue, status string, wf *tracker.WorkflowConfig) {
	b := issueDecisionBlock(ctx, issue, issues, status, wf)
	fmt.Fprintln(ctx.Stdout)
	printDecisionBlock(ctx.Stdout, ctx.Project, b, issue.Slug)
	me := whoAmI(ctx.Project, issues)
	name := me.Name
	if name == "" {
		name = "owner"
	}
	if err := tracker.RecordInjected(ctx.Project.IssueDir, tracker.Injected{Briefing: tracker.BriefingStart, Issue: issue.Slug,
		Actor: tracker.DecisionActor{Name: name, Role: string(me.Role)}, Refs: b.Refs()}, ctx.now()); err != nil {
		fmt.Fprintf(ctx.Stderr, "warning: the decisions log did not take start's injected event: %v\n", err)
	}
}
