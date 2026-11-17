package proof

import (
	"path/filepath"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// IssueRef is where the proof reads an issue's commits and files.
type IssueRef struct {
	// Ref is the commit the branch walk starts from, "" when none was found.
	Ref string
	// Name is the branch (or "the recorded branch head <sha>", "the merge
	// <sha>") Ref came from.
	Name string
	// Worktree is the folder the issue's files are read from: the found
	// branch's worktree, else root/.worktrees/<slug> (which may not exist).
	Worktree string
	// Miss is what the lookup tried and how to fix it, set when Ref is "".
	Miss string
}

// FindIssueRef finds the issue's branch the way every proof reader does:
// its branch: field or work/<slug> (checks.IssueBranch), then the recorded
// branch head or merge sha of a branch since deleted (timeline.GitRef),
// then a worktree whose branch ends in the slug or whose HANDOFF.md names
// it. The recorded shas come before the worktree scan, so a done issue
// keeps reading its own commits even when another worktree names it.
func FindIssueRef(root string, issue *tracker.Issue, rows []tracker.TransitionStat) IssueRef {
	out := IssueRef{Worktree: filepath.Join(root, ".worktrees", issue.Slug)}
	b := checks.IssueBranch(root, issue)
	use := func() IssueRef {
		out.Ref, out.Name = tracker.BranchHead(root, b.Name), b.Name
		if b.Worktree != "" {
			out.Worktree = b.Worktree
		}
		return out
	}
	if b.How == checks.HowField || b.How == checks.HowWork {
		return use()
	}
	if out.Ref, out.Name = timeline.GitRef(root, issue.Slug, rows); out.Ref != "" {
		return out
	}
	if b.Found() {
		return use()
	}
	out.Miss = b.Miss()
	return out
}
