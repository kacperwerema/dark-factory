package outputs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviseAddsVersionsAndReopensReview(t *testing.T) {
	p := project(t)
	src := filepath.Join(t.TempDir(), "brand.md")
	writeFile(t, src, "# Brand\n\nv1\n")
	o, err := Add(p, AddInput{Department: "marketing", Title: "Brand", Kind: "report", File: src}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !o.IsMarkdown() || o.Version() != 1 || len(o.Versions) != 1 || o.Previous() != nil {
		t.Fatalf("v1 = %+v", o)
	}
	if _, err := Review(p, o.ID, StateRejected, "not yet", "on the shelf", "owner", now); err != nil {
		t.Fatal(err)
	}
	writeFile(t, src, "# Brand\n\nv2\n")
	o2, err := Revise(p, o.ID, src, "answers the comments", "agent-mkt", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if o2.Version() != 2 || o2.State != StateReview || o2.File == o.File || o2.Previous().File != o.File || o2.Versions[1].Note != "answers the comments" {
		t.Fatalf("v2 = %+v", o2)
	}
	if !strings.Contains(o2.File, "-v2-") {
		t.Errorf("v2 file = %q", o2.File)
	}
	old, _ := os.ReadFile(filepath.Join(FilesDir(p), o.File))
	if string(old) != "# Brand\n\nv1\n" {
		t.Errorf("v1 file changed: %q", old)
	}
	if _, err := Review(p, o.ID, StateApproved, "ok", "on the shelf", "owner", now); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(p, o.ID, "", "agent", now); err != nil {
		t.Fatal(err)
	}
	if _, err := Revise(p, o.ID, src, "", "agent", now); err == nil || !strings.Contains(err.Error(), "published") {
		t.Errorf("revise of published = %v", err)
	}
	url, _ := Add(p, AddInput{Department: "marketing", Title: "Link", Kind: "page", URL: "https://acme.example"}, now)
	if _, err := Revise(p, url.ID, src, "", "agent", now); err == nil || !strings.Contains(err.Error(), "not a file") {
		t.Errorf("revise of a link = %v", err)
	}
}

func TestCommentsThreadsRepliesResolve(t *testing.T) {
	p := project(t)
	id := "o-20261029-ab00"
	whole, err := AddComment(p, id, Anchor{}, "  Lead with what I settle.  ", "owner", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if !whole.Anchor.Whole() || whole.Messages[0].Text != "Lead with what I settle." || !strings.HasPrefix(whole.ID, "c-") {
		t.Fatalf("whole = %+v", whole)
	}
	long := strings.Repeat("x", 600)
	pass, err := AddComment(p, id, Anchor{Heading: "proposal", HeadingText: "Proposal", Quote: "A dark   factory\nwith one light on: " + long, Prefix: strings.Repeat("p", 50) + " B. ", Suffix: " C. Run the company and more and more text"}, "B is the one.", "owner", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	a := pass.Anchor
	if len([]rune(a.Quote)) != MaxQuoteRunes || !strings.HasPrefix(a.Quote, "A dark factory with one light on: ") || len([]rune(a.Prefix)) != 30 || !strings.HasSuffix(a.Prefix, " B. ") || len([]rune(a.Suffix)) != 30 {
		t.Errorf("anchor = %+v", a)
	}
	if _, err := AddComment(p, id, Anchor{}, "   ", "owner", 1, now); err == nil {
		t.Error("empty comment accepted")
	}
	if _, err := Reply(p, id, pass.ID, "Noted: v2 makes B the line.", "agent-mkt", 1, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(p, id, whole.ID, "owner", false, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := Reply(p, id, "c-zzzz", "x", "owner", 1, now); err == nil || !strings.Contains(err.Error(), whole.ID) {
		t.Errorf("unknown thread = %v", err)
	}
	all, bad := LoadComments(p)
	if len(bad) != 0 {
		t.Fatalf("bad lines: %v", bad)
	}
	threads := all[id]
	if len(threads) != 2 || !threads[0].Resolved || threads[0].ResolvedBy != "owner" || len(threads[1].Messages) != 2 || threads[1].Messages[1].By != "agent-mkt" {
		t.Fatalf("threads = %+v", threads)
	}
	if OpenCount(threads) != 1 {
		t.Errorf("open = %d", OpenCount(threads))
	}
	if _, err := Resolve(p, id, whole.ID, "owner", true, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	all, _ = LoadComments(p)
	if OpenCount(all[id]) != 2 {
		t.Errorf("reopened: open = %d", OpenCount(all[id]))
	}
}

// A thread stays on its passage across a revision that keeps the passage,
// falls back to its heading when the passage goes, and is gone when both go.
func TestLocateSurvivesRevision(t *testing.T) {
	th := &Thread{Anchor: Anchor{Heading: "line-options", HeadingText: "Line options", Quote: "one light on: yours.", Prefix: "B. A dark factory with ", Suffix: " C. Run"}}
	v1 := "Line options\nA. Agents do the work.\nB. A dark factory with one light on: yours.\nC. Run the company."
	if place, at := Locate(th, v1, map[string]int{"line-options": 0}); place != PlaceHere || at <= 0 {
		t.Errorf("v1: %s %d", place, at)
	}
	v2 := "Line options\nWhat you settle first.\n\nB.   A dark factory with one light on:\n yours. (chosen)"
	if place, _ := Locate(th, v2, map[string]int{"line-options": 0}); place != PlaceHere {
		t.Errorf("v2 (passage kept, context changed): %s", place)
	}
	v3 := "Line options\nA. Agents do the work."
	if place, _ := Locate(th, v3, map[string]int{"line-options": 0}); place != PlaceHeading {
		t.Errorf("v3 (passage gone): %s", place)
	}
	if place, _ := Locate(th, v3, map[string]int{}); place != PlaceGone {
		t.Errorf("v3 without the heading: %s", place)
	}
	if place, _ := Locate(&Thread{}, v1, nil); place != PlaceWhole {
		t.Errorf("whole: %s", place)
	}
	// Two copies of the quote: the context picks the second.
	twice := &Thread{Anchor: Anchor{Quote: "ship it", Prefix: "then ", Suffix: ""}}
	if _, at := Locate(twice, "first ship it, then ship it", nil); at != strings.LastIndex("first ship it, then ship it", "ship it") {
		t.Errorf("repeat picked %d", at)
	}
}

func TestLineDiff(t *testing.T) {
	d := LineDiff("a\nb\nc\nd\n", "a\nB\nc\nd\ne\n")
	var got []string
	for _, l := range d {
		got = append(got, string(l.Op)+l.Text)
	}
	want := " a|-b|+B| c| d|+e"
	if strings.Join(got, "|") != want {
		t.Errorf("diff = %q, want %q", strings.Join(got, "|"), want)
	}
	if d := LineDiff("same\n", "same\n"); len(d) != 1 || d[0].Op != ' ' {
		t.Errorf("equal = %+v", d)
	}
}
