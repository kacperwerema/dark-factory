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

// HandoffHeadings are the sections every handoff note has, in the order the
// brief asks for them. ## Next is the Status page's What's next list. A
// note carries only what nothing else renders: workers, the queue and the
// owner's asks are in the lead's state file, which the fresh lead's brief
// renders live, so a note never copies them (they went stale by morning).
var HandoffHeadings = []string{"Next", "Rules in force", "Released and installed"}

// LegacyHandoffHeadings are the sections notes written before 2026-11-23
// had besides HandoffHeadings. A new note may not have them; an older note
// still loads, and the brief inlines it without them (the state file has
// their parts as they are now).
var LegacyHandoffHeadings = []string{"State", "Queue", "Waiting on the owner", "How to resume"}

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
	var copied []string
	for _, h := range LegacyHandoffHeadings {
		if hasHeading(text, h) {
			copied = append(copied, "## "+h)
		}
	}
	if len(copied) > 0 {
		return fmt.Errorf("the note has %s: the fresh lead's brief renders workers, the queue and the owner's asks live from the state file, so take them out (a note has only %s)", strings.Join(copied, ", "), strings.Join(HandoffHeadings, ", "))
	}
	return nil
}

// dropHeadings is text without its "## <name>" sections (heading to the
// next "## " heading), names matched without case.
func dropHeadings(text string, names []string) (string, bool) {
	drop := map[string]bool{}
	for _, n := range names {
		drop[strings.ToLower(n)] = true
	}
	var out []string
	skip, dropped := false, false
	for _, line := range strings.Split(text, "\n") {
		if h, ok := strings.CutPrefix(strings.TrimSpace(line), "## "); ok {
			skip = drop[strings.ToLower(strings.TrimSpace(h))]
			dropped = dropped || skip
		}
		if !skip {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n"), dropped
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
	// An older note's State, Queue and Waiting were as of the night
	// before: the state file below has them as they are now.
	text, legacy := dropHeadings(strings.TrimSpace(string(note)), LegacyHandoffHeadings)
	if legacy {
		b.WriteString("(Its State, Queue, Waiting and How to resume are left out: your state file has them as they are now.)\n\n")
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
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
