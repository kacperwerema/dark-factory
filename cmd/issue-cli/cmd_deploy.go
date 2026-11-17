package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/deploy"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var deployCommand = &Command{
	Name:      "deploy",
	ShortHelp: "Run the project's deploy: steps from workflow.yaml in order, with their checks, and record the result (--dry-run prints the plan)",
	LongHelp: `Walk the project's own deploy, the deploy: list in its workflow.yaml, in
order: check each step's needs, run it, run its probe, and stop at the first
red or refused step. Each run is recorded in <issues dir>/.deploys/<id>.json
(each step's state, exit, time, why) with its output in <id>.log; the
Report (Status page) shows the newest one.

  deploy:
    - name: backup
      run: ./scripts/backup-db.sh
      needs: no-live-workers
    - name: app
      run: ./install.sh
      needs: [main-green, owner-ok]
      probe: curl -fsS https://example.com/health
      timeout: 15m

needs: (a word or a list):
  main-green       local CI is green on main's head (issue-cli ci status)
  no-live-workers  no worker session of the project is live
  owner-ok         the owner's words, relayed with --quote

Who may run it: the lead (owner-ok steps with the owner's words in
--quote) or the owner's shell. A worker may run --dry-run only. Not DF's
own release: that is issue-cli release.

Flags:
  --dry-run          print the plan and each need's verdict now; runs and writes nothing
  --step <name>      run only that step
  --from <name>      run from that step to the end (to go on after fixing a red one)
  --quote "<words>"  the owner's words, for a step that needs owner-ok
  --last             print the newest record
  --json             the record (or the plan, with --dry-run) as JSON

Exit codes: 0 every step green (or the plan printed), 1 a step red or
refused, 2 usage error.

Examples:
  issue-cli deploy --dry-run
  issue-cli deploy --quote "ship it"
  issue-cli deploy --step landing
  issue-cli deploy --from app --quote "go on"
  issue-cli deploy --last`,
	Run: runDeploy,
}

func init() {
	registerCommand(deployCommand)
}

// deploySnippet is what deploy prints for a project with no deploy: list.
const deploySnippet = `No deploy: steps in this project's workflow.yaml. Name them in order, e.g.:

  deploy:
    - name: backup
      run: ./scripts/backup-db.sh
      needs: no-live-workers
    - name: app
      run: ./install.sh
      needs: [main-green, owner-ok]
      probe: curl -fsS https://example.com/health

Then: issue-cli deploy --dry-run (docs/workflow.md#deploy)`

// deployNoOwnerHint is the dry run's note when no step waits for the
// owner: a deploy goes out of the machine (docs/departments.md, Nothing
// goes out without the owner's stamp).
const deployNoOwnerHint = "note: no step needs owner-ok, so nothing here waits for the owner's words; a step that publishes should say needs: owner-ok"

// deployLiveWorkers names the project's live worker sessions. A var for tests.
var deployLiveWorkers = func(proj *tracker.Project, root string) []string {
	var names []string
	for _, s := range agentrun.ForProject(proj, root, agentrun.ListSessions()) {
		if name := agentrun.BareName(s.Name); strings.HasPrefix(name, "agent-") {
			names = append(names, name)
		}
	}
	return names
}

func runDeploy(ctx *Context, args []string) error {
	fs := newFlagSet("deploy", ctx)
	dry := fs.Bool("dry-run", false, "print the plan and each need's verdict; run and write nothing")
	only := fs.String("step", "", "run only this step")
	from := fs.String("from", "", "run from this step to the end (after fixing a red one)")
	quoteFlag := fs.String("quote", "", "the owner's words, for a step that needs owner-ok")
	last := fs.Bool("last", false, "print the newest record")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	proj := ctx.Project
	quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
	if *last {
		return printLastDeploy(ctx, proj)
	}
	wf := proj.LoadWorkflow()
	if wf == nil || len(wf.Deploy) == 0 {
		fmt.Fprintln(ctx.Stdout, tracker.InjectProject(proj, deploySnippet))
		return nil
	}
	if err := tracker.CheckDeploy(wf.Deploy); err != nil {
		return fmt.Errorf("%v (in %s)", err, proj.WorkflowFile)
	}
	steps, err := deploy.Select(wf.Deploy, strings.TrimSpace(*only), strings.TrimSpace(*from))
	if err != nil {
		return &usageError{msg: err.Error()}
	}
	root := deployRoot(proj)
	check := func(s tracker.DeployStep) string { return deployRefusal(ctx, proj, wf, root, s, quote) }

	if *dry {
		return printDeployPlan(ctx, proj, wf.Deploy, steps, check)
	}

	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return err
	}
	me := guardWho(ctx, issues, "deploy")
	if me.Role == tracker.RoleWorker {
		return fmt.Errorf("workers don't deploy: a deploy goes out of the machine, so the lead runs it (with the owner's words for an owner-ok step). Ask on your issue trail:\n  %s\nThe plan: %s",
			tracker.InjectProject(proj, fmt.Sprintf("issue-cli comment %s \"question: ready to deploy?\"", valueOr(me.Slug, "<slug>"))),
			tracker.InjectProject(proj, "issue-cli deploy --dry-run"))
	}
	by := valueOr(me.Name, "owner's shell")

	w := ctx.Stdout
	if ctx.JSONOutput {
		w = ctx.Stderr
	}
	fmt.Fprintf(w, "Deploy %s: %s, from %s\n", proj.Slug, countOf(len(steps), "step", "steps"), root)
	rec, err := deploy.Run(deploy.Options{
		Steps: wf.Deploy, IssueDir: proj.IssueDir, Dir: root, Only: strings.TrimSpace(*only), From: strings.TrimSpace(*from),
		By: by, Quote: quote, Sha: deployMainSha(proj, wf), Check: check, Out: w, Now: ctx.now,
	})
	if rec == nil {
		return err
	}
	if ctx.JSONOutput {
		if jerr := writeJSON(ctx.Stdout, rec); jerr != nil {
			return jerr
		}
	}
	file := filepath.Join(deploy.Dir(proj.IssueDir), rec.ID+".json")
	if err != nil {
		return fmt.Errorf("deploy ran but the record was not written: %v", err)
	}
	if f := rec.FailedStep(); f != nil {
		if f.State == deploy.Red {
			for _, line := range deployLogTail(proj, rec, 15) {
				fmt.Fprintln(w, "  │ "+line)
			}
		}
		fmt.Fprintf(w, "record: %s\n", file)
		return fmt.Errorf("deploy stopped at %s (%s): %s\nAfter fixing it, go on from there: %s", f.Name, f.State, f.Why,
			tracker.InjectProject(proj, deployResume(f.Name)+deployQuoteHint(deployOwnerFrom(wf.Deploy, f.Name))))
	}
	fmt.Fprintf(w, "✓ Deploy green (%s)\nrecord: %s\n", countOf(len(rec.Steps), "step", "steps"), file)
	return nil
}

// deployResume is the command that goes on from step name.
func deployResume(name string) string {
	return "issue-cli deploy --from " + shell.Quote(name)
}

// deployOwnerFrom reports whether step name or one after it needs owner-ok.
func deployOwnerFrom(steps []tracker.DeployStep, name string) bool {
	rest, err := deploy.Select(steps, "", name)
	if err != nil {
		return false
	}
	for _, s := range rest {
		if s.Needs.Has(tracker.NeedOwnerOK) {
			return true
		}
	}
	return false
}

// deployRoot is the project's main checkout, where the steps run.
func deployRoot(proj *tracker.Project) string {
	if root := ci.RepoRoot(projectRoot(proj)); root != "" {
		return root
	}
	return projectRoot(proj)
}

// deployMainSha is main's head when the deploy starts, "" without git.
func deployMainSha(proj *tracker.Project, wf *tracker.WorkflowConfig) string {
	root := ciRoot(proj)
	if root == "" {
		return ""
	}
	branch, err := ci.ResolveBranch(root, wf.CI)
	if err != nil {
		return ""
	}
	sha, _ := ci.Head(root, branch)
	return sha
}

// deployRefusal says why step s may not run now, "" when its needs are met.
func deployRefusal(ctx *Context, proj *tracker.Project, wf *tracker.WorkflowConfig, root string, s tracker.DeployStep, quote string) string {
	var why []string
	for _, need := range tracker.DeployNeedWords {
		if !s.Needs.Has(need) {
			continue
		}
		if w := deployNeedRefusal(ctx, proj, wf, root, need, quote); w != "" {
			why = append(why, w)
		}
	}
	return strings.Join(why, "; ")
}

func deployNeedRefusal(ctx *Context, proj *tracker.Project, wf *tracker.WorkflowConfig, root, need, quote string) string {
	switch need {
	case tracker.NeedMainGreen:
		return deployMainGreen(ctx, proj, wf)
	case tracker.NeedNoLiveWorkers:
		if names := deployLiveWorkers(proj, root); len(names) > 0 {
			return fmt.Sprintf("needs no-live-workers, but %s live: %s (%s)", countOf(len(names), "worker is", "workers are"), strings.Join(names, ", "), tracker.InjectProject(proj, "issue-cli agents"))
		}
	case tracker.NeedOwnerOK:
		if quote == "" {
			return `needs owner-ok: pass the owner's words with --quote "<owner's words>"`
		}
	}
	return ""
}

// deployMainGreen is "" when local CI's newest run is green on main's head,
// else why not, with ci's one line.
func deployMainGreen(ctx *Context, proj *tracker.Project, wf *tracker.WorkflowConfig) string {
	if !wf.CI.Enabled() {
		return "needs main-green, but local CI is off: add a ci: block to workflow.yaml (docs/workflow.md#local-ci)"
	}
	root := ciRoot(proj)
	st, err := ci.ReadState(root)
	if err != nil {
		return "needs main-green, but local CI's state does not read: " + err.Error()
	}
	branch, _ := ci.ResolveBranch(root, wf.CI)
	lock, alive := ci.ReadLock(root)
	line := ci.OneLine(st, lock, alive, branch, ctx.now())
	status := tracker.InjectProject(proj, "issue-cli ci status")
	if st == nil || st.State != ci.Green {
		return fmt.Sprintf("needs main-green: %s (%s)", line, status)
	}
	if head, err := ci.Head(root, branch); err == nil && head != "" && st.GreenSha != head {
		return fmt.Sprintf("needs main-green: local CI has not passed %s's head %s yet: %s (%s)", branch, gitdir.ShortSHA(head), line, status)
	}
	return ""
}

// deployPlanStep is one row of the dry run.
type deployPlanStep struct {
	Name    string   `json:"name"`
	Run     string   `json:"run"`
	Probe   string   `json:"probe,omitempty"`
	Needs   []string `json:"needs,omitempty"`
	Timeout string   `json:"timeout"`
	// Refused says what would refuse the step now.
	Refused string `json:"refused,omitempty"`
}

func printDeployPlan(ctx *Context, proj *tracker.Project, all, steps []tracker.DeployStep, check func(tracker.DeployStep) string) error {
	var rows []deployPlanStep
	for _, s := range steps {
		rows = append(rows, deployPlanStep{Name: s.Name, Run: s.Run, Probe: s.Probe, Needs: append([]string(nil), s.Needs...), Timeout: s.Limit().String(), Refused: check(s)})
	}
	ownerStep := false
	for _, s := range all {
		ownerStep = ownerStep || s.Needs.Has(tracker.NeedOwnerOK)
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, map[string]any{"dry_run": true, "steps": rows, "owner_step": ownerStep})
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "Deploy plan for %s (dry run: nothing runs, nothing is written):\n", proj.Slug)
	for i, r := range rows {
		fmt.Fprintf(w, "%d. %s\n", i+1, r.Name)
		fmt.Fprintf(w, "     run:   %s\n", r.Run)
		if r.Probe != "" {
			fmt.Fprintf(w, "     probe: %s\n", r.Probe)
		}
		if len(r.Needs) > 0 {
			verdict := "✓ met now"
			if r.Refused != "" {
				verdict = "✗ " + r.Refused
			}
			fmt.Fprintf(w, "     needs: %s  %s\n", strings.Join(r.Needs, ", "), verdict)
		}
	}
	if !ownerStep {
		fmt.Fprintln(w, deployNoOwnerHint)
	}
	fmt.Fprintln(w, "Run it: "+tracker.InjectProject(proj, "issue-cli deploy")+deployQuoteHint(ownerStep))
	return nil
}

func deployQuoteHint(ownerStep bool) string {
	if ownerStep {
		return ` --quote "<owner's words>"`
	}
	return ""
}

func printLastDeploy(ctx *Context, proj *tracker.Project) error {
	rec := deploy.Last(proj.IssueDir)
	if ctx.JSONOutput {
		if rec == nil {
			return writeJSON(ctx.Stdout, map[string]any{"deploys": 0})
		}
		return writeJSON(ctx.Stdout, rec)
	}
	w := ctx.Stdout
	if rec == nil {
		fmt.Fprintln(w, "No deploy recorded yet. The plan: "+tracker.InjectProject(proj, "issue-cli deploy --dry-run"))
		return nil
	}
	fmt.Fprintln(w, deployLine(rec))
	for i, s := range rec.Steps {
		mark := map[string]string{deploy.Green: "✓", deploy.Red: "✗", deploy.Refused: "✗", deploy.NotRun: "·"}[s.State]
		line := fmt.Sprintf("  %s %d. %s: %s", mark, i+1, s.Name, s.State)
		if s.Why != "" {
			line += " (" + s.Why + ")"
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "record: %s\n", filepath.Join(deploy.Dir(proj.IssueDir), rec.ID+".json"))
	return nil
}

// deployLine is a record in one line: when, who, the verdict.
func deployLine(rec *deploy.Record) string {
	line := fmt.Sprintf("Last deploy %s by %s: %s", rec.Started.Format("2006-01-02 15:04"), valueOr(rec.By, "?"), rec.State)
	if rec.Failed != "" {
		line += " at " + rec.Failed
	}
	if rec.Only != "" {
		line += " (only " + rec.Only + ")"
	}
	if rec.From != "" {
		line += " (from " + rec.From + ")"
	}
	return line + fmt.Sprintf(", %s", rec.Finished.Sub(rec.Started).Round(time.Second))
}

// deployLogTail is the last n lines of the run's log.
func deployLogTail(proj *tracker.Project, rec *deploy.Record, n int) []string {
	raw, err := os.ReadFile(filepath.Join(deploy.Dir(proj.IssueDir), rec.Log))
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
