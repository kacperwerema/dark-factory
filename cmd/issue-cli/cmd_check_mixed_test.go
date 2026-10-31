package main

import (
	"os"
	"strings"
	"testing"
)

// mixedProofFixture is the proof fixture with a ## Design section in front of
// the criteria: D1, D2 are plain boxes, AC1 and AC2 are ticked by proof.
func mixedProofFixture(t *testing.T) (string, func() *Context, func() string) {
	t.Helper()
	proj, issuePath, _ := makeProofFixture(t, "2026-10-23")
	data := strings.Replace(readFile(t, issuePath), "## Acceptance Criteria", "## Design\n- [ ] Sketch it\n- [ ] Name it\n\n## Acceptance Criteria", 1)
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return issuePath, func() *Context { ctx, _, _ := newTestContext(proj, false); return ctx }, func() string { return readFile(t, issuePath) }
}

func TestCheckMixedIdsTicksPlainRefusesCriteria(t *testing.T) {
	issuePath, newCtx, _ := mixedProofFixture(t)
	ctx := newCtx()
	err := runCheck(ctx, []string{"cli/sample", "D1", "d2", "AC1"})
	if err != nil {
		t.Fatalf("check D1 d2 AC1: plain ids ticked, so no error: %v", err)
	}
	out := ctx.Stdout.(interface{ String() string }).String()
	body := readFile(t, issuePath)
	assertContains(t, body, "- [x] Sketch it\n")
	assertContains(t, body, "- [x] Name it\n")
	assertContains(t, body, "- [ ] The count shows on the card\n")
	assertContains(t, out, "✓ Checked: D1")
	assertContains(t, out, "✓ Checked: D2")
	assertContains(t, out, "✗ Not ticked: AC1 is ticked by its proof, not by hand (issue-cli prove cli/sample --list)")

	// Only criteria (and boxes already done): nothing ticked, non-zero exit.
	ctx = newCtx()
	err = runCheck(ctx, []string{"cli/sample", "D1", "AC2"})
	if err == nil || !strings.Contains(err.Error(), "ticked by their proof") {
		t.Fatalf("check D1 (done) AC2: nothing ticked, want the refusal: %v", err)
	}
	assertContains(t, readFile(t, issuePath), "- [ ] The card glows (visual)\n")

	// An unknown id among them still ticks nothing.
	_, newCtx2, read2 := mixedProofFixture(t)
	ctx = newCtx2()
	if err := runCheck(ctx, []string{"cli/sample", "D1", "D9", "AC1"}); err == nil {
		t.Fatal("an unknown id: want an error")
	}
	assertContains(t, read2(), "- [ ] Sketch it\n")
}

func TestCheckMixedIdsOwnerBoxesStillRefuseAll(t *testing.T) {
	issuePath, newCtx, read := mixedProofFixture(t)
	data := read() + "\n## Human Testing\n- [ ] Owner looked at it\n"
	if err := os.WriteFile(issuePath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	asWorker(t)
	ctx := newCtx()
	err := runCheck(ctx, []string{"cli/sample", "D1", "HT1", "AC1"})
	if err == nil {
		t.Fatal("a worker ticking an owner box among mixed ids: want a refusal")
	}
	assertContains(t, err.Error(), "these boxes are the owner's")
	assertContains(t, err.Error(), "HT1 Owner looked at it")
	if got := read(); got != data {
		t.Fatalf("a refused call changed the file:\n%s", got)
	}
}
