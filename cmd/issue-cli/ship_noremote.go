package main

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// projectHasNoRemote reports whether the project's repo is known to have
// no git remote. A root git can't read (no repo) is not "no remote".
func projectHasNoRemote(ctx *Context) bool {
	has, err := gitmain.HasRemote(projectRoot(ctx.Project))
	return err == nil && !has
}

// tickNoRemoteShipping ticks the Shipping boxes that need a git remote (a
// push, a PR) with "N/A: no git remote" when the project's repo has none,
// and returns the side-effect line ("" when nothing was ticked). On a repo
// with a remote it changes nothing.
func tickNoRemoteShipping(ctx *Context, issue *tracker.Issue) string {
	if !projectHasNoRemote(ctx) {
		return ""
	}
	var ticked []tracker.CheckboxItem
	_, _, err := tracker.UpdateIssueBody(issue.FilePath, func(body string) (string, bool, error) {
		next, t := tracker.TickNoRemoteBoxes(body)
		ticked = t
		return next, len(t) > 0, nil
	})
	if err != nil || len(ticked) == 0 {
		return ""
	}
	return fmt.Sprintf("No git remote: ticked %s with N/A: %s", tracker.TickedIDs(ticked), tracker.NANoGitRemote)
}

// noRemoteNA reports whether check --na may tick a Shipping box: the
// reason is "no git remote", the box needs a remote and the repo has none.
func noRemoteNA(ctx *Context, it tracker.CheckboxItem, reason string) bool {
	return strings.EqualFold(strings.TrimSpace(reason), tracker.NANoGitRemote) &&
		tracker.IsRemoteStepBox(it.Label()) && projectHasNoRemote(ctx)
}
