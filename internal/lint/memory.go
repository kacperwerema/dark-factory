package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/memory"
)

// checkMemory reads a workflow file's memory: embedder: (docs/memory.md,
// Semantic search) for settings search would refuse and fall back to BM25
// on: both or neither of url / command, a URL that leaves the machine (a
// hosted embedder is the owner's gate), no model, a bad timeout or
// min_similarity. It also finds memories tied to a status the project no
// longer has, which nobody is shown any more, and memories that read as a
// note on one issue rather than a durable project fact.
func checkMemory(env *Env) []Finding {
	out := memoryAtFindings(env)
	out = append(out, memoryNoteFindings(env)...)
	cfg := env.Workflow.MemoryEmbedder()
	if cfg == nil {
		return out
	}
	where := env.where(env.lineOf("memory:"))
	for _, p := range cfg.Problems() {
		out = append(out, Finding{Where: where, Message: p + "; memory search ranks by words only until it is fixed", Fix: "see docs/memory.md, Semantic search"})
	}
	return out
}

// memoryAtFindings is one finding per valid project memory whose --at names
// no status. The memories are the project's, so a department's file (its own
// Env) doesn't list them again.
func memoryAtFindings(env *Env) []Finding {
	if env.Project == nil || env.Project.IssueDir == "" || env.Department != "" {
		return nil
	}
	set, err := memory.ProjectStore(env.Project.IssueDir).Load()
	if err != nil {
		return nil
	}
	names := env.Project.AllStatuses()
	known := make(map[string]bool, len(names))
	for _, s := range names {
		known[strings.ToLower(s)] = true
	}
	var out []Finding
	for _, m := range set.Active(env.Now) {
		if m.At != "" && !known[strings.ToLower(m.At)] {
			out = append(out, Finding{
				Where:   m.ID,
				Message: fmt.Sprintf("memory %s matters at %q, a status no workflow here has: no transition prints it", m.ID, m.At),
				Fix:     fmt.Sprintf("issue-cli memory edit %s --at <status> (or --at none)", m.ID),
			})
		}
	}
	return out
}

// memoryNoteFindings is one finding per valid project memory whose text
// reads as a note on one issue (memory.IssueNote with its source issue, or
// memory.NamesIssue over the board): memory keeps durable facts, the issue
// file and HANDOFF.md the current work (docs/memory.md, Memory as a cache).
func memoryNoteFindings(env *Env) []Finding {
	if env.Project == nil || env.Project.IssueDir == "" || env.Department != "" {
		return nil
	}
	set, err := memory.ProjectStore(env.Project.IssueDir).Load()
	if err != nil {
		return nil
	}
	slugs := make([]string, 0, len(env.Issues))
	for _, is := range env.Issues {
		slugs = append(slugs, is.Slug)
	}
	var out []Finding
	for _, m := range set.Active(env.Now) {
		why := memory.IssueNote(m.Text, m.Source.Issue)
		if why == "" {
			why = memory.NamesIssue(m.Text, slugs)
		}
		if why == "" {
			continue
		}
		out = append(out, Finding{
			Where:   m.ID,
			Message: fmt.Sprintf("memory %s reads as a note on one issue (%s): memory keeps durable project facts", m.ID, why),
			Fix:     fmt.Sprintf("issue-cli memory edit %s --text \"<the general fact>\" (or issue-cli memory forget %s --reason \"a note on one issue\")", m.ID, m.ID),
		})
	}
	return out
}
