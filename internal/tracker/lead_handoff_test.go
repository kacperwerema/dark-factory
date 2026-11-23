package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const newNote = "# Handoff\n\n## Next\n\n1. Land the branch.\n\n## Rules in force\n\n- x#D-1: the owner's rule\n\n## Released and installed\n\n- released: v1.2.0\n- installed: v1.2.0-3-gabc\n"

// A note has Next, Rules in force and Released and installed, and none of
// the parts the state file renders live.
func TestCheckHandoffNoteWantsTheThreeSectionsOnly(t *testing.T) {
	if err := CheckHandoffNote(newNote); err != nil {
		t.Fatalf("the three-section note: %v", err)
	}
	for _, legacy := range LegacyHandoffHeadings {
		err := CheckHandoffNote(newNote + "\n## " + legacy + "\n\n- agent-one\n")
		if err == nil || !strings.Contains(err.Error(), "## "+legacy) || !strings.Contains(err.Error(), "state file") {
			t.Errorf("a note with ## %s: %v", legacy, err)
		}
	}
	if err := CheckHandoffNote("## Next\n\n1. x\n"); err == nil || !strings.Contains(err.Error(), "## Rules in force") || !strings.Contains(err.Error(), "## Released and installed") {
		t.Errorf("a note with only ## Next: %v", err)
	}
}

// A note written before the change still loads: its ## Next is read (the
// Status page's list) and the brief inlines it without the parts the state
// file now has.
func TestAnOlderNoteWithStateAndQueueStillLoads(t *testing.T) {
	dir := t.TempDir()
	old := "# Handoff\n\n## State\n\n- agent-one · in progress\n\n## Queue\n\n- #7 the queued one\n\n## Waiting on the owner\n\n- the owner's ask\n\n## Rules in force\n\n- x#D-1: the owner's rule\n\n## How to resume\n\n- read the state\n\n## Next\n\n1. Land the branch.\n2. Keep the tick running.\n"
	path := filepath.Join(dir, "2026-11-20-lead-p.md")
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := NewestHandoffNote(dir, "lead-p"); got != path {
		t.Fatalf("newest note %q", got)
	}
	next, ok := SectionText(old, "Next")
	if !ok || !strings.Contains(next, "1. Land the branch.") || !strings.Contains(next, "2. Keep the tick running.") {
		t.Errorf("## Next of the old note: %v %q", ok, next)
	}
	sec := LeadHandoffSection(dir, "lead-p")
	for _, want := range []string{"## Where the last lead left off", "## Next", "1. Land the branch.", "## Rules in force", "x#D-1", "your state file has them"} {
		if !strings.Contains(sec, want) {
			t.Errorf("no %q in\n%s", want, sec)
		}
	}
	for _, gone := range []string{"## State", "agent-one", "## Queue", "the queued one", "## Waiting on the owner", "the owner's ask", "## How to resume"} {
		if strings.Contains(sec, gone) {
			t.Errorf("the old note's %q is inlined:\n%s", gone, sec)
		}
	}
	// A new note is inlined whole, without the note about left-out parts.
	if err := os.WriteFile(filepath.Join(dir, "2026-11-21-lead-p.md"), []byte(newNote), 0o644); err != nil {
		t.Fatal(err)
	}
	sec = LeadHandoffSection(dir, "lead-p")
	if !strings.Contains(sec, "released: v1.2.0") || strings.Contains(sec, "left out") {
		t.Errorf("new note inlined as:\n%s", sec)
	}
}
