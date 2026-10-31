package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/memory"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// memory entities: who knows what. The index of #topics and @names over
// the memories the caller can see, folded on each call (phase 4,
// factory/shared-memory-phase-4-entities-and-who-knows-what).
func runMemoryEntities(ctx *Context, args []string) error {
	fs := newFlagSet("memory entities", ctx)
	f, apply := filterFlags(fs)
	only := fs.String("only", "", "only topics (#…) or agents (@…): topic or agent")
	if err := parseFlags(ctx, fs, args); err != nil {
		return err
	}
	apply()
	if err := f.check(); err != nil {
		return err
	}
	switch *only {
	case "", memory.EntityTopic, memory.EntityAgent:
	default:
		return &usageError{msg: fmt.Sprintf("--only %q: topic or agent", *only)}
	}
	issues, _ := tracker.LoadIssues(ctx.Project.IssueDir)
	mems, err := visibleMemories(ctx, memoryCaller(ctx.Project, issues).Viewer, *f)
	if err != nil {
		return err
	}
	var idx []memory.Entity
	for _, e := range memory.Index(mems) {
		if *only == "" || e.Kind == *only {
			idx = append(idx, e)
		}
	}
	if ctx.JSONOutput {
		if idx == nil {
			idx = []memory.Entity{}
		}
		return writeJSON(ctx.Stdout, idx)
	}
	w := ctx.Stdout
	if len(idx) == 0 {
		fmt.Fprintf(w, "No entities in the %d memories you can see. Write #topic or @agent in a memory, or tag it:\n", len(mems))
		fmt.Fprintln(w, `  issue-cli memory add "@lead-dark-factory knows the #playtest runner" --kind who`)
		return nil
	}
	for _, e := range idx {
		line := fmt.Sprintf("%-28s %s (%s)", e.Name, fmt.Sprintf("%d %s", len(e.IDs), pluralWord(len(e.IDs), "memory", "memories")), e.KindsLine())
		if len(e.Who) > 0 {
			line += " · known by " + strings.Join(e.Who, ", ")
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "One entity's memories: issue-cli memory search %s\n", idx[0].Name)
	return nil
}

// printEntityHint says which entities a new memory carries, and how a who
// memory links an agent to a topic when it doesn't (a hint, never a
// refusal: #D-3).
func printEntityHint(w io.Writer, m memory.Memory) {
	ents := m.Entities()
	if len(ents) > 0 {
		fmt.Fprintf(w, "Entities: %s (issue-cli memory search %s)\n", strings.Join(ents, " "), ents[0])
	}
	if m.Kind != memory.KindWho {
		return
	}
	var agent, topic bool
	for _, e := range ents {
		agent = agent || memory.EntityKind(e) == memory.EntityAgent
		topic = topic || memory.EntityKind(e) == memory.EntityTopic
	}
	if !agent || !topic {
		fmt.Fprintf(w, "Hint: a who memory links @<agent or person> to #<topic>, so memory search @x and memory entities find it: issue-cli memory edit %s --text \"@lead-x knows the #playtest runner\"\n", m.ID)
	}
}
