package memory

import (
	"strings"
	"testing"
	"time"
)

func TestConsolidatePackFindsClustersStaleExpiredAndPromotions(t *testing.T) {
	day := func(d int) string { return t0.Add(-time.Duration(d) * 24 * time.Hour).Format(time.RFC3339) }
	a := memAt("m-a", "make shot takes one screenshot of one page", day(10), 0, "")
	b := memAt("m-b", "make shot takes a screenshot of one page quickly", day(9), 0, "")
	c := memAt("m-c", "make shot takes one screenshot of a page", day(8), 0, "")
	other := memAt("m-d", "make shot takes one screenshot of one page", day(8), 0, "")
	other.Scope = DepartmentScope("marketing") // another scope: never merged with a, b, c
	stale := memAt("m-e", "the old board used port 8090 for the viewer", day(300), 0, "")
	expired := memAt("m-f", "the freeze lasts until october", day(40), 0, "")
	expired.Expires = "2026-10-15"
	promo := memAt("m-g", "run make validate before proposing a cli change", day(60), 7, day(1))
	promo.Kind = KindConvention
	gone := memAt("m-h", "make shot takes one screenshot of one page", day(5), 0, "")
	gone.InvalidAt = day(4)
	pack := Consolidate([]Memory{a, b, c, other, stale, expired, promo, gone}, t0)

	if len(pack.Clusters) != 1 {
		t.Fatalf("clusters %+v, want one", pack.Clusters)
	}
	if ids := strings.Join(pack.Clusters[0].IDs(), ","); ids != "m-a,m-b,m-c" {
		t.Errorf("cluster ids %s, want m-a,m-b,m-c (one scope, valid only, oldest first)", ids)
	}
	if len(pack.Stale) != 1 || pack.Stale[0].ID != "m-e" {
		t.Errorf("stale %+v, want m-e", pack.Stale)
	}
	if len(pack.Expired) != 1 || pack.Expired[0].ID != "m-f" {
		t.Errorf("expired %+v, want m-f", pack.Expired)
	}
	if len(pack.Promote) != 1 || pack.Promote[0].ID != "m-g" {
		t.Errorf("promote %+v, want m-g", pack.Promote)
	}
}

// AC2: a merge goes through the update step: one new fact supersedes the
// cluster, the old ones stay in the log as superseded, nothing is deleted.
func TestMergeSupersedesTheClusterNeverDeleting(t *testing.T) {
	s := newStore(t)
	m1 := add(t, s, Draft{Text: "make shot takes one screenshot of one page"}, AddOptions{}).Memory
	m2 := add(t, s, Draft{Text: "make shot takes a screenshot of one page quickly"}, AddOptions{Mode: ModeNew}).Memory
	m3 := add(t, s, Draft{Text: "make shot takes one screenshot of a page"}, AddOptions{Mode: ModeNew}).Memory
	pack := Consolidate(load(t, s).All(), t0)
	if len(pack.Clusters) != 1 || len(pack.Clusters[0].Members) != 3 {
		t.Fatalf("pack %+v, want one cluster of 3", pack.Clusters)
	}
	ids := pack.Clusters[0].IDs()
	merged := add(t, s, Draft{Text: "make shot URL=<page> takes one screenshot of one page (no playtest session)"},
		AddOptions{Mode: ModeSupersedes, Target: ids[0], Targets: ids[1:]})
	if merged.Op != OpSupersede {
		t.Fatalf("op %s", merged.Op)
	}
	set := load(t, s)
	if n := len(set.All()); n != 4 {
		t.Fatalf("%d memories in the log, want 4: nothing deleted", n)
	}
	for _, id := range []string{m1.ID, m2.ID, m3.ID} {
		m := set.Get(id)
		if m == nil || m.SupersededBy != merged.Memory.ID || m.State(t0) != "superseded" {
			t.Errorf("%s: %+v, want superseded by %s", id, m, merged.Memory.ID)
		}
		if len(set.History(id)) != 2 {
			t.Errorf("%s history %+v, want add + supersede", id, set.History(id))
		}
	}
	if got := set.Get(merged.Memory.ID).Links; strings.Join(got, ",") != strings.Join(ids, ",") {
		t.Errorf("links %v, want %v", got, ids)
	}
	for _, e := range set.events {
		if e.Op == OpOwnerDelete || e.Op == OpForget {
			t.Errorf("a merge wrote %s", e.Op)
		}
	}
	if again := Consolidate(set.All(), t0); len(again.Clusters) != 0 {
		t.Errorf("after the merge no cluster is left: %+v", again.Clusters)
	}
	// An unknown id refuses the whole merge.
	if _, err := s.Add(Draft{Text: "another fact entirely about tmux sockets"}, AddOptions{Mode: ModeSupersedes, Target: merged.Memory.ID, Targets: []string{"m-ffffff"}}, t0); err == nil {
		t.Error("an unknown id in --supersedes should refuse")
	}
	if n := len(load(t, s).All()); n != 4 {
		t.Errorf("a refused merge wrote something: %d", n)
	}
}

func TestConsolidateAt5000IsCheap(t *testing.T) {
	s := newStore(t)
	if err := s.seed(5000, t0); err != nil {
		t.Fatal(err)
	}
	mems := load(t, s).All()
	start := time.Now()
	pack := Consolidate(mems, t0)
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("consolidate at 5,000 took %s", d)
	}
	if len(pack.Clusters) > MaxClusters {
		t.Errorf("clusters %d over the cap", len(pack.Clusters))
	}
}
