package spend

import (
	"math"
	"sort"
	"strings"
)

// The brief by section: the first user message cut where the engine's own
// renderers start a part, so a cut to one part is re-measured by
// spend --context. A lead brief (internal/briefing/lead.go,
// department_lead.go) has fixed sections; a worker's briefing
// (tracker.AgentDispatchPromptTemplate and the stage parts) is cut at the
// engine's own "## " headings, an issue body's headings staying in the
// issue section. Only lengths are kept, never text.

// Kinds of brief.
const (
	BriefLead   = "lead"
	BriefWorker = "worker"
)

// Lead brief sections, in the order they print.
const (
	SectionRole        = "role"         // the project's lead text and the role bullets
	SectionRules       = "rules"        // - Rules: and the rules.md / workflow.yaml rules
	SectionPins        = "pins"         // - Memory: and the pinned lead memories
	SectionDecisions   = "decisions"    // - Decisions in force and its lines
	SectionHandoffNote = "handoff_note" // the newest sessions/ note, as inlined
	SectionChatSummary = "chat_summary" // ### The last day's lead chat
	SectionStateFile   = "state_file"   // the lead state file after a rotate or flush
	SectionRestartList = "restart_list" // ## Restart these watches
)

// LeadSectionNames are a lead brief's sections in print order.
var LeadSectionNames = []string{SectionRole, SectionRules, SectionPins, SectionDecisions, SectionHandoffNote, SectionChatSummary, SectionStateFile, SectionRestartList}

// SectionIntro is a worker briefing's text before its first heading.
const SectionIntro = "intro"

// workerHeadings are the "## " headings the engine writes into a worker's
// briefing; any other heading (the issue body's own) stays in the section
// it is in.
var workerHeadings = []string{
	"Current status guidance", "How to work", "Reading code and docs", "Issue metadata", "Red checks",
	"Worktree", "Branch", "Base branch", "Handoff", "Your lead", "Decisions in force", "Rules", "Memory",
	"Open branches", "Shell on this machine", "Local CI", "Already on main?", "You start this stage fresh",
	"Resuming after a closed session", "Your issue-cli build", "This stage", "Before you start",
	"The owner's words", "The owner's words on this issue", "Your department", "Your departments",
}

// workerSection is the section key of a worker heading ("" for another).
func workerSection(line string) string {
	h, ok := strings.CutPrefix(line, "## ")
	if !ok {
		return ""
	}
	h = strings.TrimSpace(h)
	for _, w := range workerHeadings {
		if h == w {
			return sectionKey(w)
		}
	}
	return ""
}

// sectionKey is a heading as a JSON-friendly key: "How to work" → how_to_work.
func sectionKey(h string) string {
	var b strings.Builder
	sep := false
	for _, r := range strings.ToLower(h) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			if sep && b.Len() > 0 {
				b.WriteByte('_')
			}
			sep = false
			b.WriteRune(r)
		} else {
			sep = true
		}
	}
	return b.String()
}

// splitBrief is a brief's kind and its sections in characters, in order;
// "" and nil for a message that is no engine brief (what the owner typed).
func splitBrief(text string) (string, []namedChars) {
	lines := strings.SplitAfter(text, "\n")
	for _, l := range lines {
		if strings.TrimRight(l, "\n") == "## Your role" {
			return BriefLead, splitLeadBrief(lines)
		}
	}
	if strings.HasPrefix(text, "You have been assigned this issue") || strings.Contains(text, "\n## How to work\n") {
		return BriefWorker, splitWorkerBrief(lines)
	}
	return "", nil
}

// splitLeadBrief cuts a lead brief's lines into LeadSectionNames, each
// summed wherever it occurs, in that order (sections absent are left out).
func splitLeadBrief(lines []string) []namedChars {
	chars := map[string]int{}
	cur := SectionRole
	inHead := true // still in the role part (before the handoff sections)
	listed := false
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\n")
		switch {
		case line == "## Where the last lead left off":
			// After a rotate it carries the state file; else the note.
			cur, inHead = SectionHandoffNote, false
			if strings.HasPrefix(nextText(lines, i), "You are a fresh session of ") {
				cur = SectionStateFile
			}
		case line == "### The last day's lead chat" && !inHead:
			cur = SectionChatSummary
		case line == "## Your state file":
			// After a flush, or a plain spawn's live parts of it.
			cur, inHead = SectionStateFile, false
		case line == "## Restart these watches" && cur == SectionStateFile:
			cur, listed = SectionRestartList, false
		case cur == SectionRestartList:
			// The list ends at the first line after it that is no item.
			if strings.HasPrefix(line, "- ") {
				listed = true
			} else if listed && strings.TrimSpace(line) != "" {
				cur = SectionStateFile
			}
		case inHead && strings.HasPrefix(line, "- Rules:"):
			cur = SectionRules
		case inHead && strings.HasPrefix(line, "- Memory:"):
			cur = SectionPins
		case inHead && strings.HasPrefix(line, "- Decisions in force"):
			cur = SectionDecisions
		case cur == SectionDecisions && line != "" && !strings.HasPrefix(line, " "):
			// Its lines are indented: the next bullet is the role's again.
			cur = SectionRole
		}
		chars[cur] += len(raw)
	}
	var out []namedChars
	for _, name := range LeadSectionNames {
		if chars[name] > 0 {
			out = append(out, namedChars{name, chars[name]})
		}
	}
	return out
}

// nextText is the first non-blank line after lines[i].
func nextText(lines []string, i int) string {
	for _, l := range lines[i+1:] {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// splitWorkerBrief cuts a worker's briefing at the engine's headings, in
// the order they come (a heading met twice adds to its first place).
func splitWorkerBrief(lines []string) []namedChars {
	var out []namedChars
	at := map[string]int{}
	cur := SectionIntro
	for _, raw := range lines {
		if s := workerSection(strings.TrimRight(raw, "\n")); s != "" {
			cur = s
		}
		i, ok := at[cur]
		if !ok {
			i = len(out)
			at[cur] = i
			out = append(out, namedChars{cur, 0})
		}
		out[i].chars += len(raw)
	}
	return out
}

// briefTokens shares the brief's tokens among its sections by their
// characters, largest remainder, so they add up to the brief exactly.
func briefTokens(brief int64, sections []namedChars) []NamedTokens {
	total := 0
	for _, s := range sections {
		total += s.chars
	}
	if total == 0 || brief <= 0 {
		return nil
	}
	out := make([]NamedTokens, len(sections))
	type rem struct {
		i int
		f float64
	}
	rems := make([]rem, len(sections))
	var given int64
	for i, s := range sections {
		exact := float64(brief) * float64(s.chars) / float64(total)
		whole := math.Floor(exact)
		out[i] = NamedTokens{s.name, int64(whole)}
		given += int64(whole)
		rems[i] = rem{i, exact - whole}
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].f > rems[b].f })
	for k := 0; given < brief && k < len(rems); k++ {
		out[rems[k].i].Tokens++
		given++
	}
	return out
}

// medianSections is the brief sections' median over the sessions of the
// commonest kind of brief (a lead's and a worker's sections don't mix),
// each section on its own, in the order the briefs have them; the kind
// and how many sessions it is over.
func medianSections(all []*FloorParts) ([]NamedTokens, string, int) {
	byKind := map[string][]*FloorParts{}
	for _, p := range all {
		if p.BriefKind != "" && len(p.BriefSections) > 0 {
			byKind[p.BriefKind] = append(byKind[p.BriefKind], p)
		}
	}
	kind := BriefLead
	if len(byKind[BriefWorker]) > len(byKind[BriefLead]) {
		kind = BriefWorker
	}
	of := byKind[kind]
	if len(of) == 0 {
		return nil, "", 0
	}
	// Names in brief order: each one after the name before it in the
	// first brief that has it.
	var names []string
	for _, p := range of {
		prev := -1
		for _, s := range p.BriefSections {
			idx := -1
			for i, n := range names {
				if n == s.Name {
					idx = i
					break
				}
			}
			if idx < 0 {
				idx = prev + 1
				names = append(names[:idx], append([]string{s.Name}, names[idx:]...)...)
			}
			prev = idx
		}
	}
	var out []NamedTokens
	for _, name := range names {
		vs := make([]int64, len(of))
		for i, p := range of {
			for _, s := range p.BriefSections {
				if s.Name == name {
					vs[i] += s.Tokens
				}
			}
		}
		if m := median(vs); m > 0 {
			out = append(out, NamedTokens{name, m})
		}
	}
	return out, kind, len(of)
}

// CharsPerToken is the estimate a brief's text is measured by before it
// runs: about 2.7 characters a token, the ratio spend --context's floors
// come to over all their characters.
const CharsPerToken = 2.7

// BriefSection is one section of a brief's text: its spend --context name,
// its characters and its estimated tokens (chars/CharsPerToken, rounded).
type BriefSection struct {
	Name   string `json:"name"`
	Chars  int    `json:"chars"`
	Tokens int64  `json:"tokens"`
}

// SplitBriefText is a brief's kind (BriefLead, BriefWorker; "" for text
// that is no engine brief) and its sections in brief order, cut where
// spend --context cuts a transcript's first message, so a brief rendered
// now (issue-cli lead brief --render --sections) names the same sections.
func SplitBriefText(text string) (string, []BriefSection) {
	kind, secs := splitBrief(text)
	out := make([]BriefSection, 0, len(secs))
	for _, s := range secs {
		out = append(out, BriefSection{Name: s.name, Chars: s.chars, Tokens: EstimateTokens(s.chars)})
	}
	return kind, out
}

// EstimateTokens is chars/CharsPerToken, rounded.
func EstimateTokens(chars int) int64 {
	return int64(math.Round(float64(chars) / CharsPerToken))
}
