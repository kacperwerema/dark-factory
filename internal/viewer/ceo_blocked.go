package viewer

import (
	"regexp"
	"strings"
)

// What Claude Code's permission layer does to the CEO's tool calls, read for
// the Conversation: a permission prompt the CEO waits on (a menu whose
// question is "Do you want to …?", permissionOf), and a call it refused, the
// auto mode classifier or a built-in safety check (blockedOf). A refusal is
// the tool's result: "Permission for this action was denied by the Claude
// Code auto mode classifier. Reason: [Production Deploy]. …" in the
// transcript, the same text after "⎿ Error:" under the call on the pane.
// Both texts are Claude Code's and undocumented: what doesn't match stays
// what it was, a tool in the folded list.

// CEOBlocked is one tool call the permission layer refused.
type CEOBlocked struct {
	Tool    string `json:"tool"`
	Command string `json:"command,omitempty"`
	// By is who refused it: "auto mode classifier" or "safety check".
	By     string `json:"by"`
	Reason string `json:"reason,omitempty"`
}

var (
	// blockedReason is the classifier's stated reason, up to the advice
	// that follows it.
	blockedReason = regexp.MustCompile(`Reason: (.+?)\.(?:\s+If you have|\s+IMPORTANT|$)`)
	// blockedFlagged is what a safety check says it flagged.
	blockedFlagged = regexp.MustCompile(`What was flagged: (.+)$`)
	// paneDashed is the dashed rule a permission box draws around the
	// command: "╌╌╌╌".
	paneDashed = regexp.MustCompile(`^[╌┄┈╍\s]+$`)
)

// blockedTextMax caps the command and reason a blocked item shows.
const blockedTextMax = 300

// blockedOf reads a tool result as a refusal by the permission layer, or nil
// when it is something else (a failed command, the owner's own No).
func blockedOf(tool, command, result string) *CEOBlocked {
	result = strings.Join(strings.Fields(result), " ")
	result = strings.TrimSpace(strings.TrimPrefix(result, "Error:"))
	if !strings.HasPrefix(result, "Permission for this ") || !strings.Contains(result, " denied ") {
		return nil
	}
	b := &CEOBlocked{Tool: tool, Command: clipRunes(strings.Join(strings.Fields(command), " "), blockedTextMax)}
	switch {
	case strings.Contains(result, "auto mode classifier"):
		b.By = "auto mode classifier"
		if m := blockedReason.FindStringSubmatch(result); m != nil {
			b.Reason = strings.TrimSuffix(strings.TrimPrefix(m[1], "["), "]")
		}
	case strings.Contains(result, "safety check"):
		b.By = "safety check"
		if m := blockedFlagged.FindStringSubmatch(result); m != nil {
			b.Reason = m[1]
		}
	default:
		b.By = "permission rule"
	}
	if b.Reason == "" {
		// The first sentence says who refused it.
		b.Reason = result
		if i := strings.Index(result, ". "); i > 0 {
			b.Reason = result[:i+1]
		}
	}
	b.Reason = clipRunes(b.Reason, blockedTextMax)
	return b
}

// paneBlocked reads a tool call's paragraph on the pane ("Bash(cmd) ⎿
// Waiting… ⎿ Error: Permission for this command was denied …") for a
// refusal under it.
func paneBlocked(text string) *CEOBlocked {
	parts := strings.Split(text, "⎿")
	if len(parts) < 2 {
		return nil
	}
	call := strings.TrimSpace(parts[0])
	open := strings.Index(call, "(")
	if open < 0 {
		return nil
	}
	tool := call[:open]
	command := strings.TrimSuffix(strings.TrimSpace(call[open+1:]), ")")
	for _, part := range parts[1:] {
		if b := blockedOf(tool, command, part); b != nil {
			return b
		}
	}
	return nil
}

// permissionOf marks p as a permission prompt when its question is Claude
// Code's "Do you want to …?", and reads the tool, the command and the note
// out of the box lines above the question. It also drops the box's dashed
// rules and tip line from Detail, which are drawing, not the menu's subject.
func permissionOf(p *CEOPrompt, box []string) {
	var detail []string
	for _, l := range p.Detail {
		if !paneDashed.MatchString(l) && !strings.HasPrefix(l, "Tip:") {
			detail = append(detail, l)
		}
	}
	p.Detail = detail
	if !strings.HasPrefix(p.Question, "Do you want to ") {
		return
	}
	p.Permission = true
	command := strings.HasSuffix(p.Title, " command")
	p.Tool = strings.TrimSuffix(p.Title, " command")
	var inside, outside []string
	dashed, title := false, false
	for _, raw := range box {
		t := boxText(raw)
		switch {
		case t == "" || isRule(raw):
		case paneDashed.MatchString(t):
			dashed = !dashed
		case !title:
			title = true
		case strings.HasPrefix(t, "Tip:"):
		case dashed:
			inside = append(inside, t)
		default:
			outside = append(outside, t)
		}
	}
	switch {
	case command && len(inside) > 0:
		p.Command, p.Note = inside, outside
	case len(outside) > 0:
		// A command box without the dashed rules has the command first; a
		// file's box names the file outside them, its content inside.
		p.Command = outside[:1]
		if command {
			p.Note = outside[1:]
		}
	}
}

// permissionWaiting is the working line while a permission prompt is open.
func permissionWaiting(p *CEOPrompt) string {
	text := "Waiting on a permission prompt"
	if p.Tool != "" {
		text += ": " + p.Tool
	}
	if len(p.Command) > 0 {
		text += " · " + clipRunes(p.Command[0], 80)
	}
	return text
}
