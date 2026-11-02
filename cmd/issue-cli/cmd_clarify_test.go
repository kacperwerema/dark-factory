package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const clarifyFixture = "\n## Clarify\n\n" +
	"### Q1. Where does the owner answer?\n" +
	"1. **On the Station** (recommended) needs_you.go keeps one row. Cost: one card.\n" +
	"2. **In each row** Cost: a form per row.\n" +
	"**Answer:** —\n\n" +
	"### Q2. Keep comments?\n" +
	"1. **Keep** (recommended) Cost: nothing.\n" +
	"2. **Drop** Cost: a script.\n" +
	"**Answer:** —\n"

func addClarify(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(clarifyFixture)...), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestClarifyShowsAssumesAndRecordsTheOwnersAnswer(t *testing.T) {
	proj, samplePath := makeGateFixture(t, "in design", "", true)

	// No section yet: the format is the hint.
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runClarify(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "No ## Clarify yet on cli/sample")
	assertContains(t, stdout.String(), "**Answer:** —")

	addClarify(t, samplePath)
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runClarify(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	assertContains(t, out, "== Clarify (2 questions: 2 open) ==")
	assertContains(t, out, "Q1. Where does the owner answer?  [open]")
	assertContains(t, out, "1. On the Station (recommended) · cost: one card")

	// The lead relays the owner's answer to Q2; --quote is required.
	ctx, _, _ = newTestContext(proj, false)
	if err := runClarify(ctx, []string{"cli/sample", "--answer", "Q2=2"}); err == nil || !strings.Contains(err.Error(), "--answer needs --quote") {
		t.Fatalf("no quote: %v", err)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local) }
	if err := runClarify(ctx, []string{"cli/sample", "--answer", "q2=2", "--quote", "Drop them.", "--how", "chat"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "✓ Recorded the owner's answer to Q2: - **D-1** · 2026-10-31 · chat: Q2 Keep comments → Drop, \"Drop them.\"")
	body := readFile(t, samplePath)
	assertContains(t, body, `**Answer:** 2 · 2026-10-31 · owner: "Drop them." (D-1)`)

	// --assume marks the rest assumed and leaves the owner's answer.
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runClarify(ctx, []string{"cli/sample", "--assume"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "✓ Assumed Q1 (the recommendation)")
	assertContains(t, stdout.String(), "Q1. Where does the owner answer?  [assumed — took the recommendation]")
	assertContains(t, stdout.String(), "→ 1. On the Station")
	assertContains(t, readFile(t, samplePath), "**Answer:** assumed — took the recommendation")

	ctx, stdout, _ = newTestContext(proj, true)
	if err := runClarify(ctx, []string{"cli/sample"}); err != nil {
		t.Fatal(err)
	}
	var got clarifyOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if len(got.Clarify.Questions) != 2 || got.Clarify.Questions[0].Answer.State != tracker.AnswerAssumed || got.Clarify.Questions[1].Answer.State != tracker.AnswerOwner {
		t.Fatalf("json = %+v", got.Clarify)
	}

	ctx, _, _ = newTestContext(proj, false)
	if err := runClarify(ctx, []string{"cli/sample", "--reason", "x"}); err == nil {
		t.Fatal("--reason without --assume should be refused")
	}
}

func TestClarifyAccess(t *testing.T) {
	ro := commandAccess["clarify"]
	if !ro([]string{"x/y"}) || !ro([]string{"x/y", "--json"}) || ro([]string{"x/y", "--assume"}) || ro([]string{"x/y", "--answer", "Q1=1"}) {
		t.Fatal("clarify reads unless --assume or --answer")
	}
}
