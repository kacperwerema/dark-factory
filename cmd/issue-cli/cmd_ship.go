package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/ship"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var shipCommand = &Command{
	Name:      "ship",
	ShortHelp: "Land the issue's branch on local main: merge main, check, fast-forward, re-merge when main moved",
	LongHelp: `Land the issue's work branch on local main, in its worktree:

  1. merge local main into the branch ("chore(merge): main into <slug>"),
     unless main is already in it;
  2. run the full check (--check, else the project's ci.run commands
     without a tier: make validate here), unless it already passed on
     this HEAD;
  3. git -C <main checkout> merge --ff-only <branch>;
  4. when that refuses because main moved: merge main again and, on a
     clean merge, run the re-check (go build, go vet and the touched
     packages' tests; the full check without a go.mod), then fast-forward
     again, up to --rounds times;
  5. after a landing that came through a re-check, run the full check on
     the landed tree.

A conflict, a red check or a fast-forward refused for another reason
(uncommitted changes in the main checkout) stops it, main untouched. It
never pushes, forces or skips hooks, and it ticks no box.

The last line on stdout is the verdict, and the exit code matches it:
  ship: LANDED <sha>                                          exit 0
  ship: NOT LANDED (<reason>)                                 exit 1
  ship: LANDED <sha>, check after landing FAIL (<steps>)      exit 3
Read that line, not $? of a pipe: "issue-cli ship | tail" exits 0 on a
refusal. On exit 3 main holds the branch: fix forward at once and post a
tests: comment naming the red step.

A landing posts one progress: comment naming the sha each check ran on.
The lead can land a worker's branch by naming the issue: issue-cli ship <slug>.

Examples:
  issue-cli ship
  issue-cli ship --dry-run
  issue-cli ship <slug> --check "make validate" --rounds 2`,
	Run: runShip,
}

func init() {
	registerCommand(shipCommand)
}

func runShip(ctx *Context, args []string) error {
	slug, rest, err := issueSlug(ctx, args, "ship")
	if err != nil {
		return err
	}
	fs := newFlagSet("ship", ctx)
	check := fs.String("check", "", "the full check, a shell command (default: the project's ci.run commands without a tier)")
	recheck := fs.String("recheck", "", `the check after a clean re-merge, a shell command, or "full" (default: go build, go vet, the touched packages' tests)`)
	rounds := fs.Int("rounds", ship.DefaultRounds, "re-merges after a moved main before giving up")
	dryRun := fs.Bool("dry-run", false, "print the plan and run nothing")
	if err := parseFlags(ctx, fs, rest); err != nil {
		return err
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	o, err := shipOptions(ctx, issue)
	if err != nil {
		return err
	}
	if *check != "" {
		o.Check = []string{*check}
	}
	switch strings.TrimSpace(*recheck) {
	case "":
	case "full":
		o.Recheck = o.Check
	default:
		o.Recheck = []string{*recheck}
	}
	o.Rounds = *rounds
	o.Out, o.Now = ctx.Stdout, ctx.Now
	if *dryRun {
		fmt.Fprintf(ctx.Stdout, "ship %s (dry run, nothing runs):\n", o.Branch)
		for _, l := range ship.Plan(o) {
			fmt.Fprintln(ctx.Stdout, "  "+l)
		}
		return nil
	}
	res := ship.Run(o)
	if res.Landed {
		text := "progress: ship: " + strings.Join(res.Records, "; ")
		_ = tracker.AddCommentBy(issue.FilePath, 0, text, "cli", commentAuthor(ctx.Project, issue))
		fmt.Fprintf(ctx.Stdout, "Next: issue-cli check %s <the Shipping boxes>, then issue-cli transition %s --to done\n", issue.Slug, issue.Slug)
	}
	fmt.Fprintln(ctx.Stdout, res.Verdict())
	if code := res.ExitCode(); code != 0 {
		return &exitCodeError{Code: code}
	}
	return nil
}

// shipOptions finds the issue's branch and worktree, the main checkout and
// the branch it ships into, and the project's full check.
func shipOptions(ctx *Context, issue *tracker.Issue) (ship.Options, error) {
	root := projectRoot(ctx.Project)
	b := checks.IssueBranch(root, issue)
	if !b.Found() || b.Worktree == "" {
		return ship.Options{}, fmt.Errorf("ship: no worktree on %s's branch (looked for %s)", issue.Slug, strings.Join(b.Tried, ", "))
	}
	st, ok := tracker.MainCheckoutState(root)
	if !ok {
		return ship.Options{}, fmt.Errorf("ship: %s is not in a git repository", root)
	}
	main := ""
	if d, ok := dispatchBase(issue); ok && d.BaseBranch != "" {
		main = d.BaseBranch
	} else if name, _, err := tracker.MainHead(root); err == nil {
		main = name
	}
	if main == "" {
		main = "main"
	}
	o := ship.Options{
		Worktree: b.Worktree, MainDir: st.Path, Main: main, Branch: b.Name,
		Slug: issue.Slug,
	}
	if wf := ctx.Project.LoadWorkflow(); wf != nil && wf.CI != nil {
		for _, c := range wf.CI.Run {
			if c.TierName() == tracker.CIDefaultTier && strings.TrimSpace(c.Cmd) != "" {
				o.Check = append(o.Check, c.Cmd)
			}
		}
	}
	return o, nil
}
