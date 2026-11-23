package leadstate

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// section is the body of ## name in out, up to the next ## heading.
func section(t *testing.T, out, name string) string {
	t.Helper()
	i := strings.Index(out, "\n## "+name+"\n")
	if i < 0 {
		t.Fatalf("no ## %s in:\n%s", name, out)
	}
	rest := out[i+len("\n## "+name+"\n"):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	if j := strings.Index(rest, "\nAdd: "); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

func capView(now time.Time) View {
	return View{Lead: "lead-x", Project: "x", At: now, Commands: Commands{
		Decisions: "issue-cli decisions --lead ceo", Memories: "issue-cli memory list --scope lead",
		Note: "issue-cli lead state note", All: "issue-cli lead state --all"}}
}

// AC2: 12 notes, 8 tomorrow items (2 done) and 9 Today refs: each section
// shows its 5 newest and one +N more line naming the command for the rest.
func TestStateCapsTodayTomorrowAndNotesAtTheFiveNewest(t *testing.T) {
	var s Store
	now := pinnedNow()
	for i := 1; i <= 12; i++ {
		s.Add(KindNote, fmt.Sprintf("note %02d", i), now.Add(time.Duration(i)*time.Minute))
	}
	var done []int
	for i := 1; i <= 8; i++ {
		id := s.Add(KindTomorrow, fmt.Sprintf("check %02d", i), now.Add(time.Duration(i)*time.Minute))
		if i == 7 || i == 8 {
			done = append(done, id)
		}
	}
	for _, id := range done {
		if err := s.Done(id); err != nil {
			t.Fatal(err)
		}
	}
	v := capView(now)
	for i := 1; i <= 5; i++ {
		v.Decisions = append(v.Decisions, Ref{ID: fmt.Sprintf("a#D-%d", i), Text: "decided", At: now.Add(time.Duration(2*i) * time.Minute)})
	}
	for i := 1; i <= 4; i++ {
		v.Memories = append(v.Memories, Ref{ID: fmt.Sprintf("m-%d", i), Text: "learned", At: now.Add(time.Duration(2*i+1) * time.Minute)})
	}
	out := Render(v, s)

	notes := section(t, out, "Notes")
	if !strings.Contains(notes, "+7 more: issue-cli lead state --all") {
		t.Errorf("Notes lacks its +7 more line:\n%s", notes)
	}
	for i := 1; i <= 12; i++ {
		has := strings.Contains(notes, fmt.Sprintf("note %02d", i))
		if want := i > 7; has != want {
			t.Errorf("note %02d shown = %v, want %v:\n%s", i, has, want, notes)
		}
	}

	tom := section(t, out, "Tomorrow")
	if !strings.Contains(tom, "+1 more: issue-cli lead state --all") {
		t.Errorf("Tomorrow lacks its +1 more line:\n%s", tom)
	}
	for i := 1; i <= 8; i++ {
		has := strings.Contains(tom, fmt.Sprintf("check %02d", i))
		// 7 and 8 are done and drop; of the 6 open ones the 5 newest show.
		if want := i >= 2 && i <= 6; has != want {
			t.Errorf("check %02d shown = %v, want %v:\n%s", i, has, want, tom)
		}
	}

	today := section(t, out, "Today")
	if !strings.Contains(today, "+4 more: issue-cli decisions --lead ceo · issue-cli memory list --scope lead") {
		t.Errorf("Today lacks its +4 more line:\n%s", today)
	}
	// Oldest to newest: D-1 m-1 D-2 m-2 D-3 m-3 D-4 m-4 D-5; the last 5 show.
	for _, id := range []string{"a#D-3:", "m-3:", "a#D-4:", "m-4:", "a#D-5:"} {
		if !strings.Contains(today, id) {
			t.Errorf("Today lacks %s:\n%s", id, today)
		}
	}
	for _, id := range []string{"a#D-1:", "m-1:", "a#D-2:", "m-2:"} {
		if strings.Contains(today, id) {
			t.Errorf("Today shows the older %s:\n%s", id, today)
		}
	}

	v.Full = true
	full := Render(v, s)
	if n := strings.Count(section(t, full, "Notes"), "\n- "); n != 11 {
		t.Errorf("--all shows %d notes after the first, want all 12:\n%s", n, section(t, full, "Notes"))
	}
	if strings.Contains(full, "more: issue-cli lead state --all") {
		t.Errorf("--all still caps:\n%s", full)
	}
}

// AC3: a note or Today line that only repeats a pinned memory's id and title
// is not rendered; one that says more is.
func TestStateDropsLinesThatOnlyRepeatAPin(t *testing.T) {
	var s Store
	now := pinnedNow()
	s.Add(KindNote, "pinned m-abc: Never cp over a binary in place", now)
	s.Add(KindNote, "m-abc Never cp over a binary in place.", now.Add(time.Minute))
	s.Add(KindTomorrow, "Pinned memory m-abc", now)
	s.Add(KindNote, "m-abc bit us again on the release build", now.Add(2*time.Minute))
	v := capView(now)
	v.Pinned = []Ref{{ID: "m-abc", Text: "Never cp over a binary in place"}}
	v.Memories = []Ref{{ID: "m-abc", Text: "Never cp over a binary in place", At: now}, {ID: "m-new", Text: "fresh fact", At: now}}
	out := Render(v, s)
	notes := section(t, out, "Notes")
	if strings.Contains(notes, "Never cp") {
		t.Errorf("Notes repeats the pin:\n%s", notes)
	}
	if !strings.Contains(notes, "m-abc bit us again") {
		t.Errorf("Notes lost the line that says more:\n%s", notes)
	}
	if tom := section(t, out, "Tomorrow"); tom != "Nothing noted." {
		t.Errorf("Tomorrow repeats the pin:\n%s", tom)
	}
	today := section(t, out, "Today")
	if strings.Contains(today, "m-abc") || !strings.Contains(today, "m-new: fresh fact") {
		t.Errorf("Today:\n%s", today)
	}
}
