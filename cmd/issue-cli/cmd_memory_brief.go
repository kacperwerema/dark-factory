package main

import (
	"fmt"
	"io"

	"strings"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Start's injection limits: pins are Letta-style core memory (a few, short).
const (
	startMemoryLimit = 5
	pinLimit         = 10
	pinChars         = 1500
	// A memory at a status: what entering it adds, so it stays small. 600
	// holds one long memory (MaxText) or two short ones.
	statusMemoryLimit = 3
	statusMemoryChars = 600
)

// issueViewer is what the worker on issue may read.
func issueViewer(p *tracker.Project, issue *tracker.Issue) memory.Viewer {
	return memory.Viewer{Department: deptKey(p.DepartmentOf(issue)), Agent: agentNameForSlug(issue.Slug)}
}

// issueQuery is the ranker's query for issue.
func issueQuery(p *tracker.Project, issue *tracker.Issue, limit int) memory.Query {
	return memory.Query{
		Text:   issue.Title + " " + issue.System + " " + strings.Join(issue.Labels, " ") + " " + issue.Slug,
		System: issue.System, Department: deptKey(p.DepartmentOf(issue)), Limit: limit,
	}
}

// printStartMemories is start's == Memories == block: the pinned memories
// of the issue's scopes, then those matching the issue, recorded as used.
func printStartMemories(w io.Writer, ctx *Context, issue *tracker.Issue) []string {
	p := ctx.Project
	fmt.Fprintln(w, "\n== Memories ==")
	mems, err := visibleMemories(ctx, issueViewer(p, issue), memFilter{})
	if err != nil {
		fmt.Fprintf(w, "Could not read the memories: %v\n", err)
		return nil
	}
	scores := memoryScores(ctx)
	pins, rest := memory.SplitPins(mems, pinLimit, pinChars)
	at, rest := memory.ForStage(rest, issue.Status, scores, ctx.now(), statusMemoryLimit, statusMemoryChars)
	ranker, _ := memoryRankerScored(ctx, scores)
	hits := ranker.Rank(issueQuery(p, issue, startMemoryLimit), rest)
	if len(mems) == 0 {
		fmt.Fprintln(w, "None yet on this project.")
	}
	if len(pins.Shown) > 0 {
		fmt.Fprintln(w, "Pinned:")
		for _, m := range pins.Shown {
			fmt.Fprintf(w, "  %s (%s) %s\n", m.ID, m.Kind, m.Text)
		}
		if pins.More > 0 {
			fmt.Fprintf(w, "  +%d pinned: issue-cli memory list --pinned\n", pins.More)
		}
	}
	if len(at.Shown) > 0 {
		fmt.Fprintf(w, "At %s:\n", issue.Status)
		writeStatusMemories(w, ctx, at, issue.Status)
	}
	if len(hits) > 0 {
		fmt.Fprintln(w, "Matching this issue:")
		for _, h := range hits {
			printMemory(w, h.Memory, ctx)
		}
	} else if len(mems) > 0 {
		fmt.Fprintln(w, "None matches this issue.")
	}
	if len(mems) > len(pins.Shown)+len(at.Shown)+len(hits) {
		fmt.Fprintf(w, "(%d you can see: issue-cli memory search <words>)\n", len(mems))
	}
	var used []string
	for _, m := range pins.Shown {
		used = append(used, m.ID)
	}
	for _, m := range at.Shown {
		used = append(used, m.ID)
	}
	for _, h := range hits {
		used = append(used, h.ID)
	}
	recordUse(ctx, used, agentNameForSlug(issue.Slug), issue.Slug, issue.Status)
	fmt.Fprintf(w, "Learned a durable project fact the next agent should know (not a note on this issue: that goes in the issue or HANDOFF.md)? issue-cli memory add \"<fact>\" --kind fact|convention|pitfall|howto|who (a similar one: --updates <id>)\n")
	return used
}

// writeStatusMemories is at's lines and, when some did not fit, the one line
// that finds them.
func writeStatusMemories(w io.Writer, ctx *Context, at memory.Pins, status string) {
	for _, m := range at.Shown {
		fmt.Fprintf(w, "  %s (%s) %s\n", m.ID, m.Kind, m.Text)
	}
	if at.More > 0 {
		fmt.Fprintf(w, "  +%d more: %s\n", at.More, tracker.InjectProject(ctx.Project, fmt.Sprintf("issue-cli memory list --at %s", shell.Quote(status))))
	}
}

// printTransitionMemories is the block a transition ends with when memories
// matter at the status issue just entered, recorded as used. Nothing at all
// when none do: most transitions gain no lines.
func printTransitionMemories(w io.Writer, ctx *Context, issue *tracker.Issue) {
	mems, err := visibleMemories(ctx, issueViewer(ctx.Project, issue), memFilter{})
	if err != nil {
		return
	}
	at, _ := memory.ForStage(mems, issue.Status, memoryScores(ctx), ctx.now(), statusMemoryLimit, statusMemoryChars)
	if len(at.Shown) == 0 {
		return
	}
	fmt.Fprintf(w, "\n== Memories for %s ==\n", issue.Status)
	writeStatusMemories(w, ctx, at, issue.Status)
	used := make([]string, 0, len(at.Shown))
	for _, m := range at.Shown {
		used = append(used, m.ID)
	}
	recordUse(ctx, used, agentNameForSlug(issue.Slug), issue.Slug, issue.Status)
}

// durableHint is where a note on one issue belongs instead of memory.
const durableHint = "Memory keeps durable project facts, true across issues. A note on this work goes in the issue (a comment, ## Decisions) or HANDOFF.md; a lesson for later goes through the post-mortem or memory consolidate, with the owner's round. Reworded as a general fact? add it again"

// checkDurable refuses a worker's memory that reads as a note on its own
// issue (memory.IssueNote) and warns on one naming another issue; a lead or
// the owner gets the same words as a warning (memory as a cache, the
// epic's D-2).
func checkDurable(ctx *Context, who memCaller, issue *tracker.Issue, issues []*tracker.Issue, text string) error {
	own := ""
	if issue != nil {
		own = issue.Slug
	}
	worker := !who.Lead && !who.Owner
	if why := memory.IssueNote(text, own); why != "" {
		if worker {
			return fmt.Errorf("refused: this reads as a note on one issue (%s). %s", why, durableHint)
		}
		fmt.Fprintf(ctx.Stderr, "warning: this reads as a note on one issue (%s). %s.\n", why, durableHint)
		return nil
	}
	slugs := make([]string, 0, len(issues))
	for _, is := range issues {
		if is.Slug != own {
			slugs = append(slugs, is.Slug)
		}
	}
	if why := memory.NamesIssue(text, slugs); why != "" {
		fmt.Fprintf(ctx.Stderr, "warning: %s; a memory should stay true after that issue closes (its source is kept with --issue).\n", why)
	}
	return nil
}

// memoryScores is the usefulness of the project's and the machine's
// memories, joined with this project's traces (memory.Store.Scores). A
// store that can't be read scores nothing.
func memoryScores(ctx *Context) memory.Scores {
	out := memory.Scores{}
	if ctx.Project == nil {
		return out
	}
	for _, st := range []memory.Store{memory.ProjectStore(ctx.Project.IssueDir), memory.MachineStore()} {
		sc, err := st.Scores(ctx.Project.IssueDir)
		if err != nil {
			continue
		}
		for id, u := range sc {
			out[id] = u
		}
	}
	return out
}

// usefulnessShort is a score in a few words: "+0.12 over 14 (helps)" or
// "not measured (3 of 8)".
func usefulnessShort(u memory.Usefulness) string {
	if !u.Measured() {
		return fmt.Sprintf("not measured (%d of %d)", u.Known, memory.UsefulMin)
	}
	return fmt.Sprintf("%+.2f over %d (%s)", u.Lift, u.Known, u.Word())
}

// usefulnessLine is memory show's Usefulness line: overall, then per
// status.
func usefulnessLine(u memory.Usefulness) string {
	if u.Injections == 0 {
		return fmt.Sprintf("Usefulness: not measured yet: no traced stage it reached (scored from %d with a known gate result)", memory.UsefulMin)
	}
	line := "Usefulness: " + usefulnessShort(u)
	if !u.Measured() {
		line = fmt.Sprintf("Usefulness: not measured yet (%d of %d stages with a known gate result)", u.Known, memory.UsefulMin)
	}
	var parts []string
	for _, st := range u.Statuses() {
		sc := u.ByStatus[st]
		if sc.Measured() {
			parts = append(parts, fmt.Sprintf("%s %+.2f (%d)", st, sc.Lift, sc.Known))
		} else {
			parts = append(parts, fmt.Sprintf("%s — (%d)", st, sc.Known))
		}
	}
	if len(parts) > 0 {
		line += " · " + strings.Join(parts, " · ")
	}
	return line
}

// recordUse writes use events to each id's store; errors only cost the
// count. issue and status name the stage they reached, for usefulness.
func recordUse(ctx *Context, ids []string, by, issue, status string) {
	var own, machine []string
	for _, id := range ids {
		if memory.StoreOfID(id) == "machine" {
			machine = append(machine, id)
		} else {
			own = append(own, id)
		}
	}
	_ = memory.ProjectStore(ctx.Project.IssueDir).RecordUse(own, by, issue, status, ctx.now())
	_ = memory.MachineStore().RecordUse(machine, by, issue, status, ctx.now())
}

// printMemoryReflection is the hint at done and retrospective
// (LangMem's reflection, done by the agent): what to add, and the similar
// memories to update instead.
func printMemoryReflection(w io.Writer, ctx *Context, issue *tracker.Issue) {
	fmt.Fprintln(w, "\n== Memory ==")
	fmt.Fprintf(w, "Anything the next agent should know? issue-cli memory add \"<fact>\" --issue %s --kind fact|convention|pitfall|howto|who\n", issue.Slug)
	fmt.Fprintf(w, "What this issue's trail could teach: issue-cli memory candidates %s\n", issue.Slug)
	mems, err := visibleMemories(ctx, issueViewer(ctx.Project, issue), memFilter{})
	if err != nil {
		return
	}
	ranker, _ := memoryRanker(ctx)
	hits := ranker.Rank(issueQuery(ctx.Project, issue, 3), mems)
	if len(hits) == 0 {
		return
	}
	fmt.Fprintln(w, "Already remembered about this (update one instead with --updates <id>, or --supersedes <id> if it is no longer true):")
	for _, h := range hits {
		fmt.Fprintf(w, "  %s (%s) %s\n", h.ID, h.Kind, h.Text)
	}
}
