package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/critique"
	"github.com/kacperwerema/dark-factory/internal/redrounds"
	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var critiqueCommand = &Command{
	Name:      "critique",
	ShortHelp: "Run a fresh read-only critic on an issue before its critique gate",
	LongHelp: `Run a fresh critic: a read-only agent that did not write the work reads the
issue, the branch's diff, the changed files and the settled rules (paths only,
never your summary), assumes the work is wrong and reports findings. A blocks
finding holds the workflow's critique gate until a new run clears it or the
owner overrides it. should findings are shown; notes are kept in the log.

Usage:
  issue-cli critique <slug> [--agent claude|codex] [--brief]
  issue-cli critique <slug> --show
  issue-cli critique <slug> --override --quote "<the owner's words>"

  --brief      print the brief the critic would get and start nothing
  --show       print the newest run from the current status
  --override   the owner lets the gate pass past the newest run (refused
               from a worker's shell)

Before starting it checks mem-guard and the CPU load (under the core count).
It runs headless and read-only: claude -p with only Read/Grep/Glob, or codex
exec --sandbox read-only, in the issue's worktree (so its tokens count for
the issue in issue-cli spend). The agent is routing's pick switched to the
other installed agent, unless critique: agent: or --agent names one;
critique: command: in workflow.yaml replaces the command (brief on stdin).

Runs are kept in <issues>/.critique/runs.jsonl and each leaves a critique:
comment on the issue.

The critic's output format:

` + critique.Format,
	Run: runCritique,
}

func init() {
	registerCommand(critiqueCommand)
}

func runCritique(ctx *Context, args []string) error {
	slug, rest, err := requireSlug(args, "critique")
	if err != nil {
		return err
	}
	fs := newFlagSet("critique", ctx)
	agentFlag := fs.String("agent", "", "agent CLI for the critic: claude or codex")
	briefOnly := fs.Bool("brief", false, "print the brief and start nothing")
	show := fs.Bool("show", false, "print the newest run from the current status")
	override := fs.Bool("override", false, "the owner lets the gate pass past the newest run")
	quoteFlag := fs.String("quote", "", "with --override: the owner's words")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	issue, issues, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	slug = issue.Slug
	wf, err := issueWorkflow(ctx, issue)
	if err != nil {
		return err
	}
	wf = wf.ForIssue(issue)
	issuesDir := ctx.Project.IssueDir

	if *show || *override {
		runs, err := critique.Load(issuesDir, slug)
		if err != nil {
			return err
		}
		latest := critique.Latest(runs, slug, issue.Status)
		if latest == nil {
			return fmt.Errorf("no critique run for %s from %s yet:\n\n  issue-cli critique %s", slug, issue.Status, slug)
		}
		if *show {
			fmt.Fprintln(ctx.Stdout, critique.CommentText(latest))
			for _, f := range latest.Report.Of(critique.Note) {
				if !f.Malformed {
					fmt.Fprintln(ctx.Stdout, "- "+f.String())
				}
			}
			return nil
		}
		me := whoAmI(ctx.Project, issues)
		if me.Role == tracker.RoleWorker {
			return fmt.Errorf("an override is the owner's (you are %s, detected from %s): fix the blocks findings and run issue-cli critique %s again, or ask on your trail:\n  %s",
				valueOr(me.Name, "a worker"), me.Signal, slug,
				tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli comment %s \"question: ...\"", slug)))
		}
		quote := strings.TrimSpace(normalizeEscapedText(*quoteFlag))
		if quote == "" {
			return &usageError{msg: "critique --override needs --quote: the owner's words that let the gate pass. Nothing was recorded."}
		}
		if !latest.Holds() {
			fmt.Fprintf(ctx.Stdout, "Run %s is %s: nothing to override.\n", latest.ID, latest.Verdict())
			return nil
		}
		o := critique.Override{At: ctx.Now(), Quote: quote, By: valueOr(me.Name, "owner")}
		if err := critique.RecordOverride(issuesDir, latest.ID, o); err != nil {
			return err
		}
		_ = tracker.AddComment(issue.FilePath, 0, fmt.Sprintf("critique: run %s overridden by the owner: %q", latest.ID, quote), "issue-cli")
		fmt.Fprintf(ctx.Stdout, "✓ Run %s overridden: the critique gate out of %s passes.\n", latest.ID, issue.Status)
		return nil
	}

	to, ok := wf.CritiqueEdge(issue.Status)
	if !ok {
		var edges []string
		for _, t := range wf.CritiqueEdges() {
			edges = append(edges, t.From+" → "+t.To)
		}
		where := "no edge in this workflow has one"
		if len(edges) > 0 {
			where = "it has one on " + strings.Join(edges, ", ")
		}
		return fmt.Errorf("no critique gate out of %s for %s (%s); nothing to critique now", issue.Status, slug, where)
	}

	root := projectRoot(ctx.Project)
	dir := filepath.Join(root, ".worktrees", slug)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		dir = root
	}
	in, head := critiqueInput(root, dir, ctx.Project, issue, issues, to, ctx.now())
	if *briefOnly {
		fmt.Fprint(ctx.Stdout, critique.Brief(in))
		return nil
	}

	// Held runs are red rounds of the critique check: while its cap
	// question is open the critic is not run again.
	if rr, err := loadRedRounds(ctx, wf, issue); err == nil {
		check := redrounds.CritiqueCheck(issue.Status, to)
		if q, open := rr.Open[check]; open {
			return &redrounds.HoldError{Slug: slug, Question: q, Streak: rr.Streak(check), Max: rr.Max}
		}
	}

	timeout, err := critique.Timeout(wf.Critique)
	if err != nil {
		return err
	}
	if mem := autostart.ReadMemory(); mem.Found && !mem.CanStart {
		return fmt.Errorf("mem-guard says no new agent now (%s); nothing started — run issue-cli critique %s again when memory is lower", mem.Note, slug)
	}
	if cpu := autostart.ReadLoad(); cpu.Found && cpu.Cores > 0 && cpu.Avg1 >= float64(cpu.Cores) {
		return fmt.Errorf("load %s on %d cores: the machine is busy; nothing started — run issue-cli critique %s again when the load is under %d", autostart.FormatLoad(cpu.Avg1), cpu.Cores, slug, cpu.Cores)
	}

	agent, why := critiqueAgent(ctx.Project, wf, issue, *agentFlag, ctx.Now())
	var command string
	if wf.Critique != nil && strings.TrimSpace(wf.Critique.Command) != "" {
		command = wf.Critique.Command
		why += "; critique: command:"
	}
	argvFor := func(agent string) ([]string, error) {
		if command != "" {
			return nil, nil
		}
		argv, err := critiqueArgv(agent)
		if err != nil {
			return nil, err
		}
		if !routing.Installed(agent) {
			return nil, fmt.Errorf("%s is not on PATH; install it, pass --agent, or set critique: command: in %s", agent, issueWorkflowFileLabel(ctx.Project, wf))
		}
		return argv, nil
	}
	argv, err := argvFor(agent)
	if err != nil {
		return err
	}
	// Write the diff to its file only now that the critic starts.
	if in.Diff != "" {
		if err := writeCritiqueDiff(dir, filepath.Join(dir, in.Diff)); err != nil {
			in.Diff = ""
		}
	}
	brief := critique.Brief(in)
	env := []string{"CRITIQUE_SLUG=" + slug, "CRITIQUE_FROM=" + issue.Status, "CRITIQUE_TO=" + to, "CRITIQUE_PATHS=" + strings.Join(in.Paths(), "\n")}
	runCritic := func(agent, why string, argv []string) (string, error) {
		fmt.Fprintf(ctx.Stdout, "Critic: %s (%s), read-only in %s, %s → %s; up to %s…\n", agent, why, dir, issue.Status, to, timeout)
		return critique.Exec(context.Background(), dir, command, argv, brief, env, timeout)
	}
	out, runErr := runCritic(agent, why, argv)

	// A critic stopped by its usage limit: that subscription rests
	// (routing's state, as for a worker) and the critic runs once more on
	// the other installed agent that isn't resting.
	var fallback string
	if lim, ok := critiqueLimit(agent, command, out, runErr, ctx.Now()); ok {
		_, _ = routing.Mark(routing.Dir(), lim, ctx.Project.Slug, "critique:"+slug, ctx.Now())
		fallback = fmt.Sprintf("%s hit its usage limit (resting until %s)", lim.Agent, routing.RestUntilText(routing.Rest{Until: lim.Until, KnownReset: lim.KnownReset}, ctx.Now()))
		fmt.Fprintf(ctx.Stdout, "%s: marked resting.\n", fallback)
		next := ""
		chosen := strings.HasPrefix(why, "--agent") || strings.HasPrefix(why, "critique: agent:")
		if command == "" && !chosen {
			st, _ := routing.Load(routing.Dir())
			next = critiqueOther(st, lim.Agent, ctx.Now())
		}
		if chosen {
			fmt.Fprintf(ctx.Stdout, "%s was chosen by %s, so no other agent is tried; the run is recorded as failed.\n", lim.Agent, why)
		} else if next == "" {
			fmt.Fprintln(ctx.Stdout, "No other agent to run the critic on (installed and not resting); the run is recorded as failed.")
		} else if nargv, err := argvFor(next); err != nil {
			fmt.Fprintf(ctx.Stdout, "No fallback run on %s: %v; the run is recorded as failed.\n", next, err)
			fallback += "; no fallback run on " + next + ": " + err.Error()
		} else {
			agent = next
			fallback += "; ran on " + next
			out, runErr = runCritic(next, "fallback: "+lim.Agent+" at its usage limit", nargv)
			// Only one fallback; a second limit is marked, not run again.
			if lim2, ok := critiqueLimit(next, command, out, runErr, ctx.Now()); ok {
				_, _ = routing.Mark(routing.Dir(), lim2, ctx.Project.Slug, "critique:"+slug, ctx.Now())
				fmt.Fprintf(ctx.Stdout, "%s hit its usage limit too: marked resting; the run is recorded as failed.\n", next)
			}
		}
	}

	run := &critique.Run{Slug: slug, From: issue.Status, To: to, At: ctx.Now(), Agent: agent, Head: head, Report: critique.Parse(out), Fallback: fallback}
	if runErr != nil {
		run.Failed, run.Error = true, runErr.Error()
	}
	if err := critique.Record(issuesDir, run); err != nil {
		return fmt.Errorf("recording the critique run: %w", err)
	}
	text := critique.CommentText(run)
	_ = tracker.AddComment(issue.FilePath, 0, text, "issue-cli")
	fmt.Fprintln(ctx.Stdout, text)
	if mal := run.Report.Malformed(); len(mal) > 0 {
		fmt.Fprintf(ctx.Stdout, "\n%d malformed finding%s kept at their severity (see above).\n", len(mal), critiquePlural(len(mal)))
	}
	fmt.Fprintln(ctx.Stdout)
	if err := critique.GateError(run, slug, issue.Status); err != nil {
		fmt.Fprintf(ctx.Stdout, "The gate %s → %s holds: %v\n", issue.Status, to, err)
		critiqueRedRound(ctx, wf, issue, redrounds.CritiqueCheck(issue.Status, to))
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "✓ No blocks finding: the critique gate %s → %s passes.\n", issue.Status, to)
	if n := run.Report.Count(critique.Should); n > 0 {
		fmt.Fprintf(ctx.Stdout, "%d should finding%s above: fix them or say why not on the trail.\n", n, critiquePlural(n))
	}
	return nil
}

func critiquePlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// critiqueRedRound says where a held run leaves the critique check's red
// rounds and, at the cap, posts the owner's question.
func critiqueRedRound(ctx *Context, wf *tracker.WorkflowConfig, issue *tracker.Issue, check string) {
	rr, err := loadRedRounds(ctx, wf, issue)
	if err != nil {
		return
	}
	s := rr.Streak(check)
	if s.Rounds == 0 {
		return
	}
	if _, open := rr.Open[check]; !open && s.AtCap(rr.Max) {
		if err := tracker.AddCommentBy(issue.FilePath, 0, redrounds.QuestionText(s, rr.Max), "cli", commentAuthor(ctx.Project, issue)); err != nil {
			fmt.Fprintf(ctx.Stderr, "warning: red-round question not posted: %v\n", err)
			return
		}
		fmt.Fprintf(ctx.Stdout, "\nThe critique check is %s: it is a question for the owner now (a question: red rounds: comment), so the critic is not run again until the owner resolves it. See: issue-cli rounds %s\n", s.Label(rr.Max), issue.Slug)
		return
	}
	why := ""
	if n := len(s.History); n > 0 {
		why = fmt.Sprintf(" — round %d: %s", s.History[n-1].N, s.History[n-1].Reason())
	}
	fmt.Fprintf(ctx.Stdout, "\nThe critique check is %s%s (max_red_rounds); only a finding that comes back counts, and at %d it becomes a question for the owner (issue-cli rounds %s).\n", s.Label(rr.Max), why, rr.Max, issue.Slug)
}

// critiqueArgv is the critic's read-only command for an agent; a seam so
// tests run stand-in CLIs and never a real critic.
var critiqueArgv = critique.Argv

// critiqueLimit finds the agent's usage-limit message in a run that exited
// non-zero: a CLI at its limit stops with an error. A run that finished, or
// one that timed out (its output may quote a limit message from the work
// it read), is never read as a limit. FindLimit reads only the last lines.
// With critique: command: the agent is unknown, so either CLI's message
// counts.
func critiqueLimit(agent, command, out string, runErr error, now time.Time) (routing.Limit, bool) {
	if runErr == nil || strings.HasPrefix(runErr.Error(), "timed out") {
		return routing.Limit{}, false
	}
	text := out + "\n" + runErr.Error()
	scan := agent
	if command != "" {
		scan = ""
	}
	return routing.FindLimit(scan, text, now)
}

// critiqueOther is another known agent than not, installed and not resting;
// "" when there is none.
func critiqueOther(st *routing.State, not string, now time.Time) string {
	for _, other := range routing.KnownAgents {
		if other == not {
			continue
		}
		if _, resting := st.Resting(other, now); !resting && routing.Installed(other) {
			return other
		}
	}
	return ""
}

// critiqueAgent picks the critic's CLI: --agent or critique: agent: wins;
// otherwise routing's pick for the issue (the worker's agent), switched to
// the other known agent when it is installed and not resting.
func critiqueAgent(proj *tracker.Project, wf *tracker.WorkflowConfig, issue *tracker.Issue, explicit string, now time.Time) (agent, why string) {
	if a := strings.TrimSpace(explicit); a != "" {
		return a, "--agent"
	}
	if wf.Critique != nil && strings.TrimSpace(wf.Critique.Agent) != "" {
		return strings.TrimSpace(wf.Critique.Agent), "critique: agent:"
	}
	st, _ := routing.Load(routing.Dir())
	c := routing.Pick(wf.Routing, routing.IssueSubject(proj, issue), "", st, now)
	worker := c.Agent
	if worker == "" {
		worker = tracker.DefaultAgent
	}
	if other := critiqueOther(st, worker, now); other != "" {
		return other, "not the worker's " + worker
	}
	return worker, "routing: " + c.Why
}

// critiqueInput gathers the paths the critic reads, relative to dir where
// they sit inside it: the issue file, its epic's, the diff file it will
// get, the changed files and the settled rules. Nothing else.
func critiqueInput(root, dir string, proj *tracker.Project, issue *tracker.Issue, issues []*tracker.Issue, to string, now time.Time) (critique.Input, string) {
	in := critique.Input{Slug: issue.Slug, From: issue.Status, To: to, Root: dir, Issue: issue.FilePath}
	if parent := tracker.ParentSlug(issue); parent != "" {
		for _, e := range issues {
			if e.Slug == parent {
				in.Epic = e.FilePath
			}
		}
	}
	if rules := filepath.Join(dir, "docs", "departments.md"); pathExists(rules) {
		in.Rules = []string{"docs/departments.md"}
	}
	in.Decisions = critiqueDecisions(proj, issue, issues, now)
	if dir == root {
		return in, ""
	}
	base, err := tracker.MainBranch(root)
	if err != nil || base == "" {
		base = "main"
	}
	head := strings.TrimSpace(critiqueGit(dir, "rev-parse", "--short", "HEAD"))
	for _, f := range strings.Split(critiqueGit(dir, "diff", "--name-only", base+"...HEAD"), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			in.Changed = append(in.Changed, f)
		}
	}
	in.Touched = critiqueTouched(proj, issues, in.Changed, now)
	if len(in.Changed) > 0 {
		in.Diff = critiqueDiffFile
		critiqueDiffBase = base
	}
	return in, head
}

// critiqueDiffFile is where the branch's diff is written, relative to the
// worktree.
const critiqueDiffFile = ".git-critique/branch.diff"

// critiqueDiffBase is the base critiqueInput diffed against.
var critiqueDiffBase = "main"

// writeCritiqueDiff writes the branch's diff to path (outside git's view:
// .git-critique is ignored by the worktree's info/exclude).
func writeCritiqueDiff(dir, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	excludeCritiqueDir(dir)
	return os.WriteFile(path, []byte(critiqueGit(dir, "diff", critiqueDiffBase+"...HEAD")), 0o644)
}

func excludeCritiqueDir(dir string) {
	gitDir := strings.TrimSpace(critiqueGit(dir, "rev-parse", "--git-common-dir"))
	if gitDir == "" {
		return
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	ex := filepath.Join(gitDir, "info", "exclude")
	data, _ := os.ReadFile(ex)
	if strings.Contains(string(data), ".git-critique/") {
		return
	}
	_ = os.MkdirAll(filepath.Dir(ex), 0o755)
	f, err := os.OpenFile(ex, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		f.WriteString("\n")
	}
	f.WriteString(".git-critique/\n")
}

func critiqueGit(dir string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// critiqueLine is show's Critique: line: the newest run's verdict and
// counts, or the command when the issue's next gate wants a critic and
// none has run from here; "" when neither applies.
func critiqueLine(issuesDir string, wf *tracker.WorkflowConfig, issue *tracker.Issue) string {
	runs, _ := critique.Load(issuesDir, issue.Slug)
	latest := critique.Latest(runs, issue.Slug, "")
	_, gate := wf.ForIssue(issue).CritiqueEdge(issue.Status)
	switch {
	case latest != nil && (!gate || strings.EqualFold(latest.From, issue.Status)):
		r := latest.Report
		return fmt.Sprintf("Critique: %s → %s %s (%d blocks, %d should, %d note) — issue-cli critique %s --show",
			latest.From, latest.To, latest.Verdict(), r.Count(critique.Blocks)+len(r.Problems), r.Count(critique.Should), r.Count(critique.Note), issue.Slug)
	case gate:
		return fmt.Sprintf("Critique: none from %s yet; the next gate wants one — issue-cli critique %s", issue.Status, issue.Slug)
	}
	return ""
}

// critiqueTouched is the hard decisions whose paths: the branch's changed
// files match (the decisions_respected audit), for the brief: refs, the
// files holding them and the matching files, never text.
func critiqueTouched(proj *tracker.Project, issues []*tracker.Issue, changed []string, now time.Time) []critique.TouchedRef {
	if len(changed) == 0 {
		return nil
	}
	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(proj))
	var out []critique.TouchedRef
	for _, t := range tracker.TouchedDecisions(index, changed, now) {
		out = append(out, critique.TouchedRef{Ref: t.Entry.Ref, File: t.Entry.Issue.FilePath, Files: t.Files})
	}
	return out
}

// critiqueDecisions is every decision in force for issue at its status
// (the critic reads them all, uncapped): refs and holder files only. It
// only reads; the critic's brief writes no injected event.
func critiqueDecisions(proj *tracker.Project, issue *tracker.Issue, issues []*tracker.Issue, now time.Time) []critique.DecisionRef {
	index := tracker.BuildDecisionIndex(issues, tracker.ClosedIssueFunc(proj))
	var dept func(*tracker.Issue) string
	if proj != nil {
		dept = proj.DepartmentOf
	}
	var out []critique.DecisionRef
	for _, f := range tracker.InForce(index, issue, issue.Status, tracker.InForceOptions{Department: dept, Today: now}) {
		out = append(out, critique.DecisionRef{Ref: f.Entry.Ref, Hard: f.Entry.Hard, Scope: f.Entry.ScopeOrDefault(), File: f.Entry.Issue.FilePath})
	}
	return out
}
