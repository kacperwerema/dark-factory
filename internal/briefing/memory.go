package briefing

import (
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Briefing limits for pinned memories (Letta's core memory budget).
const (
	memoryPinLimit = 10
	memoryPinChars = 1500
)

// Limits for lead-scope memories in a lead's brief. Their pins are the
// lead's standing rules, on their own budget so project pins can't crowd
// them out; a lead brief shows every pin as its title and Never sentences
// (writePinTitles), the unpinned ones as one clipped line each.
const (
	leadPinLimit  = 12
	leadPinChars  = 6000
	leadListLimit = 8
	leadListChars = 100
)

// briefingMemories is the valid memories viewer may read in the project's
// and the machine's store.
func briefingMemories(proj *tracker.Project, v memory.Viewer) []memory.Memory {
	var mems []memory.Memory
	if proj != nil && proj.IssueDir != "" {
		if set, err := memory.ProjectStore(proj.IssueDir).Load(); err == nil {
			mems = append(mems, set.All()...)
		}
	}
	if set, err := memory.MachineStore().Load(); err == nil {
		mems = append(mems, set.All()...)
	}
	var keep []memory.Memory
	for _, m := range mems {
		if m.VisibleTo(v) && m.Valid(memoryNow()) {
			keep = append(keep, m)
		}
	}
	return keep
}

// splitLeadScope parts mems into the lead scope and everything else.
func splitLeadScope(mems []memory.Memory) (lead, rest []memory.Memory) {
	for _, m := range mems {
		if m.Scope == memory.ScopeLead {
			lead = append(lead, m)
		} else {
			rest = append(rest, m)
		}
	}
	return lead, rest
}

// briefingPins is the pinned memories viewer may read.
func briefingPins(proj *tracker.Project, v memory.Viewer) memory.Pins {
	pins, _ := memory.SplitPins(briefingMemories(proj, v), memoryPinLimit, memoryPinChars)
	return pins
}

// MemoryPromptSection is the "## Memory" part of a worker's briefing: the
// pinned memories of the issue's scopes (always true, short) and how to
// read and write the rest. The memories matching the issue come from
// issue-cli start, so the briefing stays stable.
func MemoryPromptSection(proj *tracker.Project, issue *tracker.Issue) string {
	v := workerViewer(proj, issue)
	var b strings.Builder
	b.WriteString("\n## Memory\n\n")
	b.WriteString("What earlier agents learned: `issue-cli start` prints the memories matching this issue, `issue-cli memory search <words>` finds more.\n")
	if pins := briefingPins(proj, v); len(pins.Shown) > 0 {
		writePins(&b, pins)
	}
	b.WriteString("Memory is for durable project facts, true across issues: `issue-cli memory add \"<fact>\" --kind fact|convention|pitfall|howto|who` (`--at <status>` for a fact about a step; a similar one makes add refuse: `--updates <id>`, `--supersedes <id>` or `--new`). Notes on this work go in the issue and HANDOFF.md, lessons through the post-mortem. Never put a secret in a memory.\n")
	return b.String()
}

// MemoryPromptIDs is the ids of the pins MemoryPromptSection shows.
func MemoryPromptIDs(proj *tracker.Project, issue *tracker.Issue) []string {
	var ids []string
	for _, m := range briefingPins(proj, workerViewer(proj, issue)).Shown {
		ids = append(ids, m.ID)
	}
	return ids
}

// workerViewer is who reads issue's worker briefing.
func workerViewer(proj *tracker.Project, issue *tracker.Issue) memory.Viewer {
	v := memory.Viewer{Agent: tracker.AgentSessionName(issue.Slug)}
	if proj != nil {
		if d := proj.DepartmentOf(issue); d != "" {
			v.Department = tracker.DepartmentKey(d)
		}
	}
	return v
}

// MemoryLeadLine is the lead brief's line on memory, with the pins the
// lead (name) sees.
func MemoryLeadLine(proj *tracker.Project, name, department string) string {
	v := memory.Viewer{Agent: name, All: department == "", Lead: true}
	if department != "" {
		v.Department = tracker.DepartmentKey(department)
	}
	var b strings.Builder
	b.WriteString("- Memory: issue-cli memory search|list|show reads what agents learned; pin what every briefing should carry (issue-cli memory pin <id>, leads and the owner only; issue-cli memory list --suggested shows the workers' suggestions); your own notebook is --scope agent. A standing rule for every lead and no worker is --scope lead: it stays in this project's log, and neither a secret nor deal, client or price context goes in one.\n")
	lead, rest := splitLeadScope(briefingMemories(proj, v))
	rules, others := memory.SplitPins(lead, leadPinLimit, leadPinChars)
	if len(rules.Shown) > 0 || len(rest) > 0 {
		b.WriteString("  Pins are titles here, with their Never rules in full: read one whole with issue-cli memory show <id> before you act on it.\n")
	}
	if len(rules.Shown) > 0 {
		b.WriteString("  Standing rules (lead scope):\n")
		writePinTitles(&b, rules.Shown)
		writePinsMore(&b, rules)
	}
	if pins, _ := memory.SplitPins(rest, memoryPinLimit, memoryPinChars); len(pins.Shown) > 0 {
		writePinsWith(&b, pins, writePinTitles)
	}
	writeLeadList(&b, others)
	return b.String()
}

// writeLeadList is the unpinned lead memories, one clipped line each.
func writeLeadList(b *strings.Builder, mems []memory.Memory) {
	if len(mems) == 0 {
		return
	}
	b.WriteString("  Lead memories, not pinned (issue-cli memory show <id>):\n")
	for i, m := range mems {
		if i == leadListLimit {
			fmt.Fprintf(b, "- +%d more: issue-cli memory list --scope lead\n", len(mems)-i)
			break
		}
		fmt.Fprintf(b, "- %s (%s) %s\n", m.ID, m.Kind, clipLine(m.Text, leadListChars))
	}
}

// clipLine cuts text to n characters, with an ellipsis when it was longer.
func clipLine(text string, n int) string {
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return strings.TrimRight(string(r[:n]), " ") + "…"
}

// LeadMemoriesLine is the line lead spawn prints: how many lead-scope
// memories a lead started with, and where they are.
func LeadMemoriesLine(proj *tracker.Project) string {
	lead, _ := splitLeadScope(briefingMemories(proj, memory.Viewer{Lead: true}))
	if len(lead) == 0 {
		return "Lead memories: none yet. A standing rule for every lead: issue-cli memory add \"<rule>\" --scope lead"
	}
	pinned := 0
	for _, m := range lead {
		if m.Pinned {
			pinned++
		}
	}
	return fmt.Sprintf("Lead memories: %d (%d pinned, in the brief as titles): issue-cli memory list --scope lead", len(lead), pinned)
}

// writePins is the pins by layer, machine first: the same two layers
// ## Rules shows, one budget between them.
func writePins(b *strings.Builder, pins memory.Pins) {
	writePinsWith(b, pins, writePinList)
}

// writePinsWith is writePins with each layer written by write.
func writePinsWith(b *strings.Builder, pins memory.Pins, write func(*strings.Builder, []memory.Memory)) {
	var machine, project []memory.Memory
	for _, m := range pins.Shown {
		if m.Scope == memory.ScopeMachine {
			machine = append(machine, m)
		} else {
			project = append(project, m)
		}
	}
	if len(machine) > 0 {
		b.WriteString("Pinned on every project:\n")
		write(b, machine)
	}
	if len(project) > 0 {
		b.WriteString("Pinned on this project:\n")
		write(b, project)
	}
	writePinsMore(b, pins)
}

func writePinList(b *strings.Builder, mems []memory.Memory) {
	for _, m := range mems {
		fmt.Fprintf(b, "- %s (%s) %s\n", m.ID, m.Kind, m.Text)
	}
}

// writePinTitles is a lead brief's pins: each one's title and its Never
// sentences (the hard rules a lead must not miss), not the whole text, which
// issue-cli memory show <id> prints (the lead floor report's lever 6).
func writePinTitles(b *strings.Builder, mems []memory.Memory) {
	for _, m := range mems {
		line := pinTitle(m.Text)
		for _, n := range neverSentences(m.Text) {
			if !strings.Contains(line, n) {
				line += " " + n
			}
		}
		fmt.Fprintf(b, "- %s (%s) %s\n", m.ID, m.Kind, line)
	}
}

// Caps on a pin's line in a lead brief.
const (
	pinTitleChars = 120
	pinNeverChars = 200
)

// pinTitle is a memory's first sentence without its parentheses (the
// owner's quote and the source a rule carries), clipped: what the rule is,
// not why.
func pinTitle(text string) string {
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		return ""
	}
	t := sentences[0]
	if bare := dropParens(t); bare != "" {
		t = bare
	}
	return clipLine(t, pinTitleChars)
}

// neverSentences is text's sentences after the first that start with
// "Never", each clipped.
func neverSentences(text string) []string {
	var out []string
	for i, s := range splitSentences(text) {
		if i > 0 && strings.HasPrefix(s, "Never ") {
			out = append(out, clipLine(s, pinNeverChars))
		}
	}
	return out
}

// splitSentences cuts text after each ". ", "! " or "? " outside
// parentheses, so an owner's quoted words don't end a sentence.
func splitSentences(text string) []string {
	var out []string
	depth, start := 0, 0
	r := []rune(text)
	for i, c := range r {
		switch c {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '.', '!', '?':
			if depth == 0 && i+1 < len(r) && r[i+1] == ' ' {
				if s := strings.TrimSpace(string(r[start : i+1])); s != "" {
					out = append(out, s)
				}
				start = i + 1
			}
		}
	}
	if s := strings.TrimSpace(string(r[start:])); s != "" {
		out = append(out, s)
	}
	return out
}

// dropParens is s without its parenthesised parts, spaces tidied.
func dropParens(s string) string {
	var b strings.Builder
	depth := 0
	for _, c := range s {
		switch {
		case c == '(':
			depth++
		case c == ')' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(c)
		}
	}
	t := strings.Join(strings.Fields(b.String()), " ")
	for _, p := range []string{" .", " ,", " ;", " :"} {
		t = strings.ReplaceAll(t, p, p[1:])
	}
	return t
}

func writePinsMore(b *strings.Builder, pins memory.Pins) {
	if pins.More > 0 {
		fmt.Fprintf(b, "- +%d more pinned: issue-cli memory list --pinned\n", pins.More)
	}
}

// memoryNow is the clock pins are judged valid by; a var for tests.
var memoryNow = time.Now
