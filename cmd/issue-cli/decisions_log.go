package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// recordDecisionHolds appends one held event per hold the ledger rules
// found on this real transition attempt (refused, or passed under YOLO).
// A dry run never calls it; a log it can't write is a warning.
func recordDecisionHolds(ctx *Context, wf *tracker.WorkflowConfig, issues []*tracker.Issue, slug, from, to string) {
	holds := wf.DecisionHoldsFound()
	if len(holds) == 0 {
		return
	}
	me := whoAmI(ctx.Project, issues)
	name, role := me.Name, string(me.Role)
	if name == "" {
		name = "owner"
	}
	if err := tracker.RecordHolds(ctx.Project.IssueDir, tracker.Holds{Issue: slug, From: from, To: to, Passed: wf.DeferDecisionHolds,
		Actor: tracker.DecisionActor{Name: name, Role: role}, Holds: holds}, ctx.now()); err != nil {
		fmt.Fprintf(ctx.Stderr, "warning: the decisions log did not take the held events: %v\n", err)
		return
	}
	if !wf.DeferDecisionHolds && !ctx.JSONOutput {
		fmt.Fprintf(ctx.Stderr, "Logged: %s\n", tracker.InjectProject(ctx.Project, "issue-cli decisions log --ref "+holds[0].Ref))
	}
}

// runDecisionsLog is issue-cli decisions log: the audit log's done events
// in time order, filtered by ref, issue and day. Reads only.
func runDecisionsLog(ctx *Context, args []string) error {
	fs := newFlagSet("decisions log", ctx)
	refFlag := fs.String("ref", "", "<slug>#D-n: only this decision's events (injected ones that carried it too)")
	issueFlag := fs.String("issue", "", "<slug>: only events about this issue or its decisions")
	sinceFlag := fs.String("since", "", "YYYY-MM-DD: only events on or after this day (local time)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	ref := strings.TrimSpace(*refFlag)
	if ref != "" && !strings.Contains(ref, "#D-") {
		return fmt.Errorf("--ref %q: want a full ref, <slug>#D-n (issue-cli decisions lists them)", ref)
	}
	var since time.Time
	if s := strings.TrimSpace(*sinceFlag); s != "" {
		t, err := time.ParseInLocation("2006-01-02", s, time.Local)
		if err != nil {
			return fmt.Errorf("--since %q: want YYYY-MM-DD", s)
		}
		since = t
	}
	all, err := tracker.ReadDecisionLog(ctx.Project.IssueDir)
	if err != nil {
		return fmt.Errorf("cannot read the decisions log: %w", err)
	}
	evs := tracker.FilterDecisionLog(all, tracker.DecisionLogFilter{Ref: ref, Issue: strings.ToLower(strings.TrimSpace(*issueFlag)), Since: since})
	if ctx.JSONOutput {
		if evs == nil {
			evs = []tracker.DecisionEvent{}
		}
		return writeJSON(ctx.Stdout, evs)
	}
	if len(evs) == 0 {
		if len(all) == 0 {
			fmt.Fprintf(ctx.Stdout, "The decisions log is empty (%s). It fills as decisions are recorded:\n  %s\n",
				tracker.DecisionLogPath(ctx.Project.IssueDir), tracker.InjectProject(ctx.Project, `issue-cli decide <slug> --text "..."`))
		} else {
			fmt.Fprintf(ctx.Stdout, "No events match these filters (%d in the log).\n", len(all))
		}
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "== Decisions log (%s) ==\n", fmt.Sprintf("%d %s", len(evs), pluralWord(len(evs), "event", "events")))
	for _, ev := range evs {
		fmt.Fprintln(ctx.Stdout, decisionLogLine(ev))
	}
	return nil
}

// decisionLogLine is one event as a line: when · event · what · who.
func decisionLogLine(ev tracker.DecisionEvent) string {
	when := ev.At
	if t, err := time.Parse(time.RFC3339, ev.At); err == nil {
		when = t.Local().Format("2006-01-02 15:04")
	}
	parts := []string{when, ev.Event}
	switch ev.Event {
	case tracker.DecisionInjected:
		what := ev.Briefing + " briefing"
		if ev.Issue != "" {
			what += " for " + ev.Issue
		}
		parts = append(parts, what, fmt.Sprintf("%d %s", len(ev.Refs), pluralWord(len(ev.Refs), "ref", "refs")))
	case tracker.DecisionHeld:
		verdict := "refused"
		if ev.Passed {
			verdict = "passed (YOLO)"
		}
		parts = append(parts, ev.Ref, fmt.Sprintf("%s on %s (%s → %s)", ev.Rule, ev.Issue, ev.From, ev.To), verdict)
	case tracker.DecisionReplaced:
		parts = append(parts, ev.Ref+" by "+ev.ByRef)
	default:
		parts = append(parts, ev.Ref)
		if ev.Old != "" || ev.New != "" {
			old, nw := ev.Old, ev.New
			if old == "" {
				old = "{}"
			}
			if nw == "" {
				nw = "{}"
			}
			parts = append(parts, old+" → "+nw)
		}
		if ev.Issue != "" && ev.Event != tracker.DecisionRecorded {
			parts = append(parts, "issue "+ev.Issue)
		}
	}
	if ev.Comment > 0 {
		parts = append(parts, fmt.Sprintf("comment #%d", ev.Comment))
	}
	who := ev.By
	if ev.Role != "" {
		who += " (" + ev.Role + ")"
	}
	if strings.TrimSpace(who) != "" {
		parts = append(parts, who)
	}
	return strings.Join(parts, " · ")
}
