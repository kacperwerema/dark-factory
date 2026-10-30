package main

import (
	"regexp"
	"strconv"
	"strings"
)

// The CEO chat reads the conversation out of the lead's tmux pane: Claude
// Code's TUI echoes what the owner typed after "> " (or "❯ "), starts each of
// its own paragraphs and tool calls with "⏺", shows tool output after "⎿",
// a spinner line ending "esc to interrupt" while it works, a numbered menu
// when it asks for a permission, and the session mode in the footer. The
// parse is a best effort: anything it doesn't recognise stays text in the
// CEO's turn, and the Terminal view always has the raw pane.

// CEOTurn is one turn of the conversation: the owner's message or the CEO's
// paragraphs and the tools it used in between.
type CEOTurn struct {
	// From is "you" or "ceo".
	From string `json:"from"`
	// Text holds the paragraphs: the owner's message is one, the CEO's are
	// each "⏺" paragraph with the lines that followed it.
	Text []string `json:"text"`
	// Tools are the CEO's tool calls by name ("Bash", "Read"), in order,
	// one per call.
	Tools []string `json:"tools,omitempty"`
	// Bubbles, Calls and Asks come from the transcript (ceo_transcript.go):
	// the CEO's whole messages, its tool calls with labels, and its
	// AskUserQuestion calls. A pane turn has none of them.
	Bubbles []CEOBubble    `json:"bubbles,omitempty"`
	Calls   []CEOToolCall  `json:"calls,omitempty"`
	Asks    []*CEOQuestion `json:"asks,omitempty"`
}

// ToolSummary is the folded line for the turn's tool calls: "used 3 tools:
// Bash ×2, Read".
func (t CEOTurn) ToolSummary() string {
	if len(t.Tools) == 0 {
		return ""
	}
	var names []string
	count := map[string]int{}
	for _, name := range t.Tools {
		if count[name] == 0 {
			names = append(names, name)
		}
		count[name]++
	}
	for i, name := range names {
		if n := count[name]; n > 1 {
			names[i] = name + " ×" + strconv.Itoa(n)
		}
	}
	return "used " + plural(len(t.Tools), "tool", "tools") + ": " + strings.Join(names, ", ")
}

// CEOWorking is the spinner line while the CEO works.
type CEOWorking struct {
	Text string `json:"text"`
}

// CEOPrompt is a numbered menu the CEO waits on: a permission or a choice.
type CEOPrompt struct {
	Title    string `json:"title"`
	Question string `json:"question"`
	// Detail is what the menu is about (the command), as shown above the
	// question.
	Detail  []string    `json:"detail,omitempty"`
	Choices []CEOChoice `json:"choices"`
}

// CEOChoice is one menu option; Key is the digit that picks it.
type CEOChoice struct {
	Key  string `json:"key"`
	Text string `json:"text"`
	// Selected is the option the TUI's cursor is on.
	Selected bool `json:"selected"`
}

// CEOPane is everything the chat reads out of one capture.
type CEOPane struct {
	Turns []CEOTurn `json:"turns"`
	// Older says the pane starts mid-conversation: earlier turns scrolled
	// out of the capture and are only in tmux.
	Older   bool        `json:"older"`
	Working *CEOWorking `json:"working,omitempty"`
	Prompt  *CEOPrompt  `json:"prompt,omitempty"`
	// Ask is an AskUserQuestion widget the CEO waits on (ceo_question.go).
	Ask *CEOPaneAsk `json:"ask,omitempty"`
	// Mode is the session's permission mode from the footer: "auto",
	// "plan", "accept edits"; empty when the footer doesn't say.
	Mode string `json:"mode"`
}

var (
	// paneSpinner is the working line: "✻ Thinking… (32s · esc to interrupt)".
	paneSpinner = regexp.MustCompile(`^\S\s+(.+?….*?esc to interrupt.*)$`)
	// paneMode is the footer's mode: "⏵⏵ auto mode on (shift+tab to cycle)",
	// "⏸ plan mode on", "⏵⏵ accept edits on".
	paneMode = regexp.MustCompile(`(?i)\b(auto mode|plan mode|accept edits|bypass permissions)(?: mode)? on\b`)
	// paneChoice is one menu option: "❯ 1. Yes", "  2. No".
	paneChoice = regexp.MustCompile(`^(❯\s*)?([1-9])\.\s+(.+)$`)
	// paneRule is a box border or a horizontal rule: nothing but drawing.
	paneRule = regexp.MustCompile(`^[─━═╭╮╰╯┌┐└┘├┤\-\s]+$`)
)

// paneTail is how many lines at the bottom the spinner, the menu and the
// footer are looked for in.
const paneTail = 40

// parseCEOPane reads a cleaned pane (cleanPane) into turns, the working
// line, an open menu and the mode.
func parseCEOPane(pane string) CEOPane {
	lines := strings.Split(pane, "\n")
	out := CEOPane{Turns: []CEOTurn{}}
	tail := len(lines) - paneTail
	if tail < 0 {
		tail = 0
	}
	for i := len(lines) - 1; i >= tail; i-- {
		if m := paneMode.FindStringSubmatch(boxText(lines[i])); m != nil {
			out.Mode = strings.ToLower(m[1])
			out.Mode = strings.TrimSuffix(strings.TrimSuffix(out.Mode, " mode"), " permissions")
			break
		}
	}
	// The input box and everything under it (footer, hints) are not the
	// conversation: cut at the rule above the last prompt line.
	end := len(lines)
	for i := len(lines) - 1; i >= tail && i > 0; i-- {
		if t := boxText(lines[i]); (t == ">" || t == "❯" || strings.HasPrefix(t, "> ") || strings.HasPrefix(t, "❯ ")) && isRule(lines[i-1]) {
			end = i - 1
			break
		}
	}
	if a, start := parsePaneAsk(lines[:end], tail); a != nil {
		out.Ask, end = a, start
	} else if p, start := paneMenu(lines[:end], tail); p != nil {
		out.Prompt, end = p, start
	}
	conv := lines[:end]
	for i := end - 1; i >= tail && i >= 0; i-- {
		if m := paneSpinner.FindStringSubmatch(strings.TrimSpace(lines[i])); m != nil {
			out.Working = &CEOWorking{Text: strings.TrimSpace(m[1])}
			conv = append(append([]string(nil), lines[:i]...), lines[i+1:end]...)
			break
		}
	}
	out.Turns, out.Older = paneTurnsOf(strings.Join(conv, "\n"))
	return out
}

// isRule reports whether line is a box border or a horizontal rule.
func isRule(line string) bool {
	return strings.TrimSpace(line) != "" && paneRule.MatchString(line)
}

// boxText is a pane line without box borders and padding.
func boxText(line string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│┃|"))
}

// paneMenu finds a numbered menu (1., 2., … in a row) near the bottom of
// lines, under a question. It returns the menu and the line its box starts
// on, so the conversation stops above it.
func paneMenu(lines []string, tail int) (*CEOPrompt, int) {
	last := -1
	for i := len(lines) - 1; i >= tail && i >= 0; i-- {
		if paneChoice.MatchString(boxText(lines[i])) {
			last = i
			break
		}
		if t := boxText(lines[i]); t != "" && !isRule(lines[i]) && !strings.HasPrefix(t, "Esc") && !strings.Contains(t, "to cancel") {
			return nil, 0
		}
	}
	if last < 0 {
		return nil, 0
	}
	first := last
	for first > tail && paneChoice.MatchString(boxText(lines[first-1])) {
		first--
	}
	var choices []CEOChoice
	for i := first; i <= last; i++ {
		m := paneChoice.FindStringSubmatch(boxText(lines[i]))
		if m[2] != strconv.Itoa(len(choices)+1) {
			return nil, 0
		}
		choices = append(choices, CEOChoice{Key: m[2], Text: strings.TrimSpace(m[3]), Selected: m[1] != ""})
	}
	// The question sits right above the options.
	q := first - 1
	for q >= 0 && boxText(lines[q]) == "" {
		q--
	}
	if q < 0 || !strings.HasSuffix(boxText(lines[q]), "?") || len(choices) < 2 {
		return nil, 0
	}
	p := &CEOPrompt{Question: boxText(lines[q]), Choices: choices}
	// Above the question, up to the box's top or a blank line pair: the
	// title and what the menu is about.
	start := q
	for start > tail && start > 0 {
		prev := lines[start-1]
		if isRule(prev) {
			start--
			break
		}
		if strings.HasPrefix(strings.TrimSpace(prev), "⏺") || strings.HasPrefix(strings.TrimSpace(prev), ">") {
			break
		}
		start--
	}
	var above []string
	for i := start; i < q; i++ {
		if t := boxText(lines[i]); t != "" && !isRule(lines[i]) {
			above = append(above, t)
		}
	}
	if len(above) > 0 {
		p.Title, p.Detail = above[0], above[1:]
	}
	return p, start
}

// paneTurnsOf splits the conversation part of a pane into turns. Lines
// before the first "> " or "⏺" (the welcome box) are skipped; older is true
// when the first turn is the CEO's, so the owner's message that started it
// scrolled off.
func paneTurnsOf(pane string) ([]CEOTurn, bool) {
	turns := []CEOTurn{}
	started := false
	for _, para := range paneParagraphs(pane) {
		if para == "" || isRule(para) {
			continue
		}
		switch {
		case strings.HasPrefix(para, "> ") || strings.HasPrefix(para, "❯ "):
			text := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(para, ">"), "❯"))
			turns = append(turns, CEOTurn{From: "you", Text: []string{text}})
			started = true
		case strings.HasPrefix(para, "⏺"):
			text := strings.TrimSpace(strings.TrimPrefix(para, "⏺"))
			cur := ceoTurn(&turns)
			if m := toolCall.FindString(text); m != "" {
				cur.Tools = append(cur.Tools, strings.TrimSuffix(m, "("))
			} else if text != "" {
				cur.Text = append(cur.Text, text)
			}
			started = true
		case strings.HasPrefix(para, "⎿") || !started:
			// Tool output (shown folded) and the welcome box.
		default:
			// A bullet or a line the TUI didn't indent: still the CEO's.
			cur := ceoTurn(&turns)
			cur.Text = append(cur.Text, para)
		}
	}
	return turns, len(turns) > 0 && turns[0].From == "ceo"
}

// ceoTurn is the last turn when it is the CEO's, or a new CEO turn.
func ceoTurn(turns *[]CEOTurn) *CEOTurn {
	if n := len(*turns); n > 0 && (*turns)[n-1].From == "ceo" {
		return &(*turns)[n-1]
	}
	*turns = append(*turns, CEOTurn{From: "ceo"})
	return &(*turns)[len(*turns)-1]
}
