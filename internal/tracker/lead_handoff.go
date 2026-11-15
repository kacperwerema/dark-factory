package tracker

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Caps on what the nightly lead flush writes
// (factory/two-agents-…#D-6). A fresh lead's first prompt carries the note
// and the summary whole, so they stay short.
const (
	HandoffNoteMaxLines    = 80
	HandoffSummaryMaxLines = 30
)

// ChoreType is the type of a job run that closes itself: no commit, no owner
// review, no post-mortem (the workflow's `chore` type, the chore-type entry).
const ChoreType = "chore"

// HandoffHeadings are the sections every flushed note has, in the order the
// brief asks for them. ## Next is the Status page's What's next list.
var HandoffHeadings = []string{"State", "Queue", "Waiting on the owner", "Rules in force", "How to resume", "Next"}

// HandoffSummaryName is the file name of the day's lead-chat summary, beside
// the note. It can't match NewestHandoffNote's -<lead>.md.
func HandoffSummaryName(day time.Time, lead string) string {
	return day.Format("2006-01-02") + "-" + lead + ".summary.md"
}

// handoffSummaryOf is the summary beside the note at notePath.
func handoffSummaryOf(notePath string) string {
	return strings.TrimSuffix(notePath, ".md") + ".summary.md"
}

// CheckHandoffNote says what is wrong with a note the flush is about to
// write: too long, or a section missing. nil when it may be written.
func CheckHandoffNote(text string) error {
	if n := countLines(text); n > HandoffNoteMaxLines {
		return fmt.Errorf("the note is %d lines, the cap is %d: a fresh lead reads it whole, so cut what memory or the board already hold", n, HandoffNoteMaxLines)
	}
	var missing []string
	for _, h := range HandoffHeadings {
		if !hasHeading(text, h) {
			missing = append(missing, "## "+h)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the note has no %s (every note has: %s)", strings.Join(missing, ", "), strings.Join(HandoffHeadings, ", "))
	}
	return nil
}

// CheckHandoffSummary says what is wrong with a chat summary.
func CheckHandoffSummary(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("the summary is empty: say what the owner and the lead settled, or \"No lead chat today.\"")
	}
	if n := countLines(text); n > HandoffSummaryMaxLines {
		return fmt.Errorf("the summary is %d lines, the cap is %d", n, HandoffSummaryMaxLines)
	}
	return nil
}

func countLines(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func hasHeading(text, name string) bool {
	for _, line := range strings.Split(text, "\n") {
		if h, ok := strings.CutPrefix(strings.TrimSpace(line), "## "); ok && strings.EqualFold(strings.TrimSpace(h), name) {
			return true
		}
	}
	return false
}

// LeadHandoffSection is the part of the CEO's first prompt that carries the
// newest note in notesDir and its chat summary (factory/two-agents-…#D-7);
// "" with no note. An older note longer than the cap is cut, with its path.
func LeadHandoffSection(notesDir, lead string) string {
	path := NewestHandoffNote(notesDir, lead)
	if path == "" {
		return ""
	}
	note, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Where the last lead left off\n\n")
	fmt.Fprintf(&b, "From %s:\n\n", path)
	lines := strings.Split(strings.TrimSpace(string(note)), "\n")
	cut := len(lines) > HandoffNoteMaxLines
	if cut {
		lines = lines[:HandoffNoteMaxLines]
	}
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n")
	if cut {
		fmt.Fprintf(&b, "\n(The note goes on: read the rest in %s.)\n", path)
	}
	if sum, err := os.ReadFile(handoffSummaryOf(path)); err == nil && strings.TrimSpace(string(sum)) != "" {
		b.WriteString("\n### The last day's lead chat\n\n")
		b.WriteString(strings.TrimSpace(string(sum)))
		b.WriteString("\n")
	}
	return b.String()
}
