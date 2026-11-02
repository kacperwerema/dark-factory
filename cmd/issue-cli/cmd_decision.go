package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var decisionCommand = &Command{
	Name:      "decision",
	ShortHelp: "Challenge a decision you think is wrong; the owner resolves it (challenge, resolve, challenges)",
	LongHelp: `Challenges through the lead (docs/workflow.md#decision-ledger).

  issue-cli decision challenge <ref> --why "…" [--propose "…"] [--for <slug>]
      Say a decision is wrong. <ref> is <slug>#D-n (or D-n of the --for
      issue). Posts a challenge: comment on your issue and on the
      decision's, logs a challenged event, shows the lead on its next call
      and the owner as one Needs you row. Until the owner answers, your
      issue's gates with the decision_unchallenged rule hold; no other
      issue is held. A worker challenges for its own issue only; a lead or
      the owner's shell names --for.

  issue-cli decision resolve <ref> --keep [--for <slug>] [--quote "…"]
      The owner keeps the decision: closes every open challenge on <ref>
      (or only --for's). The owner's act: a worker is refused, a lead
      relays the owner's words with --quote (a decision: comment on the
      decision's issue; under YOLO it waits in the after-the-fact review),
      the owner's shell may. To change it instead, record the replacing
      decision: issue-cli decide <slug> --replaces <ref> --text "…" (that
      closes the challenge too).

  issue-cli decision challenges [--json]
      The open challenges. Reads only.

Examples:
  issue-cli decision challenge sim/rules#D-2 --why "PRs give the owner a review step" --propose "merge via PRs"
  issue-cli decision resolve sim/rules#D-2 --keep --quote "keep merging into main"`,
	Subcommands: []string{"challenge", "resolve", "challenges"},
	Run:         runDecision,
}

func init() {
	registerCommand(decisionCommand)
}

func runDecision(ctx *Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("decision needs a subcommand: challenge, resolve or challenges\n\nRun: issue-cli decision --help")
	}
	switch args[0] {
	case "challenge":
		return runDecisionChallenge(ctx, args[1:])
	case "resolve":
		return runDecisionResolve(ctx, args[1:])
	case "challenges":
		return runDecisionChallenges(ctx, args[1:])
	}
	return fmt.Errorf("unknown decision subcommand %q: challenge, resolve or challenges\n\nRun: issue-cli decision --help", args[0])
}

// challengeWrite is where this session's challenge write goes.
func challengeWrite(ctx *Context, issues []*tracker.Issue, actor tracker.DecisionActor, author string) tracker.ChallengeWrite {
	return tracker.ChallengeWrite{
		IssueDir: ctx.Project.IssueDir, Actor: actor, Author: author,
		File: func(slug string) string {
			if is := tracker.FindIssue(issues, slug); is != nil {
				return is.FilePath
			}
			return ""
		},
	}
}

// decisionRefArg resolves <slug>#D-n (or D-n of base) to a known decision.
func decisionRefArg(ctx *Context, index *tracker.DecisionIndex, base, raw, verb string) (*tracker.DecisionEntry, error) {
	ref := tracker.NormalizeDecisionRef(base, raw)
	if ref == "" {
		return nil, fmt.Errorf("decision %s %q: want <slug>#D-n (or D-n with --for)", verb, raw)
	}
	e := index.Lookup(ref)
	if e == nil {
		slug, _, _ := strings.Cut(ref, "#")
		return nil, fmt.Errorf("decision %s %s: no such decision; nothing written\n\nList the decisions in that issue:\n  %s", verb, ref,
			tracker.InjectProject(ctx.Project, "issue-cli decisions --issue "+slug))
	}
	return e, nil
}

func runDecisionChallenge(ctx *Context, args []string) error {
	raw := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		raw, args = args[0], args[1:]
	}
	fs := newFlagSet("decision challenge", ctx)
	whyFlag := fs.String("why", "", "why the decision is wrong (required)")
	proposeFlag := fs.String("propose", "", "what you would decide instead")
	forFlag := fs.String("for", "", "the challenger's issue (a worker's own by default)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("decision challenge needs a ref\n\nExample:\n  issue-cli decision challenge <slug>#D-2 --why \"…\"")
	}
	why := strings.TrimSpace(normalizeEscapedText(*whyFlag))
	if why == "" {
		return fmt.Errorf("decision challenge needs --why: what is wrong with %s; nothing written", raw)
	}
	issues, err := loadProjectIssues(ctx)
	if err != nil {
		return err
	}
	me := whoAmI(ctx.Project, issues)
	forSlug := strings.TrimSpace(*forFlag)
	if forSlug == "" && me.Role == tracker.RoleWorker {
		forSlug = me.Slug
	}
	if forSlug == "" {
		return fmt.Errorf("decision challenge: name the challenging issue with --for <slug>: its gates wait for the owner's answer; nothing written")
	}
	challenger := tracker.FindIssue(issues, forSlug)
	if challenger == nil {
		return fmt.Errorf("--for %s: no such issue; nothing written", forSlug)
	}
	ctx.issue = challenger
	if err := ownIssueOnly(ctx, issues, challenger, "decision challenge for"); err != nil {
		return err
	}
	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(ctx.Project))
	e, err := decisionRefArg(ctx, index, challenger.Slug, raw, "challenge")
	if err != nil {
		return err
	}
	if e.ReplacedBy != nil {
		return fmt.Errorf("decision challenge %s: it is replaced by %s; challenge that one; nothing written", e.Ref, e.ReplacedBy.Ref)
	}
	open, err := tracker.LoadOpenChallenges(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	for _, c := range tracker.ChallengesBy(tracker.ChallengesOn(open, e.Ref), challenger.Slug) {
		return fmt.Errorf("%s already challenged %s on %s (%s); it waits for the owner's answer; nothing written",
			challenger.Slug, e.Ref, c.Since.Format("2006-01-02"), c.Why)
	}
	actor := decisionWrite(ctx, me, nil).Actor
	c := tracker.Challenge{Ref: e.Ref, Issue: challenger.Slug, Why: why, Propose: strings.TrimSpace(normalizeEscapedText(*proposeFlag))}
	if err := tracker.OpenChallenge(challengeWrite(ctx, issues, actor, commentAuthor(ctx.Project, challenger)), c, ctx.now()); err != nil {
		return fmt.Errorf("failed to record the challenge: %w", err)
	}
	route := tracker.LeadFor(ctx.Project, challenger, issues, sessionLive(ctx.Project))
	fmt.Fprintf(ctx.Stdout, "✓ Challenged %s for %s\n", e.Ref, challenger.Slug)
	if e.Slug != challenger.Slug {
		fmt.Fprintf(ctx.Stdout, "  challenge: comments on %s and %s\n", challenger.Slug, e.Slug)
	}
	fmt.Fprintf(ctx.Stdout, "%s's gates with decision_unchallenged hold until the owner answers; no other issue is held.\n", challenger.Slug)
	fmt.Fprintf(ctx.Stdout, "The lead (%s, %s) sees it on its next call, the owner in Needs you. Keep: decision resolve %s --keep; change: decide %s --replaces %s.\n",
		route.Lead, liveWord(route.Live), e.Ref, e.Slug, e.Ref)
	return nil
}

func runDecisionResolve(ctx *Context, args []string) error {
	raw := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		raw, args = args[0], args[1:]
	}
	fs := newFlagSet("decision resolve", ctx)
	keepFlag := fs.Bool("keep", false, "the owner keeps the decision")
	forFlag := fs.String("for", "", "only this challenger's challenge")
	quoteFlag := fs.String("quote", "", "the owner's own words (required from a lead)")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("decision resolve needs a ref\n\nExample:\n  issue-cli decision resolve <slug>#D-2 --keep --quote \"…\"")
	}
	issues, err := loadProjectIssues(ctx)
	if err != nil {
		return err
	}
	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(ctx.Project))
	base := strings.TrimSpace(*forFlag)
	e, err := decisionRefArg(ctx, index, base, raw, "resolve")
	if err != nil {
		return err
	}
	if !*keepFlag {
		return fmt.Errorf("decision resolve %s: give --keep to keep it; to change it, record the decision that replaces it (that resolves the challenge):\n  %s",
			e.Ref, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s --replaces %s --text \"…\" --quote \"…\"", e.Slug, e.Ref)))
	}
	decIssue := tracker.FindIssue(issues, e.Slug)
	quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
	owner := ownerActFor(ctx, decIssue, issues, quote)
	if owner.err != nil {
		return ownerChallengeRefusal(owner.err)
	}
	list, err := openChallengesFor(ctx, e.Ref, base)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return fmt.Errorf("decision resolve %s: no open challenge on it; nothing written (open ones: issue-cli decision challenges)", e.Ref)
	}
	if err := resolveChallenges(ctx, issues, owner, list, tracker.ChallengeKept, "", quote); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Kept %s: %d challenge(s) resolved\n", e.Ref, len(list))
	for _, c := range list {
		fmt.Fprintf(ctx.Stdout, "  %s goes on\n", c.Issue)
	}
	if owner.relay != nil {
		fmt.Fprintf(ctx.Stdout, "Relayed the owner's words in a decision: comment on %s%s.\n", e.Slug, yoloNote(owner.relay))
	}
	return nil
}

// ownerChallengeRefusal words the owner-act refusal for a challenge.
func ownerChallengeRefusal(err error) error {
	msg := strings.Replace(err.Error(), "changes to a hard decision are the owner's: a worker can't make a decision hard, amend a hard one or replace it",
		"answering a challenge is the owner's: a worker can't resolve it, or replace or confirm a challenged decision", 1)
	return fmt.Errorf("%s; nothing written", msg)
}

// openChallengesFor is the open challenges on ref, only forSlug's when set.
func openChallengesFor(ctx *Context, ref, forSlug string) ([]tracker.Challenge, error) {
	open, err := tracker.LoadOpenChallenges(ctx.Project.IssueDir)
	if err != nil {
		return nil, err
	}
	list := tracker.ChallengesOn(open, ref)
	if forSlug != "" {
		list = tracker.ChallengesBy(list, forSlug)
	}
	return list, nil
}

// resolveChallenges closes list as an owner act: resolved events, the
// challenge-resolved: comments and, for a lead relay, the decision:
// comment on the decision's issue.
func resolveChallenges(ctx *Context, issues []*tracker.Issue, owner ownerActRules, list []tracker.Challenge, how, byRef, quote string) error {
	w := decisionWrite(ctx, owner.me, owner.relay)
	cw := challengeWrite(ctx, issues, w.Actor, owner.me.Name)
	if err := tracker.ResolveChallenges(cw, list, how, byRef, quote, ctx.now()); err != nil {
		return fmt.Errorf("failed to resolve the challenge: %w", err)
	}
	if owner.relay != nil && how == tracker.ChallengeKept {
		ref := list[0].Ref
		if p := cw.File(strings.SplitN(ref, "#", 2)[0]); p != "" {
			text := tracker.DecisionRelayComment(ref, "kept (challenge resolved)", *owner.relay)
			if err := tracker.AddCommentBy(p, 0, text, "cli", owner.relay.Lead); err != nil {
				return fmt.Errorf("the challenge is resolved, but the decision: comment failed: %w", err)
			}
		}
	}
	return nil
}

func runDecisionChallenges(ctx *Context, args []string) error {
	fs := newFlagSet("decision challenges", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	open, err := tracker.LoadOpenChallenges(ctx.Project.IssueDir)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		if open == nil {
			open = []tracker.Challenge{}
		}
		return writeJSON(ctx.Stdout, open)
	}
	if len(open) == 0 {
		fmt.Fprintln(ctx.Stdout, "No open challenges. An agent opens one with: issue-cli decision challenge <ref> --why \"…\"")
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "== Open challenges (%d) ==\n", len(open))
	for _, c := range open {
		writeChallenge(ctx, c)
	}
	return nil
}

func writeChallenge(ctx *Context, c tracker.Challenge) {
	fmt.Fprintf(ctx.Stdout, "%s · for %s · %s\n", c.Ref, c.Issue, c.Since.Local().Format("2006-01-02"))
	fmt.Fprintf(ctx.Stdout, "    why: %s\n", c.Why)
	if c.Propose != "" {
		fmt.Fprintf(ctx.Stdout, "    propose: %s\n", c.Propose)
	}
}

// loadProjectIssues is every issue in the project.
func loadProjectIssues(ctx *Context) ([]*tracker.Issue, error) {
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return nil, fmt.Errorf("loading issues: %w", err)
	}
	return issues, nil
}

// leadChallengesShown caps the challenges the lead's block prints.
const leadChallengesShown = 5

// printLeadChallenges shows a lead, after any issue-cli call, the open
// challenges in its scope it has not been shown: each once (its challenge:
// comment on the challenger's issue is marked done), Needs you keeps them
// until the owner answers. It reads the log and never writes it.
func printLeadChallenges(ctx *Context, command string) {
	if ctx == nil || ctx.Project == nil || ctx.JSONOutput || command == "decision" || ctx.Project.IssueDir == "" {
		return
	}
	open, err := tracker.LoadOpenChallenges(ctx.Project.IssueDir)
	if err != nil || len(open) == 0 {
		return
	}
	issues, err := tracker.LoadIssues(ctx.Project.IssueDir)
	if err != nil {
		return
	}
	me := whoAmI(ctx.Project, issues)
	if me.Role != tracker.RoleProjectLead && me.Role != tracker.RoleDepartmentLead {
		return
	}
	type shown struct {
		c    tracker.Challenge
		path string
		id   int
	}
	var fresh []shown
	for _, c := range open {
		issue := tracker.FindIssue(issues, c.Issue)
		if issue == nil {
			continue
		}
		if me.Role == tracker.RoleDepartmentLead && !strings.EqualFold(ctx.Project.DepartmentOf(issue), me.Department) {
			continue
		}
		comments, err := tracker.LoadComments(issue.FilePath)
		if err != nil {
			continue
		}
		head := tracker.ChallengePrefix + " " + c.Ref + " "
		for _, cm := range comments {
			if strings.HasPrefix(strings.TrimSpace(cm.Text), head) && cm.Status != "done" {
				fresh = append(fresh, shown{c: c, path: issue.FilePath, id: cm.ID})
				break
			}
		}
	}
	if len(fresh) == 0 {
		return
	}
	w := ctx.Stdout
	fmt.Fprintln(w)
	fmt.Fprintf(w, "== Decision challenges (%d new) ==\n", len(fresh))
	for i, s := range fresh {
		if i == leadChallengesShown {
			fmt.Fprintf(w, "  … %d more: issue-cli decision challenges\n", len(fresh)-i)
			break
		}
		fmt.Fprintf(w, "  %s challenged by %s: %s\n", s.c.Ref, s.c.Issue, s.c.Why)
		if s.c.Propose != "" {
			fmt.Fprintf(w, "    propose: %s\n", s.c.Propose)
		}
		_ = tracker.MarkCommentsDone(s.path, []int{s.id})
	}
	ref := fresh[0].c.Ref
	fmt.Fprintf(w, "Only the owner answers (Needs you). Relay their words: keep %s; change %s\n",
		tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decision resolve %s --keep --quote \"…\"", ref)),
		tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli decide %s --replaces %s --text \"…\" --quote \"…\"", strings.SplitN(ref, "#", 2)[0], ref)))
}
