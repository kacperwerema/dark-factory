// Package critique is the fresh critic: a read-only agent that did not
// write the work reads it from paths only and reports findings as
// blocks / should / note with path:line. A blocks finding holds the
// workflow's `critique` gate until a new run clears it or the owner
// overrides it. See docs/Workflow/critique.md.
package critique

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Severities, strongest first.
const (
	Blocks = "blocks"
	Should = "should"
	Note   = "note"
)

// Finding is one line of the critic's output.
type Finding struct {
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	EndLine  int    `json:"end_line,omitempty"`
	// More is every further line or range the finding cites
	// (path:29,114 or path:14-16,144-147), in the critic's order.
	More []Span `json:"more,omitempty"`
	// Text is what the critic wrote after path:line, with the finding's
	// continuation lines (a lead-in line ending in ':' and its bullets)
	// joined by "\n".
	Text string `json:"text,omitempty"`
	// Malformed: the line starts with a severity word but isn't
	// "<severity> <path>:<line> — <text>"; Raw keeps it as written and
	// Problem says what is missing. It still counts at its severity.
	Malformed bool   `json:"malformed,omitempty"`
	Raw       string `json:"raw,omitempty"`
	Problem   string `json:"problem,omitempty"`
}

// Span is one line or range a finding cites.
type Span struct {
	Line    int `json:"line"`
	EndLine int `json:"end_line,omitempty"`
}

func (s Span) String() string {
	w := strconv.Itoa(s.Line)
	if s.EndLine > s.Line {
		w += "-" + strconv.Itoa(s.EndLine)
	}
	return w
}

// Spans is every line or range the finding cites, the first one first.
func (f Finding) Spans() []Span {
	if f.Line == 0 {
		return nil
	}
	return append([]Span{{Line: f.Line, EndLine: f.EndLine}}, f.More...)
}

// Where is "path:line", "path:line-end", or with several lines
// "path:29,114" / "path:14-16,144-147".
func (f Finding) Where() string {
	if f.Path == "" {
		return ""
	}
	var parts []string
	for _, s := range f.Spans() {
		parts = append(parts, s.String())
	}
	return f.Path + ":" + strings.Join(parts, ",")
}

// String is the finding in the format the critic writes: one line, with
// any continuation lines indented under it (so a bullet stays the
// finding's in a markdown comment, and Parse reads it back the same).
func (f Finding) String() string {
	if f.Malformed {
		return fmt.Sprintf("%s (malformed: %s) %s", f.Severity, f.Problem, indentMore(strings.TrimSpace(f.Raw)))
	}
	return fmt.Sprintf("%s %s — %s", f.Severity, f.Where(), indentMore(f.Text))
}

// indentMore indents every line after the first by two spaces.
func indentMore(s string) string {
	return strings.ReplaceAll(s, "\n", "\n  ")
}

// Report is a parsed critic output.
type Report struct {
	Findings []Finding `json:"findings"`
	Coverage string    `json:"coverage,omitempty"`
	Unread   []string  `json:"unread,omitempty"`
	// Problems are output-level faults (no Coverage line); each holds the
	// gate like a blocks finding.
	Problems []string `json:"problems,omitempty"`
}

// Count is how many findings have severity sev (malformed included).
func (r Report) Count(sev string) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == sev {
			n++
		}
	}
	return n
}

// Of is the findings of severity sev, in output order.
func (r Report) Of(sev string) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == sev {
			out = append(out, f)
		}
	}
	return out
}

// Malformed is the malformed findings.
func (r Report) Malformed() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Malformed {
			out = append(out, f)
		}
	}
	return out
}

var (
	// severityStart: a line that means to be a finding.
	severityStart = regexp.MustCompile(`^(?:[-*]\s+)?\**(blocks|should|note)\**(?:\s+|:\s*|$)(.*)$`)
	// wellFormed is the rest after the severity word.
	// The lines are one line or range, or several comma-separated
	// (path:29,114, path:14-16,144-147).
	wellFormed = regexp.MustCompile("^`?([^\\s`]+?):(\\d+(?:-\\d+)?(?:, ?\\d+(?:-\\d+)?)*)`?(?:\\s+(?:—|–|-)|\\s*:)\\s+(\\S.*)$")
	coverageRe = regexp.MustCompile(`(?i)^(?:[-*]\s+)?\**coverage\**\s*:\s*(.*)$`)
	unreadRe   = regexp.MustCompile(`(?i)^(?:[-*]\s+)?\**not read\**\s*:\s*(.*)$`)
)

// Format is the output contract, quoted in the brief and in help.
const Format = `One finding per line, strongest first:

  blocks <path>:<line> — <what is wrong and why it makes the work wrong>
  should <path>:<line> — <what should change>
  note <path>:<line> — <worth knowing>

<line> may be a range (42-50) or several (42,57 or 42-50,61). A finding
that ends in ':' may list its cases as "- " bullets on the next lines
(no blank line between). Then, required:

  Coverage: <what you read, e.g. "read 6 of 8 files">
  Not read: <paths you did not open, comma-separated, or "none">`

// Parse reads a critic's output. A line that starts with a severity
// begins a finding; one that doesn't fit the format is kept as
// malformed, never dropped. A finding whose text ends in ':' is a
// lead-in: the bullet lines right after it ("- ", "* ", "1. ") are its
// cases and join its Text (Raw when malformed), up to the first line
// that is not a bullet or a bullet that is a well-formed finding. A case
// is only a line this parser would otherwise ignore: a bullet starting
// with a severity word, Coverage: or Not read: reads as it always did,
// so output that held the gate still holds it, and the cases after it
// still join the lead-in. Every other line is the critic's prose and is ignored.
func Parse(out string) Report {
	var r Report
	sawCoverage := false
	lead := -1 // the open lead-in taking its cases, -1 when none
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		bullet := bulletRe.MatchString(line)
		if lead >= 0 && !bullet {
			lead = -1
		}
		if lead >= 0 && isCase(line) {
			f := &r.Findings[lead]
			if f.Malformed {
				f.Raw += "\n" + line
			} else {
				f.Text += "\n" + line
			}
			continue
		}
		if line == "" {
			continue
		}
		if m := coverageRe.FindStringSubmatch(line); m != nil {
			sawCoverage = true
			r.Coverage = strings.TrimSpace(m[1])
			continue
		}
		if m := unreadRe.FindStringSubmatch(line); m != nil {
			r.Unread = append(r.Unread, splitPaths(m[1])...)
			continue
		}
		f, ok := parseFinding(line)
		if !ok {
			continue
		}
		r.Findings = append(r.Findings, f)
		if lead >= 0 && f.Malformed {
			continue // a bullet the lead-in can't take; its later cases still join it
		}
		lead = -1
		last := f.Text
		if f.Malformed {
			last = f.Raw
		}
		if strings.HasSuffix(last, ":") {
			lead = len(r.Findings) - 1
		}
	}
	if !sawCoverage {
		r.Problems = append(r.Problems, "no Coverage: line (the critic's output is incomplete)")
	}
	return r
}

// parseFinding reads a line that starts with a severity word: a
// well-formed finding, or a malformed one kept as written. ok is false
// for a line that doesn't start with a severity.
func parseFinding(line string) (Finding, bool) {
	m := severityStart.FindStringSubmatch(line)
	if m == nil {
		return Finding{}, false
	}
	sev := strings.ToLower(m[1])
	rest := strings.TrimSpace(m[2])
	if w := wellFormed.FindStringSubmatch(rest); w != nil {
		f := Finding{Severity: sev, Path: w[1], Text: strings.TrimSpace(w[3])}
		if spans := parseSpans(w[2]); len(spans) > 0 {
			f.Line, f.EndLine, f.More = spans[0].Line, spans[0].EndLine, spans[1:]
		}
		if f.Line > 0 {
			return f, true
		}
	}
	return Finding{Severity: sev, Malformed: true, Raw: line, Problem: problemOf(rest)}, true
}

// bulletRe: a line that is a list item.
var bulletRe = regexp.MustCompile(`^[-*+]\s|^\d+[.)]\s`)

// isCase: inside a lead-in, the line is one of its cases: a bullet the
// parser would otherwise ignore (not a finding, Coverage: or Not read:).
func isCase(line string) bool {
	return bulletRe.MatchString(line) && !severityStart.MatchString(line) &&
		!coverageRe.MatchString(line) && !unreadRe.MatchString(line)
}

// parseSpans reads "29", "14-16" or "29,114" / "14-16, 144-147"; nil
// when a line is 0.
func parseSpans(s string) []Span {
	var out []Span
	for _, part := range strings.Split(s, ",") {
		a, b, isRange := strings.Cut(strings.TrimSpace(part), "-")
		sp := Span{}
		sp.Line, _ = strconv.Atoi(a)
		if isRange {
			sp.EndLine, _ = strconv.Atoi(b)
		}
		if sp.Line <= 0 {
			return nil
		}
		out = append(out, sp)
	}
	return out
}

func problemOf(rest string) string {
	switch {
	case rest == "":
		return "no path:line and no text"
	case !regexp.MustCompile(`\S+:\d+`).MatchString(rest):
		return "no path:line"
	default:
		return "no text after path:line"
	}
}

func splitPaths(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "none") || s == "-" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.Trim(strings.TrimSpace(p), "`"); p != "" {
			out = append(out, p)
		}
	}
	return out
}
