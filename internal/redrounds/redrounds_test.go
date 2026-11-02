package redrounds

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/critique"
	"github.com/kacperwerema/dark-factory/internal/tracker"
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
	got := Count(results, nil, time.Time{})
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
	if got := Count(results, nil, time.Time{}); len(got) != 1 || got[0].Rounds != 1 {
		t.Fatalf("after a green only one round counts: %+v", got)
	}
	// Nothing at or before since counts.
	results = []Result{red("x", 1, "h1"), red("x", 2, "h2"), red("x", 3, "h3")}
	if got := Count(results, map[string]time.Time{"x": at(2)}, time.Time{}); len(got) != 1 || got[0].Rounds != 1 {
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
		{Sha: "s3", State: ci.Red, Failed: "go test ./...", Failures: []string{"TestA"}, Finished: at(3), Merged: []ci.Merged{{Slug: "f/x"}}},
	}
	got := Count(FromCI(runs, "f/x"), nil, time.Time{})
	if len(got) != 1 || got[0].Check != "ci:go test ./..." || got[0].Rounds != 2 || got[0].Last.Head != "s3" {
		t.Fatalf("ci streaks = %+v", got)
	}
	runs = append(runs, ci.Run{Sha: "s4", State: ci.Green, Finished: at(4), Merged: []ci.Merged{{Slug: "f/x"}},
		Commands: []ci.CommandResult{{Cmd: "go test ./..."}}})
	if got := Count(FromCI(runs, "f/x"), nil, time.Time{}); len(got) != 0 {
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
	err := st.Hold("f/x", nil)
	if err == nil || !strings.Contains(err.Error(), "comment #7") || !strings.Contains(err.Error(), "red 4 of 4") {
		t.Fatalf("hold = %v", err)
	}
	// Resolved: no hold, and the rounds before it no longer count.
	done := open
	done.Status = "done"
	st = Build(results, []tracker.Comment{done}, 4)
	if st.Hold("f/x", nil) != nil || len(st.Streaks) != 0 {
		t.Fatalf("resolved question still holds: %+v", st)
	}
}

// The critic's first real use (internal/critique/testdata): one failed
// codex run and five held claude runs at one branch head. Each held run is
// a red round of the critique check, but every run's findings were new
// (the work changed between runs), so each one starts the count over: the
// cap of 4 is never reached. The failed run is not a round.
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
	if len(s.History) != 5 || s.Rounds != 1 || s.AtCap(4) || !strings.HasPrefix(s.Last.Why, "2 blocks: blocks ") || !strings.Contains(s.Last.Why, "spec-flow-in-use-") {
		t.Fatalf("streak = %+v", s)
	}
	for _, rd := range s.History[1:] {
		if rd.Count != 1 || rd.Reason() != "all failures new: the count starts over" {
			t.Errorf("round %d: count %d, %q", rd.N, rd.Count, rd.Reason())
		}
	}
	if need := st.NeedQuestion(); len(need) != 0 {
		t.Fatalf("need question = %+v", need)
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
	if s := st.Streak(check); s.Rounds != 1 || len(s.History) != 3 {
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

// heldRun is a held critique run on design → backlog with these blocks.
func heldRun(slug string, min int, blocks ...string) *critique.Run {
	return &critique.Run{ID: fmt.Sprintf("r%d", min), Slug: slug, From: "in design", To: "backlog", At: at(min),
		Report: critique.Parse(strings.Join(blocks, "\n") + "\nCoverage: all")}
}

// AC1: four held critic runs whose findings are all new never raise the
// cap question; the same finding four times does, even when the critic
// cites another line and writes it in another case.
func TestCritiqueSameFindingCapsNewFindingsDoNot(t *testing.T) {
	check := CritiqueCheck("in design", "backlog")
	var different, same []*critique.Run
	for i := 1; i <= 4; i++ {
		different = append(different, heldRun("f/x", i, fmt.Sprintf("blocks docs/d.md:%d — problem number %c is new", i, 'a'+i)))
		text := "The gate never checks the owner."
		if i%2 == 0 {
			text = "the gate  never checks the OWNER"
		}
		same = append(same, heldRun("f/x", i, fmt.Sprintf("blocks docs/d.md:%d — %s", 10+i, text), fmt.Sprintf("blocks docs/e.md:1 — other %c", 'a'+i)))
	}
	st := Build(FromCritique(different, "f/x"), nil, 4)
	if s := st.Streak(check); s.Rounds != 1 || len(st.NeedQuestion()) != 0 {
		t.Fatalf("new findings each run: %+v", s)
	}
	st = Build(FromCritique(same, "f/x"), nil, 4)
	s := st.Streak(check)
	if s.Rounds != 4 || len(st.NeedQuestion()) != 1 {
		t.Fatalf("same finding four times: %+v", s)
	}
	if r := s.History[3].Reason(); !strings.HasPrefix(r, "same finding: d.md — the gate never checks the owner") || !strings.HasSuffix(r, "as round 3") {
		t.Errorf("reason = %q", r)
	}
	// A finding that comes back after a run without it still counts.
	back := []*critique.Run{
		heldRun("f/x", 1, "blocks docs/d.md:1 — A"),
		heldRun("f/x", 2, "blocks docs/d.md:1 — B"),
		heldRun("f/x", 3, "blocks docs/d.md:1 — A"),
	}
	if s := Build(FromCritique(back, "f/x"), nil, 4).Streak(check); s.Rounds != 2 || s.History[2].SameAs != 1 {
		t.Errorf("A, B, A = %+v", s)
	}
}

// The four designs that hit the cap on 2026-10-31 (remote access, web
// push, the ledger epic): their real held runs, copied from the board's
// .critique/runs.jsonl. Each run's findings were new, so none reaches the
// cap now.
func TestRealHeldDesignRunsDoNotCap(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "held-design-runs.jsonl"))
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
	if err != nil {
		t.Fatal(err)
	}
	slugs := map[string]bool{}
	for _, r := range runs {
		slugs[r.Slug] = true
	}
	if len(slugs) != 3 {
		t.Fatalf("slugs = %v", slugs)
	}
	check := CritiqueCheck("in design", "backlog")
	for slug := range slugs {
		results := FromCritique(runs, slug)
		if len(results) != 4 {
			t.Fatalf("%s: %d held runs, want 4", slug, len(results))
		}
		// The old rule: four held runs, four rounds, the cap.
		st := Build(results, nil, 4)
		s := st.Streak(check)
		if len(s.History) != 4 || s.AtCap(4) || len(st.NeedQuestion()) != 0 {
			t.Errorf("%s: %s, history %d", slug, s.Label(4), len(s.History))
		}
	}
}

// AC2: a failing test that changes name between rounds starts the count
// over; the same failing test four times caps. Timings and the head in the
// output don't make the same failure look new.
func TestFailingTestNameDecidesTheCount(t *testing.T) {
	gate := func(min int, test string) Result {
		msg := fmt.Sprintf("shell check failed (exit 1): \"go test ./...\"\n  stdout=\"--- FAIL: %s (0.0%ds)\\nFAIL\\tpkg\\t1.2%ds\\n\"", test, min, min)
		return Result{Check: "gate:command_succeeds:go test ./...", Red: true, At: at(min), Head: fmt.Sprintf("h%d", min), Why: msg, Failures: FailureKeys(msg)}
	}
	var renamed, same []Result
	for i := 1; i <= 4; i++ {
		renamed = append(renamed, gate(i, fmt.Sprintf("TestStep%d", i)))
		same = append(same, gate(i, "TestStep"))
	}
	if keys := FailureKeys(same[0].Why); len(keys) != 1 || keys[0] != "test:TestStep" {
		t.Fatalf("keys = %v", keys)
	}
	if s := Build(renamed, nil, 4).Streaks[0]; s.Rounds != 1 || s.AtCap(4) {
		t.Errorf("renamed each round: %+v", s)
	}
	s := Build(same, nil, 4).Streaks[0]
	if s.Rounds != 4 || !s.AtCap(4) || s.History[3].Reason() != "same failing test TestStep as round 3" {
		t.Errorf("same test four times: %s, %q", s.Label(4), s.History[3].Reason())
	}
	// Local CI: the same failure names repeat, new ones start over.
	runs := []ci.Run{
		{Sha: "s1", State: ci.Red, Failed: "go test ./...", Failures: []string{"TestA"}, Finished: at(1), Merged: []ci.Merged{{Slug: "f/x"}}},
		{Sha: "s2", State: ci.Red, Failed: "go test ./...", Failures: []string{"TestB"}, Finished: at(2), Merged: []ci.Merged{{Slug: "f/x"}}},
	}
	if s := Count(FromCI(runs, "f/x"), nil, time.Time{}); s[0].Rounds != 1 {
		t.Errorf("ci renamed: %+v", s)
	}
	// Output with no test names: the same text (numbers and shas masked)
	// repeats, another text starts over.
	a := FailureKeys("lint: 3 problems at 1a2b3c4d in 0.4s")
	b := FailureKeys("lint: 5 problems at 9f8e7d6c in 1.1s")
	c := FailureKeys("vet: shadowed err")
	if len(a) != 1 || a[0] != b[0] || a[0] == c[0] {
		t.Errorf("output keys: %v %v %v", a, b, c)
	}
}

// The proof check: the same unproven criterion is the same failure.
func TestUnprovenCriterionKeys(t *testing.T) {
	msg := "2 of 4 acceptance criteria are not proven:\n  AC2 Something works\n    ✗ test TestX — failing\n  AC4 Other\n\nProve each one with any of (shown for AC2):\n  - x"
	if got := strings.Join(FailureKeys(msg), ","); got != "criterion:AC2,criterion:AC4" {
		t.Errorf("keys = %s", got)
	}
}

// AC3: an owner comment between rounds starts the count over; a comment by
// anyone else does not.
func TestOwnerCommentStartsTheCountOver(t *testing.T) {
	results := []Result{red("gate:a", 1, "h1"), red("gate:a", 2, "h2"), red("gate:a", 3, "h3"), red("gate:a", 5, "h5")}
	if s := Build(results, nil, 4).Streak("gate:a"); s.Rounds != 4 {
		t.Fatalf("without a comment: %+v", s)
	}
	owner := tracker.Comment{ID: 1, Text: "try the other port", Author: tracker.OwnerAuthor, Date: at(4).Format(time.RFC3339)}
	worker := tracker.Comment{ID: 2, Text: "progress: still red", Author: "agent-f-x", Date: at(4).Format(time.RFC3339)}
	if s := Build(results, []tracker.Comment{worker}, 4).Streak("gate:a"); s.Rounds != 4 {
		t.Errorf("a worker's comment reset the count: %+v", s)
	}
	st := Build(results, []tracker.Comment{owner, worker}, 4)
	if s := st.Streak("gate:a"); s.Rounds != 1 || len(s.History) != 1 || len(st.NeedQuestion()) != 0 {
		t.Errorf("after the owner's comment: %+v", s)
	}
	// An open cap question stays open: the owner commenting doesn't close it.
	q := tracker.Comment{ID: 3, Text: QuestionText(Build(results, nil, 4).Streak("gate:a"), 4), Status: "open", Date: at(5).Format(time.RFC3339)}
	st = Build(results, []tracker.Comment{q, owner}, 4)
	if st.Hold("f/x", nil) == nil {
		t.Error("an open question stopped holding after an owner comment")
	}
}

// A cap question holds only the transitions that run its check: one on a
// gate or critique check the transition no longer runs stays open but lets
// it through; one on a CI check or a named check holds every transition.
func TestHoldIgnoresChecksTheTransitionDoesNotRun(t *testing.T) {
	critiqueCheck := CritiqueCheck("in design", "backlog")
	q := func(id int, check string) tracker.Comment {
		return tracker.Comment{ID: id, Text: QuestionPrefix + check + " — red 4 of 4", Status: "open", Date: at(1).Format(time.RFC3339)}
	}
	st := Build(nil, []tracker.Comment{q(1, critiqueCheck)}, 4)
	if err := st.Hold("f/x", map[string]bool{"gate:criteria_proven": true}); err != nil {
		t.Errorf("held on a check the transition doesn't run: %v", err)
	}
	if err := st.Hold("f/x", map[string]bool{critiqueCheck: true}); err == nil {
		t.Error("not held on the check the transition runs")
	}
	if len(st.Open) != 1 {
		t.Error("the question was closed")
	}
	for _, check := range []string{"ci:go test ./...", "test:TestFoo"} {
		if Build(nil, []tracker.Comment{q(2, check)}, 4).Hold("f/x", map[string]bool{}) == nil {
			t.Errorf("%s: not held", check)
		}
	}
}
