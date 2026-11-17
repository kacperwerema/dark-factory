package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/memguard"
	"github.com/kacperwerema/dark-factory/internal/milestone"
	"github.com/kacperwerema/dark-factory/internal/privacy"
)

// releaseMemCheck is mem-guard check. A var so tests don't read the machine.
var releaseMemCheck = memguard.Check

// releaseCheckCommands is what release check runs: local CI's default tier
// (ci.run), else make validate.
func releaseCheckCommands(ctx *Context) []string {
	var cmds []string
	for _, c := range ctx.Project.LoadWorkflow().CI.Commands("") {
		if s := strings.TrimSpace(c.Cmd); s != "" {
			cmds = append(cmds, s)
		}
	}
	if len(cmds) == 0 {
		cmds = []string{"make validate"}
	}
	return cmds
}

// runReleaseCheck validates the candidate a publish ships: it runs local
// CI's commands in the release worktree, on its committed tree, and records
// the commit that passed (the card's validated check). A failure clears the
// record. Main's head plays no part: the release branch is what ships.
func runReleaseCheck(ctx *Context, args []string) error {
	fs := newFlagSet("release check", ctx)
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return &usageError{msg: "release check takes the version: issue-cli release check v0.54.1"}
	}
	cut, err := privacy.ParseVersion(fs.Arg(0))
	if err != nil {
		return &usageError{msg: err.Error()}
	}
	root := privacy.MainCheckout(ctx.Project.TelemetryRoot())
	branch := milestone.ReleaseBranch(cut)
	wt := filepath.Join(root, ".worktrees", milestone.ReleaseWorktreeName(cut))
	if _, err := os.Stat(wt); err != nil {
		return fmt.Errorf("no release worktree %s: cut the release first (issue-cli release cut %s)", wt, cut)
	}
	if out, err := exec.Command("git", "-C", wt, "status", "--porcelain", "--untracked-files=no").Output(); err != nil {
		return fmt.Errorf("reading %s: %v", wt, err)
	} else if s := strings.TrimSpace(string(out)); s != "" {
		return fmt.Errorf("%s has uncommitted changes: the check validates the commit that ships, so commit or drop them first:\n%s", wt, s)
	}
	headOut, err := exec.Command("git", "-C", wt, "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("reading %s's head: %v", wt, err)
	}
	head := strings.TrimSpace(string(headOut))
	if found, blocked, msg := releaseMemCheck(); found && blocked {
		return fmt.Errorf("not checking %s now: mem-guard check is blocked (%s). Run it again when a worker finishes", cut, msg)
	}
	out := ctx.Stdout
	for _, c := range releaseCheckCommands(ctx) {
		fmt.Fprintf(out, "→ %s (in %s at %s)\n", c, wt, gitdir.ShortSHA(head))
		cmd := exec.Command("sh", "-c", c)
		cmd.Dir, cmd.Stdout, cmd.Stderr = wt, ctx.Stdout, ctx.Stderr
		if err := cmd.Run(); err != nil {
			privacy.ClearValidated(root, branch)
			return fmt.Errorf("%s failed on %s at %s (%v): the candidate is not validated, and publish refuses it. Pick the fix from main (issue-cli release pick <slug|sha>), cut the next candidate, and check again", c, branch, gitdir.ShortSHA(head), err)
		}
	}
	if err := privacy.RecordValidated(root, branch, head); err != nil {
		return fmt.Errorf("the check passed, but recording it failed: %v", err)
	}
	fmt.Fprintf(out, "✓ Validated %s at %s: recorded; Waiting for release shows it, and publish reads it\n", branch, gitdir.ShortSHA(head))
	return nil
}
