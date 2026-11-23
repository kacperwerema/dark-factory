package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/spend"
	"github.com/kacperwerema/dark-factory/internal/tokens"
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

--context prints where the tokens come from instead: every turn re-reads
the whole context (input + cache read + cache write of one API call), so
a session costs its context × turns. Per box and for the top sessions of
the last --days calendar days (default 7): sessions, turns, the floor
(the first turn's context: CLAUDE.md, the briefing, tools and skills,
before any work; the median over the box's sessions), median and max
context per turn, compactions, and the floor's share (floor × turns over
the context read). Under Workers and Leads, under each top session and
in one box's view, the floor by part: system prompt, tools (the three
biggest named), MCP, CLAUDE.md files (global, project, memory), skills,
brief, other; read from the request Claude Code writes before the first
answer, scaled to the floor's tokens (an estimate, about ±10%; a part is
its median over the sessions). Under it, the brief by section: a lead's
role, rules, pins, decisions, handoff note, chat summary, state file and
restart list; a worker's at the briefing's own headings (--json:
floor_parts.brief_sections). A resumed session has no floor of its own. Only a
session's main transcript is read (not its subagents'), and Claude Code's
only: Codex is not covered. The Stats page's Context floor trend is the
worker sessions' floor per day (the context health family).

--kinds prints where the tokens go by kind of session instead, for the
last --days calendar days (default 7) or --since/--until (a date or an
RFC 3339 time): per project and kind (workers, leads, subagents,
post-mortems, critics, sweeps, lead-flush and other jobs, benchmark runs,
health reviews, your own sessions) sessions, turns, tokens read (input +
cache), output, est. USD, share of the window's USD and % of the weekly
limit; then workers by type and stage (the issue's status at each turn).
A session's kind comes from its agent name (lead-*, agent-<slug>,
postmortem-*, critic-*, bench-*, review-*), else its folder (an issue's
worktree: its worker; the project folder: yours). A job's run issue (job:
lead-flush, a sweep) is its job. Benchmark tasks run on a sandbox HOME
count from their run records. --all-projects reads every project in
projects.yaml, and the transcript folders of none as "other". Each price
shows as assumed or confirmed (confirmed: true on its prices.yaml entry);
weekly_limit: {usd: N} or {tokens: N} in prices.yaml fills % WEEK (pro
rata for the window). --prices <file> reads another prices file.

--turns prints each issue's worker turns by cause: transition (an
issue-cli transition call; refused when its result was an error),
validate (make validate, or reading its log), read (one Read, or one
cat/head/tail/nl/sed -n of one file), red round (any other turn while a
check of the issue's rounds or the session's make validate is red),
rest; with the context each cause's
turns read. Two counts name turns a batch would save: dry runs
(transition calls with --dry-run) and follow-on reads (a read turn
straight after another read turn of the same session). Pass a slug for
one issue. Command text is read only to pick
the cause; nothing of it is kept.

Measure before and after a change: run --kinds --json and --turns --json
on a fixed --since/--until window before it ships and on a window of the
same length after (docs/workflow-stats.md, "Measure before and after").

Examples:
  issue-cli spend
  issue-cli spend --kinds --all-projects --days 7
  issue-cli spend --kinds --json --since 2026-11-11T20:00:00Z --until 2026-11-18
  issue-cli spend --turns --days 3
  issue-cli spend --turns factory/some-issue --json
  issue-cli spend marketing
  issue-cli spend --json
  issue-cli spend --context
  issue-cli spend --context --days 1 product`,
	Run: runSpend,
}

func init() { registerCommand(spendCommand) }

// loadSpend is the project's spend now; a seam for tests.
var loadSpend = func(ctx *Context) *spend.Report {
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	return spend.Load(ctx.Project, issues, ctx.now(), spend.Options{})
}

// loadContext is the project's context numbers over days; a seam for tests.
var loadContext = func(ctx *Context, days int) *spend.ContextReport {
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	return spend.LoadContext(ctx.Project, issues, ctx.now(), days, spend.Options{})
}

func runSpend(ctx *Context, args []string) error {
	fs := newFlagSet("spend", ctx)
	withContext := fs.Bool("context", false, "where the tokens come from: the context floor and per-turn size, per box and top session")
	days := fs.Int("days", spend.ContextDays, "with --context, --kinds or --turns: calendar days back, today included")
	kinds := fs.Bool("kinds", false, "tokens, est. USD and share by project and kind of session (workers by type and stage, leads, subagents, post-mortems, critics, sweeps, jobs, benchmark, yours)")
	allProjects := fs.Bool("all-projects", false, "with --kinds: every project in projects.yaml, and the folders of none as other")
	turns := fs.Bool("turns", false, "each issue's worker turns by cause: transition, validate, read, red round, rest")
	since := fs.String("since", "", "with --kinds or --turns: the window's start, a date or an RFC 3339 time (instead of --days)")
	until := fs.String("until", "", "with --kinds or --turns: the window's end (default now; a date counts the whole day)")
	pricesFile := fs.String("prices", "", "with --kinds: read prices and weekly_limit from this file instead of prices.yaml")
	if err := parseFlagsArgs(ctx, fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return unexpectedArgError(fs, fs.Arg(1))
	}
	pos := fs.Args()
	if *allProjects && !*turns {
		*kinds = true
	}
	switch {
	case *kinds && *turns, *kinds && *withContext, *turns && *withContext:
		return fmt.Errorf("--context, --kinds and --turns are separate views: give one")
	case *allProjects && *turns:
		return fmt.Errorf("--all-projects goes with --kinds: --turns reads this project's issues")
	case *pricesFile != "" && !*kinds:
		return fmt.Errorf("--prices goes with --kinds: issue-cli spend --kinds --prices %s", *pricesFile)
	case (*since != "" || *until != "") && !*kinds && !*turns:
		if *withContext {
			return fmt.Errorf("--since and --until go with --kinds or --turns; --context counts calendar days back: issue-cli spend --context --days <n>")
		}
		return fmt.Errorf("--since and --until go with --kinds or --turns (issue-cli spend --kinds --since <date>); --context counts calendar days back: issue-cli spend --context --days <n>")
	}
	if *kinds || *turns {
		if *days < 1 {
			return fmt.Errorf("--days must be 1 or more, got %d", *days)
		}
		w, err := spendWindowOf(ctx.now(), *days, daysSet(fs), *since, *until)
		if err != nil {
			return err
		}
		if *turns {
			slug := ""
			if len(pos) == 1 {
				slug = pos[0]
			}
			return runSpendTurns(ctx, w, slug)
		}
		if len(pos) == 1 {
			return fmt.Errorf("--kinds takes no box: it reads the whole project (--all-projects: every one)")
		}
		return runSpendKinds(ctx, w, *allProjects, *pricesFile)
	}
	if *withContext {
		if *days < 1 {
			return fmt.Errorf("--days must be 1 or more, got %d", *days)
		}
		return runSpendContext(ctx, pos, *days)
	}
	if daysSet(fs) {
		return fmt.Errorf("--days goes with --context, --kinds or --turns: issue-cli spend --kinds --days %d", *days)
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
	fmt.Fprintln(w, "\nContext floor and turn size, per box and session: issue-cli spend --context")
	fmt.Fprintln(w, "By project and kind of session: issue-cli spend --kinds --all-projects · worker turns by cause: issue-cli spend --turns")
	return nil
}

func daysSet(fs interface{ Visit(func(*flag.Flag)) }) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == "days" })
	return set
}

// runSpendContext prints spend --context: the whole project, or one box.
func runSpendContext(ctx *Context, pos []string, days int) error {
	rep := loadContext(ctx, days)
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
		boxes = []*spend.ContextBox{b}
	}
	if ctx.JSONOutput {
		if len(pos) == 1 {
			return writeJSON(ctx.Stdout, boxes[0])
		}
		return writeJSON(ctx.Stdout, rep)
	}
	w := ctx.Stdout
	fmt.Fprintf(w, "Context per turn since %s (%d days; Claude Code transcripts on this machine, main sessions only)\n", rep.Since.Format("2006-01-02"), rep.Days)
	fmt.Fprintln(w, "A turn re-reads its whole context; the floor is the first turn's (CLAUDE.md, briefing, tools), median over sessions.")
	if rep.All.Sessions == 0 {
		fmt.Fprintf(w, "No session of this project in the last %d days.\n", rep.Days)
		return nil
	}
	if len(pos) == 0 {
		fmt.Fprintf(w, "\nAll:     %s\n", contextLine(rep.All))
		fmt.Fprintf(w, "Workers: %s\n", contextLine(rep.Workers))
		printFloorParts(w, "         ", rep.Workers)
		fmt.Fprintf(w, "Leads:   %s\n", contextLine(rep.Leads))
		printFloorParts(w, "         ", rep.Leads)
	}
	for _, b := range boxes {
		head := "== " + b.Name
		if b.Parent != "" {
			head += " (in group " + b.Parent + ")"
		}
		if len(b.RolledFrom) > 0 {
			head += " · incl. " + strings.Join(b.RolledFrom, ", ")
		}
		fmt.Fprintln(w, "\n"+head+" ==")
		if b.Sessions == 0 {
			fmt.Fprintln(w, "  no sessions")
			continue
		}
		fmt.Fprintf(w, "  %s\n", contextLine(b.ContextStats))
		if len(pos) == 1 {
			printFloorParts(w, "  ", b.ContextStats)
		}
		if len(b.Top) > 0 && len(pos) == 1 {
			printTopSessions(w, b.Top)
		}
	}
	if len(pos) == 0 {
		if u := rep.Unattributed; u.Sessions > 0 {
			fmt.Fprintf(w, "\nUnattributed (yours): %s\n", contextLine(u))
		}
		fmt.Fprintf(w, "\nTop %d sessions by context read:\n", spend.TopSessions)
		printTopSessions(w, rep.Top)
	}
	return nil
}

// contextLine is one ContextStats as a line.
func contextLine(st spend.ContextStats) string {
	line := fmt.Sprintf("%s · %s · floor %s · turn median %s, max %s · %s",
		countOf(st.Sessions, "session", "sessions"), countOf(st.Turns, "turn", "turns"), tokens.Format(st.Floor), tokens.Format(st.Median), tokens.Format(st.Max), countOf(st.Compactions, "compaction", "compactions"))
	if st.Floored > 0 {
		line += fmt.Sprintf(" · floor share %d%% of %s read", int(st.FloorShare*100+0.5), tokens.Format(st.Context))
	} else {
		line += " · " + tokens.Format(st.Context) + " read"
	}
	if st.Resumed > 0 {
		line += " · " + strconv.Itoa(st.Resumed) + " resumed"
	}
	return line
}

func printTopSessions(w io.Writer, top []spend.ContextSession) {
	for _, s := range top {
		who := s.Issue
		switch {
		case who == "" && s.Agent != "":
			who = s.Agent
		case who == "":
			who = "yours"
		}
		floor := tokens.Format(s.Floor)
		if s.Resumed {
			floor = "resumed"
		} else if s.Floor == 0 {
			floor = "before window"
		}
		fmt.Fprintf(w, "    %s  %s  %s · floor %s · turn median %s, max %s · %s · %s read\n",
			s.Start.Local().Format("2006-01-02 15:04"), who, countOf(s.Turns, "turn", "turns"), floor, tokens.Format(s.Median), tokens.Format(s.Max), countOf(s.Compactions, "compaction", "compactions"), tokens.Format(s.Context))
		if s.Parts != nil {
			fmt.Fprintf(w, "      floor: %s\n", floorPartsLine(s.Parts))
			if line := briefSectionsLine(s.Parts); line != "" {
				fmt.Fprintf(w, "      %s brief: %s\n", s.Parts.BriefKind, line)
			}
		}
	}
}

// printFloorParts is the floor-by-part line under a stats line, when its
// sessions' transcripts carry a request shape.
func printFloorParts(w io.Writer, indent string, st spend.ContextStats) {
	if st.Parts == nil {
		return
	}
	fmt.Fprintf(w, "%sfloor by part (median of %s, est. ±10%%): %s\n", indent, countOf(st.PartsOf, "session", "sessions"), floorPartsLine(st.Parts))
	if line := briefSectionsLine(st.Parts); line != "" {
		fmt.Fprintf(w, "%s%s brief by section (median of %s): %s\n", indent, st.Parts.BriefKind, countOf(st.Parts.BriefSectionsOf, "session", "sessions"), line)
		if st.Parts.BriefKind == spend.BriefLead {
			fmt.Fprintf(w, "%s  (the brief a lead would get now: issue-cli lead brief --render --sections)\n", indent)
		}
	}
}

// briefSectionsLine is the brief's sections in brief order, "" without a
// split (what the owner typed, or no request shape).
func briefSectionsLine(p *spend.FloorParts) string {
	var out []string
	for _, s := range p.BriefSections {
		out = append(out, strings.ReplaceAll(s.Name, "_", " ")+" "+tokens.Format(s.Tokens))
	}
	return strings.Join(out, " · ")
}

// floorPartLabels are the parts as printed.
var floorPartLabels = map[string]string{
	spend.PartSystem: "system", spend.PartTools: "tools", spend.PartMCP: "MCP", spend.PartClaudeMD: "CLAUDE.md",
	spend.PartSkills: "skills", spend.PartBrief: "brief", spend.PartOther: "other",
}

// floorPartsLine is a floor's parts: tools with their three biggest, the
// CLAUDE.md files by kind.
func floorPartsLine(p *spend.FloorParts) string {
	var out []string
	for _, name := range spend.FloorPartNames {
		part := floorPartLabels[name] + " " + tokens.Format(p.Part(name))
		var detail []string
		switch name {
		case spend.PartTools:
			for _, t := range p.ToolSizes {
				if len(detail) == 3 {
					break
				}
				if !strings.HasPrefix(t.Name, "mcp__") {
					detail = append(detail, t.Name+" "+tokens.Format(t.Tokens))
				}
			}
		case spend.PartClaudeMD:
			for _, f := range p.Files {
				detail = append(detail, f.Name+" "+tokens.Format(f.Tokens))
			}
		}
		if len(detail) > 0 && p.Part(name) > 0 {
			part += " (" + strings.Join(detail, ", ") + ")"
		}
		out = append(out, part)
	}
	return strings.Join(out, " · ")
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
		weeks = append(weeks, tokens.Format(wk.Tokens()))
	}
	line := fmt.Sprintf("Spend: %s this week · %d weeks: %s", spend.Line(b.ThisWeek()), spend.Weeks, strings.Join(weeks, " / "))
	if b.Budget != nil {
		line += " · budget " + b.Budget.Text + overText(b.Budget)
	}
	return line + " (issue-cli spend " + b.Key + ")"
}
