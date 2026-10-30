package tracker

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestConfirmDecision_RewritesTheSameLine(t *testing.T) {
	fp := writeDecisionIssue(t, "Body cites D-2.\n\n## Decisions\n"+
		"- **D-1** · 2026-10-02 · YOLO: (provisional) Gold resets each stage. (also considered: keep it) (replaces economy/gold#D-0)\n"+
		"- **D-2** · 2026-10-03 · lead, YOLO: (provisional, YOLO) Shop opens at stage 2, \"the owner said shops are fine\"\n"+
		"- **D-3** · 2026-10-04 · deferred · overnight provisional: Balance later.\n"+
		"- **D-4** · 2026-10-05 · chat: Keep the map, \"keep it\"\n\n## Design\nText.")
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.Local)

	d, err := ConfirmDecision(fp, "economy/gold", "d-1", "", now)
	if err != nil {
		t.Fatalf("ConfirmDecision D-1: %v", err)
	}
	if d.ID != "D-1" || d.Quote != "Confirmed." || d.Text != "Gold resets each stage" || d.How != "confirmed by owner 2026-10-30" ||
		d.AlsoConsidered != "keep it" || len(d.Replaces) != 1 || !d.Date.Equal(time.Date(2026, 10, 2, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("D-1 = %+v", d)
	}
	if NeedsOwnerWords(d) {
		t.Fatal("confirmed D-1 still needs the owner's words")
	}

	// A relayed quote stays when the owner adds no words; typed words win.
	if d, err = ConfirmDecision(fp, "economy/gold", "D-2", "", now); err != nil || d.Quote != "the owner said shops are fine" || d.How != "lead, confirmed by owner 2026-10-30" || d.Text != "Shop opens at stage 2" {
		t.Fatalf("D-2 = %+v, %v", d, err)
	}
	if d, err = ConfirmDecision(fp, "economy/gold", "D-3", "  later,\n fine ", now); err != nil || d.Quote != "later, fine" || !d.Deferred || d.How != "overnight, confirmed by owner 2026-10-30" {
		t.Fatalf("D-3 = %+v, %v", d, err)
	}

	body := readBody(t, fp)
	for _, want := range []string{
		`- **D-1** · 2026-10-02 · confirmed by owner 2026-10-30: Gold resets each stage, "Confirmed." (also considered: keep it) (replaces economy/gold#D-0)`,
		`- **D-3** · 2026-10-04 · deferred · overnight, confirmed by owner 2026-10-30: Balance later, "later, fine"` + "\n- **D-4**",
		"## Design\nText.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	if got := len(ParseDecisions("economy/gold", body)); got != 4 {
		t.Fatalf("decisions after confirm = %d, want 4", got)
	}

	if _, err := ConfirmDecision(fp, "economy/gold", "D-1", "again", now); !errors.Is(err, ErrDecisionConfirmed) {
		t.Fatalf("second confirm err = %v, want ErrDecisionConfirmed", err)
	}
	if _, err := ConfirmDecision(fp, "economy/gold", "D-4", "", now); !errors.Is(err, ErrDecisionConfirmed) {
		t.Fatalf("owner's quote err = %v, want ErrDecisionConfirmed", err)
	}
	if _, err := ConfirmDecision(fp, "economy/gold", "D-9", "", now); !errors.Is(err, ErrDecisionNotFound) {
		t.Fatalf("missing err = %v, want ErrDecisionNotFound", err)
	}
}

func TestNeedsOwnerWords(t *testing.T) {
	for _, tc := range []struct {
		d    Decision
		want bool
	}{
		{Decision{How: "chat", Text: "x"}, true},
		{Decision{How: "chat", Text: "x", Quote: "yes"}, false},
		{Decision{How: "lead chat", Text: "(provisional) x", Quote: "yes"}, true},
		{Decision{How: "worker, YOLO", Text: "x", Quote: "yes"}, true},
		{Decision{How: "overnight provisional", Text: "x", Quote: "yes"}, true},
		{Decision{How: "lead, confirmed by owner 2026-10-30", Text: "(provisional) x", Quote: "yes"}, false},
	} {
		if got := NeedsOwnerWords(tc.d); got != tc.want {
			t.Errorf("NeedsOwnerWords(%+v) = %v, want %v", tc.d, got, tc.want)
		}
	}
}
