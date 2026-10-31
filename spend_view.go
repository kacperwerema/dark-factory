package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/michal-franc/issue-viewer/internal/spend"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The rooms' spend: what each box spent by week, read by internal/spend
// from Claude Code's transcripts on this machine. Estimates, shown only;
// a budget never blocks (factory/department-budgets-…#D-3).

// WbSpend is a room's spend block.
type WbSpend struct {
	Box string
	// Line is this week's tokens and estimated cost; Empty when the box
	// spent nothing in its weeks (Hint names the command).
	Line  string
	Empty bool
	Hint  string
	// Cols are the weeks, oldest first, for the mini chart; Rows the same
	// newest first, for the table.
	Cols, Rows []WbSpendCol
	// Budget is "x of y"; Over shows it red (only shown).
	Budget string
	Over   bool
	// BudgetErr is a budget: that doesn't parse.
	BudgetErr string
	// Rolled is "incl. N departments" on a group or HQ.
	Rolled string
	Top    []WbSpendIssue
	// Title is the tooltip: the token kinds and what the numbers are.
	Title string
	// Tokens is this week's tokens, short ("2.9B"), for the floor's tiles.
	Tokens string
	// Subs is the split by subscription on HQ's block ("claude 1.2M tokens
	// · codex 300k tokens this week"); "" elsewhere or when only one spent.
	Subs string
}

// WbSpendCol is one week's column.
type WbSpendCol struct {
	Label, Text string
	Pct         int
	Now         bool
}

// WbSpendIssue is one of the box's top issues.
type WbSpendIssue struct {
	Slug, Title, URL, Text string
}

// spendNote is what every spend block says the numbers are.
const spendNote = "Estimate from Claude Code transcripts on this machine (Codex's own session logs are counted only in HQ's By subscription line); nothing leaves the machine. Cost from prices.yaml; a budget is shown only, never enforced."

// companySpend reads the project's spend for the company page.
func companySpend(p *tracker.Project, issues []*tracker.Issue, now time.Time) *spend.Report {
	return spend.Load(p, issues, now, spend.Options{ProjectsDir: spendProjectsDir()})
}

// spendProjectsDir is where transcripts are read; a seam for tests.
var spendProjectsDir = func() string { return claudeProjectsDir() }

// roomSpend is box key's block, nil when the report has no such box.
func roomSpend(rep *spend.Report, key, prefix, projectFlag string) *WbSpend {
	if rep == nil {
		return nil
	}
	b := rep.Find(key)
	if b == nil {
		return nil
	}
	out := &WbSpend{Box: b.Key, Line: spend.Line(b.ThisWeek()) + " this week", Empty: b.Empty(), Tokens: spend.FormatTokens(b.ThisWeek().Tokens())}
	if out.Empty {
		out.Hint = "issue-cli" + projectFlag + " spend " + b.Key
	}
	var max int64
	for _, w := range b.Weeks {
		if w.Tokens() > max {
			max = w.Tokens()
		}
	}
	for i := len(b.Weeks) - 1; i >= 0; i-- {
		w := b.Weeks[i]
		col := WbSpendCol{Text: spend.Line(w.Tally), Now: i == 0}
		if t, err := time.Parse("2006-01-02", w.Start); err == nil {
			col.Label = t.Format("Jan 2")
		}
		if max > 0 {
			col.Pct = int(w.Tokens() * 100 / max)
			if col.Pct == 0 && w.Tokens() > 0 {
				col.Pct = 2
			}
		}
		out.Cols = append(out.Cols, col)
		out.Rows = append([]WbSpendCol{col}, out.Rows...)
	}
	if s := b.Budget; s != nil {
		if s.Err != "" {
			out.BudgetErr = s.Err
		} else {
			out.Budget, out.Over = s.Text, s.Over
		}
	}
	if n := len(b.RolledFrom); n > 0 {
		out.Rolled = "incl. " + plural(n, "box", "boxes")
		if b.Key != spend.HQ {
			out.Rolled = "incl. " + plural(n, "department", "departments")
		}
	}
	for _, is := range b.Top {
		out.Top = append(out.Top, WbSpendIssue{Slug: is.Slug, Title: is.Title, URL: prefix + "/issue/" + is.Slug, Text: spend.Line(is.Tally)})
	}
	if b.Key == spend.HQ {
		out.Subs = subscriptionsLine(rep)
	}
	t := b.ThisWeek()
	out.Title = fmt.Sprintf("this week: input %s · output %s · cache read %s · cache write %s. %s",
		spend.FormatTokens(t.Input), spend.FormatTokens(t.Output), spend.FormatTokens(t.CacheRead), spend.FormatTokens(t.CacheWrite), spendNote)
	if len(rep.Unpriced) > 0 {
		out.Title += " No price for " + strings.Join(rep.Unpriced, ", ") + ": add it to " + rep.PricesPath + "."
	}
	return out
}

// subscriptionsLine is this week's spend by agent CLI, "" unless a second
// subscription spent something in the weeks shown.
func subscriptionsLine(rep *spend.Report) string {
	var parts []string
	spent := 0
	for _, s := range rep.Subscriptions {
		if s.Total().Empty() {
			continue
		}
		spent++
		parts = append(parts, s.Agent+" "+spend.FormatTokens(s.ThisWeek().Tokens())+" tokens")
	}
	if spent < 2 {
		return ""
	}
	return strings.Join(parts, " · ") + " this week"
}

// projectFlag is " --project <slug>" for hints.
func projectFlag(p *tracker.Project) string {
	if p == nil || p.Slug == "" {
		return ""
	}
	return " --project " + p.Slug
}
