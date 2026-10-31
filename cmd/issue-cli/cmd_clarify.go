package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

var clarifyCommand = &Command{
	Name:      "clarify",
	ShortHelp: "Show the issue's clarify round (## Clarify), assume open answers, or record the owner's",
	LongHelp: `The clarify round: the design step writes every question for the owner
once, under ## Clarify, each with 2-4 options, what each costs, and the
recommendation first citing the work. The owner answers them in one go on
the issue's Station in the viewer. Outside the round, a question: comment is
for a blocker only.

  ## Clarify

  ### Q1. Where does the owner answer?
  1. **On the Station** (recommended) one card, needs_you.go keeps one row. Cost: one template block.
  2. **In each Needs you row.** Cost: a form per row.
  **Answer:** —

The answer line is "—" (open), "<n> · <date> · owner: \"<words>\" (D-k)"
(the owner's, also the decision D-k) or "assumed[ <n>] — <reason>" (the
agent's alone; without <n> the recommendation). Nothing to ask: the whole
section is one line, "Nothing to ask: <why>."

With no flags it prints the round, each answer's state and the format's
problems.

  --assume          mark every open answer "assumed — <reason>" (the
                    recommendation). Under YOLO this replaces decide
                    --provisional lines and question: comments for the
                    design's questions; the YOLO pass out of in design does
                    it by itself, and the after-the-fact review lists them.
  --reason R        the reason for --assume (default "took the recommendation")
  --answer Qn=<k>   record the owner's answer, option k, given in chat (the
                    lead relays it): the answer line and a D-n decision
  --quote Q         the owner's own words for --answer (required)
  --how H           the decision's how for --answer (default "clarify round")
  --json            the parsed round as JSON

Examples:
  issue-cli clarify ui/board-filters
  issue-cli clarify ui/board-filters --assume --reason "YOLO: took the recommendation"
  issue-cli clarify ui/board-filters --answer Q2=1 --quote "Keep it on the Station." --how chat`,
	Run: runClarify,
}

func init() { registerCommand(clarifyCommand) }

type clarifyOutput struct {
	Slug     string                    `json:"slug"`
	Clarify  tracker.Clarify           `json:"clarify"`
	Assumed  []tracker.ClarifyQuestion `json:"assumed,omitempty"`
	Answered *tracker.ClarifyQuestion  `json:"answered,omitempty"`
	Decision *tracker.Decision         `json:"decision,omitempty"`
}

func runClarify(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "clarify")
	if err != nil {
		return err
	}
	fs := newFlagSet("clarify", ctx)
	assume := fs.Bool("assume", false, "mark every open answer assumed (the recommendation)")
	reason := fs.String("reason", "", "the reason for --assume")
	answer := fs.String("answer", "", "Qn=<option>: record the owner's answer given in chat")
	quote := fs.String("quote", "", "the owner's own words for --answer")
	how := fs.String("how", "", `the decision's how for --answer (default "clarify round")`)
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	if *reason != "" && !*assume {
		return fmt.Errorf("--reason goes with --assume")
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	out := clarifyOutput{Slug: issue.Slug}
	switch {
	case *assume && *answer != "":
		return fmt.Errorf("--assume and --answer don't go together: one records the agent's answers, the other the owner's")
	case *assume:
		qs, err := tracker.AssumeOpen(issue.FilePath, tracker.ClarifySection, *reason)
		if err != nil {
			return fmt.Errorf("failed to mark answers assumed: %w", err)
		}
		out.Assumed = qs
	case *answer != "":
		qid, opt, ok := strings.Cut(*answer, "=")
		n, convErr := strconv.Atoi(strings.TrimSpace(opt))
		if !ok || convErr != nil || tracker.NormalizeQuestionID(qid) == "" {
			return fmt.Errorf("--answer %q: want Qn=<option number>, e.g. --answer Q2=1", *answer)
		}
		if strings.TrimSpace(*quote) == "" {
			return fmt.Errorf("--answer needs --quote: the owner's own words\n\nExample:\n  issue-cli clarify %s --answer %s --quote \"Yes, the first one.\" --how chat", issue.Slug, *answer)
		}
		q, d, err := tracker.AnswerQuestion(issue.FilePath, issue.Slug, tracker.AnswerInput{ID: qid, Option: n, Words: *quote, How: *how}, ctx.now())
		switch {
		case errors.Is(err, tracker.ErrQuestionNotFound):
			return fmt.Errorf("%w\n\nSee the questions:\n  issue-cli clarify %s", err, issue.Slug)
		case err != nil:
			return err
		}
		out.Answered, out.Decision = &q, &d
	}
	if out.Clarify, err = tracker.ClarifyOfFile(issue.FilePath); err != nil {
		return err
	}
	if out.Clarify.Questions == nil {
		out.Clarify.Questions = []tracker.ClarifyQuestion{}
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	w := ctx.Stdout
	switch {
	case *assume && len(out.Assumed) == 0:
		fmt.Fprintln(w, "No open answer to assume.")
	case *assume:
		ids := make([]string, len(out.Assumed))
		for i, q := range out.Assumed {
			ids[i] = q.ID
		}
		fmt.Fprintf(w, "✓ Assumed %s (the recommendation); the owner can still answer them on the Station, and under YOLO they wait in the after-the-fact review.\n", strings.Join(ids, ", "))
	case out.Answered != nil:
		fmt.Fprintf(w, "✓ Recorded the owner's answer to %s: %s\n", out.Answered.ID, out.Decision.Raw)
	}
	printClarify(w, issue.Slug, out.Clarify)
	return nil
}

// printClarify writes the round: each question with its options and answer,
// then the problems, or how to start one.
func printClarify(w io.Writer, slug string, c tracker.Clarify) {
	switch {
	case !c.Found:
		fmt.Fprintf(w, "No ## Clarify yet on %s. In design, write every question for the owner there at once:\n", slug)
		fmt.Fprintln(w, clarifyFormatHint)
		return
	case c.Nothing && len(c.Questions) == 0:
		fmt.Fprintf(w, "== Clarify: nothing to ask ==\n  %s\n", c.Why)
	default:
		fmt.Fprintf(w, "== Clarify (%s) ==\n", clarifyCounts(c))
		for _, q := range c.Questions {
			fmt.Fprintf(w, "%s. %s  [%s]\n", q.ID, q.Text, answerWords(q))
			for _, o := range q.Options {
				mark := " "
				if ch := q.Chosen(); ch != nil && ch.Num == o.Num {
					mark = "→"
				}
				line := fmt.Sprintf("  %s %d. %s", mark, o.Num, o.Label)
				if o.Recommended {
					line += " (recommended)"
				}
				if o.Cost != "" {
					line += " · cost: " + o.Cost
				}
				fmt.Fprintln(w, line)
			}
		}
	}
	if len(c.Problems) > 0 {
		fmt.Fprintln(w, "Problems in the format:")
		for _, p := range c.Problems {
			fmt.Fprintln(w, "  - "+p)
		}
	}
}

// clarifyFormatHint is the format, for a hint where a round is missing.
const clarifyFormatHint = `  ## Clarify

  ### Q1. <question>
  1. **<option>** (recommended) <why, citing the work>. Cost: <cost>.
  2. **<option>** Cost: <cost>.
  **Answer:** —

  (2-4 options each; nothing to ask: the section is one line "Nothing to ask: <why>.")
  issue-cli clarify --help`

func answerWords(q tracker.ClarifyQuestion) string {
	switch q.Answer.State {
	case tracker.AnswerOpen:
		return "open"
	case tracker.AnswerAssumed:
		return "assumed — " + q.Answer.Reason
	case tracker.AnswerOwner:
		s := fmt.Sprintf("owner %s: %q", q.Answer.Date, q.Answer.Words)
		if q.Answer.Decision != "" {
			s += " " + q.Answer.Decision
		}
		return s
	}
	return "answered: " + q.Answer.Words
}

// clarifyCounts is "3 questions: 1 open, 2 assumed".
func clarifyCounts(c tracker.Clarify) string {
	n := len(c.Questions)
	s := strconv.Itoa(n) + " question"
	if n != 1 {
		s += "s"
	}
	var parts []string
	if k := len(c.Open()); k > 0 {
		parts = append(parts, strconv.Itoa(k)+" open")
	}
	if k := len(c.Assumed()); k > 0 {
		parts = append(parts, strconv.Itoa(k)+" assumed")
	}
	if k := n - len(c.Unanswered()); k > 0 {
		parts = append(parts, strconv.Itoa(k)+" answered")
	}
	if len(parts) > 0 {
		s += ": " + strings.Join(parts, ", ")
	}
	return s
}

// clarifyOnTransition runs after a transition out of from. When a YOLO
// gate passed, every open clarify question becomes an assumed answer (the
// recommendation), which the review after YOLO lists. Leaving a status whose
// prompt asks for the round, it names the format's problems, or says the
// round is missing.
func clarifyOnTransition(ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue, from string, yoloPassed bool) error {
	c, err := tracker.ClarifyOfFile(issue.FilePath)
	if err != nil {
		return nil
	}
	w := ctx.Stdout
	if ctx.JSONOutput {
		w = ctx.Stderr
	}
	if yoloPassed && len(c.Open()) > 0 {
		qs, err := tracker.AssumeOpen(issue.FilePath, tracker.ClarifySection, "YOLO: "+tracker.DefaultAssumedReason)
		if err != nil {
			return fmt.Errorf("YOLO: marking the open clarify answers assumed: %w", err)
		}
		ids := make([]string, len(qs))
		for i, q := range qs {
			ids[i] = q.ID
		}
		fmt.Fprintf(w, "YOLO: clarify %s assumed (the recommendation) — the owner reviews them after the fact, or answers them on the Station\n", strings.Join(ids, ", "))
	}
	if !strings.Contains(wf.StatusPrompt(from), "## Clarify") {
		return nil
	}
	switch {
	case !c.Found:
		fmt.Fprintf(w, "Clarify: %s left %s with no ## Clarify. Write the round (or one \"Nothing to ask: <why>.\" line): issue-cli clarify %s\n", issue.Slug, from, issue.Slug)
	case len(c.Problems) > 0:
		fmt.Fprintf(w, "Clarify: the round's format has problems (issue-cli clarify %s):\n", issue.Slug)
		for _, p := range c.Problems {
			fmt.Fprintln(w, "  - "+p)
		}
	}
	return nil
}
