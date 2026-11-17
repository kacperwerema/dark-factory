package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var worktreesCommand = &Command{
	Name:      "worktrees",
	ShortHelp: "Make an issue's worktree with the project's setup (add), or remove the ones on main (prune)",
	LongHelp: `Make or sweep the project's issue worktrees (.worktrees/<slug>).

  issue-cli worktree add [<slug>]
  issue-cli worktrees prune [--dry-run] [--json]

add makes .worktrees/<slug> on work/<slug>, cut from the main branch
(ci.branch when set; a work/<slug> that exists is checked out as is),
leaves the issues tree out as dispatch does (worktree_sparse_exclude), and
runs the workflow's worktree_setup command in it once. The last line is
the worktree's path: cd "$(issue-cli worktree add | tail -1)". The slug
defaults to your issue (ISSUE_VIEWER_ISSUE_SLUG). A worktree that exists is
left as it is: nothing runs, and it says so. With worktree: true dispatch
makes the worktree itself; add is for projects whose workers make their own.

prune removes a worktree (git worktree remove, then git worktree prune;
the branch stays) only when every rule holds:
  - its branch is on the main checkout's branch (ci.branch when set)
  - git status --porcelain is empty (untracked files count)
  - no tmux pane's current path and no process's arguments are inside it
  - nothing at its top level and not its HEAD changed in the last 3 hours
    (a worker just started on a new branch still has it on main)
  - it isn't a special tree: .ci, detached, or a branch outside work/
A release tree (.worktrees/release-vX.Y on release/vX.Y) goes once a vX.Y.N
tag is out, a newer line is published too, and its branch has no commits
past its newest vX.Y.N (no pick or candidate waiting), when it is also
clean, idle and not live. The branch stays; release cut adds the tree back.
Every tree kept is listed with why: dirty, unmerged, live, recent, special.

The transition to done removes the issue's own worktree the same way (no
3-hour rule), and the lead's tick runs this sweep once a day. dispatch,
dispatch --fresh and the critic recreate a worktree that is gone.

  --dry-run   list what would go and what stays; remove nothing
  --json      the verdicts as JSON

Examples:
  issue-cli worktrees prune --dry-run
  issue-cli --project family-meal-assistant worktrees prune`,
	Run: runWorktrees,
}

func init() {
	registerCommand(worktreesCommand)
	registerAlias("worktree", "worktrees")
}

func runWorktrees(ctx *Context, args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	if sub == "add" {
		return runWorktreeAdd(ctx, args)
	}
	fs := newFlagSet("worktrees prune", ctx)
	dry := fs.Bool("dry-run", false, "list what would go; remove nothing")
	asJSON := fs.Bool("json", false, "the verdicts as JSON")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if sub != "prune" || fs.NArg() != 0 {
		return &usageError{msg: "usage: issue-cli worktrees prune [--dry-run] [--json] | worktree add [<slug>]"}
	}
	if !*dry {
		issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
		if me := guardWho(ctx, issues, "worktrees prune"); me.Role == tracker.RoleWorker {
			guardNever(ctx, "worktrees prune")
			return fmt.Errorf("workers don't sweep the project's worktrees (you are %s): --dry-run only lists them", valueOr(me.Name, "a worker"))
		}
	}
	in, err := pruneInput(ctx)
	if err != nil {
		return err
	}
	verdicts, err := agentrun.Sweep(in, *dry)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(ctx.Stdout, verdicts)
	}
	printSweep(ctx.Stdout, in.Root, verdicts, *dry)
	return nil
}

// pruneInput is the sweep's input for ctx's project: the main checkout, the
// branch work lands on, now, and what is live on the machine.
func pruneInput(ctx *Context) (agentrun.PruneInput, error) {
	root := gitmain.Root(projectRoot(ctx.Project))
	main, err := gitmain.Branch(root, queueCIBranch(ctx.Project))
	if err != nil {
		return agentrun.PruneInput{}, err
	}
	return agentrun.PruneInput{Root: root, Main: main, Now: ctx.now(), Idle: agentrun.PruneIdle, Live: agentrun.LiveDirs(), Base: pruneBases(ctx.Project)}, nil
}

// pruneBases is PruneInput.Base for proj: work/<slug>'s issue's dispatch
// base, the issues read on first use.
func pruneBases(proj *tracker.Project) func(branch string) string {
	var issues []*tracker.Issue
	loaded := false
	return func(branch string) string {
		slug, ok := strings.CutPrefix(branch, "work/")
		if !ok {
			return ""
		}
		if !loaded {
			issues, _ = tracker.LoadIssues(proj.IssueDir)
			loaded = true
		}
		return queueBaseOf(issues, slug)
	}
}

// sweepCounts is how many trees went and how many stayed, by reason.
func sweepCounts(verdicts []agentrun.TreeVerdict) (removed int, kept map[string]int) {
	kept = map[string]int{}
	for _, v := range verdicts {
		if v.Kind == agentrun.TreeRemoved {
			removed++
		} else {
			kept[v.Kind]++
		}
	}
	return removed, kept
}

func keptWords(kept map[string]int) string {
	var kinds []string
	for k := range kept {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var parts []string
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%d %s", kept[k], k))
	}
	return strings.Join(parts, ", ")
}

func printSweep(w io.Writer, root string, verdicts []agentrun.TreeVerdict, dry bool) {
	rel := func(p string) string { return strings.TrimPrefix(p, root+"/") }
	removed, kept := sweepCounts(verdicts)
	verb := "Removed"
	if dry {
		verb = "Would remove"
	}
	for _, v := range verdicts {
		if v.Kind == agentrun.TreeRemoved {
			fmt.Fprintf(w, "  - %s  (%s)\n", rel(v.Path), v.Why)
		}
	}
	for _, v := range verdicts {
		if v.Kind != agentrun.TreeRemoved {
			fmt.Fprintf(w, "  kept %s: %s  (%s)\n", rel(v.Path), v.Kind, v.Why)
		}
	}
	line := fmt.Sprintf("%s %s", verb, countOf(removed, "worktree", "worktrees"))
	if len(kept) > 0 {
		line += "; kept " + keptWords(kept)
	}
	fmt.Fprintln(w, line+".")
}

// removeShippedWorktree removes issue's worktree once it is at its last
// status and its branch is on main, after its session closed, and prints
// one line saying what it did or why the tree stays. Silent when the issue
// has no worktree. A worker's own pane is still inside it when the worker
// runs the transition: the lead's tick removes it once that session closes.
func removeShippedWorktree(ctx *Context, w io.Writer, issue *tracker.Issue) {
	proj := ctx.Project
	if proj == nil || issue == nil {
		return
	}
	wf := proj.LoadWorkflowForIssue(issue)
	if wf == nil || !wf.IsLastStatus(issue.Status) {
		return
	}
	v, ok := shippedTreeVerdict(ctx, issue)
	if !ok {
		return
	}
	if v.Kind == agentrun.TreeRemoved {
		fmt.Fprintf(w, "Worktree: removed %s (%s; the branch stays).\n", v.Path, v.Why)
		return
	}
	fmt.Fprintf(w, "Worktree: kept %s: %s (%s). %s\n", v.Path, v.Kind, v.Why, keptHint(proj, v.Kind))
}

func keptHint(proj *tracker.Project, kind string) string {
	switch kind {
	case agentrun.TreeLive:
		return "The lead's tick removes it once nothing runs there."
	case agentrun.TreeUnmerged:
		return "It goes once the branch is on main."
	case agentrun.TreeDirty:
		return "Commit or drop the changes, then " + tracker.InjectProject(proj, "issue-cli worktrees prune") + "."
	}
	return ""
}

// shippedTreeVerdict tries to remove issue's worktree (removeIssueTree);
// ok is false when the issue has none on disk or git can't say.
var shippedTreeVerdict = func(ctx *Context, issue *tracker.Issue) (agentrun.TreeVerdict, bool) {
	proj := ctx.Project
	wt := tracker.IssueWorktree(projectRoot(proj), issue, proj.LoadWorkflowForIssue(issue))
	if wt.Path == "" || !dirExists(wt.Path) {
		return agentrun.TreeVerdict{}, false
	}
	in, err := pruneInput(ctx)
	if err != nil {
		return agentrun.TreeVerdict{}, false
	}
	return agentrun.RemoveIssueTree(in, wt.Path, wt.Branch), true
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
