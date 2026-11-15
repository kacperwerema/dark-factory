// Package rules holds the factory's two layers of standing rules: the
// owner's machine file (rules.md under the config dir), which reaches every
// project's leads and workers, and a project's own rules: in workflow.yaml,
// which adds to it. A project rule may narrow a machine rule and say so;
// Conflicts finds the ones that contradict one without saying. Nothing here
// writes rules.md: it is the owner's file. See docs/workflow.md, Rules.
package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/memory"
)

// BriefChars is how much of one layer a briefing carries, whole rules only.
const BriefChars = 4000

// Rule is one standing rule of either layer.
type Rule struct {
	// Key names the rule: a machine rule's [key], a project rule's id.
	// "" for a rule nothing needs to point at.
	Key  string `json:"key,omitempty"`
	Text string `json:"text"`
	// Narrows is the key of the machine rule this project rule narrows.
	Narrows string `json:"narrows,omitempty"`
	// Line is the rule's first line in rules.md, 1-based; 0 for a project
	// rule.
	Line int `json:"line,omitempty"`
}

// Label is how a finding or a briefing names the rule: its [key], else its
// line. Never its text, which a machine rule's reader in another project
// should meet only in a briefing.
func (r Rule) Label() string {
	switch {
	case r.Key != "":
		return "[" + r.Key + "]"
	case r.Line > 0:
		return fmt.Sprintf("line %d", r.Line)
	}
	return "unnamed rule"
}

// File is rules.md read.
type File struct {
	Path  string `json:"path"`
	Rules []Rule `json:"rules"`
	// Stray is the lines that are neither a rule, a heading, a comment nor
	// blank: they reach nobody, so lint names them.
	Stray []int `json:"stray,omitempty"`
}

// MachinePath is the owner's rules file: $XDG_CONFIG_HOME, else ~/.config.
func MachinePath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ai-workflow-engine", "rules.md")
}

// LoadMachine reads the machine's rules. No file is no rules, not an error:
// most machines have none.
func LoadMachine() (File, error) {
	path := MachinePath()
	f := File{Path: path}
	if path == "" {
		return f, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	f.Rules, f.Stray = Parse(string(raw))
	return f, nil
}

var (
	bulletRe = regexp.MustCompile(`^[-*] +(.*)$`)
	keyRe    = regexp.MustCompile(`^\[([A-Za-z0-9][A-Za-z0-9_-]*)\]\s*(.*)$`)
)

// Parse reads rules.md: every "- " bullet is a rule, an indented line
// continues it, a leading [key] names it. Headings, comments and blank lines
// are skipped; any other line comes back in stray.
func Parse(text string) (out []Rule, stray []int) {
	inComment, open := false, false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case inComment:
			inComment = !strings.Contains(trimmed, "-->")
			continue
		case strings.HasPrefix(trimmed, "<!--"):
			inComment = !strings.Contains(trimmed, "-->")
			open = false
			continue
		case trimmed == "":
			open = false
			continue
		case strings.HasPrefix(trimmed, "#"):
			open = false
			continue
		}
		if m := bulletRe.FindStringSubmatch(line); m != nil {
			r := Rule{Line: i + 1, Text: strings.TrimSpace(m[1])}
			if k := keyRe.FindStringSubmatch(r.Text); k != nil {
				r.Key, r.Text = k[1], k[2]
			}
			out = append(out, r)
			open = true
			continue
		}
		if open && line != strings.TrimLeft(line, " \t") {
			last := &out[len(out)-1]
			last.Text = strings.TrimSpace(last.Text + " " + trimmed)
			continue
		}
		stray = append(stray, i+1)
	}
	return out, stray
}

// Cut is the rules that fit one layer of a briefing, in order, and how many
// did not.
func Cut(all []Rule) (shown []Rule, more int) {
	n := 0
	for i, r := range all {
		n += len(r.Text)
		if n > BriefChars && i > 0 {
			return all[:i], len(all) - i
		}
	}
	return all, 0
}

// Conflict is a project rule that contradicts a machine rule without
// declaring narrows:.
type Conflict struct {
	Project Rule
	Machine Rule
	// Why is "name" (the project rule takes a machine rule's key) or
	// "wording" (one forbids what the other asks for).
	Why string
}

// Conflicts is every project rule that contradicts a machine one. A project
// rule that names the machine rule in narrows: has said what it does, so
// that pair is not a conflict.
func Conflicts(machine, project []Rule) []Conflict {
	var out []Conflict
	for _, p := range project {
		for _, m := range machine {
			if m.Key != "" && p.Narrows == m.Key {
				continue
			}
			switch {
			case m.Key != "" && p.Key == m.Key:
				out = append(out, Conflict{Project: p, Machine: m, Why: "name"})
			case opposed(m.Text, p.Text):
				out = append(out, Conflict{Project: p, Machine: m, Why: "wording"})
			}
		}
	}
	return out
}

var negationRe = regexp.MustCompile(`(?i)\b(never|not|no|don't|dont|do not|must not|mustn't|cannot|can't|avoid|forbidden|refuse[sd]?)\b`)

// negationWords are dropped before two rules' words are compared: they say
// which way a rule points, not what it is about.
var negationWords = map[string]bool{"never": true, "avoid": true, "forbidden": true, "refuse": true, "refused": true, "cannot": true, "dont": true, "must": true, "always": true}

// opposed: exactly one of the two forbids, and they are about the same
// thing: at least two content words in common, half or more of the shorter
// rule's. A match on a narrowing that was not declared is a true finding;
// the fix is to declare it.
func opposed(a, b string) bool {
	if negationRe.MatchString(a) == negationRe.MatchString(b) {
		return false
	}
	wa, wb := contentWords(a), contentWords(b)
	shorter := len(wa)
	if len(wb) < shorter {
		shorter = len(wb)
	}
	shared := 0
	for w := range wa {
		if wb[w] {
			shared++
		}
	}
	return shared >= 2 && shared*2 >= shorter
}

func contentWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range memory.Words(s) {
		if !negationWords[w] {
			out[w] = true
		}
	}
	return out
}
