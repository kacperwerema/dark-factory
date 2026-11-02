package main

import (
	"fmt"
	"regexp"
	"strings"

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
	slug, rest, err := requireSlug(args, "replace")
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
	_, _, err = tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		next, ok, err := tracker.ReplaceIssueBodySection(body, section, text, force)
		if err == nil && ok {
			err = guard(body, next)
		}
		return next, ok, err
	})
	if err != nil {
		return writeErr("failed to replace", err)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Replaced section %q in %s\n", section, issue.Slug)
	return nil
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
	var old string
	guard := ownerTickGuard(ctx, issue)
	_, _, err = tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		next, was, err := tracker.ReplaceSectionLine(body, section, line, re, text)
		if err != nil {
			return body, false, err
		}
		old = was
		return next, next != body, guard(body, next)
	})
	if err != nil {
		return writeErr("failed to replace", err)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Replaced a line of section %q in %s\n", section, issue.Slug)
	fmt.Fprintf(ctx.Stdout, "  was: %s\n", old)
	fmt.Fprintf(ctx.Stdout, "  now: %s\n", text)
	return nil
}
