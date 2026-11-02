package tracker

import (
	"fmt"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
)

// Briefing limits for pinned memories (Letta's core memory budget).
const (
	memoryPinLimit = 10
	memoryPinChars = 1500
)

// briefingPins is the pinned memories viewer may read in the project's
// and the machine's store.
func briefingPins(proj *Project, v memory.Viewer) memory.Pins {
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
		if m.Pinned && m.VisibleTo(v) && m.Valid(memoryNow()) {
			keep = append(keep, m)
		}
	}
	pins, _ := memory.SplitPins(keep, memoryPinLimit, memoryPinChars)
	return pins
}

// MemoryPromptSection is the "## Memory" part of a worker's briefing: the
// pinned memories of the issue's scopes (always true, short) and how to
// read and write the rest. The memories matching the issue come from
// issue-cli start, so the briefing stays stable.
func MemoryPromptSection(proj *Project, issue *Issue) string {
	v := memory.Viewer{Agent: AgentSessionName(issue.Slug)}
	if proj != nil {
		if d := proj.DepartmentOf(issue); d != "" {
			v.Department = DepartmentKey(d)
		}
	}
	var b strings.Builder
	b.WriteString("\n## Memory\n\n")
	b.WriteString("Shared memory holds what earlier agents learned, with its source. `issue-cli start` prints the memories matching this issue; `issue-cli memory search <words>` finds more.\n")
	if pins := briefingPins(proj, v); len(pins.Shown) > 0 {
		b.WriteString("Pinned (always true here):\n")
		writePins(&b, pins)
	}
	b.WriteString("When you learn something the next agent should know (a convention, a pitfall, how something is done, who knows what), add it: `issue-cli memory add \"<fact>\" --kind fact|convention|pitfall|howto|who`. If a similar one exists, add refuses and lists it: update it (`--updates <id>`), replace it (`--supersedes <id>`) or add yours anyway (`--new`). Never put a secret in a memory.\n")
	return b.String()
}

// MemoryLeadLine is the lead brief's line on memory, with the pins the
// lead (name) sees.
func MemoryLeadLine(proj *Project, name, department string) string {
	v := memory.Viewer{Agent: name, All: department == ""}
	if department != "" {
		v.Department = DepartmentKey(department)
	}
	var b strings.Builder
	b.WriteString("- Memory: issue-cli memory search|list|show reads what agents learned; pin what every briefing should carry (issue-cli memory pin <id>, leads and the owner only; issue-cli memory list --suggested shows the workers' suggestions); your own notebook is --scope agent.\n")
	if pins := briefingPins(proj, v); len(pins.Shown) > 0 {
		b.WriteString("  Pinned:\n")
		writePins(&b, pins)
	}
	return b.String()
}

func writePins(b *strings.Builder, pins memory.Pins) {
	for _, m := range pins.Shown {
		fmt.Fprintf(b, "- %s (%s) %s\n", m.ID, m.Kind, m.Text)
	}
	if pins.More > 0 {
		fmt.Fprintf(b, "- +%d more pinned: issue-cli memory list --pinned\n", pins.More)
	}
}

// memoryNow is the clock pins are judged valid by; a var for tests.
var memoryNow = time.Now
