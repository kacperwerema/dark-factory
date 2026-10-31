package lint

// checkMemory reads a workflow file's memory: embedder: (docs/memory.md,
// Semantic search) for settings search would refuse and fall back to BM25
// on: both or neither of url / command, a URL that leaves the machine (a
// hosted embedder is the owner's gate), no model, a bad timeout or
// min_similarity.
func checkMemory(env *Env) []Finding {
	cfg := env.Workflow.MemoryEmbedder()
	if cfg == nil {
		return nil
	}
	where := env.where(env.lineOf("memory:"))
	var out []Finding
	for _, p := range cfg.Problems() {
		out = append(out, Finding{Where: where, Message: p + "; memory search ranks by words only until it is fixed", Fix: "see docs/memory.md, Semantic search"})
	}
	return out
}
