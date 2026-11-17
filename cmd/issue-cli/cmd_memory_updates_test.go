package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/memory"
)

// The 2026-11-15 incident: --updates named a memory about a different fact
// (19% of the same words) and replaced its whole text. Under a third of the
// words in common, --updates prints the old text and refuses; --yes writes it.
func TestMemoryUpdatesRefusesAnUnrelatedFact(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	old := "Routing (workflow.yaml routing:): every type starts on Claude since 2026-11-05. Codex is paused because its sandbox asks to approve each issue-cli and git write outside the worktree; it comes back once the owner picks routing: codex: writable/approval values."
	unrelated := "Routing picks a model as well as an agent (since 2026-11-15): a rule's model: is passed as --model to that rule's agent; with none, claude workers on docs and report start on claude-sonnet-5-5. The dispatch sidecar records model; compare with issue-cli stats --by-model."
	if sim := memory.Similarity(old, unrelated); sim >= memory.UpdateThreshold {
		t.Fatalf("fixture overlaps %.2f, want under %.2f", sim, memory.UpdateThreshold)
	}
	if err := runMemory(ctx, []string{"add", old}); err != nil {
		t.Fatal(err)
	}
	m := onlyMemory(t, proj)

	out.Reset()
	err := runMemory(ctx, []string{"add", unrelated, "--updates", m.ID})
	var un *memory.UnrelatedUpdateError
	if !errors.As(err, &un) {
		t.Fatalf("unrelated --updates: err %v, out %q", err, out.String())
	}
	msg := err.Error()
	for _, want := range []string{"nothing was written", old, "--supersedes " + m.ID, "--new", "--yes"} {
		assertContains(t, msg, want)
	}
	if got := onlyMemory(t, proj); got.Text != old {
		t.Fatalf("refused update still wrote: %q", got.Text)
	}

	// The same fact, better worded, still goes through without --yes.
	better := strings.Replace(old, "Codex is paused", "Codex stays paused", 1)
	if err := runMemory(ctx, []string{"add", better, "--updates", m.ID}); err != nil {
		t.Fatalf("a rewording refused: %v", err)
	}
	if got := onlyMemory(t, proj); got.Text != better {
		t.Fatalf("rewording not written: %q", got.Text)
	}

	out.Reset()
	if err := runMemory(ctx, []string{"add", unrelated, "--updates", m.ID, "--yes"}); err != nil {
		t.Fatalf("--yes: %v", err)
	}
	assertContains(t, out.String(), "✓ Updated "+m.ID)
	if got := onlyMemory(t, proj); got.Text != unrelated {
		t.Fatalf("--yes not written: %q", got.Text)
	}

	if err := runMemory(ctx, []string{"add", "x y z words", "--new", "--yes"}); err == nil {
		t.Error("--yes without --updates accepted")
	}
}

// The near-duplicate refusal names --new for a different fact, and warns
// that --updates overwrites the whole text.
func TestNearDuplicateRefusalNamesTheRightFlag(t *testing.T) {
	memoryAs(t, "")
	proj := goalsProject(t)
	ctx, _ := goalsCtx(t, proj, false)
	if err := runMemory(ctx, []string{"add", "The playtest runner needs Chrome for Testing on macOS"}); err != nil {
		t.Fatal(err)
	}
	first := onlyMemory(t, proj)
	err := runMemory(ctx, []string{"add", "The playtest runner needs Chrome for Testing on macOS and Linux"})
	var sim *memory.SimilarError
	if !errors.As(err, &sim) {
		t.Fatalf("near duplicate: %v", err)
	}
	lineWith := func(flag string) string {
		for _, l := range strings.Split(err.Error(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), flag) {
				return l
			}
		}
		t.Fatalf("no %s line in:\n%s", flag, err)
		return ""
	}
	assertContains(t, lineWith("--new"), "a different fact")
	assertContains(t, lineWith("--updates "+first.ID), "the same fact")
	assertContains(t, lineWith("--updates "+first.ID), "whole text")
	assertContains(t, lineWith("--supersedes "+first.ID), "no longer true")
	if strings.Contains(lineWith("--updates "+first.ID), "different") {
		t.Errorf("--updates offered for a different fact: %q", lineWith("--updates "+first.ID))
	}
}
