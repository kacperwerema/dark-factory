package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var appendCommand = &Command{
	Name:      "append",
	ShortHelp: "Append content to issue body",
	LongHelp: `Append content to the issue body. With --section, append into an existing
section; if --body starts with a heading the issue has at that level (or a
shallower one), it auto-routes into that section. A deeper heading of the name
is a different section: "## X" on an issue with only "### X" starts a new one.
A sub-heading of the body that the section already has ("### Automated" into
a Test Plan with one) is not written twice: its lines go under it, and the
success line names it. An append that still leaves a heading twice warns.

Use --body-file <path> (or --body-file - for stdin) to supply the body as raw
bytes, which avoids shell mangling of backticks and parentheses in inline code.

To fix one line of a section instead of adding a correction paragraph, name
the line exactly with --replace: the one line that reads it (leading and
trailing spaces aside) becomes --body. No match, or several, is refused with
the section's numbered lines. (Same as issue-cli replace --match.) To replace
a whole section, use issue-cli replace <slug> --section "<name>" --body-file <file>;
a bare --replace is refused with both commands named.

For screenshots use issue-cli attach <slug> <file> --section "<name>": it saves
the image where the viewer serves it and appends its link.

Examples:
  issue-cli append <slug> --section "Design" --body "- [ ] edge case covered"
  issue-cli append <slug> --body "## Test Plan\n\n### Automated\n- test 1"
  issue-cli append <slug> --section "Design" --body-file design.md
  issue-cli append <slug> --section "Design" --replace "- Risk: tbd" --body "- Risk: none"
  printf '%s' "- uses ` + "`Asteroid.NetRate`" + ` (workers × yield)" | issue-cli append <slug> --body-file -`,
	Run: runAppend,
}

func init() {
	registerCommand(appendCommand)
}

func runAppend(ctx *Context, args []string) error {
	slug, rest, err := issueSlug(ctx, args, "append")
	if err != nil {
		return err
	}
	fs := newFlagSet("append", ctx)
	bodyFlag := fs.String("body", "", "content to append")
	textFlag := fs.String("text", "", "alias for --body")
	bodyFileFlag := fs.String("body-file", "", "read body from file (or - for stdin); avoids shell mangling")
	sectionFlag := fs.String("section", "", "section name to append into")
	forceFlag := fs.Bool("force", false, "force append even when target section is missing")
	replaceFlag := fs.String("replace", "", "replace this one exact line of --section with --body instead of appending")
	if err := parseFlags(ctx, fs, rest); err != nil {
		if strings.HasPrefix(err.Error(), "flag needs an argument: --replace") {
			return bareReplaceError(slug, sectionArg(rest), err)
		}
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
	if text == "" {
		return fmt.Errorf("append requires --body or --body-file\n\nExamples:\n  issue-cli append <slug> --section \"Design\" --body \"- [ ] edge case covered\"\n  issue-cli append <slug> --section \"Design\" --body-file design.md\n  cat design.md | issue-cli append <slug> --section \"Design\" --body-file -")
	}

	if flagWasSet(fs, "replace") {
		return appendReplace(ctx, slug, section, *replaceFlag, text, force)
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}

	guard := ownerTickGuard(ctx, issue)
	var before, after string
	_, _, err = tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		next, ok, err := tracker.AppendIssueBody(body, text)
		if strings.TrimSpace(section) != "" {
			next, ok, err = tracker.AppendIssueBodyToSection(body, section, text, force)
		}
		if err == nil && ok {
			err = guard(body, next)
			before, after = body, next
		}
		return next, ok, err
	})
	if err != nil {
		return writeErr("failed to append", err)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Appended to %s\n", issue.Slug)
	printSubheadingNotes(ctx, issue.Slug, section, text, before, after)
	hintSection := section
	if strings.TrimSpace(hintSection) == "" {
		hintSection = "<section>"
	}
	fmt.Fprintf(ctx.Stdout, "Wrong line? issue-cli append %s --section %s --replace '<old line>' --body '<new line>' swaps it.\n", issue.Slug, shell.Quote(hintSection))
	printDesignEditPreview(ctx.Stdout, ctx, issue, before, after)
	return nil
}

// printSubheadingNotes says which of the body's sub-headings the append put
// its lines under (the section already had them), and warns when the append
// still wrote a heading a second time in one place.
func printSubheadingNotes(ctx *Context, slug, section, text, before, after string) {
	if before == after {
		return
	}
	target := section
	if strings.TrimSpace(target) == "" {
		// Auto-routed: the section is the body's opening heading.
		first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
		target = first
	}
	if merged := tracker.MergedSubheadings(before, after, target, text); len(merged) > 0 {
		fmt.Fprintf(ctx.Stdout, "Merged under the existing %s: no second heading written.\n", strings.Join(merged, ", "))
	}
	if dups := tracker.NewDuplicateHeadings(before, after); len(dups) > 0 {
		fmt.Fprintf(ctx.Stderr, "warning: the issue now has %s twice in one place; see it with issue-cli show %s and fold the copy back with issue-cli replace %s --section <name> --body-file <file>\n", strings.Join(dups, ", "), slug, slug)
	}
}

// appendReplace is append --replace: the one line of section that reads
// old (leading and trailing spaces aside) becomes text, through replace's
// one-line path, so zero or several matches are refused with the section's
// numbered lines.
func appendReplace(ctx *Context, slug, section, old, text string, force bool) error {
	example := "\n\nExample:\n  issue-cli append " + slug + " --section \"Design\" --replace \"- Risk: tbd\" --body \"- Risk: none\""
	switch {
	case strings.TrimSpace(section) == "":
		return &usageError{msg: "append --replace needs --section: the line is looked up in one section" + example}
	case force:
		return &usageError{msg: "append --replace takes no --force: the line must already be in the section" + example}
	case strings.TrimSpace(old) == "":
		return bareReplaceError(slug, section, nil)
	case strings.Contains(strings.TrimSpace(old), "\n"):
		return &usageError{msg: "append --replace takes one old line; for a whole section use issue-cli replace " + slug + " --section \"" + section + "\" --body-file <file>"}
	}
	re := regexp.MustCompile(`^[ \t]*` + regexp.QuoteMeta(strings.TrimSpace(old)) + `[ \t]*$`)
	err := replaceLine(ctx, slug, section, text, 0, re.String(), false, true)
	if err != nil {
		// Name the line the caller gave, not the regexp built from it.
		msg := strings.Replace(err.Error(), "--match "+strconv.Quote(re.String()), "--replace "+shell.Quote(strings.TrimSpace(old)), 1)
		msg = strings.Replace(msg, "narrow it or pass --line", "give the whole line, or pick one by number: issue-cli replace "+slug+" --section "+shell.Quote(section)+" --line <n> --text '<new line>'", 1)
		if msg != err.Error() {
			return fmt.Errorf("%s", msg)
		}
	}
	return err
}

// bareReplaceError refuses append --replace without the old line: the caller
// most likely meant to replace the whole section, which is replace --section.
// It names that command and the one-line form, with the slug and section the
// call gave.
func bareReplaceError(slug, section string, err error) error {
	name := "<name>"
	if strings.TrimSpace(section) != "" {
		name = shell.Quote(section)
	}
	return &usageError{err: err, msg: "append --replace takes the old line's text: it swaps one line of the section.\n\n" +
		"Whole section:\n  issue-cli replace " + slug + " --section " + name + " --body-file <file>\n" +
		"One line:\n  issue-cli append " + slug + " --section " + name + " --replace '<old line>' --body '<new line>'"}
}

// sectionArg is the --section value in args (as --section X or
// --section=X), for a message written when parsing failed; "" when none.
func sectionArg(args []string) string {
	for i, a := range args {
		if a == "--" {
			break
		}
		for _, f := range []string{"--section", "-section"} {
			if v, ok := strings.CutPrefix(a, f+"="); ok {
				return v
			}
			if a == f && i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return ""
}
