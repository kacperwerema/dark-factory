package lint

import (
	"fmt"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// checkMemory reads a workflow file's memory: embedder: (docs/memory.md,
// Semantic search) for settings search would refuse and fall back to BM25
// on: both or neither of url / command, a URL that leaves the machine (a
// hosted embedder is the owner's gate), no model, a bad timeout or
// min_similarity. It also finds memories tied to a status the project no
// longer has, which nobody is shown any more, memories that read as a
// note on one issue rather than a durable project fact, clusters of
// memories retelling one fact, and pitfalls or howtos about a step with no
// --at, which transition never prints at that step.
func checkMemory(env *Env) []Finding {
	var out []Finding
	if set := projectMemories(env); set != nil {
		out = append(out, memoryAtFindings(env, set)...)
		out = append(out, memoryNoteFindings(env, set)...)
		out = append(out, memoryClusterFindings(env, set)...)
		out = append(out, memoryStepFindings(env, set)...)
	}
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

// projectMemories is the project's memory log, nil when there is none to
// read. The memories are the project's, so a department's file (its own
// Env) doesn't list them again.
func projectMemories(env *Env) *memory.Set {
	if env.Project == nil || env.Project.IssueDir == "" || env.Department != "" {
		return nil
	}
	set, err := memory.ProjectStore(env.Project.IssueDir).Load()
	if err != nil {
		return nil
	}
	return set
}

// memoryAtFindings is one finding per valid project memory whose --at names
// no status.
func memoryAtFindings(env *Env, set *memory.Set) []Finding {
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
func memoryNoteFindings(env *Env, set *memory.Set) []Finding {
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

// memoryClusterFindings is one finding per group of near-duplicate project
// memories (memory.NearDuplicates): one fact written several times in other
// words, so a worker shown one of them misses what the others add. Ids
// only: a lead memory's text stays out of the output.
func memoryClusterFindings(env *Env, set *memory.Set) []Finding {
	var out []Finding
	for _, c := range memory.NearDuplicates(set.Active(env.Now), env.Now) {
		ids := c.IDs()
		who := ""
		if c.Scope == memory.ScopeLead {
			who = " (lead scope: a lead or the owner merges them)"
		}
		out = append(out, Finding{
			Where:   ids[0],
			Message: fmt.Sprintf("memories %s (%d, %s scope) share most of their words (overlap %.2f): one fact written %d times, and a worker shown one of them misses the rest%s", strings.Join(ids, ", "), len(ids), c.Scope, c.Similarity, len(ids), who),
			Fix:     fmt.Sprintf("merge what is one fact: issue-cli memory edit %s --text \"<the one fact>\" [--at <status>], then issue-cli memory forget <id> --reason \"merged into %s\" for each other; reword a memory that only shares a sentence", ids[0], ids[0]),
		})
	}
	return out
}

// memoryStepFindings is one finding per valid project pitfall or howto
// with no --at whose text names a step of the workflow (memory.NamesStep
// over the project's open statuses): transition prints a memory at a status
// only when it is tied there (docs/memory.md, At a status).
func memoryStepFindings(env *Env, set *memory.Set) []Finding {
	var steps []string
	for _, s := range env.Project.AllStatuses() {
		if !tracker.IsClosedStatus(env.Workflow, s) {
			steps = append(steps, s)
		}
	}
	var out []Finding
	for _, m := range set.Active(env.Now) {
		if m.At != "" || m.Scope != memory.ScopeProject || (m.Kind != memory.KindPitfall && m.Kind != memory.KindHowto) {
			continue
		}
		st := memory.NamesStep(m.Text, steps)
		if st == "" {
			continue
		}
		out = append(out, Finding{
			Where:   m.ID,
			Message: fmt.Sprintf("memory %s (%s) is about the %s step but has no --at: transition into %s never prints it", m.ID, m.Kind, st, st),
			Fix:     fmt.Sprintf("issue-cli memory edit %s --at %s", m.ID, shell.Quote(st)),
		})
	}
	return out
}
