package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// dispatchBrief is the briefing a dispatched worker was started with: the
// text of its session's dispatch-prompt.txt, when it was written, and the
// decision refs and memory ids its sidecar record says it carried. start
// reads it to leave out what the worker has already read: the brief's
// "how to work" asks for start first, so whatever start repeats is read
// twice and stays in every later turn's context.
type dispatchBrief struct {
	norm      string
	at        time.Time
	decisions map[string]bool
	memories  map[string]bool
}

// dispatchPromptPath is where the dispatch wrote slug's briefing.
func dispatchPromptPath(proj *tracker.Project, slug string) string {
	return filepath.Join(projectRoot(proj), ".agent-logs", tracker.AgentSessionName(slug), "dispatch-prompt.txt")
}

// loadDispatchBrief is the briefing of issue's dispatched session, or nil
// when start runs anywhere else: the session names the issue
// (ISSUE_VIEWER_ISSUE_SLUG, set by the dispatch) and its prompt file is
// there to read.
func loadDispatchBrief(proj *tracker.Project, issue *tracker.Issue) *dispatchBrief {
	if os.Getenv("ISSUE_VIEWER_ISSUE_SLUG") != issue.Slug {
		return nil
	}
	path := dispatchPromptPath(proj, issue.Slug)
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	b := &dispatchBrief{norm: normSpace(string(data)), at: info.ModTime(), decisions: map[string]bool{}, memories: map[string]bool{}}
	if store, err := tracker.LoadStats(issue.FilePath); err == nil {
		for i := len(store.Briefings) - 1; i >= 0; i-- {
			r := store.Briefings[i]
			if r.Kind != tracker.BriefingDispatch && r.Kind != tracker.BriefingReprompt {
				continue
			}
			for _, ref := range r.Decisions {
				b.decisions[ref] = true
			}
			for _, id := range r.Memories {
				b.memories[id] = true
			}
			break
		}
	}
	return b
}

// normSpace folds every run of white space to one blank.
func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// has says the briefing holds text already (white space aside). A nil
// brief holds nothing, so a start outside a dispatch prints everything.
func (b *dispatchBrief) has(text string) bool {
	if b == nil {
		return false
	}
	n := normSpace(text)
	return n != "" && strings.Contains(b.norm, n)
}

// hasDecision says the briefing carried ref: its record lists it, or its
// text names it.
func (b *dispatchBrief) hasDecision(ref string) bool {
	return b != nil && (b.decisions[ref] || strings.Contains(b.norm, ref))
}

// hasMemory says the briefing showed memory id.
func (b *dispatchBrief) hasMemory(id string) bool {
	return b != nil && (b.memories[id] || strings.Contains(b.norm, id))
}

// lacking is the texts the briefing doesn't hold yet.
func (b *dispatchBrief) lacking(texts []string) []string {
	var out []string
	for _, t := range texts {
		if !b.has(t) {
			out = append(out, t)
		}
	}
	return out
}

// printBriefDecisions is start's decisions block for a dispatched worker:
// only the decisions its briefing did not carry, recorded as injected.
func printBriefDecisions(ctx *Context, b *dispatchBrief, issue *tracker.Issue, issues []*tracker.Issue, status string, wf *tracker.WorkflowConfig) []string {
	block := issueDecisionBlock(ctx, issue, issues, status, wf)
	var fresh []tracker.InForceEntry
	for _, it := range append(append([]tracker.InForceEntry{}, block.Shown...), block.Folded...) {
		if !b.hasDecision(it.Entry.Ref) {
			fresh = append(fresh, it)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	fmt.Fprintln(ctx.Stdout, "\n== Decisions since your briefing ==")
	var refs []string
	for _, it := range fresh {
		fmt.Fprintln(ctx.Stdout, tracker.InjectProject(ctx.Project, tracker.DecisionBriefLine(it.Entry)))
		refs = append(refs, it.Entry.Ref)
	}
	me := whoAmI(ctx.Project, issues)
	name := me.Name
	if name == "" {
		name = "owner"
	}
	if err := tracker.RecordInjected(ctx.Project.IssueDir, tracker.Injected{Briefing: tracker.BriefingStart, Issue: issue.Slug,
		Actor: tracker.DecisionActor{Name: name, Role: string(me.Role)}, Refs: refs}, ctx.now()); err != nil {
		fmt.Fprintf(ctx.Stderr, "warning: the decisions log did not take start's injected event: %v\n", err)
	}
	return refs
}

// printBriefMemories is start's memory block for a dispatched worker: the
// pinned, status and matching memories its briefing did not show, one
// line each (the briefing already says how to search and add memories).
func printBriefMemories(w io.Writer, ctx *Context, b *dispatchBrief, issue *tracker.Issue) []string {
	mems, err := visibleMemories(ctx, issueViewer(ctx.Project, issue), memFilter{})
	if err != nil || len(mems) == 0 {
		return nil
	}
	scores := memoryScores(ctx)
	pins, rest := memory.SplitPins(mems, pinLimit, pinChars)
	at, rest := memory.ForStage(rest, issue.Status, scores, ctx.now(), statusMemoryLimit, statusMemoryChars)
	ranker, _ := memoryRankerScored(ctx, scores)
	shown := append(append([]memory.Memory{}, pins.Shown...), at.Shown...)
	for _, h := range ranker.Rank(issueQuery(ctx.Project, issue, startMemoryLimit), rest) {
		shown = append(shown, h.Memory)
	}
	var used []string
	for _, m := range shown {
		if b.hasMemory(m.ID) {
			continue
		}
		if len(used) == 0 {
			fmt.Fprintln(w, "\n== Memories your briefing lacks (one in full: "+tracker.InjectProject(ctx.Project, "issue-cli memory show <id>")+") ==")
		}
		fmt.Fprintf(w, "- %s (%s) %s\n", m.ID, m.Kind, cutText(normSpace(m.Text), briefMemoryRunes))
		used = append(used, m.ID)
	}
	recordUse(ctx, used, agentNameForSlug(issue.Slug), issue.Slug, issue.Status)
	return used
}

// printBriefComments prints the comments made on issue after its briefing
// was written: the briefing embeds the body, not what came later.
func printBriefComments(w io.Writer, b *dispatchBrief, issue *tracker.Issue) {
	var fresh []tracker.Comment
	for _, c := range issue.Comments() {
		if t, ok := tracker.CommentTime(c.Date); ok && t.After(b.at) {
			fresh = append(fresh, c)
		}
	}
	if len(fresh) == 0 {
		return
	}
	fmt.Fprintln(w, "\n== Comments since your briefing ==")
	for _, c := range fresh {
		fmt.Fprintf(w, "- #%d %s %s: %s\n", c.ID, tracker.CommentAuthor(c), c.Date, normSpace(c.Text))
	}
}

// briefMemoryRunes is how much of a memory's text a dispatched worker's
// start shows: enough to tell whether it matters, memory show has the rest.
const briefMemoryRunes = 160

// cutText is s cut to n runes, with an ellipsis when it was longer.
func cutText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// briefFootnote is the line a dispatched worker's start ends with.
const briefFootnote = "Left out what your briefing holds (guidance, decisions, memories, open branches): issue-cli start <slug> --full prints it all."

// printBriefOverlaps is the open-branches block without the lines the
// briefing already has.
func printBriefOverlaps(ctx *Context, b *dispatchBrief, issue *tracker.Issue, issues []*tracker.Issue) {
	var lines []string
	for _, l := range tracker.IssueBranchOverlaps(ctx.Project, projectRoot(ctx.Project), issue, issues).Lines(issue.Slug) {
		if l = tracker.InjectProject(ctx.Project, l); !b.has(l) {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(ctx.Stdout, "== Open branches since your briefing ==")
	for _, l := range lines {
		fmt.Fprintln(ctx.Stdout, l)
	}
	fmt.Fprintln(ctx.Stdout)
}

// hasCILine says the briefing has the same local CI line, its "(5m ago)"
// aside: the state is the same, only the age moved on.
func (b *dispatchBrief) hasCILine(line string) bool {
	if i := strings.LastIndex(line, " ("); i > 0 {
		line = line[:i]
	}
	return b.has(line)
}
