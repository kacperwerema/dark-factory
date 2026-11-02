package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var checkEditCommand = &Command{
	Name:      "check-edit",
	ShortHelp: "Change one checkbox's text by id (keeps its tick)",
	LongHelp: `Replace the text of one checkbox, addressed by its id from 'issue-cli checklist'.
The box keeps its tick state and position, so its id doesn't change.

Examples:
  issue-cli check-edit <slug> D3 --text "Approach documented in ## Design"
  issue-cli check-edit <slug> "Design#3" --text "..."

Don't start the text with the id: the CLI prints the id in front of the box
already (a leading copy of it is dropped). To drop a box instead, keeping
the other boxes' ids: issue-cli check-remove <slug> <id>. To change other
lines in a section, use: issue-cli replace <slug> --section <name> --line N --text "..."`,
	Run: runCheckEdit,
}

func init() {
	registerCommand(checkEditCommand)
}

func runCheckEdit(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "check-edit")
	if err != nil {
		return err
	}
	fs := newFlagSet("check-edit", ctx)
	textFlag := fs.String("text", "", "the box's new text")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	example := fmt.Sprintf("\n\nExample:\n  issue-cli check-edit %s D3 --text \"new box text\"", slug)
	if fs.NArg() != 1 {
		return &usageError{msg: "check-edit needs exactly one box id (see issue-cli checklist " + slug + ")" + example}
	}
	ref := fs.Arg(0)
	text := strings.TrimSpace(normalizeEscapedText(*textFlag))
	if text == "" {
		return fmt.Errorf("check-edit requires --text%s", example)
	}
	if strings.Contains(text, "\n") {
		return fmt.Errorf("a checkbox's text must be one line%s", example)
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	if err := refuseOwnerBoxEdit(ctx, issue, ref, text); err != nil {
		return err
	}
	var before tracker.CheckboxItem
	_, _, err = tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		next, item, err := tracker.EditCheckboxText(body, ref, text)
		if err != nil {
			return body, false, err
		}
		before = item
		return next, next != body, nil
	})
	if err != nil {
		return fmt.Errorf("%w\n\nRun: issue-cli checklist %s", err, issue.Slug)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Edited %s [%s #%d]\n", before.ID, before.Section, before.Index)
	fmt.Fprintf(ctx.Stdout, "  was: %s\n", before.Label())
	fmt.Fprintf(ctx.Stdout, "  now: %s\n", tracker.StripCheckboxID(before.ID, text))
	fmt.Fprintf(ctx.Stdout, "file: %s\n", issue.FilePath)
	return nil
}

// refuseOwnerBoxEdit refuses an agent session an edit that would take an
// owner box from the owner: an N/A written into it, or its "(owner)"
// marker dropped. Other edits to the wording pass.
func refuseOwnerBoxEdit(ctx *Context, issue *tracker.Issue, ref, text string) error {
	who := agentSession(ctx)
	if who == "" {
		return nil
	}
	it, st := tracker.ResolveCheckboxRef(tracker.ListCheckboxes(issue.BodyRaw), ref)
	if st != tracker.RefFound {
		return nil
	}
	why := boxProofReport(ctx, issue).OwnerBoxWhy(it)
	if why == "" {
		return nil
	}
	_, na := tracker.NAClaim(text)
	dropsMarker := proof.IsOwnerCriterion(it.Text) && !proof.IsOwnerCriterion(text)
	if !na && !dropsMarker {
		return nil
	}
	return fmt.Errorf("%s is the owner's box (%s): %s can't write an N/A into it or drop its (owner) marker, and nothing changed. Leave it for the owner; under YOLO it waits in the review queue.\n\nAsk on your issue trail:\n  %s",
		it.ID, why, who, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: ...\"", issue.Slug)))
}
