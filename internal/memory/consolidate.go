package memory

import (
	"sort"
	"time"
)

// Consolidation (phase 3, LangMem's background manager and A-MEM's note
// evolution, done by an agent): the pack lists what an agent could merge,
// retire or promote. It writes nothing; the agent acts through the update
// step (add --supersedes id,id, --updates, forget), and nothing here
// deletes.

// Thresholds of the pack.
const (
	// StaleStrength: an active, unpinned memory weaker than this is listed
	// as stale (about 180 days untouched with no uses).
	StaleStrength = 0.25
	// PromoteUses and PromoteDays: a project memory used this often and at
	// least this old is proposed for CLAUDE.md.
	PromoteUses = 5
	PromoteDays = 30
	// MaxClusters caps the pack's clusters.
	MaxClusters = 20
)

// Cluster is near-duplicate active memories in one scope.
type Cluster struct {
	Scope   string   `json:"scope"`
	Members []Memory `json:"members"`
	// Similarity is the highest word overlap between two members.
	Similarity float64 `json:"similarity"`
}

// IDs are the members' ids, oldest first.
func (c Cluster) IDs() []string {
	out := make([]string, len(c.Members))
	for i, m := range c.Members {
		out[i] = m.ID
	}
	return out
}

// Pack is what a consolidation run reviews.
type Pack struct {
	Clusters []Cluster `json:"clusters"`
	// More is how many clusters were left out past MaxClusters.
	More    int      `json:"more,omitempty"`
	Stale   []Memory `json:"stale"`
	Expired []Memory `json:"expired"`
	Promote []Memory `json:"promote"`
}

// Consolidate builds the pack over mems (the memories the caller may see,
// invalid ones included) at now.
func Consolidate(mems []Memory, now time.Time) Pack {
	var p Pack
	var active []Memory
	for _, m := range mems {
		switch {
		case m.InvalidAt != "":
		case m.Expired(now):
			p.Expired = append(p.Expired, m)
		default:
			active = append(active, m)
		}
	}
	sort.SliceStable(active, func(i, j int) bool { return active[i].Created < active[j].Created })
	p.Clusters, p.More = clusters(active)
	for _, m := range active {
		if !m.Pinned && Strength(m, now) < StaleStrength {
			p.Stale = append(p.Stale, m)
		}
		if promotable(m, now) {
			p.Promote = append(p.Promote, m)
		}
	}
	return p
}

func promotable(m Memory, now time.Time) bool {
	if m.Scope != ScopeProject || m.Uses < PromoteUses {
		return false
	}
	switch m.Kind {
	case KindConvention, KindPitfall, KindHowto:
	default:
		return false
	}
	c, err := time.Parse(time.RFC3339, m.Created)
	return err == nil && now.Sub(c) >= PromoteDays*24*time.Hour
}

// clusters joins active memories of one scope whose words overlap at
// least SimilarThreshold (the update step's near-duplicate line), biggest
// cluster first.
func clusters(active []Memory) ([]Cluster, int) {
	byScope := map[string][]int{}
	var scopes []string
	for i, m := range active {
		if _, ok := byScope[m.Scope]; !ok {
			scopes = append(scopes, m.Scope)
		}
		byScope[m.Scope] = append(byScope[m.Scope], i)
	}
	sets := wordIDs(active)
	parent := make([]int, len(active))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for _, sc := range scopes {
		idx := byScope[sc]
		for a := 0; a < len(idx); a++ {
			for b := a + 1; b < len(idx); b++ {
				i, j := idx[a], idx[b]
				if jaccard(sets[i], sets[j]) < SimilarThreshold {
					continue
				}
				if ri, rj := find(i), find(j); ri != rj {
					parent[rj] = ri
				}
			}
		}
	}
	groups := map[int][]int{}
	var roots []int
	for i := range active {
		r := find(i)
		if _, ok := groups[r]; !ok {
			roots = append(roots, r)
		}
		groups[r] = append(groups[r], i)
	}
	var out []Cluster
	for _, r := range roots {
		g := groups[r]
		if len(g) < 2 {
			continue
		}
		c := Cluster{Scope: active[g[0]].Scope}
		for a, i := range g {
			c.Members = append(c.Members, active[i])
			for _, j := range g[a+1:] {
				if sim := jaccard(sets[i], sets[j]); sim > c.Similarity {
					c.Similarity = sim
				}
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Members) != len(out[j].Members) {
			return len(out[i].Members) > len(out[j].Members)
		}
		return out[i].Similarity > out[j].Similarity
	})
	more := 0
	if len(out) > MaxClusters {
		more = len(out) - MaxClusters
		out = out[:MaxClusters]
	}
	return out, more
}

// wordIDs is each memory's distinct words as sorted ids, so a pair's
// overlap is one merge walk (5,000 memories are 12.5M pairs).
func wordIDs(mems []Memory) [][]int32 {
	ids := map[string]int32{}
	out := make([][]int32, len(mems))
	for i, m := range mems {
		for _, w := range Words(m.Text) {
			id, ok := ids[w]
			if !ok {
				id = int32(len(ids))
				ids[w] = id
			}
			out[i] = append(out[i], id)
		}
		sort.Slice(out[i], func(a, b int) bool { return out[i][a] < out[i][b] })
	}
	return out
}

// jaccard is the overlap of two sorted word-id sets (Similarity on their
// texts), or 0 when their sizes alone keep it under SimilarThreshold.
func jaccard(a, b []int32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// |a∩b| ≤ min, so the overlap can't reach the threshold when the sizes
	// differ more than that: skip the walk.
	lo, hi := len(a), len(b)
	if lo > hi {
		lo, hi = hi, lo
	}
	if float64(lo)/float64(hi) < SimilarThreshold {
		return 0
	}
	inter, i, j := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			inter++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}
