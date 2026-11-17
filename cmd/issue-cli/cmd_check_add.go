package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var checkAddCommand = &Command{
	Name:      "check-add",
	ShortHelp: "Add one open box to a section; prints its new id",
	LongHelp: `Add one unticked box to a "## " section and print its id. The box goes
right after the section's last box, so it takes the next index (AC4 → AC5)
and every other box keeps its id.

Examples:
  issue-cli check-add <slug> --section "Acceptance criteria" --text "Refuses an empty name"
  issue-cli check-add <slug> --section Design --text "Error wording reviewed"

The section must exist (any case); an unknown one is refused with the
issue's sections. To start a new section: issue-cli append <slug> --section
<name> --body "- [ ] ...". An agent can't add a box the owner ticks: text
marked "(owner)" or a box in Human Testing. Change a box's text with
issue-cli check-edit, drop one with issue-cli check-remove.`,
	Run: runCheckAdd,
}

func init() {
	registerCommand(checkAddCommand)
}

func runCheckAdd(ctx *Context, args []string) error {
	slug, rest, err := issueSlug(ctx, args, "check-add")
	if err != nil {
		return err
	}
	fs := newFlagSet("check-add", ctx)
	sectionFlag := fs.String("section", "", `the "## " section to add the box to`)
	textFlag := fs.String("text", "", "the box's text (one line)")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	example := fmt.Sprintf("\n\nExample:\n  issue-cli check-add %s --section \"Acceptance criteria\" --text \"new box text\"", slug)
	if fs.NArg() != 0 {
		return &usageError{msg: "check-add takes no words after the slug: the text goes in --text" + example}
	}
	section := strings.TrimSpace(*sectionFlag)
	text := strings.TrimSpace(normalizeEscapedText(*textFlag))
	if section == "" {
		return fmt.Errorf("check-add requires --section%s", example)
	}
	if text == "" {
		return fmt.Errorf("check-add requires --text%s", example)
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	// Dry run on the body as read: the refusals (unknown section, owner
	// box) change nothing.
	_, preview, err := tracker.AddCheckbox(issue.BodyRaw, section, text)
	if err != nil {
		return fmt.Errorf("%w\n\nRun: issue-cli checklist %s", err, issue.Slug)
	}
	if who := guardAgent(ctx, "change owner boxes"); who != "" {
		if why := boxProofReport(ctx, issue).OwnerBoxWhy(preview); why != "" {
			return fmt.Errorf("%s would be the owner's box (%s): %s can't add it, and nothing changed. The owner adds their own boxes; add yours without the (owner) marker, outside Human Testing.\n\nAsk on your issue trail:\n  %s",
				preview.ID, why, who, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: ...\"", issue.Slug)))
		}
	}

	var added tracker.CheckboxItem
	_, _, err = tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		next, item, err := tracker.AddCheckbox(body, section, text)
		if err != nil {
			return body, false, err
		}
		added = item
		return next, true, nil
	})
	if err != nil {
		return fmt.Errorf("%w\n\nRun: issue-cli checklist %s", err, issue.Slug)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]interface{}{
			"id": added.ID, "section": added.Section, "index": added.Index, "text": added.Text,
		})
	}
	ref := added.ID
	if ref == "" {
		ref = shell.Quote(fmt.Sprintf("%s#%d", added.Section, added.Index))
	}
	fmt.Fprintf(ctx.Stdout, "✓ Added %s [%s #%d]: %s\n", ref, added.Section, added.Index, added.Label())
	fmt.Fprintln(ctx.Stdout, "  The other boxes keep their ids.")
	fmt.Fprintf(ctx.Stdout, "  Tick it when done: issue-cli check %s %s\n", issue.Slug, ref)
	fmt.Fprintf(ctx.Stdout, "file: %s\n", issue.FilePath)
	return nil
}
