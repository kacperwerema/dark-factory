package memory

import (
	"testing"
	"time"
)

func memAt(id, text, created string, uses int, lastUsed string) Memory {
	return Memory{ID: id, Text: text, Kind: KindFact, Scope: ScopeProject, Created: created, Uses: uses, LastUsed: lastUsed, Hash: Hash(text)}
}

func TestStrengthHalvesEveryNinetyDaysAndUsesRaiseIt(t *testing.T) {
	now := t0
	day := func(d int) string { return now.Add(-time.Duration(d) * 24 * time.Hour).Format(time.RFC3339) }
	fresh := Strength(memAt("m-1", "x", day(0), 0, ""), now)
	half := Strength(memAt("m-2", "x", day(90), 0, ""), now)
	used := Strength(memAt("m-3", "x", day(90), 6, ""), now)
	recentUse := Strength(memAt("m-4", "x", day(400), 1, day(1)), now)
	if fresh != 1 {
		t.Errorf("fresh strength %v, want 1", fresh)
	}
	if half < 0.49 || half > 0.51 {
		t.Errorf("90 days strength %v, want 0.5", half)
	}
	if used <= half {
		t.Errorf("uses should raise strength: %v <= %v", used, half)
	}
	if recentUse < 0.95 {
		t.Errorf("a use yesterday makes it recent: %v", recentUse)
	}
	pinned := memAt("m-5", "x", day(900), 0, "")
	pinned.Pinned = true
	if Strength(pinned, now) != 1 {
		t.Errorf("pinned keeps full strength")
	}
}

// AC3: an old, unused memory ranks below a recent, used one with the same
// words; it still shows (decay orders, it does not hide or delete).
func TestOldUnusedMemoriesRankBelowRecentUsedOnes(t *testing.T) {
	day := func(d int) string { return t0.Add(-time.Duration(d) * 24 * time.Hour).Format(time.RFC3339) }
	old := memAt("m-old", "the playtest runner finds chrome for testing by itself", day(400), 0, "")
	recent := memAt("m-new", "the playtest runner finds chrome for testing by itself", day(20), 3, day(2))
	mems := []Memory{old, recent}
	plain := BM25{}.Rank(Query{Text: "playtest chrome"}, mems)
	if len(plain) != 2 || plain[0].Score != plain[1].Score {
		t.Fatalf("same words should tie before decay: %+v", plain)
	}
	hits := Decayed{Inner: BM25{}, Now: t0}.Rank(Query{Text: "playtest chrome", Limit: 5}, []Memory{old, recent})
	if len(hits) != 2 || hits[0].ID != "m-new" || hits[1].ID != "m-old" {
		t.Fatalf("decayed order %+v, want m-new then m-old", hits)
	}
	if hits[1].Score < plain[1].Score*decayFloor*0.99 {
		t.Errorf("an old memory keeps at least %.0f%% of its match: %v of %v", decayFloor*100, hits[1].Score, plain[1].Score)
	}
	// A much better match still wins over a fresher weak one.
	strong := memAt("m-strong", "playtest chrome playtest chrome runner", day(200), 0, "")
	weak := memAt("m-weak", "chrome", day(0), 0, "")
	filler := memAt("m-fill", "unrelated words about the board", day(0), 0, "")
	hits = Decayed{Inner: BM25{}, Now: t0}.Rank(Query{Text: "playtest chrome", Limit: 1}, []Memory{strong, weak, filler})
	if len(hits) != 1 || hits[0].ID != "m-strong" {
		t.Errorf("limit applies after decay and a strong match wins: %+v", hits)
	}
}

// Decay deletes nothing and expiry is honoured: an expired memory is not
// valid, so it never reaches a ranker, and the log keeps it.
func TestDecayNeverDeletesAndExpiresIsHonoured(t *testing.T) {
	s := newStore(t)
	add(t, s, Draft{Text: "the release branch is cut on fridays", Expires: "2026-10-01"}, AddOptions{})
	add(t, s, Draft{Text: "make shot takes one screenshot of one page"}, AddOptions{})
	set := load(t, s)
	later := t0.Add(5 * 365 * 24 * time.Hour)
	if n := len(set.Active(later)); n != 1 {
		t.Fatalf("active %d, want 1 (the expired one is invalid)", n)
	}
	if n := len(set.All()); n != 2 {
		t.Fatalf("all %d, want 2: nothing deleted", n)
	}
	hits := Decayed{Inner: BM25{}, Now: later}.Rank(Query{Text: "screenshot page"}, set.Active(later))
	if len(hits) != 1 {
		t.Fatalf("a five-year-old memory still ranks: %+v", hits)
	}
}
