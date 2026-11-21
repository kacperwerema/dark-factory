package main

import (
	"fmt"
)

var reelLegalCommand = &Command{
	Name:      "reel-legal",
	ShortHelp: "Run a reel's legal check (claims, music licence, AI label, testimonial, consent, trademarks) without moving it",
	LongHelp: `Run the legal check a reel's script → render and render → review gates
run (reel_legal_check), and print each finding with its fix. It changes
nothing. Exit code 1 when the check would hold the reel.

It reads the issue's ## Script (every spoken line and caption), its
## Legal check declarations (music, music_licence, ai_content, ai_label,
face, persona, consent, disclaimer, claim: <phrase> = <authorisation>,
storyboard) and the storyboard JSON they name, against the phrases in the
department's departments/<dept>/reel-legal.yaml, the project's own copy.

It holds a reel on: a nutrition or health claim with no authorisation (EU
1924/2006), music with no licence, AI content with no label (EU AI Act art.
50, platform rules), a first-person testimonial or a customer persona
(Omnibus directive), a face with no written consent, a trademark with an
endorsement phrase, or one named with no disclaimer. It is a floor, not
legal advice: the owner still reviews the finished reel.

Usage:
  issue-cli reel-legal [<slug>]

docs/departments.md, "Reels".`,
	Run: runReelLegal,
}

func init() {
	registerCommand(reelLegalCommand)
}

func runReelLegal(ctx *Context, args []string) error {
	fs := newFlagSet("reel-legal", ctx)
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	slug, _, err := issueSlug(ctx, fs.Args(), "reel-legal")
	if err != nil {
		return err
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	wf := ctx.Project.LoadWorkflowForIssue(issue)
	findings, err := wf.ReelLegalFindings(wf.ReelLegalAction(issue.Status), issue)
	if err != nil {
		return fmt.Errorf("reel-legal: %w\n\nRestore the rules file: issue-cli department update <dept> writes the bundled copy when yours is gone", err)
	}
	if len(findings) == 0 {
		fmt.Fprintf(ctx.Stdout, "reel-legal: PASS (%s)\n", issue.Slug)
		return nil
	}
	for _, f := range findings {
		fmt.Fprintf(ctx.Stdout, "✗ %s: %s\n  fix: %s\n", f.Rule, f.Problem, f.Fix)
	}
	fmt.Fprintf(ctx.Stdout, "reel-legal: HOLD (%d finding(s)): fix each under ## Script or ## Legal check, then run issue-cli reel-legal %s again\n", len(findings), issue.Slug)
	return &exitCodeError{Code: 1}
}
