package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/lint"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// candidateWorkflow loads the workflow file --file names, read only: a
// candidate the owner is about to splice into a live file. It returns the
// config as written (no project-wide blocks merged in) and the file's
// absolute path. A file that is missing, doesn't parse or has no statuses
// is an error naming it.
func candidateWorkflow(path string) (*tracker.WorkflowConfig, string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return nil, "", fmt.Errorf("--file %s: %w", path, err)
	}
	wf, err := tracker.LoadWorkflow(abs)
	if err != nil {
		return nil, "", fmt.Errorf("--file: %w\nNothing was written; the live workflow file is unchanged. Fix the candidate and run this again.", err)
	}
	if len(wf.Statuses) == 0 {
		return nil, "", fmt.Errorf("--file %s: no statuses: list, so nothing could walk it\nNothing was written; the live workflow file is unchanged.", path)
	}
	return wf, abs, nil
}

// candidateLabel is the "Workflow file:" value for a --file candidate.
func candidateLabel(abs string) string {
	return abs + " (candidate, read only: not the file the board reads)"
}

// printCandidateLint lists the findings of the checks that read only a
// workflow file (lint.File: schema, workflow, template, placeholders, …)
// on the candidate, then where the live file and the board's checks are.
func printCandidateLint(w io.Writer, abs string, p *tracker.Project, cmd string) {
	fmt.Fprintln(w)
	raw, err := os.ReadFile(abs)
	var findings []lint.Finding
	if err == nil {
		findings, err = lint.File(abs, raw, time.Now())
	}
	switch {
	case err != nil:
		fmt.Fprintf(w, "== Lint (file checks) ==\n  ⚠ %v\n", err)
	case len(findings) == 0:
		fmt.Fprintln(w, "== Lint (file checks) ==  none")
	default:
		fmt.Fprintf(w, "== Lint (file checks): %d ==\n", len(findings))
		for _, f := range findings {
			fmt.Fprintf(w, "  ⚠ %s %s: %s\n", f.Check, f.Where, f.Message)
			if f.Fix != "" {
				fmt.Fprintf(w, "    fix: %s\n", f.Fix)
			}
		}
	}
	fmt.Fprintln(w, "Nothing was written. The board's checks (issues on these statuses, jobs, project-wide keys) before the merge, as only what the candidate changes: issue-cli health lint --workflow "+abs+" [--projects <candidate projects.yaml>] (Product's file); a department's file runs them on the live file after the swap: issue-cli health lint --check department,departments,schema")
	if p != nil {
		live := []string{"issue-cli " + cmd}
		for _, d := range p.WorkflowDepartments() {
			live = append(live, fmt.Sprintf("issue-cli %s --department %s (%s)", cmd, d.Name, d.Workflow))
		}
		fmt.Fprintf(w, "Live files: %s\n", strings.Join(live, " · "))
	}
}

// refuseWorkflowPath refuses a --workflow value that reads like a file
// path: --workflow is an alias for --system (a system overlay's name), and
// a path there named an overlay that doesn't exist.
func refuseWorkflowPath(value string) error {
	v := strings.TrimSpace(value)
	if !looksLikeWorkflowPath(v) {
		return nil
	}
	return fmt.Errorf("--workflow %q looks like a file path, but --workflow names a system overlay (an alias for --system), not a file\nA candidate workflow file, read only: issue-cli process workflow --file %s (or process transitions --file %s)\nA department's live file: issue-cli process workflow --department <name>", v, v, v)
}

func looksLikeWorkflowPath(v string) bool {
	lower := strings.ToLower(v)
	return strings.Contains(v, "/") || strings.Contains(v, string(filepath.Separator)) ||
		strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}
