package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/proof"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var proveCommand = &Command{
	Name:      "prove",
	ShortHelp: "Link an acceptance criterion to its proof; list each criterion's proof and its check",
	LongHelp: `Link each acceptance criterion (AC1, AC2, … from issue-cli checklist) to
the proof that it works. A proof is a link to something that exists, never a
copy, and the engine checks every link each time it reads it.

Most links need no prove call: declare them where you write the proof.

  playtest step   proves: [AC2]                 on the step in the session YAML
  test            Proves: AC2 TestProveList     trailer on the commit that adds or changes it
  the change      Proves: AC2                   trailer on its commit
                  (several: "Proves: AC1, AC3 TestA TestB"; a Proves: line
                  counts in any paragraph of the message, also one before
                  Co-Authored-By; only the work branch's own commits count,
                  never commits merged from main or another issue's commits
                  the branch was started on)

prove is the fallback, and the only way to record a measured value:

  issue-cli prove <slug> <AC-id> --test <TestName>      repeatable, like every link flag
  issue-cli prove <slug> <AC-id> --step <TP3>           a step of one of the issue's own playtest
                                                        sessions (another issue's is refused)
  issue-cli prove <slug> <AC-id> --commit <sha>
  issue-cli prove <slug> <AC-id> --value "690 vs 380 (1.82×)"
  issue-cli prove <slug> <AC-id> --shot <path>          only without a playtest runner,
                                                        for a criterion ending in (visual)
  issue-cli prove <slug> <AC-id> --lead "<command>"     a command only the lead can run (the worker
                                                        guard refuses it): the criterion waits on the
                                                        lead; stop, no question: comment needed
  issue-cli prove <slug> --lead-run [<AC-id>…]          the lead: run those commands, record each run
  issue-cli prove <slug> <AC-id> --clear                removes this criterion's prove links
  issue-cli prove <slug> [--list] [--json]              every criterion, its links and checks; with no
                                                        ## Acceptance Criteria on a path that never asks
                                                        for one: "no acceptance criteria: nothing to prove"
  issue-cli prove <slug> --run [--json]                 run the tests the test links name and
                                                        post the tests: comment when all pass

Checks:
  test    a test file on the branch has the name, and a tests: comment is
          newer than the branch's last code commit (merges and commits that
          only touch docs, Markdown, CHANGELOG, playtest/sessions/,
          scripts/checks/, issues/, retros/ or sessions/ don't count)
  step    the step's latest playtest result passed
  commit  reachable from the work branch or its merge
  value   none: shown as "reported"
  shot    the file exists
  lead    the lead's newest run of the command passed, after the branch's
          last code commit; until then "waits on the lead: <command>" on
          prove --list, the gate, the Station and the lead's tick

A lead command is never run through a shell. It is
  [NAME=value …] issue-cli check-script <slug> [--yes] [--cli <path in the worktree>]
  [NAME=value …] issue-cli prove <slug> --run
on the issue's own slug; env names that change how a program runs (PATH,
HOME, LD_*, GIT_*, …) are refused.

The order for a test: commit it with its Proves: trailer, then run
prove <slug> --run after the last code commit. --run runs go test -run
for the named tests in the issue's worktree (after mem-guard check, when
installed); without a go.mod it runs the project's default-tier ci:
commands there, and each name has to be in a tracked test file. It posts
"tests: prove --run at <sha>: …" only when every named test passed; it
refuses uncommitted code. With neither, run the tests and post the tests:
comment yourself.

A criterion is proven when one of its links passes. prove, prove --list and
the transition that checks proof (criteria_proven) tick the proven boxes;
issue-cli check refuses to tick a criterion by hand while the gate covers
the issue.`,
	Run: runProve,
}

func init() {
	registerCommand(proveCommand)
}

type proveOutput struct {
	proof.Report
	Proven int      `json:"proven"`
	Total  int      `json:"total"`
	Ticked []string `json:"ticked"`
	// Overlaps are the criteria a judgement step makes the owner's while a
	// test or commit link proves them too (proof.Report.Overlaps).
	Overlaps []proof.Overlap `json:"overlaps,omitempty"`
	// Run is what prove --run ran (nil without --run).
	Run *proof.TestRun `json:"run,omitempty"`
}

func runProve(ctx *Context, args []string) error {
	slug, rest, err := issueSlug(ctx, args, "prove")
	if err != nil {
		return err
	}
	fs := newFlagSet("prove", ctx)
	var links []tracker.ProofLink
	for _, kind := range []string{tracker.ProofTest, tracker.ProofStep, tracker.ProofCommit, tracker.ProofValue, tracker.ProofShot, tracker.ProofLead} {
		kind := kind
		fs.Func(kind, "repeatable: link the criterion to this "+kind, func(v string) error {
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("--%s needs a value", kind)
			}
			links = append(links, tracker.ProofLink{Kind: kind, Ref: strings.TrimSpace(v)})
			return nil
		})
	}
	clearFlag := fs.Bool("clear", false, "remove this criterion's prove links")
	runFlag := fs.Bool("run", false, "run the tests the test links name and post the tests: comment when all pass")
	leadRunFlag := fs.Bool("lead-run", false, "the lead: run the lead links' commands and record each run")
	fs.Bool("list", false, "list every criterion with its links and checks (the default without a criterion)")
	if err := parseFlagsArgs(ctx, fs, rest); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return unexpectedArgError(fs, fs.Arg(1))
	}

	issue, _, err := findIssueOrErr(ctx, slug)
	if err != nil {
		return err
	}
	criteria := tracker.Criteria(issue.BodyRaw)
	if len(criteria) == 0 {
		// replace refuses a missing section; append --section creates it.
		verb := "replace"
		if !tracker.HasSection(issue.BodyRaw, tracker.CriteriaSection) {
			verb = "append"
			if fs.NArg() == 0 && !*runFlag && !*leadRunFlag && !*clearFlag && len(links) == 0 {
				return proveNoCriteria(ctx, issue)
			}
		}
		return fmt.Errorf("%s has no checkboxes under ## %s — write each criterion as a checkbox first:\n  issue-cli %s %s --section %s --body \"- [ ] ...\"", issue.Slug, tracker.CriteriaSection, verb, issue.Slug, shell.Quote(tracker.CriteriaSection))
	}

	if *leadRunFlag {
		if *runFlag || *clearFlag || len(links) > 0 {
			return fmt.Errorf("--lead-run runs the lead links' commands: use it alone, issue-cli prove %s --lead-run [<AC-id>]", issue.Slug)
		}
		return runProveLead(ctx, issue, criteria, fs.Args())
	}

	if *runFlag {
		if fs.NArg() == 1 || *clearFlag || len(links) > 0 {
			return fmt.Errorf("--run runs every test link of the issue: use it alone, issue-cli prove %s --run", issue.Slug)
		}
		run, err := runProveTests(ctx, issue)
		if err != nil {
			return err
		}
		if err := printProof(ctx, issue, run); err != nil {
			return err
		}
		if !run.Posted {
			return fmt.Errorf("prove --run: no tests: comment posted, not every named test passed")
		}
		return nil
	}

	if fs.NArg() == 1 {
		id := fs.Arg(0)
		it, st := tracker.ResolveCheckboxRef(criteria, id)
		if st != tracker.RefFound {
			return fmt.Errorf("%s is not an acceptance criterion of %s; criteria: %s", id, issue.Slug, criteriaIDs(criteria))
		}
		switch {
		case *clearFlag && len(links) > 0:
			return fmt.Errorf("--clear removes links; don't combine it with link flags")
		case !*clearFlag && len(links) == 0:
			return fmt.Errorf("say where the proof is\n\nExamples:\n  issue-cli prove %s %s --test TestName\n  issue-cli prove %s %s --step TP3\n  issue-cli prove %s %s --value \"690 vs 380\"", issue.Slug, it.ID, issue.Slug, it.ID, issue.Slug, it.ID)
		}
		if err := saveProofLinks(ctx, issue, it, links, *clearFlag); err != nil {
			return err
		}
		for _, l := range links {
			if l.Kind == tracker.ProofLead && !ctx.JSONOutput {
				fmt.Fprintf(ctx.Stdout, "%s waits on the lead: %s\nIt shows on the lead's tick (issue-cli queue next) and the Station. Stop here: no question: comment needed. The lead runs: issue-cli prove %s --lead-run\n", it.ID, l.Ref, issue.Slug)
			}
		}
	} else if *clearFlag || len(links) > 0 {
		return fmt.Errorf("name the criterion: issue-cli prove %s <AC-id> ... (criteria: %s)", issue.Slug, criteriaIDs(criteria))
	}

	return printProof(ctx, issue, nil)
}

func criteriaIDs(items []tracker.CheckboxItem) string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return strings.Join(ids, ", ")
}

// saveProofLinks adds links to criterion it (or clears its links) in the
// data sidecar, refusing links that can't be right.
func saveProofLinks(ctx *Context, issue *tracker.Issue, it tracker.CheckboxItem, links []tracker.ProofLink, clear bool) error {
	root := projectRoot(ctx.Project)
	stats, _ := tracker.LoadStats(issue.FilePath)
	worktree := proof.FindIssueRef(root, issue, stats.Transitions).Worktree
	for i, l := range links {
		switch l.Kind {
		case tracker.ProofStep:
			if !tracker.IsCheckboxRefShaped(l.Ref) {
				return fmt.Errorf("--step %q is not a step id: use the session step's id (a checkbox id such as TP3)", l.Ref)
			}
			if err := stepInOwnSession(root, worktree, issue.Slug, it.ID, l.Ref); err != nil {
				return err
			}
		case tracker.ProofCommit:
			full, err := proof.ResolveCommit(root, l.Ref)
			if err != nil {
				return fmt.Errorf("--commit: %w", err)
			}
			links[i].Ref = full
		case tracker.ProofLead:
			if _, err := proof.ParseLeadCommand(issue.Slug, l.Ref); err != nil {
				return fmt.Errorf("--lead: %w", err)
			}
		case tracker.ProofShot:
			if hasPlaytestRunner(root, ctx.Project.LoadWorkflow()) {
				return fmt.Errorf("--shot is refused: this project has a playtest runner, so its screenshots come from playtest runs. Add `proves: [%s]` to the step that shows it", it.ID)
			}
			if !tracker.IsVisualCriterion(it.Text) {
				return fmt.Errorf("--shot is refused: %s is not marked visual. Only a criterion ending in \"(visual)\" takes a screenshot:\n  issue-cli check-edit %s %s --text %s", it.ID, issue.Slug, it.ID, shell.Quote(it.Text+" (visual)"))
			}
			if !proof.InsideCheckout(root, worktree, l.Ref) {
				return fmt.Errorf("--shot %s: the file must be in the issue's worktree or the project checkout", l.Ref)
			}
		}
		links[i].At = ctx.now().UTC()
		links[i].Criterion = it.Text
	}

	store, err := tracker.LoadData(issue.FilePath)
	if err != nil {
		return err
	}
	if store.Proofs == nil {
		store.Proofs = map[string][]tracker.ProofLink{}
	}
	id := strings.ToUpper(it.ID)
	if clear {
		delete(store.Proofs, id)
	} else {
		existing := store.Proofs[id]
		for _, l := range links {
			if l.Kind == tracker.ProofShot {
				// One hand-picked shot per criterion: a new one replaces it.
				kept := existing[:0]
				for _, e := range existing {
					if e.Kind != tracker.ProofShot {
						kept = append(kept, e)
					}
				}
				existing = kept
			}
			dup := false
			for _, e := range existing {
				if e.Kind == l.Kind && e.Ref == l.Ref {
					dup = true
					break
				}
			}
			if !dup {
				existing = append(existing, l)
			}
		}
		store.Proofs[id] = existing
	}
	if len(store.Proofs) == 0 {
		store.Proofs = nil
	}
	return tracker.SaveData(issue.FilePath, store)
}

// stepInOwnSession refuses a step id that only another issue's sessions
// have: proof reads the runs of the issue's own sessions only, so such a
// link would stay "not run". An id in no session file yet is let through:
// the session may be written later.
func stepInOwnSession(root, worktree, slug, criterion, step string) error {
	id := strings.ToUpper(strings.TrimSpace(step))
	has := func(s tracker.PlaytestSessionFile) bool {
		for _, st := range s.Steps {
			if strings.ToUpper(strings.TrimSpace(st.ID)) == id {
				return true
			}
		}
		return false
	}
	for _, s := range tracker.IssuePlaytestSessions(root, slug) {
		if has(s) {
			return nil
		}
	}
	var where []string
	seen := map[string]bool{}
	for _, dir := range []string{worktree, root} {
		for _, s := range tracker.PlaytestSessionFiles(dir) {
			if seen[s.Session] || strings.TrimSpace(s.Issue) == slug || !has(s) {
				continue
			}
			seen[s.Session] = true
			issue := strings.TrimSpace(s.Issue)
			if issue == "" {
				issue = "none"
			}
			where = append(where, "session "+s.Session+" (issue "+issue+")")
		}
	}
	if len(where) == 0 {
		return nil
	}
	return fmt.Errorf("--step %s is a step of %s, not of a session of %s: a step link resolves from the issue's own sessions only, so it would stay \"not run\".\nProve it with a step of this issue's own session (proves: [%s] on that step), or record the other session's result as a value:\n  issue-cli prove %s %s --value %s",
		id, strings.Join(where, ", "), slug, strings.ToUpper(criterion), slug, criterion, shell.Quote(id+" passed in "+strings.TrimPrefix(where[0], "session ")))
}

func printProof(ctx *Context, issue *tracker.Issue, run *proof.TestRun) error {
	comments, _ := tracker.LoadComments(issue.FilePath)
	report := proof.Collect(projectRoot(ctx.Project), issue, comments)
	ticked, err := proof.TickProven(issue.FilePath, report)
	if err != nil {
		return fmt.Errorf("ticking proven criteria: %w", err)
	}
	for i := range report.Criteria {
		for _, id := range ticked {
			if report.Criteria[i].ID == id {
				report.Criteria[i].Checked = true
			}
		}
	}
	out := proveOutput{Report: report, Total: len(report.Criteria), Ticked: ticked, Run: run, Overlaps: report.Overlaps()}
	out.Proven = out.Total - len(report.Unproven())
	if out.Ticked == nil {
		out.Ticked = []string{}
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, out)
	}
	if run != nil {
		writeTestRun(ctx.Stdout, issue.Slug, run)
	}
	writeProofReport(ctx.Stdout, out)
	wf := ctx.Project.LoadWorkflowForIssue(issue)
	if _, ok := wf.ProofGateSince(); ok && !wf.ProofRequired(issue) {
		fmt.Fprintln(ctx.Stdout, "The proof gate doesn't cover this issue (created before the gate's date): links are shown, not required.")
	}
	fmt.Fprintf(ctx.Stdout, "Evidence (with the playtest shots): %s?tab=evidence\n", issueURL(ctx.Project, issue.Slug))
	fmt.Fprintf(ctx.Stdout, "file: %s\n", issue.FilePath)
	return nil
}

func writeProofReport(w io.Writer, out proveOutput) {
	fmt.Fprintf(w, "== Proof: %s ==\n", out.Slug)
	if out.RefName != "" {
		fmt.Fprintf(w, "Branch: %s\n", out.RefName)
	}
	for _, m := range out.Missing {
		fmt.Fprintf(w, "Not read: %s\n", m)
	}
	for _, c := range out.Criteria {
		box, state := "[ ]", "UNPROVEN"
		if c.Checked {
			box = "[x]"
		}
		if c.Proven() {
			state = "proven"
		}
		fmt.Fprintf(w, "%s %s %s  %s\n", c.ID, box, state, c.Text)
		if c.Owner != "" && !c.Checked && !c.OwnerProven() {
			fmt.Fprintf(w, "    owner check (%s): only the owner ticks it; no trailer, test or value does\n", c.Owner)
		}
		for _, l := range c.Links {
			mark := "✗"
			if l.Check.OK {
				mark = "✓"
			}
			fmt.Fprintf(w, "    %s %s — %s\n", mark, proof.Describe(l.Link), l.Check.State)
			if !l.Check.OK && l.Check.Fix != "" {
				fmt.Fprintf(w, "      → %s\n", l.Check.Fix)
			}
		}
	}
	if unproven := out.Unproven(); len(unproven) > 0 {
		fmt.Fprintf(w, "Prove each UNPROVEN criterion with any of (shown for %s):\n", unproven[0].ID)
		for _, way := range proof.Ways(out.Slug, unproven[0].ID) {
			fmt.Fprintf(w, "  - %s\n", way)
		}
	}
	for _, o := range out.Overlaps {
		fmt.Fprintf(w, "Warning: %s\n", o.Line())
	}
	for _, s := range out.Strays {
		fmt.Fprintf(w, "Unknown criterion %s: %s — the issue has no such box; fix the trailer or proves: line\n", s.ID, proof.Describe(s.Link))
	}
	for _, id := range out.Ticked {
		fmt.Fprintf(w, "✓ Ticked %s (proven)\n", id)
	}
	fmt.Fprintf(w, "%d/%d criteria proven\n", out.Proven, out.Total)
}

// hasPlaytestRunner reports whether the project's screenshots come from
// playtest runs: the playtest-sessions feature is installed and the project
// has a runner to back it (playtest_command, a session under
// playtest/sessions/, or a run under .playtest/runs/). The feature alone
// comes with the template, and a lead's notes in sessions/ are no runner.
func hasPlaytestRunner(root string, wf *tracker.WorkflowConfig) bool {
	if wf == nil || !wf.HasFeature("playtest-sessions") {
		return false
	}
	if strings.TrimSpace(wf.PlaytestCommand) != "" {
		return true
	}
	if files, _ := filepath.Glob(filepath.Join(root, "playtest", "sessions", "*.yaml")); len(files) > 0 {
		return true
	}
	runs, _ := os.ReadDir(filepath.Join(root, ".playtest", "runs"))
	return len(runs) > 0
}
