package agentrun

import (
	"path/filepath"
	"slices"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// CodexSandboxFlags turns routing: codex: into codex's flags for a start in
// workDir. No block, or one that asks for nothing, gives no flags, so the
// start is exactly what Codex does by itself.
//
//   - writable: each folder goes in as --add-dir, with links resolved. git
//     is the common dir and, in a worktree, the worktree's own git dir:
//     codex keeps the dir a workspace's .git file points at read-only until
//     that dir is named too, so with the common dir alone a commit still
//     stops at index.lock (tried on codex-cli 0.163.0). It sits inside the
//     common dir, so it grants nothing more.
//   - approval: auto-review needs both flags: the reviewer only sees requests
//     under an interactive policy. ask adds nothing, as it is Codex's default.
//   - Any of these also names workspace-write: --add-dir means nothing in
//     another mode, and it keeps never from meeting a danger-full-access set
//     in the user's own config.
//
// Values come from a word list, never from the file as text, so this can't
// produce the bypass flag or a network setting. An unknown word adds nothing.
func CodexSandboxFlags(cfg *tracker.CodexSandbox, issueDir, workDir string) []string {
	if cfg == nil {
		return nil
	}
	var flags []string
	var added []string
	add := func(dir string) {
		if dir == "" || slices.Contains(added, dir) {
			return
		}
		added = append(added, dir)
		flags = append(flags, "--add-dir", dir)
	}
	for _, w := range cfg.Writable {
		switch w {
		case tracker.CodexWritableIssues:
			add(absResolved(issueDir))
		case tracker.CodexWritableGit:
			add(gitdir.Common(workDir))
			add(gitdir.Worktree(workDir))
		}
	}
	switch cfg.Approval {
	case tracker.CodexApprovalAutoReview:
		flags = append(flags, "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`)
	case tracker.CodexApprovalNever:
		flags = append(flags, "--ask-for-approval", "never")
	}
	if len(flags) == 0 {
		return nil
	}
	return append([]string{"--sandbox", "workspace-write"}, flags...)
}

// codexSandbox is the routing: codex: block a start reads: its own workflow
// file's, else the project's workflow.yaml, so a department file that says
// nothing follows the project.
func codexSandbox(proj *tracker.Project, wf *tracker.WorkflowConfig) *tracker.CodexSandbox {
	if wf != nil && wf.Routing != nil && wf.Routing.Codex != nil {
		return wf.Routing.Codex
	}
	if proj == nil {
		return nil
	}
	if main := proj.LoadWorkflow(); main != nil && main.Routing != nil {
		return main.Routing.Codex
	}
	return nil
}

// projectIssueDir is proj's issue folder, "" without a project.
func projectIssueDir(proj *tracker.Project) string {
	if proj == nil {
		return ""
	}
	return proj.IssueDir
}

// absResolved is dir absolute with its links resolved, "" for no dir or one
// that isn't there: codex needs the real path, and an --add-dir that doesn't
// exist grants nothing.
func absResolved(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	r, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return ""
	}
	return r
}
