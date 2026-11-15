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
// lead's standing rules, shown in full on their own budget, so project pins
// can't crowd them out; the unpinned ones get one clipped line each.
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
	b.WriteString("Shared memory holds what earlier agents learned, with its source. `issue-cli start` prints the memories matching this issue; `issue-cli memory search <words>` finds more.\n")
	if pins := briefingPins(proj, v); len(pins.Shown) > 0 {
		writePins(&b, pins)
	}
	b.WriteString("Memory is for durable project facts, true across issues. Notes on this work go in the issue file and HANDOFF.md, and lessons for later go through the post-mortem. When you learn such a fact (a convention, a pitfall, how something is done, who knows what), add it: `issue-cli memory add \"<fact>\" --kind fact|convention|pitfall|howto|who`. If a similar one exists, add refuses and lists it: update it (`--updates <id>`), replace it (`--supersedes <id>`) or add yours anyway (`--new`). A fact about a step, not a subject: add `--at <status>`, and transition prints it to whoever enters that status. Never put a secret in a memory.\n")
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
	if len(rules.Shown) > 0 {
		b.WriteString("  Standing rules (lead scope):\n")
		writePinList(&b, rules.Shown)
		writePinsMore(&b, rules)
	}
	if pins, _ := memory.SplitPins(rest, memoryPinLimit, memoryPinChars); len(pins.Shown) > 0 {
		writePins(&b, pins)
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
	return fmt.Sprintf("Lead memories: %d (%d pinned, in the brief in full): issue-cli memory list --scope lead", len(lead), pinned)
}

// writePins is the pins by layer, machine first: the same two layers
// ## Rules shows, one budget between them.
func writePins(b *strings.Builder, pins memory.Pins) {
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
		writePinList(b, machine)
	}
	if len(project) > 0 {
		b.WriteString("Pinned on this project:\n")
		writePinList(b, project)
	}
	writePinsMore(b, pins)
}

func writePinList(b *strings.Builder, mems []memory.Memory) {
	for _, m := range mems {
		fmt.Fprintf(b, "- %s (%s) %s\n", m.ID, m.Kind, m.Text)
	}
}

func writePinsMore(b *strings.Builder, pins memory.Pins) {
	if pins.More > 0 {
		fmt.Fprintf(b, "- +%d more pinned: issue-cli memory list --pinned\n", pins.More)
	}
}

// memoryNow is the clock pins are judged valid by; a var for tests.
var memoryNow = time.Now
