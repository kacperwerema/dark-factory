package main

import (
	"fmt"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

var checkRemoveCommand = &Command{
	Name:      "check-remove",
	ShortHelp: "Remove checkboxes by id; the other boxes keep their ids",
	LongHelp: `Remove boxes, addressed by their ids from 'issue-cli checklist'. Each one
becomes a hidden line ("<!-- removed AC6: <text> -->") that keeps its slot,
so every other box keeps its id: after removing AC6, AC7 is still AC7, and
the Proves: trailers that name it still prove it. A removed id is never
reused. Several ids in one call are all-or-nothing.

Examples:
  issue-cli check-remove <slug> AC6
  issue-cli check-remove <slug> AC6 AC7

It refuses a box the workflow put in a section a gate waits on (Design,
Implementation, ...): tick it, or say why it doesn't apply with
issue-cli check <slug> <id> --na "<reason>" (named forms: issue-cli check
--help). An agent can't
remove the owner's boxes (see issue-cli uncheck --help). Links that proved a
removed criterion are listed; prove --list shows them as strays.`,
	Run: runCheckRemove,
}

func init() {
	registerCommand(checkRemoveCommand)
}

func runCheckRemove(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "check-remove")
	if err != nil {
		return err
	}
	fs := newFlagSet("check-remove", ctx)
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return &usageError{msg: fmt.Sprintf("check-remove needs box ids (see issue-cli checklist %s)\n\nExample:\n  issue-cli check-remove %s AC6", slug, slug)}
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	_, targets, err := tracker.RemoveBoxes(issue.BodyRaw, fs.Args())
	if err != nil {
		return fmt.Errorf("%w\n\nNothing was removed. Run: issue-cli checklist %s", err, issue.Slug)
	}
	if err := refuseGateBoxes(ctx, issue, targets); err != nil {
		return err
	}
	report := boxProofReport(ctx, issue)
	if err := refuseOwnerBoxes(ctx, issue, report, fs.Args(), "remove"); err != nil {
		return err
	}

	var removed []tracker.CheckboxItem
	newBody, _, err := tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		next, r, err := tracker.RemoveBoxes(body, fs.Args())
		if err != nil {
			return body, false, err
		}
		removed = r
		return next, next != body, nil
	})
	if err != nil {
		return fmt.Errorf("%w\n\nRun: issue-cli checklist %s", err, issue.Slug)
	}
	for _, it := range removed {
		fmt.Fprintf(ctx.Stdout, "✓ Removed: %s\n", checkboxLabel(it))
		if c, ok := report.criterion(it.ID); ok && len(c.Links) > 0 {
			var links []string
			for _, l := range c.Links {
				links = append(links, string(l.Kind)+" "+l.Ref)
			}
			fmt.Fprintf(ctx.Stdout, "  Its links no longer prove anything: %s (issue-cli prove %s --list)\n", strings.Join(links, ", "), issue.Slug)
		}
	}
	fmt.Fprintln(ctx.Stdout, "  The other boxes keep their ids.")
	total, checked := tracker.CountCheckboxes(newBody)
	fmt.Fprintf(ctx.Stdout, "  Progress: %d/%d\n", checked, total)
	fmt.Fprintf(ctx.Stdout, "file: %s\n", issue.FilePath)
	return nil
}

// refuseGateBoxes refuses removing a box the workflow writes into a section
// a gate waits on: removing it would get round the gate.
func refuseGateBoxes(ctx *Context, issue *tracker.Issue, targets []tracker.CheckboxItem) error {
	wf := ctx.Project.LoadWorkflowForIssue(issue)
	var hit []tracker.CheckboxItem
	for _, it := range targets {
		if wf.GatesSection(it.Section) && wf.SectionBoxes(it.Section)[strings.TrimSpace(it.Text)] {
			hit = append(hit, it)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("these boxes are the workflow's, in a section a gate waits on; nothing was removed:")
	for _, it := range hit {
		fmt.Fprintf(&b, "\n  %s %s", it.ID, it.Label())
	}
	fmt.Fprintf(&b, "\n\nTick it when done, or say why it doesn't apply:\n  issue-cli check %s %s\n  issue-cli check %s %s --na \"no page changes\"\nThe N/A forms:\n%s",
		issue.Slug, hit[0].ID, issue.Slug, hit[0].ID, tracker.NAFormsHelp("  "))
	return fmt.Errorf("%s", b.String())
}
