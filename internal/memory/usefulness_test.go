package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var u0 = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

func boolp(b bool) *bool { return &b }

// visit is one closed span of issue at status, an hour long, starting
// hours after u0.
func visit(issue, status string, hours int, first *bool) Outcome {
	from := u0.Add(time.Duration(hours) * time.Hour)
	return Outcome{Issue: issue, Status: status, From: from, To: from.Add(time.Hour), FirstTry: first}
}

// useAt is a use event of ids, minutes into the span visit(…, hours).
func useAt(issue, status string, hours, minutes int, ids ...string) Event {
	at := u0.Add(time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute)
	return Event{Op: OpUse, At: stamp(at), By: "agent-x", Issue: issue, Status: status, IDs: ids}
}

// writeTrace writes spans as a trace file the way internal/trace does.
func writeTrace(t *testing.T, issueDir, slug string, outs []Outcome) {
	t.Helper()
	path := filepath.Join(issueDir, TraceDir, filepath.FromSlash(slug)+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, o := range outs {
		span := map[string]any{"status": o.Status, "from": o.From, "to": o.To, "open": o.Open, "wall_sec": 3600, "tokens": map[string]any{"known": false}}
		if o.FirstTry != nil {
			span["gate"] = map[string]any{"to": "next", "tries": 1, "first_try": *o.FirstTry}
		}
		line, _ := json.Marshal(span)
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A use event names the issue and status of the stage; the join puts it on
// that visit once, and old events (no issue) or events no visit holds are
// left out.
func TestUseEventCarriesIssueAndStatus(t *testing.T) {
	dir := t.TempDir()
	s := ProjectStore(dir)
	res, err := s.Add(Draft{Text: "run make validate in the background", Kind: KindHowto, Scope: ScopeProject}, AddOptions{}, u0)
	if err != nil {
		t.Fatal(err)
	}
	id := res.Memory.ID
	if err := s.RecordUse([]string{id}, "agent-a", "ui/a", "in design", u0.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	evs, err := s.readEvents()
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.Op != OpUse || last.Issue != "ui/a" || last.Status != "in design" {
		t.Fatalf("use event %+v", last)
	}
	// Old events still count as uses for strength.
	set, _ := s.Load()
	if set.Get(id).Uses != 1 {
		t.Fatalf("uses %d", set.Get(id).Uses)
	}

	outs := []Outcome{visit("ui/a", "in design", 0, boolp(true)), visit("ui/a", "in design", 5, boolp(false))}
	events := []Event{
		useAt("ui/a", "in design", 0, 1, "m-1"),
		useAt("ui/a", "in design", 0, 30, "m-1"), // the same visit again: one injection
		useAt("ui/a", "in design", 5, 2, "m-1"),
		{Op: OpUse, At: stamp(u0), By: "agent-a", IDs: []string{"m-1"}}, // before usefulness: no issue
		useAt("ui/a", "testing", 0, 1, "m-1"),                           // no testing visit
		useAt("ui/b", "in design", 0, 1, "m-1"),                         // no trace for ui/b
	}
	ins := Injections(events, outs)
	if len(ins) != 2 {
		t.Fatalf("injections %+v", ins)
	}
	if !*ins[0].FirstTry || *ins[1].FirstTry {
		t.Fatalf("outcomes %+v", ins)
	}
	// The transition stamps its use a moment before the span's recorded start.
	early := useAt("ui/a", "in design", 5, 0, "m-2")
	early.At = stamp(u0.Add(5*time.Hour - 30*time.Second))
	if got := Injections([]Event{early}, outs); len(got) != 1 || !got[0].At.Before(outs[1].From) {
		t.Fatalf("slack: %+v", got)
	}
}

// Usefulness is the mean of (first try − the status's baseline) over the
// last known injections: no score under UsefulMin, unknown gates are not
// failures, and each status has its own.
func TestUsefulnessScore(t *testing.T) {
	var outs []Outcome
	var events []Event
	// Baseline: in design passes first try half the time over 20 visits of
	// other issues.
	for i := 0; i < 20; i++ {
		outs = append(outs, visit("x/base", "in design", 100+i, boolp(i%2 == 0)))
	}
	// m-good reaches 10 design stages, all first try: lift +0.5.
	// m-bad reaches 10 design stages, half first try: lift 0.
	for i := 0; i < 10; i++ {
		slug := "ui/s" + string(rune('a'+i))
		outs = append(outs, visit(slug, "in design", i, boolp(true)))
		events = append(events, useAt(slug, "in design", i, 1, "m-good"))
		slug2 := "ui/t" + string(rune('a'+i))
		outs = append(outs, visit(slug2, "in design", i, boolp(i%2 == 0)))
		events = append(events, useAt(slug2, "in design", i, 1, "m-bad"))
	}
	// m-few reaches 3; m-open reaches 9 open spans (no gate known).
	for i := 0; i < 3; i++ {
		events = append(events, useAt("ui/s"+string(rune('a'+i)), "in design", i, 2, "m-few"))
	}
	for i := 0; i < 9; i++ {
		slug := "ui/o" + string(rune('a'+i))
		outs = append(outs, Outcome{Issue: slug, Status: "in design", From: u0.Add(time.Duration(i) * time.Hour), Open: true})
		events = append(events, useAt(slug, "in design", i, 1, "m-open"))
	}
	base := Baseline(outs)
	if b := base["in design"]; b <= 0.5 || b >= 0.8 {
		t.Fatalf("baseline %v", b)
	}
	sc := ScoreAll(events, outs)
	good, bad := sc.Of("m-good"), sc.Of("m-bad")
	if !good.Measured() || !good.Helps() || good.Known != 10 || good.FirstTry != 10 {
		t.Fatalf("good %+v", good)
	}
	if !bad.Unhelpful() || bad.Helps() {
		t.Fatalf("bad %+v", bad)
	}
	if few := sc.Of("m-few"); few.Measured() || few.Known != 3 || few.Word() != "not measured" {
		t.Fatalf("few %+v", few)
	}
	if open := sc.Of("m-open"); open.Injections != 9 || open.Known != 0 || open.Unhelpful() {
		t.Fatalf("open spans count as unknown, not failures: %+v", open)
	}
	if bs := good.ByStatus["in design"]; bs.Known != 10 || bs.Lift != good.Lift {
		t.Fatalf("by status %+v", good.ByStatus)
	}

	// The window keeps the latest UsefulWindow known injections.
	var many []Event
	var manyOuts []Outcome
	for i := 0; i < UsefulWindow+5; i++ {
		slug := "w/" + strings.Repeat("a", i+1)
		manyOuts = append(manyOuts, visit(slug, "testing", i, boolp(i >= 5)))
		many = append(many, useAt(slug, "testing", i, 1, "m-w"))
	}
	w := ScoreAll(many, manyOuts).Of("m-w")
	if w.Known != UsefulWindow || w.FirstTry != UsefulWindow || w.Injections != UsefulWindow+5 {
		t.Fatalf("window %+v", w)
	}
}

// The trace files on disk read into outcomes: slug from the path, the
// open span with no gate.
func TestReadOutcomes(t *testing.T) {
	dir := t.TempDir()
	if outs, err := ReadOutcomes(dir); err != nil || len(outs) != 0 {
		t.Fatalf("no traces: %v %v", outs, err)
	}
	writeTrace(t, dir, "factory/one", []Outcome{visit("", "idea", 0, boolp(true)), {Status: "in design", From: u0.Add(2 * time.Hour), Open: true}})
	outs, err := ReadOutcomes(dir)
	if err != nil || len(outs) != 2 {
		t.Fatalf("%+v %v", outs, err)
	}
	if outs[0].Issue != "factory/one" || outs[0].FirstTry == nil || !*outs[0].FirstTry || !outs[1].Open || outs[1].FirstTry != nil {
		t.Fatalf("%+v", outs)
	}
}

// unhelpfulScores is m-bad measured unhelpful over n design stages.
func unhelpfulScores(id string, n int) Scores {
	var outs []Outcome
	var evs []Event
	for i := 0; i < n; i++ {
		slug := "u/" + strings.Repeat("b", i+1)
		outs = append(outs, visit(slug, "in design", i, boolp(false)))
		evs = append(evs, useAt(slug, "in design", i, 1, id))
		outs = append(outs, visit("base/"+strings.Repeat("c", i+1), "in design", i, boolp(true)))
	}
	return ScoreAll(evs, outs)
}

// An unhelpful memory ranks below an equal one; a pinned one never does.
func TestUnhelpfulDecays(t *testing.T) {
	a := Memory{ID: "m-bad", Text: "the gate reads the design section", Kind: KindFact, Created: stamp(u0)}
	b := Memory{ID: "m-new", Text: "the gate reads the design section", Kind: KindFact, Created: stamp(u0)}
	scores := unhelpfulScores("m-bad", UsefulMin)
	if !scores.Unhelpful(a) {
		t.Fatalf("m-bad %+v", scores.Of("m-bad"))
	}
	r := Decayed{Inner: BM25{}, Now: u0, Scores: scores}
	hits := r.Rank(Query{Text: "gate design"}, []Memory{a, b})
	if len(hits) != 2 || hits[0].ID != "m-new" {
		t.Fatalf("hits %+v", hits)
	}
	if r.StrengthOf(a) >= r.StrengthOf(b) {
		t.Fatal("strength not lowered")
	}
	a.Pinned = true
	if scores.Unhelpful(a) || r.StrengthOf(a) != 1 {
		t.Fatal("a pinned memory is never ranked down")
	}
	// Without scores nothing changes.
	plain := Decayed{Inner: BM25{}, Now: u0}
	a.Pinned = false
	if plain.StrengthOf(a) != Strength(a, u0) {
		t.Fatal("no scores: plain strength")
	}
}

// The pack proposes forgetting an unhelpful memory measured over ForgetMin
// stages, never a pinned one, and writes nothing.
func TestConsolidateUnhelpful(t *testing.T) {
	dir := t.TempDir()
	s := ProjectStore(dir)
	add := func(text string) string {
		res, err := s.Add(Draft{Text: text, Kind: KindPitfall, Scope: ScopeProject}, AddOptions{Mode: ModeNew}, u0)
		if err != nil {
			t.Fatal(err)
		}
		return res.Memory.ID
	}
	bad, pinned, young := add("alpha bravo charlie delta"), add("echo foxtrot golf hotel"), add("india juliet kilo lima")
	if err := s.Pin(pinned, "lead-x", true, u0); err != nil {
		t.Fatal(err)
	}
	scores := Scores{}
	for id, u := range unhelpfulScores(bad, ForgetMin) {
		scores[id] = u
	}
	for id, u := range unhelpfulScores(pinned, ForgetMin) {
		scores[id] = u
	}
	for id, u := range unhelpfulScores(young, UsefulMin) {
		scores[id] = u
	}
	before, _ := os.ReadFile(s.Path)
	set, _ := s.Load()
	pack := ConsolidateScored(set.All(), scores, u0)
	if len(pack.Unhelpful) != 1 || pack.Unhelpful[0].ID != bad {
		t.Fatalf("unhelpful %+v", pack.Unhelpful)
	}
	if _, ok := pack.Scores[young]; !ok {
		t.Fatal("the pack carries each scored member's usefulness")
	}
	after, _ := os.ReadFile(s.Path)
	if string(before) != string(after) {
		t.Fatal("the pack wrote to the log")
	}
	if p := Consolidate(set.All(), u0); len(p.Unhelpful) != 0 {
		t.Fatal("no scores: no unhelpful list")
	}
}

// A stage gets its --at memories first (most useful there first), and the
// rest without memories --at another status or measured unhelpful at this
// one; unmeasured memories still reach it, pinned ones always stay.
func TestForStage(t *testing.T) {
	mk := func(id, at string) Memory {
		return Memory{ID: id, Text: id, Kind: KindFact, At: at, Created: stamp(u0)}
	}
	atA, atB, other, plain, noHelp, pin := mk("m-at-a", "testing"), mk("m-at-b", "testing"), mk("m-other", "in design"), mk("m-plain", ""), mk("m-nohelp", ""), mk("m-pin", "")
	pin.Pinned = true
	scores := unhelpfulScores("m-nohelp", UsefulMin)
	// m-nohelp is unhelpful in design only; in testing it is unmeasured.
	var outs []Outcome
	var evs []Event
	for i := 0; i < UsefulMin; i++ {
		slug := "h/" + strings.Repeat("d", i+1)
		outs = append(outs, visit(slug, "testing", i, boolp(true)), visit("hb/"+strings.Repeat("e", i+1), "testing", i, boolp(false)))
		evs = append(evs, useAt(slug, "testing", i, 1, "m-at-b"))
	}
	for id, u := range ScoreAll(evs, outs) {
		scores[id] = u
	}
	mems := []Memory{atA, atB, other, plain, noHelp, pin}

	got, rest := ForStage(mems, "testing", scores, u0, 3, 600)
	if len(got.Shown) != 2 || got.Shown[0].ID != "m-at-b" {
		t.Fatalf("at: %+v", got.Shown)
	}
	ids := func(ms []Memory) string {
		var out []string
		for _, m := range ms {
			out = append(out, m.ID)
		}
		return strings.Join(out, ",")
	}
	if r := ids(rest); r != "m-plain,m-nohelp,m-pin" {
		t.Fatalf("testing rest %s", r)
	}
	_, rest = ForStage(mems, "in design", scores, u0, 3, 600)
	if r := ids(rest); r != "m-plain,m-pin" {
		t.Fatalf("in design rest %s (m-other is shown as --at, the testing ones are --at another status, m-nohelp is unhelpful here)", r)
	}
	// The caller's cap holds.
	got, _ = ForStage(mems, "testing", scores, u0, 1, 600)
	if len(got.Shown) != 1 || got.More != 1 {
		t.Fatalf("cap: %+v", got)
	}
}

// Text that ties a memory to one issue is found; general facts pass.
func TestIssueNote(t *testing.T) {
	own := "factory/memory-as-a-cache-usefulness"
	for _, c := range []struct{ text, want string }{
		{"On this issue the gate needs the Design boxes", "this issue"},
		{"My branch has the trace merged", "my branch"},
		{"Pushed work/factory/memory-as-a-cache-usefulness-from-traces today", "work branch"},
		{"factory/memory-as-a-cache-usefulness needs the trace first", "names the issue"},
		{"The memory-as-a-cache-usefulness design waits on traces", "names the issue"},
	} {
		if got := IssueNote(c.text, own); !strings.Contains(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.text, got, c.want)
		}
	}
	for _, text := range []string{
		"internal/memory/usefulness.go reads the traces under <issues>/.traces",
		"A merge titled 'main into work/ui/x' does not count for commits_reference_issue",
		"Read the files this branch changed with tracker.BranchChanges",
		"The engine package builds the board",
		"Decision factory/memory-as-a-cache-usefulness#D-2 sets the memory policy",
		"The testing status runs make validate",
	} {
		if got := IssueNote(text, own); got != "" {
			t.Errorf("%q flagged: %s", text, got)
		}
	}
	if got := IssueNote("The engine package builds the board", "engine"); got != "" {
		t.Errorf("short own slug flagged: %s", got)
	}
	if got := NamesIssue("Fixed on main (cli/some-long-issue-slug-for-the-test)", []string{"cli/some-long-issue-slug-for-the-test"}); got == "" {
		t.Error("another issue's slug not found")
	}
}
