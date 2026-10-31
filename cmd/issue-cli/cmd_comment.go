package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

var commentCommand = &Command{
	Name:      "comment",
	ShortHelp: "Add a comment to an issue",
	LongHelp: `Append a comment block to an issue file.

Use --body-file <path> (or --body-file - for stdin) to supply the comment as raw
bytes, which avoids shell mangling of backticks and parentheses.

Examples:
  issue-cli comment <slug> "your comment here"
  issue-cli comment <slug> --text "tests: 3 unit tests added"
  issue-cli comment <slug> --body-file note.md
  cat note.md | issue-cli comment <slug> --body-file -

Correct a comment you wrote (a typo in a tests: line):
  issue-cli comment edit <slug> <id|last> --text "tests: 44 on the branch"
Only your own latest comment on the issue, from a session that signs its
comments (a lead or a worker). The owner's comments, other agents' comments
and approval:, dispatch:, yolo-review: and yolo-owner-check: comments can't be
edited. The old text stays in the comment's history (edits in the file),
and show marks the comment (edited). Decisions (decide) are never edited.`,
	Run: runComment,
}

func init() {
	registerCommand(commentCommand)
}

func runComment(ctx *Context, args []string) error {
	if len(args) > 0 && args[0] == "edit" {
		return runCommentEdit(ctx, args[1:])
	}
	slug, rest, err := requireSlug(args, "comment")
	if err != nil {
		return err
	}
	fs := newFlagSet("comment", ctx)
	textFlag := fs.String("text", "", "comment text")
	bodyFlag := fs.String("body", "", "alias for --text")
	bodyFileFlag := fs.String("body-file", "", "read comment from file (or - for stdin); avoids shell mangling")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	inline := normalizeEscapedText(*textFlag)
	if inline == "" {
		inline = normalizeEscapedText(*bodyFlag)
	}
	if inline == "" {
		inline = strings.Join(fs.Args(), " ")
	}
	inlineSet := flagWasSet(fs, "text") || flagWasSet(fs, "body") || len(fs.Args()) > 0
	text, err := resolveBodyInput(ctx, inline, inlineSet, *bodyFileFlag, flagWasSet(fs, "body-file"))
	if err != nil {
		return err
	}
	if text == "" {
		return fmt.Errorf("text is required\n\nExample:\n  issue-cli comment %s \"your comment here\"\n  issue-cli comment %s --text \"your comment here\"\n  issue-cli comment %s --body-file note.md", slug, slug, slug)
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	author := commentAuthor(ctx.Project, issue)
	if err := tracker.AddCommentBy(issue.FilePath, 0, text, "cli", author); err != nil {
		return fmt.Errorf("failed to add comment: %w", err)
	}
	if author != "" {
		fmt.Fprintf(ctx.Stdout, "✓ Comment added to %s by %s\n", issue.Slug, author)
	} else {
		fmt.Fprintf(ctx.Stdout, "✓ Comment added to %s (no author: no lead or worker session detected)\n", issue.Slug)
	}
	fmt.Fprintf(ctx.Stdout, "file: %s\n", issue.FilePath)
	if _, typed, isBalance := tracker.ParseBalanceComment(balancePrefix(ctx), text); isBalance && !typed {
		fmt.Fprintf(ctx.Stdout, "Tip: issue-cli balance %s --metric <id> --state <state> --hypothesis \"...\" --change \"...\" --evidence \"...\" puts this on the Balance board as a typed note.\n", issue.Slug)
	}
	return nil
}

// lockedCommentPrefixes are comments no one edits: the owner's recorded
// words and the YOLO review trail.
var lockedCommentPrefixes = []string{"approval:", "dispatch:", "yolo-review:", "yolo-owner-check:"}

func lockedComment(text string) string {
	t := strings.TrimSpace(text)
	for _, p := range lockedCommentPrefixes {
		if strings.HasPrefix(t, p) {
			return p
		}
	}
	return ""
}

// runCommentEdit corrects the caller's own latest comment, keeping the old
// text in the comment's history.
func runCommentEdit(ctx *Context, args []string) error {
	usage := "\n\nExample:\n  issue-cli comment edit <slug> last --text \"tests: 44 on the branch\""
	slug, rest, err := requireSlug(args, "comment edit")
	if err != nil {
		return err
	}
	fs := newFlagSet("comment", ctx)
	textFlag := fs.String("text", "", "the corrected comment")
	bodyFlag := fs.String("body", "", "alias for --text")
	bodyFileFlag := fs.String("body-file", "", "read the corrected comment from file (or - for stdin)")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	pos := fs.Args()
	if len(pos) == 0 {
		return fmt.Errorf("comment edit needs the comment's id, or last%s", usage)
	}
	ref := pos[0]
	inline := normalizeEscapedText(*textFlag)
	if inline == "" {
		inline = normalizeEscapedText(*bodyFlag)
	}
	if inline == "" {
		inline = strings.Join(pos[1:], " ")
	}
	inlineSet := flagWasSet(fs, "text") || flagWasSet(fs, "body") || len(pos) > 1
	text, err := resolveBodyInput(ctx, inline, inlineSet, *bodyFileFlag, flagWasSet(fs, "body-file"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("the corrected text is required%s", usage)
	}
	if p := lockedComment(text); p != "" {
		return fmt.Errorf("a %s comment can't be written by an edit; nothing changed", p)
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	me := commentAuthor(ctx.Project, issue)
	if me == "" {
		return fmt.Errorf("no author: this shell is no lead or worker session, so it has no comments of its own to edit; nothing changed")
	}
	id := 0
	if !strings.EqualFold(ref, "last") {
		n, err := strconv.Atoi(strings.TrimPrefix(ref, "#"))
		if err != nil || n <= 0 {
			return fmt.Errorf("%q is no comment id: pass a number or last%s", ref, usage)
		}
		id = n
	}
	if id == 0 {
		comments, err := tracker.LoadComments(issue.FilePath)
		if err != nil {
			return err
		}
		latest, ok := tracker.LatestCommentBy(comments, me)
		if !ok {
			return fmt.Errorf("%s has no comment on %s to edit; nothing changed", me, issue.Slug)
		}
		id = latest.ID
	}
	c, err := tracker.EditComment(issue.FilePath, id, text, ctx.Now(), func(comments []tracker.Comment, c tracker.Comment) error {
		by := tracker.CommentAuthor(c)
		switch {
		case by == tracker.OwnerAuthor:
			return fmt.Errorf("comment #%d is the owner's: only they change it; nothing changed", c.ID)
		case by != me:
			who := by
			if who == "" {
				who = "no one recorded"
			}
			return fmt.Errorf("comment #%d is by %s, not %s: an agent edits only its own latest comment; nothing changed", c.ID, who, me)
		}
		if p := lockedComment(c.Text); p != "" {
			return fmt.Errorf("comment #%d is a %s comment: it records what was approved and is never edited; nothing changed", c.ID, p)
		}
		if latest, _ := tracker.LatestCommentBy(comments, me); latest.ID != c.ID {
			return fmt.Errorf("comment #%d is not your latest: only your latest comment (#%d) can be edited; add a new comment to correct an older one; nothing changed", c.ID, latest.ID)
		}
		return nil
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "no comment #") {
			return fmt.Errorf("%w on %s; nothing changed", err, issue.Slug)
		}
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Comment #%d on %s edited by %s (the old text stays in its history, %d edit(s))\n", c.ID, issue.Slug, me, len(c.Edits))
	fmt.Fprintf(ctx.Stdout, "file: %s\n", issue.FilePath)
	return nil
}
