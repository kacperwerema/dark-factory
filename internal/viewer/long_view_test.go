package viewer

import (
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/leaks"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestNeedsYouNamesLongRunningSessions: the health watcher's report puts
// one machine-wide row in Needs you naming each lead past its rotation and
// each grown process, pointing at issue-cli health; an empty report none.
func TestNeedsYouNamesLongRunningSessions(t *testing.T) {
	now := time.Date(2026, 11, 21, 8, 0, 0, 0, time.Local)
	prevRead, prevRec, prevLive := readLongLived, recordLongLived, longLive
	t.Cleanup(func() { readLongLived, recordLongLived, longLive = prevRead, prevRec, prevLive; longLast.Store(nil) })
	longLive = func() bool { return true }
	recorded := 0
	recordLongLived = func(time.Time) error { recorded++; return nil }
	var gotProjects int
	readLongLived = func(ps []*tracker.Project, _ time.Time) leaks.LongReport {
		gotProjects = len(ps)
		return leaks.LongReport{
			Sessions: []leaks.SessionFinding{{Kind: leaks.SessionUnrotated, Session: "lead-orchard-marketing", Project: "orchard", Age: 8 * 24 * time.Hour}},
			Grown:    []leaks.Growth{{Name: "issue-viewer", FromMB: 400, ToMB: 1020}},
		}
	}
	refreshLongLived([]tracker.Project{{Slug: "orchard"}, {Slug: "quince"}}, now, map[string]string{})
	if recorded != 1 || gotProjects != 2 {
		t.Fatalf("recorded %d times over %d projects, want 1 over 2", recorded, gotProjects)
	}
	p := &tracker.Project{Name: "Orchard", Slug: "orchard"}
	rows := longNeedsYou(p, "", "/p/orchard", now)
	if len(rows) != 1 || rows[0].Kind != "sessions" || !machineWideKinds["sessions"] {
		t.Fatalf("rows = %+v", rows)
	}
	note := rows[0].Note
	for _, want := range []string{"1 lead session ran past its rotation (26 h)", "lead-orchard-marketing 8d0h", "1 long-lived process doubled its memory in a day", "issue-viewer 400→1020 MB", "issue-cli health prints the command"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q: %s", want, note)
		}
	}
	readLongLived = func([]*tracker.Project, time.Time) leaks.LongReport { return leaks.LongReport{} }
	refreshLongLived(nil, now, map[string]string{})
	if rows := longNeedsYou(p, "", "/p/orchard", now); len(rows) != 0 {
		t.Errorf("an empty report still has rows: %+v", rows)
	}
}
