package tracker

import (
	"strings"
	"testing"
)

func TestCountCheckboxes(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		total   int
		checked int
	}{
		{"no checkboxes", "Some text", 0, 0},
		{"all unchecked", "- [ ] a\n- [ ] b", 2, 0},
		{"all checked", "- [x] a\n- [X] b", 2, 2},
		{"mixed", "- [x] done\n- [ ] todo\n- [X] also done", 3, 2},
		{"with indentation", "  - [ ] indented\n  - [x] indented checked", 2, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total, checked := CountCheckboxes(tt.body)
			if total != tt.total || checked != tt.checked {
				t.Errorf("CountCheckboxes() = (%d, %d), want (%d, %d)", total, checked, tt.total, tt.checked)
			}
		})
	}
}

func TestCountCheckboxesInSection(t *testing.T) {
	body := "## Idea\n- [x] described\n- [x] scoped\n\n## Implementation\n- [x] code done\n- [ ] tests written\n\n## Testing\n- [ ] all passing"

	t.Run("counts only in target section", func(t *testing.T) {
		total, checked := CountCheckboxesInSection(body, "Implementation")
		if total != 2 || checked != 1 {
			t.Errorf("got (%d, %d), want (2, 1)", total, checked)
		}
	})

	t.Run("fully checked section", func(t *testing.T) {
		total, checked := CountCheckboxesInSection(body, "Idea")
		if total != 2 || checked != 2 {
			t.Errorf("got (%d, %d), want (2, 2)", total, checked)
		}
	})

	t.Run("missing section returns zero", func(t *testing.T) {
		total, checked := CountCheckboxesInSection(body, "Nonexistent")
		if total != 0 || checked != 0 {
			t.Errorf("got (%d, %d), want (0, 0)", total, checked)
		}
	})

	t.Run("case insensitive heading match", func(t *testing.T) {
		total, checked := CountCheckboxesInSection(body, "implementation")
		if total != 2 || checked != 1 {
			t.Errorf("got (%d, %d), want (2, 1)", total, checked)
		}
	})
}

func TestCheckCheckbox(t *testing.T) {
	body := "- [ ] implement feature\n- [ ] write tests\n- [x] create PR"

	t.Run("exact match", func(t *testing.T) {
		result, found := CheckCheckbox(body, "implement feature")
		if !found {
			t.Fatal("expected match")
		}
		if !strings.Contains(result, "- [x] implement feature") {
			t.Errorf("checkbox not checked: %s", result)
		}
	})

	t.Run("partial match", func(t *testing.T) {
		result, found := CheckCheckbox(body, "write")
		if !found {
			t.Fatal("expected match")
		}
		if !strings.Contains(result, "- [x] write tests") {
			t.Errorf("checkbox not checked: %s", result)
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, found := CheckCheckbox(body, "nonexistent")
		if found {
			t.Fatal("expected no match")
		}
	})

	t.Run("already checked not matched", func(t *testing.T) {
		_, found := CheckCheckbox(body, "create PR")
		if found {
			t.Fatal("already checked items should not be matched")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		_, found := CheckCheckbox(body, "IMPLEMENT")
		if !found {
			t.Fatal("expected case insensitive match")
		}
	})

	t.Run("skips checkboxes inside fenced code blocks", func(t *testing.T) {
		fenced := "```\n" +
			"- [ ] User-facing docs updated\n" +
			"```\n" +
			"\n" +
			"## Documentation\n" +
			"- [ ] User-facing docs updated\n"
		result, found := CheckCheckbox(fenced, "User-facing docs updated")
		if !found {
			t.Fatal("expected match outside fence")
		}
		// The in-fence checkbox must remain unchanged.
		fencedSegment := result[:strings.Index(result, "```\n\n")+len("```\n\n")]
		if !strings.Contains(fencedSegment, "- [ ] User-facing docs updated") {
			t.Errorf("in-fence checkbox should not be ticked:\n%s", fencedSegment)
		}
		// The real-section checkbox must be ticked.
		if !strings.Contains(result, "## Documentation\n- [x] User-facing docs updated") {
			t.Errorf("real-section checkbox should be ticked:\n%s", result)
		}
	})
}

func TestListCheckboxes(t *testing.T) {
	body := "intro\n- [ ] preamble box\n\n## Design\n- [x] one\n- [ ] two\n\n## Acceptance Criteria\n- [ ] crit a\n- [ ] crit b\n"
	items := ListCheckboxes(body)
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5", len(items))
	}
	// preamble box has empty section, index 1
	if items[0].Section != "" || items[0].Index != 1 || items[0].Text != "preamble box" {
		t.Errorf("item 0 = %+v", items[0])
	}
	// indexes reset per section
	if items[1].Section != "Design" || items[1].Index != 1 || !items[1].Checked {
		t.Errorf("item 1 = %+v", items[1])
	}
	if items[2].Section != "Design" || items[2].Index != 2 || items[2].Checked {
		t.Errorf("item 2 = %+v", items[2])
	}
	if items[3].Section != "Acceptance Criteria" || items[3].Index != 1 {
		t.Errorf("item 3 = %+v", items[3])
	}
}

func TestListCheckboxesSkipsFences(t *testing.T) {
	body := "## Design\n```\n- [ ] not real\n```\n- [ ] real box\n"
	items := ListCheckboxes(body)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (fenced box ignored)", len(items))
	}
	if items[0].Index != 1 || items[0].Text != "real box" {
		t.Errorf("item = %+v", items[0])
	}
}

func TestCheckByIndex(t *testing.T) {
	body := "## Design\n- [ ] alpha\n- [ ] beta\n\n## Acceptance Criteria\n- [ ] gamma\n"

	t.Run("by section and index", func(t *testing.T) {
		out, item, already, ok := CheckByIndex(body, "Design", 2)
		if !ok || already {
			t.Fatalf("ok=%v already=%v", ok, already)
		}
		if item.Text != "beta" {
			t.Errorf("matched %q, want beta", item.Text)
		}
		if !strings.Contains(out, "- [x] beta") || strings.Contains(out, "- [x] alpha") {
			t.Errorf("wrong box ticked:\n%s", out)
		}
	})

	t.Run("index stable after a tick", func(t *testing.T) {
		out, _, _, _ := CheckByIndex(body, "Design", 1)
		// beta is still index 2 even though alpha is now checked
		out2, item, _, ok := CheckByIndex(out, "Design", 2)
		if !ok || item.Text != "beta" {
			t.Fatalf("ok=%v item=%q", ok, item.Text)
		}
		if !strings.Contains(out2, "- [x] beta") {
			t.Errorf("beta not ticked:\n%s", out2)
		}
	})

	t.Run("whole-body index when section empty", func(t *testing.T) {
		_, item, _, ok := CheckByIndex(body, "", 3)
		if !ok || item.Text != "gamma" {
			t.Fatalf("ok=%v item=%q, want gamma", ok, item.Text)
		}
	})

	t.Run("already checked is a no-op", func(t *testing.T) {
		checked, _, _, _ := CheckByIndex(body, "Design", 1)
		out, item, already, ok := CheckByIndex(checked, "Design", 1)
		if !ok || !already {
			t.Fatalf("ok=%v already=%v", ok, already)
		}
		if item.Text != "alpha" || out != checked {
			t.Errorf("expected unchanged body for already-checked box")
		}
	})

	t.Run("out of range", func(t *testing.T) {
		if _, _, _, ok := CheckByIndex(body, "Design", 9); ok {
			t.Fatal("expected ok=false for out-of-range index")
		}
		if _, _, _, ok := CheckByIndex(body, "Nope", 1); ok {
			t.Fatal("expected ok=false for unknown section")
		}
	})
}

func TestMatchUncheckedByText(t *testing.T) {
	body := "## Design\n- [ ] write tests\n- [x] write docs\n\n## Impl\n- [ ] write tests again\n"

	t.Run("ambiguous across sections", func(t *testing.T) {
		got := MatchUncheckedByText(body, "", "write tests")
		if len(got) != 2 {
			t.Fatalf("got %d matches, want 2", len(got))
		}
	})

	t.Run("scoped by section disambiguates", func(t *testing.T) {
		got := MatchUncheckedByText(body, "Design", "write tests")
		if len(got) != 1 || got[0].Section != "Design" {
			t.Fatalf("got %+v, want single Design match", got)
		}
	})

	t.Run("checked boxes excluded", func(t *testing.T) {
		if got := MatchUncheckedByText(body, "", "write docs"); len(got) != 0 {
			t.Fatalf("got %d, want 0 (already checked)", len(got))
		}
	})
}

func TestHasTestPlan(t *testing.T) {
	t.Run("with both sections", func(t *testing.T) {
		body := "Some content\n\n## Test Plan\n\n### Automated\nUnit tests\n\n### Manual\nClick around"
		hasAuto, hasManual := HasTestPlan(body)
		if !hasAuto || !hasManual {
			t.Errorf("HasTestPlan = (%v, %v), want (true, true)", hasAuto, hasManual)
		}
	})

	t.Run("missing automated", func(t *testing.T) {
		body := "## Test Plan\n\n### Manual\nTest steps"
		hasAuto, hasManual := HasTestPlan(body)
		if hasAuto {
			t.Error("expected hasAutomated = false")
		}
		if !hasManual {
			t.Error("expected hasManual = true")
		}
	})

	t.Run("no test plan", func(t *testing.T) {
		body := "Just some content\n\n## Other Section"
		hasAuto, hasManual := HasTestPlan(body)
		if hasAuto || hasManual {
			t.Errorf("HasTestPlan = (%v, %v), want (false, false)", hasAuto, hasManual)
		}
	})

	t.Run("test plan ended by another h2", func(t *testing.T) {
		body := "## Test Plan\n### Automated\nTests\n## Next Section\n### Manual\nSteps"
		hasAuto, hasManual := HasTestPlan(body)
		if !hasAuto {
			t.Error("expected hasAutomated = true")
		}
		if hasManual {
			t.Error("Manual is outside Test Plan section, should be false")
		}
	})
}

func TestHasCommentWithPrefix(t *testing.T) {
	comments := []Comment{
		{Text: "tests: all unit tests pass"},
		{Text: "docs: updated readme"},
		{Text: "just a comment"},
	}

	tests := []struct {
		prefix string
		want   bool
	}{
		{"tests:", true},
		{"docs:", true},
		{"just", true},
		{"TESTS:", true}, // case insensitive
		{"missing:", false},
	}

	for _, tt := range tests {
		got := HasCommentWithPrefix(comments, tt.prefix)
		if got != tt.want {
			t.Errorf("HasCommentWithPrefix(%q) = %v, want %v", tt.prefix, got, tt.want)
		}
	}
}
