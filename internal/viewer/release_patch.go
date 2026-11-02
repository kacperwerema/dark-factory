package viewer

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/milestone"
	"github.com/kacperwerema/dark-factory/internal/privacy"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A patch release (vX.Y.Z, Z > 0) is cut on the existing release/vX.Y
// branch with `issue-cli release cut vX.Y.Z`, outside the milestone flow:
// its fixes are picked from main. The Waiting for release card shows its
// newest candidate (ReleaseWait.Patch) with its own checks: local CI green
// on main's head, a changelog heading or Unreleased entries on the branch,
// go build and go vet passed on the branch tip (recorded by release pick and
// cut), and nothing picked after the candidate.

// gitPatchCut is the newest patch release with a candidate tag and no
// release tag yet ("v0.58.1"), "" for none. A var so tests don't run git.
var gitPatchCut = func(root string) string {
	out, err := exec.Command("git", "-C", root, "tag", "-l", "v*").Output()
	if err != nil {
		return ""
	}
	return newestPatchCut(strings.Fields(string(out)))
}

// newestPatchCut picks, from tag names, the highest patch release that has
// a candidate and no release tag.
func newestPatchCut(tags []string) string {
	have := map[string]bool{}
	for _, t := range tags {
		have[t] = true
	}
	best := ""
	for _, t := range tags {
		rc, ok := milestone.ParseRC(t)
		if !ok || !rc.Exact || !milestone.IsPatch(rc.Cut) || have[rc.Cut] {
			continue
		}
		if best == "" || milestone.Compare(milestone.Norm(rc.Cut), milestone.Norm(best)) > 0 {
			best = rc.Cut
		}
	}
	return best
}

// gitBranchFile is path's contents at branch. A var so tests don't run git.
var gitBranchFile = func(root, branch, path string) string {
	out, err := exec.Command("git", "-C", root, "show", branch+":"+path).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// gitTip is ref's commit, "" when git fails. A var so tests don't run git.
var gitTip = func(root, ref string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "-q", "--verify", ref+"^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// releaseBuiltTip is the branch tip release pick or cut last built and
// vetted. A var so tests don't run git.
var releaseBuiltTip = privacy.BuiltTip

// buildPatchRow is the patch candidate's row; nil without one. st and on
// are local CI's state; mainBranch and mainHead the branch CI tests.
func buildPatchRow(proj *tracker.Project, wf *tracker.WorkflowConfig, changelog string, st *ci.State, ciOn bool, mainBranch, mainHead string, now time.Time) *MilestoneRow {
	root := projectRoot(proj)
	cut := gitPatchCut(root)
	if cut == "" {
		return nil
	}
	branch := milestone.ReleaseBranch(cut)
	if !gitHasBranch(root, branch) {
		return nil
	}
	ms := &MilestoneRow{Version: normVersion(cut), Cut: cut, Patch: true, RC: true, Branch: branch, Today: now.Format("2006-01-02"),
		Snapshots: wf.Privacy.Snapshots(), Worktree: filepath.Join(ciRootFor(proj), ".worktrees", milestone.ReleaseWorktreeName(cut))}
	if ciOn {
		if mainBranch == "" {
			mainBranch = "main"
		}
		ms.Checks = append(ms.Checks, ciCheck(st, mainBranch, mainHead))
	}
	if changelog != "" {
		ms.Checks = append(ms.Checks, patchChangelogCheck(gitBranchFile(root, branch, changelog), cut, branch))
	}
	if fileExists(filepath.Join(ms.Worktree, "go.mod")) {
		tip := gitTip(root, branch)
		c := ReleaseCheck{Key: "build", Text: "go build and go vet pass on " + branch + "'s tip"}
		if tip != "" && sameSha(tip, releaseBuiltTip(root, branch)) {
			c.OK, c.Meta = true, ci.Short(tip)
		} else {
			c.Meta = "not run"
			c.Detail = "not run on " + ci.Short(tip) + ": issue-cli release cut " + cut + " runs them first (so does release pick)"
		}
		ms.Checks = append(ms.Checks, c)
	}
	ms.addCandidate(root, branch, changelog)
	return ms
}

// patchChangelogCheck passes when the branch's changelog has the release's
// heading or entries under ## Unreleased.
func patchChangelogCheck(text, cut, branch string) ReleaseCheck {
	c := ReleaseCheck{Key: "changelog", Text: "## " + cut + " or entries under ## Unreleased on " + branch}
	if regexp.MustCompile(`(?m)^## ` + regexp.QuoteMeta(cut) + `(\s|$)`).MatchString(text) {
		c.OK, c.Meta = true, "## "+cut
		return c
	}
	entries, _ := parseUnreleased(text)
	if len(entries) > 0 {
		c.OK, c.Meta = true, strconv.Itoa(len(entries))
		return c
	}
	c.Meta, c.Detail = "none", "nothing under ## Unreleased on "+branch+": pick a fix with its changelog line"
	return c
}
