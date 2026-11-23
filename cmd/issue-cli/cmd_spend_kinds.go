package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kacperwerema/dark-factory/internal/period"
	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// spendWindow is the window spend --kinds and --turns read: --since and
// --until (a date or an RFC 3339 time), else the last --days calendar days.
type spendWindow struct {
	since, until time.Time
}

// parseSpendTime reads a date (its midnight; for until the next one, so the
// day is in) or an RFC 3339 time.
func parseSpendTime(s string, end bool, loc *time.Location) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		if end {
			t = t.AddDate(0, 0, 1)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%q is not a date (2026-11-11) or a time (2026-11-11T20:00:00Z)", s)
}

func spendWindowOf(now time.Time, days int, daysGiven bool, since, until string) (spendWindow, error) {
	w := spendWindow{until: now, since: period.DaysBack(now, days)}
	if since != "" && daysGiven {
		return w, fmt.Errorf("--days and --since both set the window's start: give one")
	}
	if since != "" {
		t, err := parseSpendTime(since, false, now.Location())
		if err != nil {
			return w, fmt.Errorf("--since: %w", err)
		}
		w.since = t
	}
	if until != "" {
		t, err := parseSpendTime(until, true, now.Location())
		if err != nil {
			return w, fmt.Errorf("--until: %w", err)
		}
		w.until = t
	}
	if !w.since.Before(w.until) {
		return w, fmt.Errorf("the window is empty: --since %s is not before --until %s", w.since.Format(time.RFC3339), w.until.Format(time.RFC3339))
	}
	return w, nil
}

// loadKinds is spend --kinds' report; a seam for tests.
var loadKinds = func(ctx *Context, w spendWindow, all bool, pricesPath string) *spend.KindsReport {
	opt := spend.KindsOptions{Issues: map[string][]*tracker.Issue{}}
	projects := []*tracker.Project{ctx.Project}
	if all {
		projects = nil
		for i := range ctx.AllProjects {
			projects = append(projects, &ctx.AllProjects[i])
		}
		if len(projects) == 0 {
			projects = []*tracker.Project{ctx.Project}
		}
		opt.Others = true
	}
	for _, p := range projects {
		issues, _ := tracker.LoadIssues(p.IssueDir)
		opt.Issues[p.Slug] = issues
	}
	opt.Projects = projects
	if pricesPath != "" {
		b := spend.LoadPriceBook(pricesPath)
		opt.Book = &b
	}
	return spend.LoadKinds(w.since, w.until, opt)
}

// loadTurns is spend --turns' report; a seam for tests.
var loadTurns = func(ctx *Context, w spendWindow, slug string) *spend.TurnsReport {
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	return spend.LoadTurns(ctx.Project, issues, slug, w.since, w.until, spend.Options{})
}

func windowText(since, until time.Time) string {
	days := until.Sub(since).Hours() / 24
	return fmt.Sprintf("%s → %s (%s days)", since.Format("2006-01-02 15:04"), until.Format("2006-01-02 15:04"), strconv.FormatFloat(days, 'f', 1, 64))
}

func pct(v float64) string { return strconv.FormatFloat(v*100, 'f', 1, 64) + "%" }

func runSpendKinds(ctx *Context, w spendWindow, all bool, pricesPath string) error {
	rep := loadKinds(ctx, w, all, pricesPath)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, rep)
	}
	out := ctx.Stdout
	fmt.Fprintf(out, "Spend by project and kind of session, %s; Claude Code transcripts and benchmark runs on this machine, estimates\n", windowText(rep.Since, rep.Until))
	if rep.Total.Turns == 0 {
		fmt.Fprintln(out, "Nothing counted: no Claude Code transcript of these projects' sessions in the window.")
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	head := "PROJECT\tKIND\tSESSIONS\tTURNS\tIN+CACHE\tOUTPUT\tEST. USD\tSHARE\t% WEEK\t"
	fmt.Fprintln(out)
	fmt.Fprintln(tw, head)
	for _, p := range rep.ByProject {
		kindRow(tw, p, "(all)", rep)
		for _, r := range rep.Rows {
			if r.Project == p.Project {
				kindRow(tw, r, r.Label(), rep)
			}
		}
	}
	kindRow(tw, rep.Total, "", rep)
	tw.Flush()
	if len(rep.Workers) > 0 {
		fmt.Fprintln(out, "\nWorkers by type and stage (the issue's status when the turn ran):")
		tw = tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, head)
		for _, r := range rep.Workers {
			kindRow(tw, r, r.Label(), rep)
		}
		tw.Flush()
	}
	printKindsPrices(out, rep)
	fmt.Fprintln(out, "\nNot counted: Codex sessions (issue-cli spend shows them per project). Share is of the estimated USD"+map[bool]string{true: "", false: " (tokens: nothing is priced)"}[rep.ShareOf == "usd"]+".")
	fmt.Fprintln(out, "Turns by cause per issue: issue-cli spend --turns. Before and after a change: issue-cli spend --kinds --json --since <T0> --until <T1> (docs/workflow-stats.md, \"Measure before and after\").")
	return nil
}

func kindRow(tw io.Writer, r spend.KindRow, label string, rep *spend.KindsReport) {
	project := r.Project
	if label == "" {
		project, label = "all", "(all)"
	}
	in := r.Input + r.CacheRead + r.CacheWrite
	week := "—"
	if r.LimitShare != nil {
		week = pct(*r.LimitShare)
	}
	fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t\n", project, label, r.Sessions, r.Turns,
		tokens.Format(in), tokens.Format(r.Output), kindCost(r.Tally), pct(r.Share), week)
}

// kindCost is a row's estimated USD: "$12.40", "≥ $12.40" with unpriced
// tokens, "?" when none is priced.
func kindCost(t spend.Tally) string {
	switch {
	case t.Empty():
		return "$0.00"
	case !t.Priced():
		return "?"
	case !t.CostKnown():
		return "≥ " + spend.FormatUSD(t.USD)
	}
	return spend.FormatUSD(t.USD)
}

// printKindsPrices says each model's price and whether it is confirmed,
// and the weekly limit or what to set for one.
func printKindsPrices(out io.Writer, rep *spend.KindsReport) {
	fmt.Fprintf(out, "\nPrices (USD per million tokens; %s):\n", rep.PricesPath)
	if rep.PricesErr != "" {
		fmt.Fprintf(out, "  %s does not parse (%s): no price read from it\n", rep.PricesPath, rep.PricesErr)
	}
	assumed := false
	for _, p := range rep.Prices {
		if p.Status == "none" {
			fmt.Fprintf(out, "  %-22s no price: cost unknown\n", p.Model)
			continue
		}
		key := ""
		if p.Key != p.Model {
			key = " (as " + p.Key + ")"
		}
		fmt.Fprintf(out, "  %-22s in %s · out %s · cache read %s · cache write %s  %s, from %s%s\n", p.Model,
			num(p.In), num(p.Out), num(p.CacheRead), num(p.CacheWrite), p.Status, p.Source, key)
		assumed = assumed || p.Status == "assumed"
	}
	if len(rep.Prices) == 0 {
		fmt.Fprintln(out, "  no model seen in the window")
	}
	if len(rep.Unpriced) > 0 {
		fmt.Fprintf(out, "  add the missing ones to %s:\n%s\n", rep.PricesPath, spendIndent(spend.PricesSnippet))
	}
	if assumed {
		fmt.Fprintln(out, "  assumed: not checked against the provider's list; add confirmed: true to an entry once it is")
	}
	if rep.WeeklyLimit != nil {
		fmt.Fprintf(out, "Weekly limit: %s (%s); %% WEEK is each row against it, pro rata for the window's days.\n", rep.WeeklyLimit.Text(), rep.PricesPath)
	} else {
		fmt.Fprintf(out, "Weekly limit: not set, so %% WEEK shows —. Add to %s:\n  %s\n", rep.PricesPath, spend.LimitSnippet)
	}
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func spendIndent(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

func runSpendTurns(ctx *Context, w spendWindow, slug string) error {
	rep := loadTurns(ctx, w, slug)
	if ctx.JSONOutput {
		return writeJSON(ctx.Stdout, rep)
	}
	out := ctx.Stdout
	fmt.Fprintf(out, "Worker turns by cause, %s; each turn re-reads the whole context\n", windowText(rep.Since, rep.Until))
	if len(rep.Issues) == 0 {
		fmt.Fprintln(out, "No worker turns in the window.")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(out)
	fmt.Fprintln(tw, "ISSUE\tSESSIONS\tTURNS\tTRANSITION (REFUSED)\tVALIDATE\tREAD\tRED ROUND\tREST\tRED ROUNDS\tDRY RUNS\tFOLLOW-ON READS\tFOLLOW-ON READ-ONLY\tCONTEXT READ\t")
	row := func(it spend.IssueTurns) {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d (%d)\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t\n", clipSlug(it.Slug), it.Sessions, it.Turns,
			it.By[spend.CauseTransition].Turns, it.Refused, it.By[spend.CauseValidate].Turns, it.By[spend.CauseRead].Turns,
			it.By[spend.CauseRedRound].Turns, it.By[spend.CauseRest].Turns, it.RedRounds, it.DryRuns, it.FollowOnReads, it.FollowOnReadOnly, tokens.Format(it.Context))
	}
	limit := len(rep.Issues)
	if slug == "" && limit > 20 {
		limit = 20
	}
	for _, it := range rep.Issues[:limit] {
		row(it)
	}
	if len(rep.Issues) > 1 {
		row(rep.Total)
	}
	tw.Flush()
	if limit < len(rep.Issues) {
		fmt.Fprintf(out, "(top %d of %d issues by turns; --json lists every one)\n", limit, len(rep.Issues))
	}
	t := rep.Total
	if t.Turns > 0 {
		var parts []string
		for _, c := range spend.Causes {
			n := t.By[c]
			parts = append(parts, fmt.Sprintf("%s %s of turns, %s read", strings.ReplaceAll(c, "_", " "), pct(float64(n.Turns)/float64(t.Turns)), tokens.Format(n.Context)))
		}
		fmt.Fprintf(out, "\nAll issues: %s.\n", strings.Join(parts, "; "))
		n := float64(len(rep.Issues))
		fmt.Fprintf(out, "Turns a batch would save: %d dry runs (%.2f per issue), %d follow-on reads (%.2f per issue), %d follow-on read-only turns (%s of turns, %s read).\n",
			t.DryRuns, float64(t.DryRuns)/n, t.FollowOnReads, float64(t.FollowOnReads)/n,
			t.FollowOnReadOnly, pct(float64(t.FollowOnReadOnly)/float64(t.Turns)), tokens.Format(t.FollowOnReadOnlyContext))
	}
	fmt.Fprintln(out, "Causes: transition = an issue-cli transition call (refused: its result was an error); validate = make validate or reading its log; read = one Read, or one cat/head/tail/nl/sed -n of one file; red round = any other turn while a check of the issue's rounds or the session's make validate is red; rest = the others; dry runs = transition calls with --dry-run; follow-on reads = read turns straight after another read turn; follow-on read-only = a turn of one read-only call (Read, Grep, Glob, or a shell call of grep/ls/sed -n/cat, read-only git or issue-cli) straight after another. Only counts are kept: no command text.")
	return nil
}

// clipSlug keeps a table's slug column to 70 characters.
func clipSlug(s string) string {
	if r := []rune(s); len(r) > 70 {
		return string(r[:69]) + "…"
	}
	return s
}
