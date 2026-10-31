package memory

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Hit is a memory with its score (the ranker's, or the similarity).
type Hit struct {
	Memory
	Score float64 `json:"score"`
}

// Query is what a ranker matches: words, plus what boosts a memory.
type Query struct {
	Text       string
	System     string
	Department string
	Limit      int
}

// Ranker orders memories for a query: BM25 by default, Hybrid when the
// workflow names a local embedder (phase 2).
type Ranker interface {
	Rank(q Query, mems []Memory) []Hit
}

// DefaultRanker is the ranker the commands and pages use.
var DefaultRanker Ranker = BM25{}

// BM25 ranks by Okapi BM25 over each memory's text, tags and kind, with
// boosts for the query's system and department (mem0's score_and_rank
// adds entity boosts the same way). Memories with no query word score 0
// and are left out.
type BM25 struct{}

const (
	bm25K1      = 1.2
	bm25B       = 0.75
	systemBoost = 0.3
	deptBoost   = 0.2
)

// Rank implements Ranker.
func (BM25) Rank(q Query, mems []Memory) []Hit {
	scores := bm25Scores(q.Text, mems)
	if scores == nil {
		return nil
	}
	var hits []Hit
	for i, m := range mems {
		if scores[i] == 0 {
			continue
		}
		hits = append(hits, Hit{Memory: m, Score: boost(q, m, scores[i])})
	}
	return sortHits(hits, q.Limit)
}

// bm25Scores is each memory's BM25 score for text, with no boosts; nil
// when text has no query word or there are no memories.
func bm25Scores(text string, mems []Memory) []float64 {
	qw := Words(text)
	if len(qw) == 0 || len(mems) == 0 {
		return nil
	}
	docs := make([][]string, len(mems))
	df := map[string]int{}
	total := 0
	for i, m := range mems {
		docs[i] = tokens(m.Text + " " + strings.Join(m.Tags, " ") + " " + m.Kind)
		total += len(docs[i])
		seen := map[string]bool{}
		for _, w := range docs[i] {
			if !seen[w] {
				seen[w] = true
				df[w]++
			}
		}
	}
	avg := float64(total) / float64(len(mems))
	if avg == 0 {
		avg = 1
	}
	n := float64(len(mems))
	scores := make([]float64, len(mems))
	for i := range mems {
		tf := map[string]int{}
		for _, w := range docs[i] {
			tf[w]++
		}
		score := 0.0
		for _, w := range qw {
			f := float64(tf[w])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[w])+0.5)/(float64(df[w])+0.5))
			score += idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(len(docs[i]))/avg))
		}
		scores[i] = score
	}
	return scores
}

// boost raises score for a memory on the query's system or department.
func boost(q Query, m Memory, score float64) float64 {
	if q.System != "" && strings.EqualFold(m.System, q.System) {
		score *= 1 + systemBoost
	}
	if q.Department != "" && strings.EqualFold(m.ScopeDepartment(), q.Department) {
		score *= 1 + deptBoost
	}
	return score
}

// sortHits orders hits best first, newest first on a tie, and keeps limit.
func sortHits(hits []Hit, limit int) []Hit {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Changed() > hits[j].Changed()
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// stopWords are left out of matching: they say nothing about a topic.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"from": true, "into": true, "are": true, "was": true, "not": true, "but": true,
	"its": true, "you": true, "your": true, "has": true, "have": true, "when": true,
	"then": true, "than": true, "all": true, "any": true, "can": true, "one": true,
	"out": true, "who": true, "what": true, "how": true, "via": true, "between": true,
	"fact": true,
}

// tokens are s's words with plurals folded, stop words and short words
// dropped, repeats kept (term frequency).
func tokens(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		w = stem(w)
		if len([]rune(w)) < 3 || stopWords[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// Words are s's distinct tokens in order.
func Words(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range tokens(s) {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

func stem(w string) string {
	if len(w) > 4 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return w[:len(w)-1]
	}
	return w
}
