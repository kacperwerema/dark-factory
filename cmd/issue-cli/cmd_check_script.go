package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var checkScriptCommand = &Command{
	Name:      "check-script",
	ShortHelp: "Run an issue's guided check scripts (scripts/checks/<name>.sh) from its worktree",
	LongHelp: `Guided check scripts walk the owner through an issue's manual checks that
aren't playtest sessions (CLI commands, API routes, tmux, scripts): each step
says what it checks, why, what to look at and what to expect, runs the
commands, checks what a machine can and asks the owner about the rest. The
owner's run from a terminal offers to tick the boxes that passed and to
comment the failures as "checks:".

A script is scripts/checks/<name>.sh on the issue's branch, with a
"# issue: <slug>" line in its first lines, sourcing the helper library
issue-cli ships ($CHECKS_LIB). Scripts stay on the branch: remove them in a
last commit before the merge. After the merge, check-script finds them in
git history (the commit that removed them) and runs them from main.

Usage:
  issue-cli check-script <slug>               run the issue's scripts (the owner, from a terminal)
  issue-cli check-script <slug> <name>        run one of them
  issue-cli check-script <slug> --yes         machine run: machine checks only, nothing ticked
                                              (agents: pass this before handing over)
  issue-cli check-script <slug> --yes --cli <path>
                                              run them with $ISSUE_CLI=<path> (a branch build)
  issue-cli check-script <slug> --list        list the issue's scripts (exit 1 when none)
  issue-cli check-script <slug> --new <name>  write a starting script, one step per unticked
                                              Manual box, in the issue's worktree
  issue-cli check-script --lib                print the helper library and its API

The scripts run from the issue's worktree: the workflow's, else the git
worktree on work/<slug>; when the workflow has no worktrees, the worktree
you run it from, else the project folder. They run with a bash 4 or newer (PATH, then /opt/homebrew/bin,
/usr/local/bin, /opt/local/bin; stock macOS: brew install bash). A machine
run is also what you get when stdin isn't a terminal. With the feature on,
human-testing waits for each script's newest --yes run to pass.

$ISSUE_CLI is the issue-cli a script checks (lib.sh's sic and sandbox_project
use it): --cli <path>, else the branch's own build at <worktree>/issue-cli
(go build -o ./issue-cli ./cmd/issue-cli) when there is one, else this
issue-cli. Boxes are still ticked by this issue-cli.`,
	Run: runCheckScript,
}

func init() {
	registerCommand(checkScriptCommand)
}

func runCheckScript(ctx *Context, args []string) error {
	fs := newFlagSet("check-script", ctx)
	libFlag := fs.Bool("lib", false, "print the helper library")
	yesFlag := fs.Bool("yes", false, "machine run: machine checks only, nothing ticked")
	listFlag := fs.Bool("list", false, "list the issue's scripts")
	newFlag := fs.String("new", "", "write a starting script with this name")
	cliFlag := fs.String("cli", "", "the issue-cli the scripts check ($ISSUE_CLI), e.g. a branch build")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if *libFlag {
		if fs.NArg() > 0 {
			return unexpectedArgError(fs, fs.Arg(0))
		}
		_, err := ctx.Stdout.Write(checks.Lib)
		return err
	}
	if fs.NArg() == 0 {
		return &usageError{msg: "check-script requires <slug>\n\nExample:\n  issue-cli check-script <slug>\n  issue-cli check-script --lib"}
	}
	if fs.NArg() > 2 {
		return unexpectedArgError(fs, fs.Arg(2))
	}
	slug := fs.Arg(0)
	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	if *newFlag != "" {
		checkout, err := issueCheckout(ctx.Project, issue)
		if err != nil {
			return err
		}
		return newCheckScript(ctx, issue, checkout, *newFlag)
	}
	root, rootErr := checkScriptRoot(ctx.Project, issue)
	if rootErr == nil {
		if checkout, err := issueCheckout(ctx.Project, issue); err == nil && checkout != root {
			fmt.Fprintf(ctx.Stdout, "work/%s is merged into %s: running from %s, not its old worktree %s\n\n", issue.Slug, checks.MergedInto(root, checkout), root, checkout)
		}
	}

	var found []checks.Script
	if rootErr == nil {
		found = checks.Find([]string{root}, issue.Slug)
	}
	// The shipping step removes the scripts before the merge: on main they are
	// only in history, while the owner's run may still be due.
	var removed []checks.Removed
	if len(found) == 0 {
		histRoot := root
		if rootErr != nil {
			histRoot = projectRoot(ctx.Project)
		}
		if removed = checks.FromHistory(histRoot, issue.Slug); len(removed) > 0 {
			root, rootErr = histRoot, nil
			for _, r := range removed {
				found = append(found, checks.Script{Name: r.Name, Path: r.Path, Root: root, From: r.Commit})
			}
		}
	}
	if fs.NArg() == 2 {
		found = filterScripts(found, fs.Arg(1))
	}
	if *listFlag {
		return listCheckScripts(ctx, issue, root, found)
	}
	if rootErr != nil {
		return rootErr
	}
	if len(found) == 0 {
		return noCheckScripts(issue, root, fs.Arg(1))
	}
	if len(removed) > 0 {
		var keep []checks.Removed
		for _, r := range removed {
			if len(filterScripts(found, r.Name)) > 0 {
				keep = append(keep, r)
			}
		}
		written, cleanup, err := checks.WriteRemoved(root, keep)
		if err != nil {
			return err
		}
		defer cleanup()
		for _, r := range keep {
			fmt.Fprintf(ctx.Stdout, "%s from history (removed in %s before the merge): running it from %s\n", r.Path, r.Short(), root)
		}
		fmt.Fprintln(ctx.Stdout)
		found = written
	}
	cli, err := scriptIssueCLI(ctx, root, *cliFlag)
	if err != nil {
		return err
	}
	return runCheckScripts(ctx, issue, found, *yesFlag, cli)
}

// scriptIssueCLI is the issue-cli a run's scripts check ($ISSUE_CLI): the
// --cli path, else the branch build in the issue's own checkout (not the
// project folder a merged issue runs from: a build left there is no
// branch's), else "" for this issue-cli.
func scriptIssueCLI(ctx *Context, root, flag string) (string, error) {
	if flag != "" {
		abs, err := filepath.Abs(flag)
		if err != nil {
			return "", err
		}
		if fi, err := os.Stat(abs); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			return "", &usageError{msg: "--cli " + flag + ": not an executable file"}
		}
		return abs, nil
	}
	if root == "" || root == projectRoot(ctx.Project) {
		return "", nil
	}
	build := filepath.Join(root, tracker.BranchBuild)
	if fi, err := os.Stat(build); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
		fmt.Fprintf(ctx.Stdout, "Checking the branch build %s ($ISSUE_CLI; --cli <path> for another)\n\n", build)
		return build, nil
	}
	return "", nil
}

// checkScriptRoot is where the issue's scripts live and run: its checkout
// (issueCheckout), unless that is a worktree already merged into main and
// left clean, whose code and scripts are older than main's: then the project
// folder.
func checkScriptRoot(proj *tracker.Project, issue *tracker.Issue) (string, error) {
	checkout, err := issueCheckout(proj, issue)
	if err != nil {
		return "", err
	}
	if checks.MergedInto(projectRoot(proj), checkout) != "" {
		return projectRoot(proj), nil
	}
	return checkout, nil
}

// issueCheckout is where the issue's work happens and --new writes: its
// worktree, else the git worktree on its branch (work/<slug>, kept even when
// the workflow declares none), else, when the workflow has no worktrees, the
// worktree of the project it runs from (a slug with a space has no work/
// branch), else the project folder.
func issueCheckout(proj *tracker.Project, issue *tracker.Issue) (string, error) {
	wf := proj.LoadWorkflowForIssue(issue)
	path, branch, enabled := tracker.ResolveWorktree(projectRoot(proj), issue.Slug, wf)
	if enabled && pathExists(path) {
		return path, nil
	}
	if wt := checks.IssueWorktree(projectRoot(proj), issue.Slug); wt != "" {
		return wt, nil
	}
	if !enabled {
		if cwd, err := workingDir(); err == nil {
			if wt := checks.WorktreeHolding(projectRoot(proj), cwd); wt != "" {
				return wt, nil
			}
		}
		return projectRoot(proj), nil
	}
	return "", fmt.Errorf("no worktree for %s at %s\n\nCheck scripts live on the issue's branch (%s) and run from its worktree.\nCreate it with: git worktree add %s %s", issue.Slug, path, branch, path, branch)
}

func filterScripts(found []checks.Script, name string) []checks.Script {
	name = strings.TrimSuffix(name, ".sh")
	for _, s := range found {
		if s.Name == name {
			return []checks.Script{s}
		}
	}
	return nil
}

func noCheckScripts(issue *tracker.Issue, root, name string) error {
	what := "no check scripts"
	if name != "" {
		what = "no check script " + name
	}
	return &exitCodeError{Code: 1, Msg: fmt.Sprintf("%s for %s under %s, nor removed from it in git history\n\nA script is scripts/checks/<name>.sh with a \"# issue: %s\" line near the top.\nStart one: issue-cli check-script %s --new <name>", what, issue.Slug, filepath.Join(root, checks.Dir), issue.Slug, shellQuote(issue.Slug))}
}

type checkScriptEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	OwnerRun bool   `json:"owner_run"`
	From     string `json:"from,omitempty"`
}

func listCheckScripts(ctx *Context, issue *tracker.Issue, root string, found []checks.Script) error {
	if ctx.JSONOutput {
		entries := []checkScriptEntry{}
		for _, s := range found {
			entries = append(entries, checkScriptEntry{Name: s.Name, Path: s.Path, OwnerRun: checks.OwnerRun([]string{s.Root}, s.Name), From: s.From})
		}
		if err := writeJSON(ctx.Stdout, entries); err != nil {
			return err
		}
		if len(found) == 0 {
			return &exitCodeError{Code: 1}
		}
		return nil
	}
	if len(found) == 0 {
		if root == "" {
			return &exitCodeError{Code: 1, Msg: "no check scripts for " + issue.Slug + " (no worktree)"}
		}
		return noCheckScripts(issue, root, "")
	}
	for _, s := range found {
		run := "no owner run yet"
		if checks.OwnerRun([]string{s.Root}, s.Name) {
			run = "owner has run it"
		}
		if s.From != "" {
			fmt.Fprintf(ctx.Stdout, "%s  %s  (%s; from history: removed in %.7s before the merge)\n", s.Name, s.Path, run, s.From)
			continue
		}
		fmt.Fprintf(ctx.Stdout, "%s  %s  (%s)\n", s.Name, s.Path, run)
	}
	return nil
}

func runCheckScripts(ctx *Context, issue *tracker.Issue, found []checks.Script, yes bool, cli string) error {
	lib, err := checks.LibPath()
	if err != nil {
		return fmt.Errorf("writing the check library: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		self = "issue-cli"
	}
	if cli == "" {
		cli = self
	}
	project := ctx.ProjectSlug
	if ctx.Project != nil && ctx.Project.Slug != "" {
		project = ctx.Project.Slug
	}
	bash, err := checks.FindBash()
	if err != nil {
		return &exitCodeError{Code: 2, Msg: err.Error()}
	}
	// The scripts and lib.sh call bash by name too (clean_shell, stand-ins):
	// the same one comes first on PATH.
	path := filepath.Dir(bash) + string(os.PathListSeparator) + os.Getenv("PATH")
	var failed []string
	for i, s := range found {
		if i > 0 {
			fmt.Fprintln(ctx.Stdout)
		}
		cmd := exec.Command(bash, s.Path)
		cmd.Dir = s.Root
		cmd.Stdin, cmd.Stdout, cmd.Stderr = ctx.Stdin, ctx.Stdout, ctx.Stderr
		cmd.Env = append(os.Environ(),
			"PATH="+path,
			"CHECKS_LIB="+lib,
			"CHECKS_SLUG="+issue.Slug,
			"CHECKS_PROJECT="+project,
			"CHECKS_ROOT="+s.Root,
			"CHECKS_NAME="+s.Name,
			"CHECKS_SCRIPT="+s.Path,
			"CHECKS_ISSUE_CLI="+self,
			"ISSUE_CLI="+cli,
		)
		if ctx.Config.Path != "" {
			cmd.Env = append(cmd.Env, "ISSUE_VIEWER_CONFIG="+ctx.Config.Path)
		}
		if yes {
			cmd.Env = append(cmd.Env, "CHECKS_YES=1")
		}
		err := cmd.Run()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exitErr) && exitErr.ExitCode() == 130:
			return &exitCodeError{Code: 130, Msg: "stopped in " + s.Name}
		case errors.As(err, &exitErr):
			failed = append(failed, s.Name)
		default:
			return fmt.Errorf("running %s: %w", s.Path, err)
		}
	}
	if len(failed) > 0 {
		return &exitCodeError{Code: 1, Msg: "check script failed: " + strings.Join(failed, ", ")}
	}
	if yes {
		printOwnerBoxesLeft(ctx, issue)
	}
	return nil
}

// printOwnerBoxesLeft names, after a passing machine run, the issue's
// unticked boxes only the owner can check: the run ticked none of them.
func printOwnerBoxesLeft(ctx *Context, issue *tracker.Issue) {
	comments, _ := tracker.LoadComments(issue.FilePath)
	left := proof.Collect(projectRoot(ctx.Project), issue, comments).OwnerWaiting()
	fmt.Fprintln(ctx.Stdout)
	if len(left) == 0 {
		fmt.Fprintf(ctx.Stdout, "Machine run passed. No box on %s is left to the owner.\n", issue.Slug)
		return
	}
	fmt.Fprintf(ctx.Stdout, "Machine run passed. Left to the owner on %s (never tick these yourself):\n", issue.Slug)
	for _, it := range left {
		fmt.Fprintf(ctx.Stdout, "  %s  %q  (%s)\n", it.ID, it.Text, it.Why)
	}
}

func newCheckScript(ctx *Context, issue *tracker.Issue, root, name string) error {
	name = strings.TrimSuffix(name, ".sh")
	if !checks.ValidName(name) {
		return &usageError{msg: fmt.Sprintf("script name %q: use lowercase letters, digits and dashes, e.g. issue-cli check-script %s --new lead-tab", name, issue.Slug)}
	}
	path := filepath.Join(root, checks.Dir, name+".sh")
	if pathExists(path) {
		return fmt.Errorf("%s already exists; edit it, or pick another name", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(checks.Skeleton(issue.Slug, name, manualBoxes(issue.BodyRaw))), 0o755); err != nil {
		return err
	}
	fmt.Fprintf(ctx.Stdout, "✓ Wrote %s\n", path)
	fmt.Fprintln(ctx.Stdout, "  Fill in each step (the helpers: issue-cli check-script --lib), then:")
	fmt.Fprintf(ctx.Stdout, "  issue-cli check-script %s --yes    machine run, before you hand over\n", shellQuote(issue.Slug))
	fmt.Fprintf(ctx.Stdout, "  The owner runs: issue-cli check-script %s (or ▶ Run checks in the viewer)\n", shellQuote(issue.Slug))
	fmt.Fprintln(ctx.Stdout, "  Commit it on the issue branch; remove it in a last commit before the merge.")
	return nil
}

// manualBoxes are the unticked boxes under the Test Plan's ### Manual
// heading: one starting step each.
func manualBoxes(body string) []checks.Box {
	lines := strings.Split(body, "\n")
	var boxes []checks.Box
	for _, item := range tracker.ListCheckboxes(body) {
		if item.Checked || item.ID == "" || !strings.EqualFold(item.Section, "Test Plan") {
			continue
		}
		if subheading(lines, item.Line) != "manual" {
			continue
		}
		boxes = append(boxes, checks.Box{ID: item.ID, Text: item.Text})
	}
	return boxes
}

// subheading is the nearest "### " heading above line, lowercased.
func subheading(lines []string, line int) string {
	for i := line - 1; i >= 0 && i < len(lines); i-- {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "### ") {
			return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "### ")))
		}
		if strings.HasPrefix(t, "## ") {
			return ""
		}
	}
	return ""
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// usesCheckScripts reports whether the workflow has the guided-check-scripts
// feature: its testing or human-testing prompt names check-script.
func usesCheckScripts(wf *tracker.WorkflowConfig) bool {
	return strings.Contains(wf.StatusPrompt("testing"), "check-script") || strings.Contains(wf.StatusPrompt("human-testing"), "check-script")
}

// printCheckScriptHint says where the issue's guided check scripts stand, at
// testing and human-testing, when the workflow uses them: the scripts and
// who runs what, or how to start one while Manual boxes are open.
func printCheckScriptHint(w io.Writer, proj *tracker.Project, wf *tracker.WorkflowConfig, issue *tracker.Issue) {
	status := strings.ToLower(issue.Status)
	if (status != "testing" && status != "human-testing") || !usesCheckScripts(wf) {
		return
	}
	root, err := checkScriptRoot(proj, issue)
	if err != nil {
		return
	}
	found := checks.Find([]string{root}, issue.Slug)
	if len(found) == 0 {
		if len(manualBoxes(issue.BodyRaw)) == 0 {
			return
		}
		fmt.Fprintln(w, "== Guided checks ==")
		fmt.Fprintln(w, "  None yet. Manual checks that aren't playtests come as a guided check script:")
		fmt.Fprintf(w, "  issue-cli check-script %s --new <name>    (helpers: issue-cli check-script --lib)\n\n", shellQuote(issue.Slug))
		return
	}
	names := make([]string, len(found))
	for i, s := range found {
		names[i] = s.Name
	}
	fmt.Fprintln(w, "== Guided checks ==")
	fmt.Fprintf(w, "  Scripts: %s\n", strings.Join(names, ", "))
	if status == "testing" {
		fmt.Fprintf(w, "  Before handing over: issue-cli check-script %s --yes    (machine checks only; never tick the owner's boxes)\n", shellQuote(issue.Slug))
	}
	fmt.Fprintf(w, "  The owner runs: issue-cli check-script %s    (▶ Run checks in the viewer; ticks the boxes that pass)\n\n", shellQuote(issue.Slug))
}

// checkScriptLibOnly reports whether args are `check-script --lib`, which
// needs no project.
func checkScriptLibOnly(args []string) bool {
	return len(args) == 1 && (args[0] == "--lib" || args[0] == "-lib")
}

// checkScriptGate is the unmet requirement of → human-testing when the
// workflow uses guided check scripts and the issue has some: each script's
// newest machine run (check-script --yes) must have passed. Nil otherwise.
func checkScriptGate(proj *tracker.Project, wf *tracker.WorkflowConfig, issue *tracker.Issue, to string) *transitionProblem {
	if !strings.EqualFold(to, "human-testing") || !usesCheckScripts(wf) {
		return nil
	}
	root, err := checkScriptRoot(proj, issue)
	if err != nil {
		return nil
	}
	var missing []string
	for _, s := range checks.Find([]string{root}, issue.Slug) {
		switch ok, ran := checks.MachineRunPassed(s.Root, s.Name); {
		case !ran:
			missing = append(missing, s.Name+": no machine run")
		case !ok:
			missing = append(missing, s.Name+": the last machine run failed")
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &transitionProblem{
		Kind:        "validator",
		Requirement: "Guided check scripts pass a machine run",
		Message:     strings.Join(missing, "; "),
		Fix:         []string{tracker.InjectProject(proj, "issue-cli check-script "+shellQuote(issue.Slug)+" --yes")},
	}
}
