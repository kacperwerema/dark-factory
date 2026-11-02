package tracker

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Confirming a decision: the owner gives their words on a D-n an agent took
// alone (or marked provisional under YOLO). The same line is rewritten in
// place, so its ref and every citation of it stay valid: the quote is set,
// the "(provisional…)" mark goes and the how ends "confirmed by owner <date>".

var (
	// ErrDecisionNotFound: the issue has no such D-n line.
	ErrDecisionNotFound = errors.New("no such decision")
	// ErrDecisionConfirmed: the decision already has the owner's words.
	ErrDecisionConfirmed = errors.New("decision already confirmed")

	provisionalMarkRe = regexp.MustCompile(`(?i)\s*\(provisional\b[^()]*\)`)
	provisionalWordRe = regexp.MustCompile(`(?i)\bprovisional\b`)
)

const (
	confirmedByOwner      = "confirmed by owner"
	defaultConfirmedQuote = "Confirmed."
)

// NeedsOwnerWords reports whether a decision still waits for the owner: it
// has no quote, its text carries a "(provisional…)" mark, or it was made in
// YOLO or provisionally, and it has not been confirmed since.
func NeedsOwnerWords(d Decision) bool {
	if strings.Contains(strings.ToLower(d.How), confirmedByOwner) {
		return false
	}
	return d.Quote == "" || provisionalMarkRe.MatchString(d.Text) || provisionalHow(d.How)
}

func provisionalHow(how string) bool {
	for _, part := range strings.Split(how, ",") {
		if p := strings.TrimSpace(part); strings.EqualFold(p, "yolo") || provisionalWordRe.MatchString(p) {
			return true
		}
	}
	return false
}

// MarkProvisional is how with "provisional" added (decide --provisional),
// the mark NeedsOwnerWords reads and confirming drops. An empty how is the
// default one.
func MarkProvisional(how string) string {
	how = strings.TrimSpace(how)
	if how == "" {
		how = defaultDecisionHow
	}
	if provisionalWordRe.MatchString(how) {
		return how
	}
	return how + ", provisional"
}

// confirmedHow drops the YOLO and provisional words from how and adds
// "confirmed by owner <date>".
func confirmedHow(how string, now time.Time) string {
	var parts []string
	for _, part := range strings.Split(how, ",") {
		p := oneLine(provisionalWordRe.ReplaceAllString(part, ""))
		if p == "" || strings.EqualFold(p, "yolo") || p == defaultDecisionHow {
			continue
		}
		parts = append(parts, p)
	}
	return strings.Join(append(parts, confirmedByOwner+" "+now.Format(decisionDateLayout)), ", ")
}

// renderParsedDecision writes a parsed decision back as its line, in the
// order parseDecisionLine reads it.
func renderParsedDecision(d Decision) string {
	line := fmt.Sprintf("- **%s** · %s · ", d.ID, d.Date.Format(decisionDateLayout))
	if d.Deferred {
		line += deferredMarker + " · "
	}
	line += d.How + ": " + d.Text
	if d.Quote != "" {
		line += fmt.Sprintf(", %q", d.Quote)
	}
	if d.AlsoConsidered != "" {
		line += " (also considered: " + d.AlsoConsidered + ")"
	}
	if len(d.Refs) > 0 {
		line += " (ref " + strings.Join(d.Refs, ", ") + ")"
	}
	if len(d.Replaces) > 0 {
		line += " (replaces " + strings.Join(d.Replaces, ", ") + ")"
	}
	if g := RenderAttrs(d.DecisionAttrs); g != "" {
		line += " " + g
	}
	return line
}

// ConfirmDecision records the owner's words on decision id ("D-3") of the
// issue in one locked write and returns it parsed. Empty words keep a quote
// the decision already has, else record "Confirmed.". It logs nothing;
// commands and the viewer use ConfirmDecisionAs.
func ConfirmDecision(filePath, slug, id, words string, now time.Time) (Decision, error) {
	return ConfirmDecisionAs(filePath, slug, id, words, false, now, DecisionWrite{})
}

// ConfirmDecisionAs is ConfirmDecision, logged, with the owner's hard option:
// hard also makes the decision hard in the same write. A decision that
// already has the owner's words can still be made hard (an amended event);
// confirming it again without hard is ErrDecisionConfirmed.
func ConfirmDecisionAs(filePath, slug, id, words string, hard bool, now time.Time, w DecisionWrite) (Decision, error) {
	id = strings.ToUpper(strings.TrimSpace(id))
	var line string
	err := writeDecision(filePath, w, now, func(body string) (decisionChange, error) {
		lines, i, d, err := findDecisionLine(body, slug, id)
		if err != nil {
			return decisionChange{}, err
		}
		old := RenderAttrs(d.DecisionAttrs)
		event := DecisionConfirmed
		switch {
		case NeedsOwnerWords(d):
			d.Text = strings.TrimSpace(provisionalMarkRe.ReplaceAllString(d.Text, ""))
			if w := oneLine(words); w != "" {
				d.Quote = w
			} else if d.Quote == "" {
				d.Quote = defaultConfirmedQuote
			}
			d.Text = strings.TrimRight(d.Text, ".")
			d.How = confirmedHow(d.How, now)
			d.Hard = d.Hard || hard
			line = renderParsedDecision(d)
		case hard && !d.Hard:
			d.Hard = true
			event = DecisionAmended
			line = spliceAttrs(d)
		default:
			return decisionChange{}, ErrDecisionConfirmed
		}
		lines[i] = lineIndent(lines[i]) + line
		return decisionChange{Body: strings.Join(lines, "\n"), Events: []DecisionEvent{{Event: event, Ref: d.Ref, Old: old, New: RenderAttrs(d.DecisionAttrs)}}}, nil
	})
	if err != nil {
		return Decision{}, err
	}
	parsed, ok := parseDecisionLine(slug, line)
	if !ok {
		return Decision{}, fmt.Errorf("internal: wrote an unparseable decision line: %s", line)
	}
	return parsed, nil
}

// findDecisionLine finds decision id's line under the issue's ## Decisions
// (fenced lines skipped), returning the body's lines, its index and the
// parsed decision.
func findDecisionLine(body, slug, id string) ([]string, int, Decision, error) {
	lines := strings.Split(body, "\n")
	fence := computeFenceFlags(lines)
	for _, sec := range findHeadingMatches(body, DecisionsSection) {
		if sec.Level != 2 {
			continue
		}
		for i := sec.StartLine + 1; i < sec.EndLine && i < len(lines); i++ {
			if fence[i] {
				continue
			}
			if d, ok := parseDecisionLine(slug, lines[i]); ok && d.ID == id {
				return lines, i, d, nil
			}
		}
	}
	return nil, 0, Decision{}, fmt.Errorf("%s#%s: %w", slug, id, ErrDecisionNotFound)
}

func lineIndent(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// spliceAttrs is d's line with only its attribute group replaced by
// d.DecisionAttrs: every byte before the group stays as it was.
func spliceAttrs(d Decision) string {
	head := strings.TrimRight(d.Raw, " \t")
	if d.AttrsAt >= 0 {
		head = d.Raw[:d.AttrsAt]
	}
	if g := RenderAttrs(d.DecisionAttrs); g != "" {
		return head + " " + g
	}
	return head
}
