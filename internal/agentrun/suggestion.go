package agentrun

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// CapturePaneStyled is the pane's screen with its escape sequences
// (capture-pane -e), which MarkSuggestion reads: a plain capture prints
// Claude Code's dimmed prompt suggestion like typed text.
var CapturePaneStyled = defaultCapturePaneStyled

func defaultCapturePaneStyled(target string) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-J", "-e", "-t", target).Output()
	return string(out), err
}

// SuggestionNote is how MarkSuggestion says the input box's dimmed text was
// a suggestion: on 2026-11-13 a lead read three of them as owner answers
// left typed and unsent in idle workers.
const SuggestionNote = "greyed suggestion, not typed: "

// paneEscape is one terminal escape sequence: CSI (SGR among them), OSC or
// a two-byte escape.
var paneEscape = regexp.MustCompile("\x1b\\[[0-9;?<=>:]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]")

// paneCell is one printed character and whether it was dim or inverse.
type paneCell struct {
	r        rune
	dim, inv bool
}

// MarkSuggestion is a pane captured with escapes (CapturePaneStyled) as
// plain text, with a dimmed suggestion in Claude Code's input box written
// out as one: "❯ (greyed suggestion, not typed: Go with option 3)". Text
// typed into the box stays as it is; a suggestion after typed text is
// marked after it. The cursor over a suggestion's first letter (one
// inverse character before the dim run) belongs to the suggestion. A pane
// with no input box or no dim text in it reads as the plain capture.
func MarkSuggestion(styled string) string {
	var dim, inv bool
	raw := strings.Split(strings.ReplaceAll(styled, "\r", ""), "\n")
	cells := make([][]paneCell, len(raw))
	plain := make([]string, len(raw))
	for i, line := range raw {
		cells[i], dim, inv = styledCells(line, dim, inv)
		var b strings.Builder
		for _, c := range cells[i] {
			b.WriteRune(c.r)
		}
		plain[i] = b.String()
	}
	top := -1
	for i := len(plain) - 1; i > 0; i-- {
		t := boxText(plain[i])
		if (t == ">" || t == "❯" || strings.HasPrefix(t, "> ") || strings.HasPrefix(t, "❯ ")) && isRule(plain[i-1]) {
			top = i
			break
		}
	}
	if top < 0 {
		return strings.Join(plain, "\n")
	}
	for j := top; j < len(plain) && (j == top || !isRule(plain[j])); j++ {
		plain[j] = markBoxLine(cells[j], plain[j], j == top)
	}
	return strings.Join(plain, "\n")
}

// styledCells reads one captured line into cells, carrying the dim and
// inverse state across lines as the terminal does.
func styledCells(line string, dim, inv bool) ([]paneCell, bool, bool) {
	var cells []paneCell
	for line != "" {
		loc := paneEscape.FindStringIndex(line)
		text := line
		if loc != nil {
			text = line[:loc[0]]
		}
		for _, r := range text {
			cells = append(cells, paneCell{r, dim, inv})
		}
		if loc == nil {
			break
		}
		if seq := line[loc[0]:loc[1]]; strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m") {
			dim, inv = applySGR(seq[2:len(seq)-1], dim, inv)
		}
		line = line[loc[1]:]
	}
	return cells, dim, inv
}

// applySGR applies one SGR's parameters to the dim and inverse state. A
// colour's arguments (38;5;n, 48;2;r;g;b) are skipped, so their numbers
// are never read as attributes.
func applySGR(params string, dim, inv bool) (bool, bool) {
	if params == "" {
		return false, false
	}
	ps := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	for i := 0; i < len(ps); i++ {
		n, _ := strconv.Atoi(ps[i])
		switch n {
		case 0:
			dim, inv = false, false
		case 2:
			dim = true
		case 22:
			dim = false
		case 7:
			inv = true
		case 27:
			inv = false
		case 38, 48, 58:
			if i+1 < len(ps) && ps[i+1] == "5" {
				i += 2
			} else if i+1 < len(ps) && ps[i+1] == "2" {
				i += 4
			}
		}
	}
	return dim, inv
}

// markBoxLine is one input-box line with its dim run marked. The prompt
// line keeps its mark ("❯ ") in front.
func markBoxLine(cells []paneCell, plain string, prompt bool) string {
	start := 0
	if prompt {
		for i, c := range cells {
			if c.r == '❯' || c.r == '>' {
				start = i + 1
				for start < len(cells) && cells[start].r == ' ' && !cells[start].inv {
					start++
				}
				break
			}
		}
	}
	var typed, suggestion strings.Builder
	for i := start; i < len(cells); i++ {
		c := cells[i]
		cursorOnSuggestion := c.inv && !c.dim && i+1 < len(cells) && cells[i+1].dim
		if c.dim || cursorOnSuggestion {
			suggestion.WriteRune(c.r)
		} else {
			typed.WriteRune(c.r)
		}
	}
	s := strings.TrimSpace(suggestion.String())
	if s == "" {
		return plain
	}
	var b strings.Builder
	for _, c := range cells[:start] {
		b.WriteRune(c.r)
	}
	if t := strings.TrimRight(typed.String(), " "); strings.TrimSpace(t) != "" {
		b.WriteString(t + " ")
	}
	b.WriteString("(" + SuggestionNote + s + ")")
	return b.String()
}
