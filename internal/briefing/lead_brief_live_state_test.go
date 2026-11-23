package briefing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/leadstate"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The lead brief carries the workers, the queue head and the owner's asks
// once, from the state file: the note copies none of them, after a flush
// as on a plain spawn, and an older note's copies are left out.
func TestLeadBriefRendersEachWorkerOnceFromTheStateFile(t *testing.T) {
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local)
	workers := []string{"agent-acme-one", "agent-acme-two"}
	v := leadstate.View{Lead: "lead-p", Project: "p", At: now.Add(-time.Minute), QueueOpen: 1, Queue: []leadstate.QueueItem{{ID: 7, Label: "the queued one"}}}
	for _, w := range workers {
		v.Workers = append(v.Workers, leadstate.Worker{Session: w, Status: "in progress"})
	}
	var store leadstate.Store
	store.Add(leadstate.KindAsk, "the owner's open ask", now)
	state := leadstate.Render(v, store)
	newNote := "# Handoff\n\n## Next\n\n1. Land the branch.\n\n## Rules in force\n\n- none\n\n## Released and installed\n\n- released: v1.2.0\n"
	oldNote := "# Handoff\n\n## State\n\n- " + strings.Join(workers, "\n- ") + "\n\n## Queue\n\n- #7 the queued one\n\n## Waiting on the owner\n\n- \"the owner's open ask\"\n\n## Rules in force\n\n- none\n\n## How to resume\n\n- read the state\n\n## Next\n\n1. Land the branch.\n"

	for _, c := range []struct {
		name, note string
		flush      bool
	}{{"after a flush", newNote, true}, {"plain spawn", newNote, false}, {"plain spawn, older note", oldNote, false}, {"after a flush, older note", oldNote, true}} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			root := t.TempDir()
			notePath := filepath.Join(root, "sessions", "2026-11-22-lead-p.md")
			if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(notePath, []byte(c.note), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(leadstate.Path("lead-p")), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(leadstate.Path("lead-p"), []byte(state), 0o644); err != nil {
				t.Fatal(err)
			}
			s := store
			if c.flush {
				s.Handover = &leadstate.Handover{At: now.Add(-time.Minute), By: leadstate.ByFlush, Note: notePath, Digest: "d.md", State: state}
			}
			if err := s.Save("lead-p"); err != nil {
				t.Fatal(err)
			}
			got := LeadBrief(nil, LeadBriefInput{Project: &tracker.Project{Slug: "p", Name: "Proj"}, Root: root, Now: now})
			for _, w := range workers {
				if n := strings.Count(got, w); n != 1 {
					t.Errorf("%s %d times in the brief, want once:\n%s", w, n, got)
				}
			}
			for _, part := range []string{"the queued one", "the owner's open ask", "1. Land the branch."} {
				if n := strings.Count(got, part); n != 1 {
					t.Errorf("%q %d times in the brief, want once", part, n)
				}
			}
		})
	}
}
