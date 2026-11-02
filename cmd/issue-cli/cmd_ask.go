package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The sibling channel: ask and answer between the workers of two open
// issues, as comments (internal/tracker/sibling_ask.go). The ask shows on
// the sibling's next issue-cli call on its issue and the answer on the
// asker's (printSiblingAsks), and the lead sees open asks on the Status page.

var askCommand = &Command{
	Name:      "ask",
	ShortHelp: "Ask a sibling issue's worker to agree something (a format, a field); the answer shows on your next call",
	LongHelp: `Ask the worker of another open issue (a sibling you build on, or one that
builds on you) to agree something with you: a record format, a field name,
an interface. The ask is written as a comment on the sibling's issue:

  ask: from <your slug>: <words>

and shows at the bottom of the sibling's next issue-cli call on its own
issue, with the line to answer it. Its answer comes back the same way on
your issue. The lead sees open asks on the Status page.

` + tracker.NotAnApproval + `
Only the owner (or the lead, under YOLO) approves; words starting with
"approval:" are refused.

Your slug is ISSUE_VIEWER_ISSUE_SLUG (a dispatched worker has it), else
--from <slug>.

Examples:
  issue-cli ask cli/declared-connections "I read handoff_route: as box keys joined by ' → '. Same for you?"
  issue-cli ask cli/declared-connections --from cli/my-issue --text "..."`,
	Run: runAsk,
}

var answerCommand = &Command{
	Name:      "answer",
	ShortHelp: "Answer a sibling's ask on your issue; the answer shows on the asker's next call",
	LongHelp: `Answer the open asks another issue's worker made on your issue
(issue-cli ask). The answer is written as a comment on the asker's issue:

  answer: from <your slug> (ask #<id>): <words>

and shows at the bottom of the asker's next issue-cli call on its issue.
The asks it answers are closed. Refused when the asker has no open ask on
your issue: the channel answers asks, it doesn't start them (use
issue-cli ask for that).

` + tracker.NotAnApproval + `

Your slug is ISSUE_VIEWER_ISSUE_SLUG (a dispatched worker has it), else
--from <slug>.

Examples:
  issue-cli answer cli/consumer "Yes: box keys, lower case, joined by ' → '."
  issue-cli answer cli/consumer --from cli/my-issue --text "..."`,
	Run: runAnswer,
}

func init() {
	registerCommand(askCommand)
	registerCommand(answerCommand)
}

// siblingArgs parses <slug> [--from <slug>] (--text|--body|words...).
func siblingArgs(ctx *Context, name string, args []string) (target, from, words string, err error) {
	target, rest, err := requireSlug(args, name)
	if err != nil {
		return "", "", "", err
	}
	fs := newFlagSet(name, ctx)
	fromFlag := fs.String("from", "", "your issue's slug (default: ISSUE_VIEWER_ISSUE_SLUG)")
	textFlag := fs.String("text", "", "the words")
	bodyFlag := fs.String("body", "", "alias for --text")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return "", "", "", err
	}
	words = normalizeEscapedText(*textFlag)
	if words == "" {
		words = normalizeEscapedText(*bodyFlag)
	}
	if words == "" {
		words = strings.Join(fs.Args(), " ")
	}
	words = strings.TrimSpace(words)
	if words == "" {
		return "", "", "", &usageError{msg: fmt.Sprintf("%s: the words are required\n\nExample:\n  issue-cli %s %s \"...\"", name, name, target)}
	}
	if strings.HasPrefix(strings.ToLower(words), "approval:") {
		return "", "", "", fmt.Errorf("%s: %s\nWords starting with \"approval:\" are refused; ask the lead with a question: comment on your own issue", name, tracker.NotAnApproval)
	}
	from = strings.TrimSpace(*fromFlag)
	if from == "" {
		from = strings.TrimSpace(os.Getenv("ISSUE_VIEWER_ISSUE_SLUG"))
	}
	if from == "" {
		return "", "", "", &usageError{msg: fmt.Sprintf("%s: your issue is unknown (ISSUE_VIEWER_ISSUE_SLUG unset): pass --from <your slug>", name)}
	}
	return target, from, words, nil
}

// siblingPair finds both issues and refuses a pair the channel doesn't take.
func siblingPair(ctx *Context, name, target, from string) (other, mine *tracker.Issue, err error) {
	other, issues, err := findIssueOrErr(ctx, target)
	if err != nil {
		return nil, nil, err
	}
	mine = tracker.FindIssue(issues, from)
	if mine == nil {
		return nil, nil, fmt.Errorf("%s: your issue %q is not on this board (--from or ISSUE_VIEWER_ISSUE_SLUG)", name, from)
	}
	if strings.EqualFold(other.Slug, mine.Slug) {
		return nil, nil, fmt.Errorf("%s: %s is your own issue; ask a sibling issue's worker", name, mine.Slug)
	}
	if tracker.ClosedIssueFunc(ctx.Project)(other) || strings.EqualFold(other.Status, "parked") {
		return nil, nil, fmt.Errorf("%s: %s is %s; nobody works on it to answer. Read its code or decisions instead", name, other.Slug, other.Status)
	}
	return other, mine, nil
}

func runAsk(ctx *Context, args []string) error {
	target, from, words, err := siblingArgs(ctx, "ask", args)
	if err != nil {
		return err
	}
	sibling, mine, err := siblingPair(ctx, "ask", target, from)
	if err != nil {
		return err
	}
	if err := tracker.AddCommentBy(sibling.FilePath, 0, tracker.AskComment(mine.Slug, words), "cli", commentAuthor(ctx.Project, mine)); err != nil {
		return fmt.Errorf("ask: %w", err)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Asked %s (ask: on its issue)\n", sibling.Slug)
	fmt.Fprintf(ctx.Stdout, "It shows on that worker's next issue-cli call; the answer shows on yours (%s).\n", mine.Slug)
	fmt.Fprintln(ctx.Stdout, "Don't wait or poll for it: carry on and read it when it comes. "+tracker.NotAnApproval)
	return nil
}

func runAnswer(ctx *Context, args []string) error {
	target, from, words, err := siblingArgs(ctx, "answer", args)
	if err != nil {
		return err
	}
	asker, mine, err := siblingPair(ctx, "answer", target, from)
	if err != nil {
		return err
	}
	comments, err := tracker.LoadComments(mine.FilePath)
	if err != nil {
		return err
	}
	var ids []int
	open := tracker.OpenAsksOn(mine.Slug, comments)
	for _, a := range open {
		if strings.EqualFold(a.From, asker.Slug) {
			ids = append(ids, a.ID)
		}
	}
	if len(ids) == 0 {
		var froms []string
		for _, a := range open {
			froms = append(froms, a.From)
		}
		msg := fmt.Sprintf("answer: %s has no open ask on %s", asker.Slug, mine.Slug)
		if len(froms) > 0 {
			msg += "; open asks are from: " + strings.Join(froms, ", ")
		}
		return fmt.Errorf("%s\nTo start a conversation: %s", msg, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli ask %s \"...\"", asker.Slug)))
	}
	if err := tracker.AddCommentBy(asker.FilePath, 0, tracker.AnswerComment(mine.Slug, ids, words), "cli", commentAuthor(ctx.Project, mine)); err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	if err := tracker.MarkCommentsDone(mine.FilePath, ids); err != nil {
		return fmt.Errorf("answer written on %s, but closing the ask failed: %w", asker.Slug, err)
	}
	fmt.Fprintf(ctx.Stdout, "✓ Answered %s (answer: on its issue); %d %s closed on %s\n", asker.Slug, len(ids), pluralWord(len(ids), "ask", "asks"), mine.Slug)
	fmt.Fprintln(ctx.Stdout, "It shows on that worker's next issue-cli call. "+tracker.NotAnApproval)
	return nil
}

// siblingAsksShown caps the lines of each kind the block prints.
const siblingAsksShown = 5

// printSiblingAsks is the channel's delivery: after a command that looked up
// an issue (ctx.issue), it prints the issue's open asks (to answer) and the
// answers not yet delivered.
// When the caller is that issue's own worker the printed answers count as
// delivered. Nothing prints for commands that wrote the channel themselves,
// JSON output or an issue without any.
func printSiblingAsks(ctx *Context, command string) {
	if ctx == nil || ctx.Project == nil || ctx.JSONOutput || ctx.issue == nil || command == "ask" || command == "answer" {
		return
	}
	issue := ctx.issue
	comments, err := tracker.LoadComments(issue.FilePath)
	if err != nil {
		return
	}
	asks := tracker.OpenAsksOn(issue.Slug, comments)
	var answers []tracker.SiblingAnswer
	for _, a := range tracker.AnswersOn(comments) {
		if !a.Delivered {
			answers = append(answers, a)
		}
	}
	if len(asks) == 0 && len(answers) == 0 {
		return
	}
	w := ctx.Stdout
	fmt.Fprintln(w)
	fmt.Fprintf(w, "== Sibling asks (%s) ==\n", issue.Slug)
	writeAsks(w, ctx.Project, asks)
	var delivered []int
	for i, a := range answers {
		if i == siblingAsksShown {
			fmt.Fprintf(w, "  … %d more answers: issue-cli show %s\n", len(answers)-i, issue.Slug)
			break
		}
		fmt.Fprintf(w, "  answer from %s: %s\n", a.From, a.Words)
		delivered = append(delivered, a.ID)
	}
	fmt.Fprintln(w, tracker.NotAnApproval)
	if self := strings.TrimSpace(os.Getenv("ISSUE_VIEWER_ISSUE_SLUG")); self != "" && strings.EqualFold(self, issue.Slug) {
		_ = tracker.MarkCommentsDone(issue.FilePath, delivered)
	}
}

func writeAsks(w io.Writer, proj *tracker.Project, asks []tracker.SiblingAsk) {
	for i, a := range asks {
		if i == siblingAsksShown {
			fmt.Fprintf(w, "  … %d more asks\n", len(asks)-i)
			break
		}
		fmt.Fprintf(w, "  ask from %s (#%d): %s\n", a.From, a.ID, a.Words)
		fmt.Fprintf(w, "    answer: %s\n", tracker.InjectProject(proj, fmt.Sprintf("issue-cli answer %s \"...\"", a.From)))
	}
}
