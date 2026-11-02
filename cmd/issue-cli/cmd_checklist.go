package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var checklistCommand = &Command{
	Name:      "checklist",
	ShortHelp: "Show checkbox status for an issue",
	LongHelp:  "Print every `- [ ]` and `- [x]` line in the issue body, grouped by section, with each box's id\n(D3, AC2) and the overall checked-vs-total count. Pass the ids to 'issue-cli check <slug> <id>...'.",
	Run:       runChecklist,
}

func init() {
	registerCommand(checklistCommand)
}

func runChecklist(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "checklist")
	if err != nil {
		return err
	}
	fs := newFlagSet("checklist", ctx)
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	total, checked := tracker.CountCheckboxes(issue.BodyRaw)

	if ctx.JSONOutput {
		items := tracker.ListCheckboxes(issue.BodyRaw)
		boxes := make([]map[string]interface{}, 0, len(items))
		for _, it := range items {
			boxes = append(boxes, map[string]interface{}{
				"section": it.Section,
				"index":   it.Index,
				"id":      it.ID,
				"text":    it.Text,
				"checked": it.Checked,
			})
		}
		return writeJSON(ctx.Stdout, map[string]interface{}{
			"total": total, "checked": checked, "items": boxes,
		})
	}

	fmt.Fprintf(ctx.Stdout, "== Checklist (%d/%d) ==\n", checked, total)
	printCheckboxes(ctx.Stdout, issue.BodyRaw)
	printTickHint(ctx.Stdout, issue.Slug, issue.BodyRaw)
	printProofHint(ctx, issue)
	return nil
}

// printProofHint says that the acceptance criteria are ticked by their
// proof, and how many are proven, when the proof gate covers the issue.
func printProofHint(ctx *Context, issue *tracker.Issue) {
	if !ctx.Project.LoadWorkflowForIssue(issue).ProofRequired(issue) {
		return
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	r := proof.Collect(projectRoot(ctx.Project), issue, comments)
	var proven []string
	for _, c := range r.Criteria {
		if c.Proven() {
			proven = append(proven, c.ID)
		}
	}
	which := ""
	if len(proven) > 0 {
		which = " (" + strings.Join(proven, " ") + ")"
	}
	fmt.Fprintf(ctx.Stdout, "Acceptance criteria are ticked by their proof, not by hand: %d/%d proven%s. See each link and what is missing: issue-cli prove %s --list\n",
		len(proven), len(r.Criteria), which, issue.Slug)
}
