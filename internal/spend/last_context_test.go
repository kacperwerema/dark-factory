package spend

import (
	"os"
	"testing"
	"time"
)

// LastContext reads the named lead's newest transcript's last turn, skips
// another session's file and a file older than since.
func TestLastContext(t *testing.T) {
	f := newFixture(t)
	root := f.p.Root()
	at := func(h int) time.Time { return time.Date(2026, 10, 30, h, 0, 0, 0, time.UTC) }
	old := f.write(root, "old.jsonl", "lead-acme", msg("o1", at(1), "claude-test", 0, 0, 999999, 0))
	lead := f.write(root, "lead.jsonl", "lead-acme",
		msg("l1", at(9), "claude-test", 10, 5, 100, 0),
		msg("l2", at(10), "claude-test", 1, 5, 412000, 30),
		msg("l3", at(10), "<synthetic>", 0, 0, 0, 0))
	other := f.write(root, "other.jsonl", "lead-acme-marketing", msg("m1", at(11), "claude-test", 0, 0, 7, 0))
	// The other session's file is the newest; the old one is oldest.
	os.Chtimes(old, at(1), at(1))
	os.Chtimes(lead, at(10), at(10))
	os.Chtimes(other, at(11), at(11))

	n, when, ok := LastContext(f.projects, root, "lead-acme", at(0))
	if !ok || n != 412031 || !when.Equal(at(10)) {
		t.Fatalf("LastContext = %d %v %v, want 412031 at 10:00", n, when, ok)
	}
	if n, _, ok := LastContext(f.projects, root, "lead-acme-marketing", at(0)); !ok || n != 7 {
		t.Fatalf("department lead = %d %v", n, ok)
	}
	if _, _, ok := LastContext(f.projects, root, "lead-acme", at(12)); ok {
		t.Fatal("a transcript older than since counted")
	}
	if _, _, ok := LastContext(f.projects, root, "lead-none", at(0)); ok {
		t.Fatal("no such session, still ok")
	}
}
