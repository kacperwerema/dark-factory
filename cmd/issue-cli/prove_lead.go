package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// leadCommandRunner runs issue-cli itself with argv in dir, env added to
// this process's own. A var so tests need not start processes.
var leadCommandRunner = func(dir string, env, argv []string) (out []byte, exit int, err error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, -1, err
	}
	cmd := exec.Command(exe, argv...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return buf.Bytes(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return buf.Bytes(), -1, err
	}
	return buf.Bytes(), 0, nil
}

// runProveLead is prove <slug> --lead-run [<AC-id>…]: the lead (or the
// owner's shell, never a worker) runs each lead link's command that has no
// passing run for the branch's last change, records every run in the
// sidecar, then prints the proof report, ticking what is proven.
func runProveLead(ctx *Context, issue *tracker.Issue, criteria []tracker.CheckboxItem, ids []string) error {
	proj := ctx.Project
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	me := whoAmI(proj, issues)
	if me.Role == tracker.RoleWorker {
		guardNever(ctx, "prove --lead-run")
		return fmt.Errorf("workers can't record a lead run (you are %s, detected from %s): a lead link waits on the lead's act. Stop here; the lead's tick (issue-cli queue next) lists it, no question: comment needed", valueOr(me.Name, "a worker"), me.Signal)
	}
	if refusal := outOfScope(proj, me, issue, "runs lead commands for"); refusal != "" {
		return fmt.Errorf("%s", refusal)
	}
	want := map[string]bool{}
	for _, id := range ids {
		it, st := tracker.ResolveCheckboxRef(criteria, id)
		if st != tracker.RefFound {
			return fmt.Errorf("%s is not an acceptance criterion of %s; criteria: %s", id, issue.Slug, criteriaIDs(criteria))
		}
		want[strings.ToUpper(it.ID)] = true
	}

	root := projectRoot(proj)
	comments, _ := tracker.LoadComments(issue.FilePath)
	report := proof.Collect(root, issue, comments)
	var todo []string
	for _, w := range report.WaitsOnLead() {
		if (len(want) == 0 || want[strings.ToUpper(w.ID)]) && !containsStr(todo, w.Command) {
			todo = append(todo, w.Command)
		}
	}
	if len(todo) == 0 {
		fmt.Fprintf(ctx.Stdout, "Nothing waits on the lead in %s: every lead link has a passing run for the last change.\n", issue.Slug)
		return printProof(ctx, issue, nil)
	}
	if mem := autostart.ReadMemory(); mem.Found && !mem.CanStart {
		return fmt.Errorf("mem-guard says no run now (%s); try again when memory is lower", mem.Note)
	}
	worktree := filepath.Join(root, ".worktrees", issue.Slug)
	head := ""
	if out, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output(); err == nil {
		head = strings.TrimSpace(string(out))
	}
	failed := 0
	for _, command := range todo {
		lc, err := proof.ParseLeadCommand(issue.Slug, command)
		if err != nil {
			fmt.Fprintf(ctx.Stdout, "✗ %v\n  → fix the link: issue-cli prove %s <AC-id> --clear, then --lead again\n", err, issue.Slug)
			failed++
			continue
		}
		var argv []string
		if ctx.ConfigPath != "" {
			if abs, err := filepath.Abs(ctx.ConfigPath); err == nil {
				argv = append(argv, "--config", abs)
			}
		}
		argv = append(argv, "--project", proj.Slug)
		argv = append(argv, lc.Args...)
		fmt.Fprintf(ctx.Stdout, "== Lead run: %s ==\n", command)
		// The project checkout, not the worktree: a run from under
		// .worktrees/ would read as a worker's.
		out, exit, runErr := leadCommandRunner(root, lc.Env, argv)
		run := tracker.LeadRun{Command: command, At: ctx.now().UTC(), Head: head, OK: runErr == nil && exit == 0, Exit: exit, By: valueOr(me.Name, "owner")}
		for _, l := range lastLines(string(out), 20) {
			run.Tail = append(run.Tail, scrubHome(l))
		}
		if runErr != nil {
			run.Tail = append(run.Tail, scrubHome(runErr.Error()))
		}
		if err := recordLeadRun(issue.FilePath, run); err != nil {
			return fmt.Errorf("recording the lead run: %w", err)
		}
		if run.OK {
			fmt.Fprintf(ctx.Stdout, "✓ passed\n")
			continue
		}
		failed++
		fmt.Fprintf(ctx.Stdout, "✗ failed (exit %d); last lines:\n", exit)
		for _, l := range run.Tail {
			fmt.Fprintf(ctx.Stdout, "    %s\n", l)
		}
	}
	if err := printProof(ctx, issue, nil); err != nil {
		return err
	}
	if failed > 0 {
		return &exitCodeError{Code: 1, Msg: fmt.Sprintf("✗ %d of %d lead commands did not pass; the criteria still wait on the lead", failed, len(todo))}
	}
	return nil
}

// recordLeadRun appends run to the issue's sidecar.
func recordLeadRun(issuePath string, run tracker.LeadRun) error {
	store, err := tracker.LoadData(issuePath)
	if err != nil {
		return err
	}
	store.LeadRuns = append(store.LeadRuns, run)
	return tracker.SaveData(issuePath, store)
}

// scrubHome writes the home folder as ~ in a recorded output line.
func scrubHome(line string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		return strings.ReplaceAll(line, home, "~")
	}
	return line
}

// leadLinkHint ends the worker guard's refusals: when a criterion's proof
// needs the refused act (a guided check script that dispatches or spawns a
// lead), the worker links it to the lead instead of asking.
func leadLinkHint(proj *tracker.Project, slug string) string {
	slug = valueOr(slug, "<slug>")
	return "\n\nA guided check script proving a criterion needs this? Link the criterion to the lead's run instead, then stop (no question: needed):\n  " +
		tracker.InjectProject(proj, fmt.Sprintf("issue-cli prove %s <AC-id> --lead \"issue-cli check-script %s --yes\"", slug, slug))
}
