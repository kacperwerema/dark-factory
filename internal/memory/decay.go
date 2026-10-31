package memory

import (
	"math"
	"time"
)

// Decay (phase 3, LangMem's strength): an old, unused memory ranks below a
// recent or used one. Strength only orders hits; nothing is deleted or
// invalidated by decay, and an expired memory is already invalid.
const (
	// HalfLifeDays: a memory untouched for this long has half its recency.
	HalfLifeDays = 90
	// decayFloor is the share of its match an old memory always keeps, so
	// it still shows when it is the only match.
	decayFloor = 0.3
	// useWeight and useCap shape how much uses push strength up.
	useWeight = 0.15
	useCap    = 1.5
)

// Strength is m's strength at now: recency × usage, between 0 and 1.
// Recency halves every HalfLifeDays from the newest of created, updated
// and last used; uses raise it (log-scaled, capped) back towards 1. A
// pinned memory has strength 1: a lead or the owner chose to keep it in
// front.
func Strength(m Memory, now time.Time) float64 {
	if m.Pinned {
		return 1
	}
	days := m.IdleDays(now)
	recency := math.Pow(0.5, days/HalfLifeDays)
	usage := math.Min(useCap, 1+useWeight*math.Log1p(float64(m.Uses)))
	return math.Min(1, recency*usage)
}

// Touched is the newest of m's created, updated and last-used times.
func (m Memory) Touched() time.Time {
	var best time.Time
	for _, s := range []string{m.Created, m.Updated, m.LastUsed} {
		if t, err := time.Parse(time.RFC3339, s); err == nil && t.After(best) {
			best = t
		}
	}
	return best
}

// IdleDays is how many days since m was last touched at now (0 when its
// times don't parse or lie ahead).
func (m Memory) IdleDays(now time.Time) float64 {
	t := m.Touched()
	if t.IsZero() || !now.After(t) {
		return 0
	}
	return now.Sub(t).Hours() / 24
}

// Decayed wraps a ranker and weighs each hit by its memory's strength at
// Now: score × (0.3 + 0.7 × strength), then re-sorts.
type Decayed struct {
	Inner Ranker
	Now   time.Time
}

// Rank implements Ranker.
func (d Decayed) Rank(q Query, mems []Memory) []Hit {
	inner := d.Inner
	if inner == nil {
		inner = BM25{}
	}
	limit := q.Limit
	q.Limit = 0 // weigh every hit before cutting
	hits := inner.Rank(q, mems)
	for i := range hits {
		hits[i].Score *= decayFloor + (1-decayFloor)*Strength(hits[i].Memory, d.Now)
	}
	return sortHits(hits, limit)
}
