package main

import (
	"strings"

	"github.com/michal-franc/issue-viewer/internal/memory"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// memoryRanker is the ranker for ctx's project: BM25 when its workflow
// names no memory: embedder:, else a Hybrid over the local embedder with
// the project's vector cache (docs/memory.md, Semantic search). The
// second value is the Hybrid, nil for BM25, so search can print its note.
//
// Either way the ranker is wrapped in memory.Decayed at ctx's clock, so an
// old, unused memory ranks below a recent or used one (phase 3).
func memoryRanker(ctx *Context) (memory.Ranker, *memory.Hybrid) {
	inner, h := baseRanker(ctx)
	return memory.Decayed{Inner: inner, Now: ctx.now()}, h
}

// baseRanker is BM25 or the Hybrid, before decay.
func baseRanker(ctx *Context) (memory.Ranker, *memory.Hybrid) {
	p := ctx.Project
	if p == nil {
		return memory.DefaultRanker, nil
	}
	cfg := p.LoadWorkflow().MemoryEmbedder()
	if cfg == nil {
		return memory.DefaultRanker, nil
	}
	h := newHybrid(cfg, p.Root(), p.IssueDir)
	return h, h
}

// newHybrid builds the Hybrid for cfg; a config with problems gives one
// that ranks by BM25 and says why.
func newHybrid(cfg *tracker.EmbedderConfig, root, issueDir string) *memory.Hybrid {
	h := &memory.Hybrid{
		Cache:         memory.VectorCache{Dir: memory.VectorDir(issueDir), Model: cfg.Model},
		MinSimilarity: cfg.MinSimilarityOrDefault(),
	}
	if probs := cfg.Problems(); len(probs) > 0 {
		h.Refused = strings.TrimPrefix(probs[0], "memory embedder: ") + " (issue-cli health lint --check memory)"
		return h
	}
	if strings.TrimSpace(cfg.URL) != "" {
		h.Embedder = memory.HTTPEmbedder{URL: strings.TrimSpace(cfg.URL), Model: cfg.Model, Timeout: cfg.TimeoutOrDefault()}
	} else {
		h.Embedder = memory.CommandEmbedder{Argv: cfg.Command, Dir: root, Model: cfg.Model, Timeout: cfg.TimeoutOrDefault()}
	}
	return h
}
