package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// projectWorkflowFile mirrors Project.LoadWorkflow's file choice: the
// project's configured file, then ./workflow.yaml. "" means the project runs
// on the built-in default workflow.
func projectWorkflowFile(p *tracker.Project) string {
	if p != nil && p.WorkflowFile != "" && isRegularFile(p.WorkflowFile) {
		return p.WorkflowFile
	}
	if isRegularFile("workflow.yaml") {
		return "workflow.yaml"
	}
	return ""
}

// staleRecordWarning is the warning for a features: record that misses
// catalog features the file has; "" when the record is fine or there is no
// file to read.
func staleRecordWarning(p *tracker.Project) string {
	path := projectWorkflowFile(p)
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	plan, err := workflowupgrade.BuildPlan(path, raw, "", false)
	if err != nil {
		return ""
	}
	return plan.StaleRecord()
}

// workflowFileLabel names the workflow file p runs on as an absolute path,
// or "built-in default" when it has none, for the "Workflow file:" line in
// process and show output.
func workflowFileLabel(p *tracker.Project) string {
	path := projectWorkflowFile(p)
	if path == "" {
		return "built-in default"
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// workflowFileFor is the file upgrade and scan read: Product's
// (projectWorkflowFile), or with --department that department's own. Run
// from another worktree of the repository that holds that file, it is the
// same file in the cwd's worktree (inWorktree): scan writes it, and a worker
// never gets an uncommitted change on main's working tree.
func workflowFileFor(p *tracker.Project, department string) (string, error) {
	dept, err := departmentFlag(p, department)
	if err != nil {
		return "", err
	}
	path := projectWorkflowFile(p)
	if dept != "" {
		path = p.DepartmentWorkflowPath(dept)
		if path == "" {
			return "", fmt.Errorf("--department %s: it runs on Product's workflow (no workflow: in projects.yaml)\n\nGive it one:\n  issue-cli workflow init --template content --out workflows/%s.yaml", dept, strings.ToLower(tracker.Slugify(dept)))
		}
	}
	if path == "" {
		return "", nil
	}
	return inWorktree(path, "")
}

// inWorktree maps path, a file in some checkout of a git repository, to the
// same file in the worktree that holds cwd ("" for the process's), when cwd
// is in another worktree of that repository (same git common dir). Outside
// git, in another repository or in the file's own checkout, path is returned
// as is. A worktree that lacks the file is an error, never a fallback to the
// other checkout's copy.
func inWorktree(path, cwd string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	fileTop, fileCommon, ok := gitCheckout(filepath.Dir(abs))
	if !ok {
		return path, nil
	}
	cwdTop, cwdCommon, ok := gitCheckout(cwd)
	if !ok || cwdCommon != fileCommon || cwdTop == fileTop {
		return path, nil
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return path, nil
	}
	rel, err := filepath.Rel(fileTop, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path, nil
	}
	mapped := filepath.Join(cwdTop, rel)
	if !isRegularFile(mapped) {
		return "", fmt.Errorf("this worktree (%s) has no %s: the project's file is %s, in another checkout of the same repository, and issue-cli reads and writes only your worktree's copy\n\nMerge main into your branch first, or run it from the checkout that holds the file", cwdTop, filepath.ToSlash(rel), abs)
	}
	return mapped, nil
}

// gitCheckout is the top-level folder and git common dir of the checkout
// holding dir ("" for the process's cwd), both with links resolved; ok is
// false outside git.
func gitCheckout(dir string) (top, common string, ok bool) {
	args := []string{"rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir"}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", "", false
	}
	if top, err = filepath.EvalSymlinks(lines[0]); err != nil {
		return "", "", false
	}
	if common, err = filepath.EvalSymlinks(lines[1]); err != nil {
		return "", "", false
	}
	return top, common, true
}
