package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var spendCommand = &Command{
	Name:      "spend",
	ShortHelp: "What HQ, Product and each department spent: tokens and estimated cost by week",
	LongHelp: `Print what each box of the project spent in the last 4 weeks (weeks
from Monday): tokens and an estimated cost, its top 5 issues, and its
budget as "x of y" when projects.yaml gives it one. A group and HQ
include what rolls up into them.

The numbers come from Claude Code's transcripts on this machine
(~/.claude/projects): a session in an issue's worktree counts for the
issue's department; in the project folder, the CEO's counts for HQ, a
department lead's for its department, and a session with no agent name
(your own) is unattributed. Only numbers are read; nothing leaves the
machine.

Cost is an estimate from the prices in
$XDG_CONFIG_HOME/ai-workflow-engine/prices.yaml (USD per million tokens;
the benchmark files' prices fill models it doesn't name). The engine
bundles no prices: without one a model's cost is unknown.

A budget (budget: {usd: 50, period: week} on a department in
projects.yaml) is shown only: it never blocks or pauses anything.

Pass hq, product or a department to print only that box.

Examples:
  issue-cli spend
  issue-cli spend marketing
  issue-cli spend --json`,
	Run: runSpend,
}

func init() { registerCommand(spendCommand) }

// loadSpend is the project's spend now; a seam for tests.
var loadSpend = func(ctx *Context) *spend.Report {
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	return spend.Load(ctx.Project, issues, ctx.now(), spend.Options{})
}

func runSpend(ctx *Context, args []string) error {
	pos, err := parsePositionals(ctx, "spend", args, 1)
	if err != nil {
		return err
	}
	rep := loadSpend(ctx)
	boxes := rep.Boxes
	if len(pos) == 1 {
		b := rep.Find(pos[0])
		if b == nil {
			var keys []string
			for _, b := range rep.Boxes {
				keys = append(keys, b.Key)
			}
			return fmt.Errorf("no box %q (boxes: %s)", pos[0], strings.Join(keys, ", "))
		}
		boxes = []*spend.Box{b}
	}
	if ctx.JSONOutput {
		if len(pos) == 1 {
			return writeJSON(ctx.Stdout, boxes[0])
		}
		return writeJSON(ctx.Stdout, rep)
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "Spend by week since %s (estimates; Claude Code transcripts on this machine)\n", rep.Boxes[0].Weeks[len(rep.Boxes[0].Weeks)-1].Start)
	if rep.Empty() {
		fmt.Fprintf(w, "No spend recorded yet: no Claude Code transcript of this project's sessions in the last %d weeks.\n", spend.Weeks)
	}
	for _, b := range boxes {
		fmt.Fprintln(w)
		printSpendBox(w, b)
	}
	if len(pos) == 0 {
		if t := sumWeeks(rep.Unattributed); !t.Empty() {
			fmt.Fprintf(w, "\nUnattributed (sessions in the project folder with no agent name, yours): %s over %d weeks\n", spend.Line(t), spend.Weeks)
		}
		printSubscriptions(w, rep)
	}
	printPricesHint(w, rep)
	return nil
}

// printSubscriptions is the split by agent CLI: this week and the weeks
// shown, or why a CLI's usage couldn't be read.
func printSubscriptions(w io.Writer, rep *spend.Report) {
	if len(rep.Subscriptions) == 0 {
		return
	}
	fmt.Fprintln(w, "\nBy subscription (this week · last 4 weeks):")
	for _, s := range rep.Subscriptions {
		switch {
		case !s.Read && s.Agent == "codex":
			fmt.Fprintf(w, "  %-7s no Codex session logs on this machine ($CODEX_HOME/sessions)\n", s.Agent)
		case !s.Read:
			fmt.Fprintf(w, "  %-7s no transcripts of this project\n", s.Agent)
		default:
			fmt.Fprintf(w, "  %-7s %s · %s\n", s.Agent, spend.Line(s.ThisWeek()), spend.Line(s.Total()))
		}
	}
}

func sumWeeks(ws []spend.Week) spend.Tally {
	var t spend.Tally
	for _, w := range ws {
		t.Add(w.Tally)
	}
	return t
}

// printSpendBox is one box: heading, budget, weeks, top issues.
func printSpendBox(w io.Writer, b *spend.Box) {
	head := "== " + b.Name
	if b.Parent != "" {
		head += " (in group " + b.Parent + ")"
	}
	if len(b.RolledFrom) > 0 {
		head += " · incl. " + strings.Join(b.RolledFrom, ", ")
	}
	fmt.Fprintln(w, head+" ==")
	if b.Budget != nil {
		fmt.Fprintf(w, "Budget: %s%s\n", b.Budget.Text, overText(b.Budget))
	}
	for i, wk := range b.Weeks {
		label := "week of " + wk.Start
		if i == 0 {
			label = "this week (" + wk.Start + ")"
		}
		fmt.Fprintf(w, "  %-24s %s\n", label, spend.Line(wk.Tally))
	}
	if len(b.Top) > 0 {
		fmt.Fprintf(w, "  top issues (%d weeks):\n", spend.Weeks)
		for _, is := range b.Top {
			fmt.Fprintf(w, "    %s  %s\n", is.Slug, spend.Line(is.Tally))
		}
	}
}

func overText(b *spend.BudgetState) string {
	if b.Over {
		return " · over (shown only, nothing is blocked)"
	}
	return ""
}

// printPricesHint names the prices file when some tokens have no price.
func printPricesHint(w io.Writer, rep *spend.Report) {
	if len(rep.Unpriced) == 0 {
		return
	}
	fmt.Fprintf(w, "\nNo price for %s: cost is unknown for their tokens. Add them to %s:\n%s\n",
		strings.Join(rep.Unpriced, ", "), rep.PricesPath, spend.PricesSnippet)
}

// spendLine is department show's Spend: line for box key; "" when the
// project has no such box.
func spendLine(rep *spend.Report, key string) string {
	b := rep.Find(key)
	if b == nil {
		return ""
	}
	var weeks []string
	for _, wk := range b.Weeks {
		weeks = append(weeks, spend.FormatTokens(wk.Tokens()))
	}
	line := fmt.Sprintf("Spend: %s this week · %d weeks: %s", spend.Line(b.ThisWeek()), spend.Weeks, strings.Join(weeks, " / "))
	if b.Budget != nil {
		line += " · budget " + b.Budget.Text + overText(b.Budget)
	}
	return line + " (issue-cli spend " + b.Key + ")"
}
