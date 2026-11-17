package viewer

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/fseventsd"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// stubDisk makes diskVerdict answer v.
func stubDisk(t *testing.T, v diskuse.Verdict) {
	t.Helper()
	prev := diskVerdict
	diskVerdict = func(time.Time) diskuse.Verdict { return v }
	t.Cleanup(func() { diskVerdict = prev })
}

// bigGoCache is the 2026-11-16 reading: 159 GB of cache on a volume 80%
// used, read an hour before now.
func bigGoCache(now time.Time) diskuse.Verdict {
	r := diskuse.Reading{At: now.Add(-time.Hour), HasGoCache: true, GoCacheMB: 159 * 1024}
	return diskuse.Check(r, true, diskuse.Volume{UsedPct: 80, FreeGB: 180, OK: true}, now)
}

// AC2: a Go cache over 100 GB, or a volume over 85% used, is one Needs you
// row that names go clean -cache and that no worker may be running; it is
// listed and counted once across projects, and nothing runs the command.
func TestNeedsYouNamesALargeGoCache(t *testing.T) {
	now := time.Now()
	p := &tracker.Project{Name: "P", Slug: "p"}
	stubDisk(t, bigGoCache(now))
	rows := diskNeedsYou(p, "", "/p/p", now)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	want := "The Go build cache holds 159 GB, the data volume is 80% used (180 GB free): the cache is over 100 GB. " +
		"A full disk stops every build and every worker at once. " +
		"Trim the cache in a terminal while no worker is running: go clean -cache (a build or test running then fails; Go rebuilds what it needs; nothing here runs it for you)."
	if r := rows[0]; r.Kind != "disk" || r.Note != want || r.URL != "/p/p/health" || r.Age != "1h" {
		t.Errorf("row = %+v\nnote %q\nwant %q", r, r.Note, want)
	}

	// A full volume alone, with no cache reading.
	stubDisk(t, diskuse.Check(diskuse.Reading{}, false, diskuse.Volume{UsedPct: 86.4, FreeGB: 130, OK: true}, now))
	if rows := diskNeedsYou(p, "", "/p/p", now); len(rows) != 1 || !strings.HasPrefix(rows[0].Note, "The data volume is 86% used (130 GB free): the volume is over 85% used.") ||
		!strings.Contains(rows[0].Note, "go clean -cache") {
		t.Errorf("full volume rows = %+v", rows)
	}

	// Under both lines: no row.
	stubDisk(t, diskuse.Check(diskuse.Reading{At: now, HasGoCache: true, GoCacheMB: 40 * 1024}, true, diskuse.Volume{UsedPct: 70, FreeGB: 300, OK: true}, now))
	if rows := diskNeedsYou(p, "", "/p/p", now); len(rows) != 0 {
		t.Errorf("quiet disk rows = %+v", rows)
	}

	// Once across projects, and in each project's count.
	a := setupStatusProject(t, now)
	b := setupStatusProject(t, now)
	b.Name, b.Slug = "Other", "other"
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	stubMachine(t, fseventsd.Verdict{})
	stubDisk(t, bigGoCache(time.Now()))
	ts := newTestServer(t, []tracker.Project{a, b})
	defer ts.Close()
	var one NeedsYouJSON
	_, js := getResp(t, ts.URL+"/p/status/needs-you.json")
	if err := json.Unmarshal([]byte(js), &one); err != nil {
		t.Fatal(err)
	}
	_, body := getResp(t, ts.URL+"/needs-you")
	if n := strings.Count(body, `data-kind="disk"`); n != 1 {
		t.Errorf("disk rows across projects = %d, want 1", n)
	}
	if !strings.Contains(body, "go clean -cache") {
		t.Error("the page across projects doesn't name go clean -cache")
	}
	// 3 rows each (the Status fixture) and the disk's row once.
	if want := `data-testid="needs_you.count">7</span>`; one.Count != 4 || !strings.Contains(body, want) {
		t.Errorf("one project counts %d (want 4); across projects missing %q", one.Count, want)
	}
}

// AC1: /health's "Where to look" lists what lies outside the checkout from
// the newest snapshot, with the volume's use and the trim command.
func TestHealthShowsTheDiskOutsideTheCheckout(t *testing.T) {
	s := healthSnap(time.Now(), map[string]float64{
		"storage.gocache_mb": 159 * 1024, "storage.gocache_new_mb": 6200, "storage.transcripts_mb": 5734,
		"storage.total_mb": 4400, "storage.volume_used_pct": 80,
	})
	l := healthLook(s, "/p/p")
	var got *WbLookList
	for i := range l.Lists {
		if l.Lists[i].Title == "Disk outside the checkout" {
			got = &l.Lists[i]
		}
	}
	if got == nil {
		t.Fatalf("no disk list in %+v", l.Lists)
	}
	if got.Sub != "the data volume 80% used · trim the cache while no worker is running: go clean -cache" {
		t.Errorf("sub = %q", got.Sub)
	}
	var lines []string
	for _, r := range got.Rows {
		lines = append(lines, r.Label+"="+r.Value)
	}
	want := "Go build cache (all worktrees)=159.00 GB|Go build cache: created in the last 24 h=6.05 GB|agent transcripts=5.60 GB|the checkout=4.30 GB"
	if strings.Join(lines, "|") != want {
		t.Errorf("rows = %s\nwant   %s", strings.Join(lines, "|"), want)
	}
	if l := healthLook(healthSnap(time.Now(), map[string]float64{"storage.total_mb": 4400}), "/p/p"); len(l.Lists) != 0 {
		t.Errorf("a snapshot without the cache lists %+v", l.Lists)
	}
}
