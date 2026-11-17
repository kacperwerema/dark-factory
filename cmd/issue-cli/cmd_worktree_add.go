package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// runWorktreeAdd is `issue-cli worktree add [<slug>]`: the issue's worktree
// .worktrees/<slug> on work/<slug>, cut from the main branch, made by the
// dispatch's own EnsureWorktree (sparse excludes, checkout, worktree_setup).
// For projects without worktree: true, whose workers make their own. The
// last line printed is the worktree's path; a worktree that exists is left
// as it is and nothing runs.
func runWorktreeAdd(ctx *Context, args []string) error {
	const usage = "usage: issue-cli worktree add [<slug>]"
	slug := ""
	if len(args) > 0 && !isFlagToken(args[0]) {
		slug, args = args[0], args[1:]
	}
	fs := newFlagSet("worktree add", ctx)
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if slug == "" {
		if slug = ownSlugEnv(); slug == "" {
			return &usageError{msg: usage + " (no slug given and " + tracker.OwnSlugEnv + " is unset)"}
		}
		noteOwnSlug(ctx, slug)
	}
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	if !tracker.BranchSafe(issue.Slug) {
		return fmt.Errorf("worktree add: git can't name a branch work/%s", issue.Slug)
	}
	root := gitmain.Root(projectRoot(ctx.Project))
	if root == "" {
		return fmt.Errorf("worktree add: %s is not in a git checkout", projectRoot(ctx.Project))
	}
	wt := tracker.WorktreeSpec{
		Path:   filepath.Join(root, ".worktrees", issue.Slug),
		Branch: "work/" + issue.Slug,
	}
	if _, err := os.Stat(wt.Path); err == nil {
		fmt.Fprintf(ctx.Stdout, "worktree exists: %s on %s (left as it is; setup not run)\n%s\n", wt.Path, wt.Branch, wt.Path)
		return nil
	}
	main, err := gitmain.Branch(root, queueCIBranch(ctx.Project))
	if err != nil {
		return fmt.Errorf("worktree add: %w", err)
	}
	wt.StartPoint = main
	wf := ctx.Project.LoadWorkflow()
	dir, steps, ok := agentrun.EnsureWorktree(root, wt, wf)
	for _, s := range steps {
		mark := "✓"
		if s.Status != "ok" {
			mark = "✗"
		}
		fmt.Fprintf(ctx.Stdout, "%s %s\n", mark, s.Name)
		if s.Detail != "" {
			fmt.Fprintf(ctx.Stdout, "  %s\n", strings.ReplaceAll(strings.TrimSpace(s.Detail), "\n", "\n  "))
		}
	}
	if !ok {
		if _, err := os.Stat(wt.Path); err == nil {
			setup := "the failed step"
			if wf != nil && wf.WorktreeSetupCmd() != "" {
				setup = wf.WorktreeSetupCmd()
			}
			return fmt.Errorf("worktree add: %s was made but not finished: fix it and run %s there by hand (a second worktree add leaves it as it is)", wt.Path, shell.Quote(setup))
		}
		return fmt.Errorf("worktree add: %s not made", wt.Path)
	}
	fmt.Fprintln(ctx.Stdout, dir)
	return nil
}
