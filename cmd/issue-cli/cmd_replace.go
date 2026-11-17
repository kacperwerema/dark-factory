package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var replaceCommand = &Command{
	Name:      "replace",
	ShortHelp: "Replace content of an existing section, or one line of it",
	LongHelp: `Replace the body of an existing top-level section, or one line of it.

Whole section:
  issue-cli replace <slug> --section "Design" --body "updated approach"
  issue-cli replace <slug> --section "Design" --body-file design.md
  cat design.md | issue-cli replace <slug> --section "Design" --body-file -

--body-file <path> (or - for stdin) reads the new content as raw bytes, as
append does, which avoids shell mangling of backticks and parentheses.
A new body that lacks a ### heading or a checkbox the old section had is
still written, with a warning naming each dropped heading, and each dropped
box by its old id and text with the check-add line that puts one back
(a later transition --check <id> would name a box that is gone).

One line, by number or by a regexp that matches exactly one line:
  issue-cli replace <slug> --section "Design" --line 3 --text "new line"
  issue-cli replace <slug> --section "Design" --match "^- Risk:" --text "- Risk: none"

Lines count from the line after the heading, blank lines included. When the
line can't be chosen, the error lists the section with line numbers.
To change a checkbox's text, use: issue-cli check-edit <slug> <id> --text "...";
to untick or drop one, issue-cli uncheck / check-remove <slug> <id>. Rewriting
a section's boxes renumbers the ids after a removed one, and Proves: trailers
that name them stop pointing at the right box.`,
	Run: runReplace,
}

func init() {
	registerCommand(replaceCommand)
}

func runReplace(ctx *Context, args []string) error {
	slug, rest, err := issueSlug(ctx, args, "replace")
	if err != nil {
		return err
	}
	fs := newFlagSet("replace", ctx)
	bodyFlag := fs.String("body", "", "new section content")
	textFlag := fs.String("text", "", "alias for --body")
	bodyFileFlag := fs.String("body-file", "", "read the new content from file (or - for stdin); avoids shell mangling")
	sectionFlag := fs.String("section", "", "section name to replace")
	forceFlag := fs.Bool("force", false, "force replace even when section is absent")
	lineFlag := fs.Int("line", 0, "replace only this line of the section (1-based)")
	matchFlag := fs.String("match", "", "replace only the one line this regexp matches")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	inline := normalizeEscapedText(*bodyFlag)
	if inline == "" {
		inline = normalizeEscapedText(*textFlag)
	}
	inlineSet := flagWasSet(fs, "body") || flagWasSet(fs, "text")
	text, err := resolveBodyInput(ctx, inline, inlineSet, *bodyFileFlag, flagWasSet(fs, "body-file"))
	if err != nil {
		return err
	}
	section := *sectionFlag
	force := *forceFlag
	if strings.TrimSpace(section) == "" {
		return fmt.Errorf("replace requires --section\n\nExample:\n  issue-cli replace %s --section \"Design\" --body \"updated approach\"", slug)
	}
	if text == "" {
		return fmt.Errorf("replace requires --body or --body-file\n\nExamples:\n  issue-cli replace %s --section \"Design\" --body \"updated approach\"\n  issue-cli replace %s --section \"Design\" --body-file design.md", slug, slug)
	}

	lineSet, matchSet := flagWasSet(fs, "line"), flagWasSet(fs, "match")
	if lineSet || matchSet {
		return replaceLine(ctx, slug, section, text, *lineFlag, *matchFlag, lineSet, matchSet)
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}

	guard := ownerTickGuard(ctx, issue)
	var dropped []string
	var droppedBoxes []tracker.CheckboxItem
	var testPlanShort bool
	var before, after string
	_, _, err = tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		old, _ := tracker.SectionText(body, section)
		next, ok, err := tracker.ReplaceIssueBodySection(body, section, text, force)
		before, after = body, next
		if err == nil && ok {
			err = guard(body, next)
		}
		dropped = droppedSubheadings(old, text)
		if err == nil && ok {
			droppedBoxes = droppedCheckboxes(body, next, section)
		}
		if strings.EqualFold(strings.TrimSpace(strings.TrimLeft(section, "# ")), "Test Plan") {
			auto, manual := tracker.HasTestPlan(next)
			testPlanShort = !auto || !manual
		}
		return next, ok, err
	})
	if err != nil {
		return writeErr("failed to replace", err)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Replaced section %q in %s\n", section, issue.Slug)
	if len(dropped) > 0 {
		fmt.Fprintf(ctx.Stdout, "⚠ the new body drops %s, which the old section had\n", strings.Join(dropped, ", "))
		if testPlanShort {
			fmt.Fprintln(ctx.Stdout, "  has_test_plan needs `### Automated` and `### Manual` under ## Test Plan: put them back before the next transition")
		}
	}
	printDroppedBoxes(ctx, issue.Slug, section, droppedBoxes)
	printDesignEditPreview(ctx.Stdout, ctx, issue, before, after)
	return nil
}

// droppedCheckboxes lists each box of section in before that the section in
// after no longer holds, in before's order. Boxes match by text (whitespace
// and case folded), each new box standing in for one old one, so a reworded
// box counts as dropped and a re-ticked or moved one doesn't.
func droppedCheckboxes(before, after, section string) []tracker.CheckboxItem {
	name := strings.TrimSpace(strings.TrimLeft(section, "# "))
	key := func(text string) string { return strings.ToLower(strings.Join(strings.Fields(text), " ")) }
	have := map[string]int{}
	for _, it := range tracker.ListCheckboxes(after) {
		if strings.EqualFold(it.Section, name) {
			have[key(it.Text)]++
		}
	}
	var dropped []tracker.CheckboxItem
	for _, it := range tracker.ListCheckboxes(before) {
		if !strings.EqualFold(it.Section, name) {
			continue
		}
		if k := key(it.Text); have[k] > 0 {
			have[k]--
		} else {
			dropped = append(dropped, it)
		}
	}
	return dropped
}

// printDroppedBoxes warns about the boxes a replace dropped: each by its old
// id, and the check-add line that puts one back.
func printDroppedBoxes(ctx *Context, slug, section string, boxes []tracker.CheckboxItem) {
	if len(boxes) == 0 {
		return
	}
	ids := make([]string, len(boxes))
	for i, b := range boxes {
		ids[i] = b.ID
	}
	noun, verb := "boxes", "name them"
	if len(boxes) == 1 {
		noun, verb = "box", "names it"
	}
	fmt.Fprintf(ctx.Stdout, "⚠ the new body drops %d %s the old section had; %s no longer %s:\n", len(boxes), noun, strings.Join(ids, " "), verb)
	for _, b := range boxes {
		tick := " "
		if b.Checked {
			tick = "x"
		}
		fmt.Fprintf(ctx.Stdout, "  %s [%s] %s\n", b.ID, tick, b.Label())
	}
	name := strings.TrimSpace(strings.TrimLeft(section, "# "))
	fmt.Fprintf(ctx.Stdout, "  put one back: issue-cli check-add %s --section %s --text '<text>'\n", shell.Quote(slug), shell.Quote(name))
}

// droppedSubheadings lists each "### " heading of old that new lacks, in
// old's order, compared without case; headings inside fenced code don't count.
func droppedSubheadings(old, new string) []string {
	have := map[string]bool{}
	for _, h := range subheadings(new) {
		have[strings.ToLower(h)] = true
	}
	var dropped []string
	for _, h := range subheadings(old) {
		if key := strings.ToLower(h); !have[key] {
			have[key] = true
			dropped = append(dropped, "`"+h+"`")
		}
	}
	return dropped
}

// subheadings returns the "### " heading lines of text, trimmed, outside
// fenced code.
func subheadings(text string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(trimmed, "### ") {
			out = append(out, strings.Join(strings.Fields(trimmed), " "))
		}
	}
	return out
}

// replaceLine is replace --line / --match: one line of the section.
func replaceLine(ctx *Context, slug, section, text string, line int, match string, lineSet, matchSet bool) error {
	if lineSet && matchSet {
		return &usageError{msg: "replace takes --line or --match, not both"}
	}
	var re *regexp.Regexp
	if matchSet {
		var err error
		if re, err = regexp.Compile(match); err != nil {
			return &usageError{msg: fmt.Sprintf("--match is not a valid regexp: %v", err)}
		}
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	var old, before, after string
	guard := ownerTickGuard(ctx, issue)
	_, _, err = tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		next, was, err := tracker.ReplaceSectionLine(body, section, line, re, text)
		if err != nil {
			return body, false, err
		}
		old, before, after = was, body, next
		return next, next != body, guard(body, next)
	})
	if err != nil {
		return writeErr("failed to replace", err)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Replaced a line of section %q in %s\n", section, issue.Slug)
	fmt.Fprintf(ctx.Stdout, "  was: %s\n", old)
	fmt.Fprintf(ctx.Stdout, "  now: %s\n", text)
	printDesignEditPreview(ctx.Stdout, ctx, issue, before, after)
	return nil
}
