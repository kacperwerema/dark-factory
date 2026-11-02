package tracker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// BranchBuild is where a worker's own build of issue-cli goes: the root of
// its worktree, a path .gitignore already ignores. check-script runs a
// script with $ISSUE_CLI set to it when it is there.
const BranchBuild = "issue-cli"

// BuildBehind counts the commits branch has in root that the build version
// (`git describe` stamped by make install, maybe "-dirty") lacks; 0 when the
// build is at or ahead of branch, or git can't tell ("dev", an unknown
// describe, no such branch). A var so tests set it.
var BuildBehind = func(root, branch, version string) int {
	v := strings.TrimSuffix(strings.TrimSpace(version), "-dirty")
	if v == "" || v == "dev" || v == "unknown" || strings.ContainsAny(v, " :") || strings.HasPrefix(v, "-") || strings.HasPrefix(branch, "-") {
		return 0
	}
	out, err := exec.Command("git", "-C", root, "rev-list", "--count", v+"^{commit}.."+branch).Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return n
}

// BuildPromptSection is the "## Your issue-cli build" part of a worker's
// briefing on the engine's own repository (one with cmd/issue-cli): when the
// installed build is behind main, main's workflow.yaml and the prompts may
// name rules and flags it doesn't know. The sanctioned way through is the
// worker's own build from its worktree, never make install: that replaces
// the build the owner may be soaking, and installing is the lead's or the
// owner's call. Empty when the build is up to date, unknown, or the project
// isn't the engine.
func BuildPromptSection(proj *Project, workDir string, wf *WorkflowConfig) string {
	if workDir == "" {
		return ""
	}
	if fi, err := os.Stat(filepath.Join(workDir, "cmd", "issue-cli")); err != nil || !fi.IsDir() {
		return ""
	}
	branch := "main"
	if wf != nil && wf.CI.Enabled() && strings.TrimSpace(wf.CI.Branch) != "" {
		branch = strings.TrimSpace(wf.CI.Branch)
	}
	n := BuildBehind(workDir, branch, BuildVersion)
	if n <= 0 {
		return ""
	}
	project := ""
	if proj != nil && proj.Slug != "" {
		project = " --project " + proj.Slug
	}
	commits := "commits"
	if n == 1 {
		commits = "commit"
	}
	return fmt.Sprintf("\n\n## Your issue-cli build\n\n"+
		"The installed issue-cli (%s) is %d %s behind %s: %s's workflow.yaml and this briefing may name rules and flags it doesn't know (an unknown validation rule, a missing flag).\n"+
		"- Build your branch's issue-cli once in your worktree and use it for this issue: `go build -o ./%s ./cmd/issue-cli`, then `./%s%s <command>` for transitions, checks and gates (the file is git-ignored). Rebuild after you merge %s.\n"+
		"- `./%s%s check-script <slug> --yes` runs your guided check scripts with $ISSUE_CLI set to that build; `--cli <path>` names another.\n"+
		"- never make install, during a soak or not: it replaces the installed build (the one the owner may be soaking) for every agent on this machine. Installing and restarting the viewer are the lead's or the owner's call.\n",
		BuildVersion, n, commits, branch, branch, BranchBuild, BranchBuild, project, branch, BranchBuild, project)
}
