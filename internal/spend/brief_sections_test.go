package spend

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// leadBrief is a lead brief as briefing.LeadBrief renders one after a
// flush close: the role with rules, pins and decisions in it, the handoff
// note (with its own ## headings), the chat summary, the state file and
// its restart list.
func leadBrief() string {
	return strings.Join([]string{
		"You are the project lead of Acme. You run in lead-acme, in the project checkout.",
		"",
		"## Your role",
		"",
		"- Role: project lead (lead-acme). You answer to the human.",
		"- Waiting: you wait in one way only.",
		"- Rules: what applies on every project is in rules.md.",
		"On every project (the owner's rules.md):",
		"- [lead-passes-gates] A lead approves a gate itself.",
		"- Memory: issue-cli memory search|list|show reads what agents learned.",
		"  Standing rules (lead scope):",
		"- m-111111 (convention) Never dispatch without the quote.",
		"- Decisions in force (project scope; hard ones are rules for every worker you start):",
		"  - ui/thing#D-1 · Buttons are blue",
		"  - +5 more: issue-cli decisions --lead ceo",
		"- Local CI: the ci: commands run on the integration branch.",
		"- Handoff: read sessions/2026-11-22-lead-acme.md first.",
		"",
		"## Where the last lead left off",
		"",
		"From sessions/2026-11-22-lead-acme.md:",
		"",
		"## State",
		"- **Main:** green.",
		"## Next",
		"1. Land the queue.",
		"",
		"### The last day's lead chat",
		"",
		"Lead chat 2026-11-21 09:30 to 2026-11-22 09:00 (lead-acme).",
		"Left open: the owner's ask.",
		"",
		"## Your state file",
		"",
		"You are a fresh session of lead-acme, started by the nightly lead flush at 09:00. Your state file as it was:",
		"",
		"# Lead state: lead-acme (acme)",
		"## Today",
		"- 09:12 shipped ui/thing",
		"## Restart these watches",
		"",
		"The old session's background shells ended with it. Start each again with Bash run_in_background:",
		"",
		"- issue-cli lead tick --watch --exit-on-news",
		"",
		"The handoff note is sessions/2026-11-22-lead-acme.md, the summary of the day's chat (both above).",
	}, "\n")
}

func TestLeadBriefSplitsBySection(t *testing.T) {
	kind, got := splitBrief(leadBrief())
	if kind != BriefLead {
		t.Fatalf("kind = %q", kind)
	}
	var names []string
	total := 0
	for _, s := range got {
		names = append(names, s.name)
		total += s.chars
	}
	if want := strings.Join(LeadSectionNames, ","); strings.Join(names, ",") != want {
		t.Errorf("sections = %v, want %s", names, want)
	}
	if total != len(leadBrief()) {
		t.Errorf("sections add up to %d characters, brief %d", total, len(leadBrief()))
	}
	// Each section begins where its renderer starts it.
	at := map[string]int{}
	for _, s := range got {
		at[s.name] = s.chars
	}
	for name, first := range map[string]string{
		SectionRules:       "- Rules: what applies on every project is in rules.md.\nOn every project (the owner's rules.md):\n- [lead-passes-gates] A lead approves a gate itself.\n",
		SectionDecisions:   "- Decisions in force (project scope; hard ones are rules for every worker you start):\n  - ui/thing#D-1 · Buttons are blue\n  - +5 more: issue-cli decisions --lead ceo\n",
		SectionRestartList: "## Restart these watches\n\nThe old session's background shells ended with it. Start each again with Bash run_in_background:\n\n- issue-cli lead tick --watch --exit-on-news\n\n",
	} {
		if at[name] != len(first) {
			t.Errorf("%s = %d characters, want %d", name, at[name], len(first))
		}
	}
	// After a rotate the left-off section is the state file, no note.
	rotated := "x\n\n## Your role\n\n- Role: lead.\n\n## Where the last lead left off\n\nYou are a fresh session of lead-acme, started by issue-cli lead rotate at 10:00.\n\n# Lead state\n"
	if _, got := splitBrief(rotated); len(got) != 2 || got[1].name != SectionStateFile {
		t.Errorf("rotated = %+v", got)
	}
	if kind, got := splitBrief("fix the button please"); kind != "" || got != nil {
		t.Errorf("an owner's message split as %q %+v", kind, got)
	}
}

func TestSpendContextSplitsALeadTranscriptsBriefBySection(t *testing.T) {
	f := newFixture(t)
	at := func(h int) time.Time { return time.Date(2026, 10, 29, h, 0, 0, 0, time.UTC) }
	brief := leadBrief()
	lines := []string{
		fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, brief),
		fmt.Sprintf(`{"type":"api-request-shape","shape":{"system":[{"type":"text","text":%q}],"tools":[%s]}}`, text(700), tool("Bash", 900)),
		fmt.Sprintf(`{"type":"api-request-blob","message":{"role":"user","content":[{"type":"text","text":%q},{"type":"text","text":%q}]}}`, brief, text(400)),
		msg("m1", at(9), "claude-test", 2400, 5, 0, 0),
	}
	f.write(f.p.Root(), "lead.jsonl", "lead-acme", lines...)

	rep := LoadContext(f.p, f.issues, now, 7, Options{ProjectsDir: f.projects})
	var lead *ContextSession
	for i := range rep.Top {
		if rep.Top[i].Lead {
			lead = &rep.Top[i]
		}
	}
	if lead == nil || lead.Parts == nil {
		t.Fatalf("no lead session with parts: %+v", rep.Top)
	}
	p := lead.Parts
	if p.BriefKind != BriefLead || len(p.BriefSections) != len(LeadSectionNames) {
		t.Fatalf("brief %q sections %+v", p.BriefKind, p.BriefSections)
	}
	var sum int64
	for i, s := range p.BriefSections {
		if s.Name != LeadSectionNames[i] || s.Tokens <= 0 {
			t.Errorf("section %d = %+v", i, s)
		}
		sum += s.Tokens
	}
	if d := sum - p.Brief; p.Brief == 0 || d*100 > p.Brief || -d*100 > p.Brief {
		t.Errorf("sections add up to %d, brief part %d (more than 1%% off)", sum, p.Brief)
	}
	// The leads' median carries them, and JSON names the field.
	if l := rep.Leads.Parts; l == nil || l.BriefKind != BriefLead || l.BriefSectionsOf != 1 || len(l.BriefSections) != len(LeadSectionNames) {
		t.Errorf("leads parts = %+v", l)
	}
	b, _ := json.Marshal(lead)
	if !strings.Contains(string(b), `"floor_parts":{`) || !strings.Contains(string(b), `"brief_sections":[{"name":"role","tokens":`) {
		t.Errorf("JSON lacks floor_parts.brief_sections: %s", b)
	}
	if strings.Contains(string(b), "Buttons are blue") {
		t.Error("brief text in the report")
	}
}

// workerBrief is a worker's briefing: the dispatch template's headings,
// an issue body with headings of its own, and stage parts.
func workerBrief() string {
	return strings.Join([]string{
		"You have been assigned this issue: ui/thing",
		"",
		"## Current status guidance",
		"Read the brief.",
		"## How to work",
		"1. issue-cli start claims it.",
		"## Issue metadata",
		"  Title: Buttons",
		"Make the buttons blue.",
		"## Acceptance criteria",
		"- [ ] AC1 blue",
		"## Decisions in force",
		"- ui/thing#D-1 · Buttons are blue",
		"## Shell on this machine",
		"- zsh.",
	}, "\n")
}

func TestWorkerBriefSplitsAtTheEnginesHeadings(t *testing.T) {
	kind, got := splitBrief(workerBrief())
	if kind != BriefWorker {
		t.Fatalf("kind = %q", kind)
	}
	var names []string
	total := 0
	for _, s := range got {
		names = append(names, s.name)
		total += s.chars
	}
	// The body's own "## Acceptance criteria" stays in the issue's section.
	if want := "intro,current_status_guidance,how_to_work,issue_metadata,decisions_in_force,shell_on_this_machine"; strings.Join(names, ",") != want {
		t.Errorf("sections = %v, want %s", names, want)
	}
	if total != len(workerBrief()) {
		t.Errorf("sections add up to %d characters, brief %d", total, len(workerBrief()))
	}
	issue := len("## Issue metadata\n  Title: Buttons\nMake the buttons blue.\n## Acceptance criteria\n- [ ] AC1 blue\n")
	if got[3].chars != issue {
		t.Errorf("issue_metadata = %d characters, want %d", got[3].chars, issue)
	}
	// Tokens are shared out to add up to the brief exactly.
	toks := briefTokens(997, got)
	var sum int64
	for _, s := range toks {
		sum += s.Tokens
	}
	if sum != 997 {
		t.Errorf("tokens add up to %d, want 997: %+v", sum, toks)
	}
}

func TestMedianSectionsKeepBriefOrderAndOneKind(t *testing.T) {
	a := &FloorParts{BriefKind: BriefLead, BriefSections: []NamedTokens{{"role", 100}, {"pins", 10}, {"state_file", 30}}}
	b := &FloorParts{BriefKind: BriefLead, BriefSections: []NamedTokens{{"role", 200}, {"pins", 20}, {"handoff_note", 50}, {"state_file", 40}}}
	c := &FloorParts{BriefKind: BriefWorker, BriefSections: []NamedTokens{{"intro", 5}}}
	got, kind, n := medianSections([]*FloorParts{a, b, c})
	if kind != BriefLead || n != 2 {
		t.Errorf("kind %q of %d", kind, n)
	}
	// handoff_note is in one of two: median 0 (the lower middle), left out.
	want := []NamedTokens{{"role", 100}, {"pins", 10}, {"state_file", 30}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("median = %v, want %v", got, want)
	}
}

// SplitBriefText names a rendered brief's sections as spend --context
// does and estimates each at chars/2.7.
func TestSplitBriefTextMatchesSpendContextsSplit(t *testing.T) {
	kind, got := SplitBriefText(leadBrief())
	wantKind, want := splitBrief(leadBrief())
	if kind != wantKind || len(got) != len(want) {
		t.Fatalf("kind %q %d sections, want %q %d", kind, len(got), wantKind, len(want))
	}
	for i, s := range got {
		if s.Name != want[i].name || s.Chars != want[i].chars {
			t.Errorf("section %d = %s %d, want %s %d", i, s.Name, s.Chars, want[i].name, want[i].chars)
		}
		if s.Tokens != int64(float64(s.Chars)/2.7+0.5) {
			t.Errorf("%s: %d tokens for %d chars, want chars/2.7", s.Name, s.Tokens, s.Chars)
		}
	}
}
