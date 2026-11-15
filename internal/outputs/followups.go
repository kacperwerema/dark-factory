package outputs

import (
	"os"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var (
	headingLine  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	followUpHead = regexp.MustCompile(`(?i)^(?:§?\d+[.)]?\s+)?follow[- ]?ups?\b`)
	listItemLine = regexp.MustCompile(`^ {0,3}(?:[-*+]|\d+[.)])\s+(\S.*)$`)
	ownerMark    = regexp.MustCompile(`(?i)\(owner\)$`)
	namesOwner   = regexp.MustCompile(`(?i)\bowner(?:'s|s'|s)?\b`)
)

// FollowUp is one top-level item of a report's Follow-ups list. Owner is
// true when the item is the owner's call, not work for the engine: it ends
// "(owner)", or the sub-heading or lead-in line above it names the owner
// ("### Owner actions", "Marketing actions, each the owner's call:").
type FollowUp struct {
	Text  string
	Owner bool
}

// FollowUps is the list under a report's Follow-ups heading (any level, a
// number in front or words after it: "## 9. Follow-ups (for the lead)"),
// top-level items only. Prose there, or an item that only says none, is no
// list: "Code fixes: none." leaves nothing to pick up.
func FollowUps(markdown string) []string {
	var texts []string
	for _, it := range FollowUpList(markdown) {
		texts = append(texts, it.Text)
	}
	return texts
}

// AgentFollowUps is the Follow-ups list without the owner's items: what an
// issue or a queue item has to pick up.
func AgentFollowUps(markdown string) []string {
	var texts []string
	for _, it := range FollowUpList(markdown) {
		if !it.Owner {
			texts = append(texts, it.Text)
		}
	}
	return texts
}

// FollowUpList is FollowUps with each item marked the owner's or not. Under
// the Follow-ups heading, a sub-heading or a line of prose (a lead-in) sets
// whose the items below it are, until the next one: the owner's when it
// names the owner, the engine's otherwise. An item ending "(owner)" is the
// owner's wherever it stands.
func FollowUpList(markdown string) []FollowUp {
	var items []FollowUp
	level, fenced, owner := 0, false, false
	inItem, inLead := false, false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if m := headingLine.FindStringSubmatch(line); m != nil {
			inItem, inLead = false, false
			if followUpHead.MatchString(m[2]) {
				level, owner = len(m[1]), false
			} else if len(m[1]) <= level {
				level = 0
			} else if level > 0 {
				owner = namesOwner.MatchString(m[2])
			}
			continue
		}
		if level == 0 || trimmed == "" {
			inItem, inLead = false, false
			continue
		}
		m := listItemLine.FindStringSubmatch(line)
		if m == nil {
			// A paragraph of prose from the margin is a lead-in; an
			// indented line, or one right under an item, continues it.
			// A lead-in of several lines names the owner if any does.
			if !inItem && line[0] != ' ' && line[0] != '\t' {
				owner = (inLead && owner) || namesOwner.MatchString(trimmed)
				inLead = true
			}
			continue
		}
		inItem, inLead = true, false
		if saysNone(m[1]) {
			continue
		}
		text := strings.TrimSpace(m[1])
		items = append(items, FollowUp{Text: text, Owner: owner || ownerMark.MatchString(strings.TrimRight(text, " .*_"))})
	}
	return items
}

func saysNone(item string) bool {
	switch strings.ToLower(strings.Trim(item, " .*_")) {
	case "none", "n/a", "nothing":
		return true
	}
	return false
}

// ReadMarkdown is a markdown output's text, newest version; ok is false for
// anything else or a file that can't be read.
func ReadMarkdown(p *tracker.Project, o *Output) (string, bool) {
	path := localPath(p, o)
	if path == "" || !o.IsMarkdown() {
		return "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(raw), true
}
