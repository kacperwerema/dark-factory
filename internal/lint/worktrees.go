package lint

import (
	"fmt"
	"path/filepath"
	"strings"

	gitchecks "github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checkWorktrees warns when a project's workflow has no worktree: true while
// its open issues' work sits in worktrees under .worktrees/ all the same:
// the workers make them by hand, under names the engine may not find, and
// worktree: true would make work/<slug> in .worktrees/<slug> for them. A
// warning only: the project keeps its choice.
func checkWorktrees(env *Env) []Finding {
	if env.Project == nil || env.Workflow == nil || env.Workflow.WorktreeEnabled() {
		return nil
	}
	root := env.Project.Root()
	under := filepath.Join(evalPath(root), ".worktrees") + string(filepath.Separator)
	closed := tracker.ClosedIssueFunc(env.Project)
	var hits []string
	for _, issue := range env.Issues {
		if closed(issue) {
			continue
		}
		b := gitchecks.IssueBranch(root, issue)
		if b.Worktree == "" || !strings.HasPrefix(evalPath(b.Worktree), under) {
			continue
		}
		hits = append(hits, fmt.Sprintf("%s on %s", issue.Slug, b.Name))
	}
	if len(hits) == 0 {
		return nil
	}
	eg := hits
	if len(eg) > 2 {
		eg = eg[:2]
	}
	noun := "issues have"
	if len(hits) == 1 {
		noun = "issue has"
	}
	return []Finding{{
		Where:   env.where(env.lineOf("worktree:")),
		Message: fmt.Sprintf("%d open %s a worktree under .worktrees/ (%s), but the workflow has no worktree: true: dispatch makes none, so each worker makes its own", len(hits), noun, strings.Join(eg, "; ")),
		Fix:     "add `worktree: true` to the workflow file: dispatch then makes .worktrees/<slug> on work/<slug>, which prove, check-script and the commit hint find at once. A warning only: leave it off to keep making worktrees by hand",
	}}
}

// evalPath is p with its links resolved (a Mac temp folder is under
// /private/var, which git prints), p itself when that fails.
func evalPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
