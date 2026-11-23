package main

import (
	"strings"
	"testing"
)

// AC1, AC2: a move prints its ✓ line first and, after the memories of the
// status it enters (and the trace line), again last, so `| tail -1` shows it.
func TestTransitionPrintsTheMoveLineFirstAndLast(t *testing.T) {
	memoryAs(t, "")
	proj := newTestProject(t)
	memAt(t, proj, "fast forward right after the merge of main", "in design")

	out := transitionOut(t, proj, "plain", "in design")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	first, last := lines[0], lines[len(lines)-1]
	if !strings.HasPrefix(first, "✓ ") || !strings.HasSuffix(first, " → in design") {
		t.Fatalf("line 1 = %q, want the move line:\n%s", first, out)
	}
	if last != first {
		t.Fatalf("last line = %q, want the move line %q again:\n%s", last, first, out)
	}
	mems := strings.Index(out, "== Memories for in design ==")
	if mems < 0 || strings.Count(out, first+"\n") != 2 || strings.LastIndex(out, first) < mems {
		t.Fatalf("want the memories between the two move lines:\n%s", out)
	}
}

// AC3: a refused move prints no ✓ <from> → <to> line.
func TestARefusedTransitionPrintsNoMoveLine(t *testing.T) {
	proj := newTestProject(t)
	ctx, out, _ := testContext(proj)
	if err := runTransition(ctx, []string{"plain", "--to", "done"}); err == nil {
		t.Fatalf("plain → done moved:\n%s", out.String())
	}
	if strings.Contains(out.String(), "→ done") {
		t.Fatalf("a refusal printed a move line:\n%s", out.String())
	}
}
