package leadstate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pinnedNow() time.Time { return time.Date(2026, 11, 15, 14, 0, 0, 0, time.Local) }

func TestStatePathUnderStateDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	want := filepath.Join(dir, "ai-workflow-engine", "lead", "lead-x.md")
	if got := Path("lead-x"); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestStoreRoundTripAndDone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var s Store
	now := pinnedNow()
	a := s.Add(KindAsk, "ship the board today", now)
	n := s.Add(KindNote, "CI flaky on TestX", now)
	if a != 1 || n != 2 {
		t.Fatalf("ids = %d, %d", a, n)
	}
	if err := s.Save("lead-x"); err != nil {
		t.Fatal(err)
	}
	got, err := Load("lead-x")
	if err != nil || len(got.Notes) != 2 {
		t.Fatalf("Load = %+v, %v", got, err)
	}
	if err := got.Done(a); err != nil {
		t.Fatal(err)
	}
	if !got.Notes[0].Done {
		t.Fatal("ask not done")
	}
	if err := got.Done(n); err != nil || len(got.Notes) != 1 {
		t.Fatalf("a done note is dropped: %+v %v", got.Notes, err)
	}
	if err := got.Done(99); err == nil {
		t.Fatal("Done(99) should fail")
	}
	if id := got.Add(KindNote, "x", now); id != 3 {
		t.Fatalf("next id after reload = %d, want 3", id)
	}
}

// A note from yesterday folds out by calendar day, even when it is less
// than 24 hours old; asks stay until done.
func TestFoldByCalendarDay(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 30, 0, 0, time.Local)
	var s Store
	s.Add(KindNote, "late yesterday", now.Add(-time.Hour))
	s.Add(KindAsk, "open ask from yesterday", now.Add(-time.Hour))
	s.Add(KindNote, "today", now)
	done := s.Add(KindTomorrow, "done item", now.Add(-time.Hour))
	_ = s.Done(done)
	out := s.Fold(now)
	if len(out["2026-11-14"]) != 1 || out["2026-11-14"][0].Text != "late yesterday" {
		t.Fatalf("folded = %+v", out)
	}
	if len(s.Notes) != 2 {
		t.Fatalf("kept = %+v", s.Notes)
	}
}

func TestFoldIntoAppendsUnderLeadNotes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "2026-11-14-lead-x.summary.md")
	if err := os.WriteFile(p, []byte("Chat summary line.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := []Note{{Text: "one", At: pinnedNow()}}
	if err := FoldInto(p, n); err != nil {
		t.Fatal(err)
	}
	if err := FoldInto(p, []Note{{Text: "two", At: pinnedNow()}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	if strings.Count(got, "## Lead notes") != 1 || !strings.Contains(got, "- 14:00 one\n- 14:00 two") || !strings.HasPrefix(got, "Chat summary line.") {
		t.Fatalf("summary:\n%s", got)
	}
}

func TestWatchesPrune(t *testing.T) {
	var s Store
	s.Register(Watch{PID: 1, Command: "a"})
	s.Register(Watch{PID: 2, Command: "b"})
	s.Register(Watch{PID: 1, Command: "a2"})
	if len(s.Watches) != 2 {
		t.Fatalf("watches = %+v", s.Watches)
	}
	if !s.Prune(func(pid int) bool { return pid == 2 }) || len(s.Watches) != 1 || s.Watches[0].PID != 2 {
		t.Fatalf("after prune = %+v", s.Watches)
	}
}

func TestRenderHoldsTheSections(t *testing.T) {
	var s Store
	now := pinnedNow()
	s.Add(KindAsk, "make it faster", now)
	s.Add(KindTomorrow, "check the nightly", now)
	s.Add(KindNote, "landed x", now)
	s.Register(Watch{PID: 42, Command: "issue-cli lead tick --watch --exit-on-news", Started: now})
	v := View{Lead: "lead-x", Project: "x", At: now,
		Workers:   []Worker{{Session: "agent-a", Status: "in progress", Last: "progress: tests green"}},
		Queue:     []QueueItem{{ID: 3, Label: "a", Quote: "do a"}},
		QueueOpen: 1, Refill: 5,
		Decisions: []Ref{{ID: "a#D-1", Text: "use x"}},
		Memories:  []Ref{{ID: "m-1", Text: "pitfall"}},
	}
	out := Render(v, s)
	for _, want := range []string{"## Workers", "agent-a · in progress · progress: tests green", "## Queue", `#3 a (owner: "do a")`, "## Owner asks", `"make it faster"`, "## Today", "a#D-1: use x", "m-1: pitfall", "## Watches", "pid 42", "issue-cli lead tick --watch --exit-on-news", "## Tomorrow", "check the nightly", "## Notes", "landed x"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// The file stays under the cap with 200 notes and 50 workers: the oldest
// notes go first and each list ends in a +N more line.
func TestRenderStaysUnderTheCap(t *testing.T) {
	var s Store
	now := pinnedNow()
	for i := 0; i < 200; i++ {
		s.Add(KindNote, strings.Repeat(fmt.Sprintf("note %d ", i), 30), now.Add(time.Duration(i)*time.Second))
		s.Add(KindAsk, strings.Repeat("ask ", 80), now)
	}
	v := View{Lead: "lead-x", Project: "x", At: now, Commands: Commands{Workers: "issue-cli agents", Note: "issue-cli lead state note"}}
	for i := 0; i < 50; i++ {
		v.Workers = append(v.Workers, Worker{Session: fmt.Sprintf("agent-%d", i), Status: "in progress", Last: strings.Repeat("x", 500)})
	}
	out := Render(v, s)
	if len(out) > MaxBytes {
		t.Fatalf("rendered %d bytes, cap %d", len(out), MaxBytes)
	}
	if !strings.Contains(out, "+30 more: issue-cli agents") {
		t.Errorf("no workers +N more line:\n%s", out[:2000])
	}
	if !strings.Contains(out, "note 199") {
		t.Errorf("the newest note went")
	}
}

// A tick watch with a live pid is live; one whose pid is gone (its session
// ended) is missing at once; a tick that exited on its own is quiet for
// TickGrace, then missing; a lead that never ran one is missing.
func TestTickStatus(t *testing.T) {
	now := time.Date(2026, 11, 16, 9, 30, 0, 0, time.Local)
	alive := func(pid int) bool { return pid == 1 }
	tick := "issue-cli --project p lead tick --watch --exit-on-news"

	var s Store
	if st := s.TickStatus(alive, now); !st.Missing || st.Command != "" || st.DeadPID != 0 {
		t.Fatalf("never ran: %+v", st)
	}
	s.Register(Watch{PID: 2, Command: "issue-cli lead watch --run probe", Started: now})
	if st := s.TickStatus(alive, now); !st.Missing {
		t.Fatalf("a lead watch is no tick: %+v", st)
	}
	s.Register(Watch{PID: 1, Command: tick, Started: now})
	if st := s.TickStatus(alive, now); st.Missing {
		t.Fatalf("live tick: %+v", st)
	}
	s.Unregister(1)
	s.Register(Watch{PID: 9, Command: tick, Started: now.Add(-20 * time.Minute)})
	if st := s.TickStatus(alive, now); !st.Missing || st.DeadPID != 9 || st.Command != tick {
		t.Fatalf("dead tick: %+v", st)
	}
	s.Ended(9, now.Add(-3*time.Minute))
	if len(s.Watches) != 1 {
		t.Fatalf("Ended keeps the watch: %+v", s.Watches)
	}
	if st := s.TickStatus(alive, now); st.Missing {
		t.Fatalf("within the grace: %+v", st)
	}
	if st := s.TickStatus(alive, now.Add(TickGrace)); !st.Missing || st.Ended.IsZero() || st.Command != tick {
		t.Fatalf("past the grace: %+v", st)
	}
}

func TestIsTick(t *testing.T) {
	for cmd, want := range map[string]bool{
		"issue-cli --project p lead tick --watch --exit-on-news":                      true,
		"issue-cli ceo tick --watch":                                                  true,
		"issue-cli lead tick --watch --exit-on-news --observe --department marketing": true,
		"issue-cli lead tick":                                                         false,
		"issue-cli lead watch --run 'lead tick --watch'":                              false,
		"issue-cli lead state":                                                        false,
	} {
		if IsTick(cmd) != want {
			t.Errorf("IsTick(%q) = %v", cmd, !want)
		}
	}
}
