package redrounds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/ci"
	"github.com/michal-franc/issue-viewer/internal/critique"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func red(check string, min int, head string) Result {
	return Result{Check: check, Red: true, At: at(min), Head: head, Why: "boom " + head}
}

func green(check string, min int) Result { return Result{Check: check, At: at(min)} }

// Rounds are counted per check from injected run records: one per head,
// a green ends the streak, other checks are counted apart.
func TestCountPerCheck(t *testing.T) {
	results := []Result{
		red("gate:a", 1, "h1"),
		red("gate:a", 2, "h1"), // same head: same round
		red("gate:b", 3, "h1"),
		red("gate:a", 4, "h2"),
		green("gate:b", 5),
		red("gate:a", 6, "h3"),
		red("gate:c", 7, ""), // no head: each red is a round
		red("gate:c", 8, ""),
	}
	got := Count(results, nil)
	want := map[string]int{"gate:a": 3, "gate:c": 2}
	if len(got) != len(want) {
		t.Fatalf("streaks = %+v, want %v", got, want)
	}
	for _, s := range got {
		if want[s.Check] != s.Rounds {
			t.Errorf("%s: %d rounds, want %d", s.Check, s.Rounds, want[s.Check])
		}
	}
	if a := Build(results, nil, 4).Streak("gate:a"); a.Last.Head != "h3" || a.Label(4) != "red 3 of 4" {
		t.Errorf("gate:a last = %+v, label %q", a.Last, a.Label(4))
	}
}

func TestCountOrderAndSince(t *testing.T) {
	// Out of order on disk: Count sorts by time.
	results := []Result{red("x", 3, "h3"), red("x", 1, "h1"), green("x", 2)}
	if got := Count(results, nil); len(got) != 1 || got[0].Rounds != 1 {
		t.Fatalf("after a green only one round counts: %+v", got)
	}
	// Nothing at or before since counts.
	results = []Result{red("x", 1, "h1"), red("x", 2, "h2"), red("x", 3, "h3")}
	if got := Count(results, map[string]time.Time{"x": at(2)}); len(got) != 1 || got[0].Rounds != 1 {
		t.Fatalf("since: %+v", got)
	}
}

func TestCannotFixIsAtCap(t *testing.T) {
	r := red("test:TestFoo", 1, "h1")
	r.CannotFix = true
	st := Build([]Result{r}, nil, 4)
	need := st.NeedQuestion()
	if len(need) != 1 || !need[0].CannotFix || !strings.Contains(need[0].Label(4), "cannot fix") {
		t.Fatalf("cannot-fix not at cap: %+v", need)
	}
}

func TestFromCI(t *testing.T) {
	runs := []ci.Run{
		{Sha: "s1", State: ci.Red, Failed: "go test ./...", Failures: []string{"TestA"}, Finished: at(1), Merged: []ci.Merged{{Slug: "f/x"}}},
		{Sha: "s2", State: ci.Red, Failed: "go test ./...", Finished: at(2), Merged: []ci.Merged{{Slug: "other"}}},
		{Sha: "s3", State: ci.Red, Failed: "go test ./...", Finished: at(3), Merged: []ci.Merged{{Slug: "f/x"}}},
	}
	got := Count(FromCI(runs, "f/x"), nil)
	if len(got) != 1 || got[0].Check != "ci:go test ./..." || got[0].Rounds != 2 || got[0].Last.Head != "s3" {
		t.Fatalf("ci streaks = %+v", got)
	}
	runs = append(runs, ci.Run{Sha: "s4", State: ci.Green, Finished: at(4), Merged: []ci.Merged{{Slug: "f/x"}},
		Commands: []ci.CommandResult{{Cmd: "go test ./..."}}})
	if got := Count(FromCI(runs, "f/x"), nil); len(got) != 0 {
		t.Fatalf("green CI run should end the streak: %+v", got)
	}
}

func TestQuestionRoundTripAndHold(t *testing.T) {
	results := []Result{red("gate:a", 1, "h1"), red("gate:a", 2, "h2"), red("gate:a", 3, "h3"), red("gate:a", 4, "h4")}
	st := Build(results, nil, 4)
	need := st.NeedQuestion()
	if len(need) != 1 {
		t.Fatalf("need = %+v", need)
	}
	text := QuestionText(need[0], 4)
	if !strings.HasPrefix(text, "question: ") || !strings.Contains(text, "Recommendation:") || !strings.Contains(text, "red 4 of 4") {
		t.Fatalf("question text: %s", text)
	}
	if strings.Index(text, "Recommendation:") > strings.Index(text, "Options:") {
		t.Fatalf("recommendation must come first: %s", text)
	}
	if c, ok := QuestionCheck(text); !ok || c != "gate:a" {
		t.Fatalf("QuestionCheck = %q %v", c, ok)
	}
	if _, ok := QuestionCheck("question: something else"); ok {
		t.Fatal("plain question read as a cap question")
	}
	open := tracker.Comment{ID: 7, Text: text, Status: "open", Date: at(4).Format(time.RFC3339)}
	st = Build(results, []tracker.Comment{open}, 4)
	if len(st.NeedQuestion()) != 0 {
		t.Fatal("open question asked again")
	}
	err := st.Hold("f/x")
	if err == nil || !strings.Contains(err.Error(), "comment #7") || !strings.Contains(err.Error(), "red 4 of 4") {
		t.Fatalf("hold = %v", err)
	}
	// Resolved: no hold, and the rounds before it no longer count.
	done := open
	done.Status = "done"
	st = Build(results, []tracker.Comment{done}, 4)
	if st.Hold("f/x") != nil || len(st.Streaks) != 0 {
		t.Fatalf("resolved question still holds: %+v", st)
	}
}

// The critic's first real use (internal/critique/testdata): one failed
// codex run and five held claude runs at one branch head. Each held run is
// its own red round of the critique check, so the cap of 4 is passed; the
// failed run is not a round.
func TestHeldCritiqueRunsCountAsRedRounds(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "critique", "testdata", "spec-flow-runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(critique.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(critique.LogPath(dir), data, 0o644); err != nil {
		t.Fatal(err)
	}
	runs, err := critique.Load(dir, "")
	if err != nil || len(runs) != 6 {
		t.Fatalf("runs = %d, %v", len(runs), err)
	}
	slug := runs[0].Slug
	check := CritiqueCheck("in design", "backlog")
	results := FromCritique(runs, slug)
	if len(results) != 5 {
		t.Fatalf("results = %+v, want the five held runs", results)
	}
	st := Build(results, nil, 4)
	s := st.Streak(check)
	if s.Rounds != 5 || !s.AtCap(4) || !strings.HasPrefix(s.Last.Why, "2 blocks: blocks ") || !strings.Contains(s.Last.Why, "spec-flow-in-use-") {
		t.Fatalf("streak = %+v", s)
	}
	if need := st.NeedQuestion(); len(need) != 1 || need[0].Check != check {
		t.Fatalf("need question = %+v", need)
	}
	// The fourth held run is where the question would have been raised.
	if s4 := Build(results[:4], nil, 4).Streak(check); s4.Rounds != 4 || !s4.AtCap(4) {
		t.Errorf("after four = %+v", s4)
	}
	if s3 := Build(results[:3], nil, 4).Streak(check); s3.AtCap(4) {
		t.Errorf("at cap after three: %+v", s3)
	}
	// A clear run ends the streak; an override counts green from its time.
	clear := &critique.Run{ID: "c", Slug: slug, From: "in design", To: "backlog", At: runs[5].At.Add(time.Minute), Report: critique.Parse("Coverage: all")}
	if s := Build(FromCritique(append(runs, clear), slug), nil, 4).Streak(check); s.Rounds != 0 {
		t.Errorf("after a clear run = %+v", s)
	}
	runs[5].Override = &critique.Override{At: runs[5].At.Add(time.Minute), Quote: "ok"}
	if s := Build(FromCritique(runs, slug), nil, 4).Streak(check); s.Rounds != 0 {
		t.Errorf("after an override = %+v", s)
	}
	runs[5].Override = nil

	// Each gate has its own streak: three held runs on an earlier gate
	// don't bring a later gate's first held run to the cap.
	later := &critique.Run{ID: "l", Slug: slug, From: "testing", To: "human-testing", At: runs[5].At.Add(time.Hour), Report: critique.Parse("blocks a.go:1 — x\nCoverage: all")}
	st = Build(FromCritique(append(runs[:4:4], later), slug), nil, 4)
	if s := st.Streak(CritiqueCheck("testing", "human-testing")); s.Rounds != 1 || s.AtCap(4) {
		t.Errorf("later gate = %+v", s)
	}
	if s := st.Streak(check); s.Rounds != 3 {
		t.Errorf("earlier gate = %+v", s)
	}

	// A run holding only on output problems (no Coverage line) is the
	// critic's fault: not a round.
	noCoverage := &critique.Run{ID: "p", Slug: slug, From: "in design", To: "backlog", At: runs[5].At.Add(time.Hour), Report: critique.Parse("looks fine")}
	if !noCoverage.Holds() {
		t.Fatal("no-coverage run must hold the gate")
	}
	if r := FromCritique([]*critique.Run{noCoverage}, slug); len(r) != 0 {
		t.Errorf("problems-only run counted: %+v", r)
	}
}
