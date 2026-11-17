package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/lint"
)

// lintWarnCap is how many findings per check the text output shows
// without --all.
const lintWarnCap = 20

// runHealthLint is issue-cli health lint: the lint checks over workflow.yaml
// and the issues, each finding with its fix, then the history lines (records of a removed
// department, counted nowhere). Every finding is a warning, so
// it exits 0 unless the usage is wrong.
func runHealthLint(ctx *Context, args []string) error {
	fs := newFlagSet("health lint", ctx)
	checkFlag := fs.String("check", "", "comma-separated checks to run (default: all; see --list)")
	listFlag := fs.Bool("list", false, "list the checks and exit")
	allFlag := fs.Bool("all", false, "print every finding (default: the first 20 per check)")
	fixFlag := fs.Bool("fix", false, "fix what can be fixed mechanically (with --check shas: swap each stale SHA for the one landed commit of the same subject)")
	wfFlag := fs.String("workflow", "", "lint this candidate workflow.yaml instead of the live one and print only the warnings it adds or removes")
	projFlag := fs.String("projects", "", "lint the project from this candidate projects.yaml (jobs, goals, departments) and print only the warnings it adds or removes")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	if *listFlag {
		for _, c := range lint.Checks() {
			fmt.Fprintf(ctx.Stdout, "  %-13s %s\n", c.Name, c.Help)
		}
		return nil
	}
	var names []string
	for _, n := range strings.Split(*checkFlag, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	for _, n := range names {
		if !slices.Contains(lint.Names(), n) {
			return &usageError{msg: fmt.Sprintf("unknown check %q (checks: %s)", n, strings.Join(lint.Names(), ", "))}
		}
	}

	if *fixFlag && (len(names) != 1 || !slices.Contains(lintFixable, names[0])) {
		return &usageError{msg: fmt.Sprintf("--fix takes one check it can fix: --check %s", strings.Join(lintFixable, "|"))}
	}

	cand := lint.Candidate{Workflow: strings.TrimSpace(*wfFlag), Projects: strings.TrimSpace(*projFlag)}
	candidate := cand.Workflow != "" || cand.Projects != ""
	if *fixFlag && candidate {
		return &usageError{msg: "--fix writes the live board: run it without --workflow/--projects"}
	}

	env, err := lint.Load(ctx.Project, ctx.now())
	if err != nil {
		return err
	}
	if candidate {
		d, err := lint.RunCandidate(env, cand, names...)
		if err != nil {
			return err
		}
		if ctx.JSONOutput {
			return writeJSON(ctx.Stdout, d)
		}
		printLintDelta(ctx.Stdout, d)
		return nil
	}
	if *fixFlag {
		return runHealthLintFixSHAs(ctx, env)
	}
	rep, err := lint.Run(env, names...)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, rep)
	}
	printLintReport(ctx.Stdout, rep, *allFlag)
	return nil
}

// lintFixable are the checks health lint --fix fixes.
var lintFixable = []string{"shas"}

// runHealthLintFixSHAs is health lint --check shas --fix: the swaps it made,
// the refs it kept and why, then the check's count on the board it wrote.
func runHealthLintFixSHAs(ctx *Context, env *lint.Env) error {
	fix, err := lint.FixSHAs(env)
	if err != nil {
		return err
	}
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, fix)
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "== health lint --fix: %s (shas) ==\n", ctx.Project.Name)
	for _, s := range fix.Swaps {
		fmt.Fprintf(w, "✓ %s: %s → %s  %s\n", s.Slug, s.Old, s.New, s.Subject)
	}
	for _, k := range fix.Kept {
		fmt.Fprintf(w, "· %s: %s kept: %s\n", k.Slug, k.Ref, k.Reason)
		if k.Subject != "" {
			fmt.Fprintf(w, "  subject: %s\n", k.Subject)
		}
		if k.Hint != "" {
			fmt.Fprintf(w, "  hint: %s\n", k.Hint)
		}
	}
	fmt.Fprintf(w, "\n%s swapped in %s, %s kept\n", lintCount(len(fix.Swaps), "ref"), lintCount(fix.Issues, "issue"), lintCount(len(fix.Kept), "ref"))
	after, err := lint.Load(ctx.Project, ctx.now())
	if err != nil {
		return err
	}
	rep, err := lint.Run(after, "shas")
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "health lint --check shas now: %s\n", lintCount(rep.Warnings, "warning"))
	return nil
}

func printLintReport(w io.Writer, rep *lint.Report, all bool) {
	fmt.Fprintf(w, "== health lint: %s (%s) ==\n", rep.Project, strings.Join(rep.Checks, ", "))
	defer printLintHistory(w, rep)
	defer printLintNotes(w, rep)
	if len(rep.Findings) == 0 {
		fmt.Fprintln(w, "✓ no warnings")
		return
	}
	shown := map[string]int{}
	hidden := map[string]int{}
	for _, f := range rep.Findings {
		shown[f.Check]++
		if !all && shown[f.Check] > lintWarnCap {
			hidden[f.Check]++
			continue
		}
		fmt.Fprintf(w, "%s %s: %s\n", f.Check, f.Where, f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		}
	}
	for _, c := range rep.Checks {
		if n := hidden[c]; n > 0 {
			fmt.Fprintf(w, "… %d more %s warnings: issue-cli health lint --check %s --all\n", n, c, c)
		}
	}
	fmt.Fprintf(w, "\n%s; the health score's lint component counts them (/health lists them)\n", lintCount(rep.Warnings, "warning"))
}

// printLintDelta prints a candidate run: each warning the candidate adds
// (+) and each it removes (-) against the live files, then the counts.
func printLintDelta(w io.Writer, d *lint.Delta) {
	var files []string
	if d.Workflow != "" {
		files = append(files, "--workflow "+d.Workflow)
	}
	if d.Projects != "" {
		files = append(files, "--projects "+d.Projects)
	}
	fmt.Fprintf(w, "== health lint %s: %s (%s) ==\n", strings.Join(files, " "), d.Project, strings.Join(d.Checks, ", "))
	if len(d.Added) == 0 && len(d.Removed) == 0 {
		fmt.Fprintln(w, "✓ the candidate adds and removes no warnings")
	}
	for _, f := range d.Added {
		fmt.Fprintf(w, "+ %s %s: %s\n", f.Check, f.Where, f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		}
	}
	for _, f := range d.Removed {
		fmt.Fprintf(w, "- %s %s: %s\n", f.Check, f.Where, f.Message)
	}
	fmt.Fprintf(w, "\n%d added, %d removed against the live files (live %s, candidate %s)\n",
		len(d.Added), len(d.Removed), lintCount(d.LiveWarnings, "warning"), lintCount(d.CandidateWarnings, "warning"))
}

// printLintHistory prints the report's history lines: records of what the
// project removed on purpose, which count as no warning.
func printLintHistory(w io.Writer, rep *lint.Report) {
	if len(rep.History) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s, kept as history (no warning, no score):\n", lintCount(len(rep.History), "info line"))
	for _, f := range rep.History {
		fmt.Fprintf(w, "· %s %s: %s\n", f.Check, f.Where, f.Message)
	}
}

// printLintNotes prints the report's notes (lint.Finding.Note): info
// lines with their fix, counted as no warning.
func printLintNotes(w io.Writer, rep *lint.Report) {
	if len(rep.Notes) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s (no warning, no score):\n", lintCount(len(rep.Notes), "note"))
	for _, f := range rep.Notes {
		fmt.Fprintf(w, "· %s %s: %s\n", f.Check, f.Where, f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		}
	}
}

func lintCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
