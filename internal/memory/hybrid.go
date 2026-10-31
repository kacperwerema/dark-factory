package memory

import (
	"context"
	"fmt"
	"math"
)

// MaxEmbedPerCall is how many memories without a vector one ranking
// embeds; the rest rank by words this time and fill in on later calls.
const MaxEmbedPerCall = 64

// Hybrid ranks by words and meaning: BM25 normalised to the call's best,
// plus the cosine between the query's vector and each memory's (mem0's
// score_and_rank), then the BM25 boosts. Before each embed request it asks
// mem-guard; at GuardLimit or more, or when the embedder fails, the call
// ranks by BM25 alone. Note says which happened
// (factory/shared-memory-phase-2-local-embeddings-behind-the-embedder-seam#D-4).
type Hybrid struct {
	Embedder Embedder
	Cache    VectorCache
	// MinSimilarity: a cosine below it adds nothing.
	MinSimilarity float64
	// MaxEmbed caps the missing vectors embedded per call (0: MaxEmbedPerCall).
	MaxEmbed int
	// Refused, when set, is why the embedder can't be used at all (a
	// hosted URL, a broken config): every call is BM25 and says why.
	Refused string

	// Note is what the last Rank did: "words + meaning (<model>)" or
	// "words only: <reason>".
	Note string
	// Embedded counts the memory vectors the last Rank asked for.
	Embedded int
}

// Weights of the two parts of a hybrid score.
const (
	wordWeight    = 0.5
	meaningWeight = 0.5
)

// Rank implements Ranker.
func (h *Hybrid) Rank(q Query, mems []Memory) []Hit {
	h.Note, h.Embedded = "", 0
	if h.Refused != "" {
		h.Note = "words only: " + h.Refused
		return BM25{}.Rank(q, mems)
	}
	if len(Words(q.Text)) == 0 || len(mems) == 0 {
		return nil
	}
	qv, vecs, err := h.vectors(q.Text, mems)
	if err != nil {
		h.Note = "words only: " + err.Error()
		return BM25{}.Rank(q, mems)
	}
	h.Note = "words + meaning (" + h.Cache.Model + ")"
	words := bm25Scores(q.Text, mems)
	best := 0.0
	for _, s := range words {
		best = math.Max(best, s)
	}
	minSim := h.MinSimilarity
	if minSim <= 0 {
		minSim = 0.5
	}
	var hits []Hit
	for i, m := range mems {
		w := 0.0
		if best > 0 {
			w = words[i] / best
		}
		c := 0.0
		if vecs[i] != nil {
			if c = Cosine(qv, vecs[i]); c < minSim {
				c = 0
			}
		}
		score := wordWeight*w + meaningWeight*c
		if score == 0 {
			continue
		}
		hits = append(hits, Hit{Memory: m, Score: boost(q, m, score)})
	}
	return sortHits(hits, q.Limit)
}

// vectors returns the query's vector and each memory's (nil for those not
// embedded yet), embedding up to MaxEmbed missing ones in the same request
// as the query.
func (h *Hybrid) vectors(text string, mems []Memory) ([]float32, [][]float32, error) {
	if h.Embedder == nil {
		return nil, nil, fmt.Errorf("no embedder")
	}
	if err := guardOK(); err != nil {
		return nil, nil, err
	}
	limit := h.MaxEmbed
	if limit <= 0 {
		limit = MaxEmbedPerCall
	}
	vecs := make([][]float32, len(mems))
	texts := []string{text}
	var missing []int
	for i, m := range mems {
		if v := h.Cache.Get(hashOf(m)); v != nil {
			vecs[i] = v
		} else if len(missing) < limit {
			missing = append(missing, i)
			texts = append(texts, m.Text)
		}
	}
	got, err := h.Embedder.Embed(context.Background(), texts)
	if err != nil {
		return nil, nil, fmt.Errorf("embedder: %v", err)
	}
	qv := got[0]
	for k, i := range missing {
		v := got[k+1]
		if len(v) != len(qv) {
			return nil, nil, fmt.Errorf("embedder: vectors of %d and %d numbers", len(qv), len(v))
		}
		vecs[i] = v
		_ = h.Cache.Put(hashOf(mems[i]), v)
	}
	h.Embedded = len(missing)
	for i, v := range vecs {
		if v != nil && len(v) != len(qv) {
			// A cached vector from another model version: skip it.
			vecs[i] = nil
		}
	}
	return qv, vecs, nil
}

// hashOf is m's text hash, computed when the log has none.
func hashOf(m Memory) string {
	if m.Hash != "" {
		return m.Hash
	}
	return Hash(m.Text)
}

// Cosine is the cosine similarity of a and b, 0 when either is zero or
// their lengths differ.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}
