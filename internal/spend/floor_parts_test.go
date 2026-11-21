package spend

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// tool is a tool definition exactly n characters long.
func tool(name string, n int) string {
	base := fmt.Sprintf(`{"name":%q,"description":""}`, name)
	return fmt.Sprintf(`{"name":%q,"description":%q}`, name, strings.Repeat("d", n-len(base)))
}

// text is n characters of secret prose.
func text(n int) string { return strings.Repeat("secret ", n/7+1)[:n] }

// attachment is an attachment record rendered as n characters.
func attachment(typ string, n int, extra string) string {
	return fmt.Sprintf(`{"type":"attachment","attachment":{"type":%q%s},"rendered":[{"content":%q}]}`, typ, extra, text(n))
}

// firstTurn is a first request as Claude Code writes it, 5,200 characters:
// system 400; tools Bash 600, Artifact 1,000 and an MCP tool 200; messages
// 3,000 (brief 1,000, CLAUDE.md files 800, skills 600, MCP instructions
// 100, the rest 500).
func firstTurn() []string {
	files := fmt.Sprintf(`,"files":[{"path":"/home/someone/CLAUDE.md","type":"Project","content":%q},{"path":"/work/p/CLAUDE.md","type":"Project","content":%q},{"path":"/home/someone/.claude/projects/p/memory/MEMORY.md","type":"AutoMem","content":%q}]`, text(300), text(300), text(200))
	return []string{
		fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q}}`, text(1000)),
		attachment("instructions", 800, files),
		attachment("skill_listing", 600, `,"skillCount":3`),
		attachment("mcp_instructions_delta", 100, ""),
		attachment("agent_listing_delta", 450, ""),
		fmt.Sprintf(`{"type":"api-request-shape","shape":{"system":[{"type":"text","text":%q},{"type":"text","text":%q}],"tools":[%s,%s,%s]}}`,
			text(100), text(300), tool("Bash", 600), tool("Artifact", 1000), tool("mcp__srv__query", 200)),
		fmt.Sprintf(`{"type":"api-request-blob","message":{"role":"user","content":[{"type":"text","text":%q},{"type":"text","text":%q}]}}`, text(1900), text(400)),
		fmt.Sprintf(`{"type":"api-request-blob","message":{"role":"system","content":[{"type":"text","text":%q}]}}`, text(700)),
	}
}

func TestFloorPartsSplitTheFirstTurnByPart(t *testing.T) {
	old := homeDir
	homeDir = func() string { return "/home/someone" }
	t.Cleanup(func() { homeDir = old })
	f := newFixture(t)
	at := func(h int) time.Time { return time.Date(2026, 10, 29, h, 0, 0, 0, time.UTC) }
	// The floor is 2,600 tokens: 2 characters a token. A later request's
	// shape (more tools) is not the floor's.
	lines := append(firstTurn(),
		msg("m1", at(9), "claude-test", 2600, 5, 0, 0),
		fmt.Sprintf(`{"type":"api-request-shape","shape":{"system":[],"tools":[%s]}}`, tool("Workflow", 9000)),
		msg("m2", at(10), "claude-test", 0, 5, 3000, 0))
	f.write(f.worktree("seo/page-refresh"), "a.jsonl", "", lines...)
	// An older transcript without a request shape: a floor, no split.
	f.write(f.worktree("ui/button"), "b.jsonl", "",
		fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q}}`, text(50)),
		msg("p1", at(9), "claude-test", 500, 0, 0, 0))

	rep := LoadContext(f.p, f.issues, now, 7, Options{ProjectsDir: f.projects})
	var split *ContextSession
	for i, s := range rep.Top {
		if s.Issue == "seo/page-refresh" {
			split = &rep.Top[i]
		} else if s.Parts != nil {
			t.Errorf("%s without a request shape has parts %+v", s.Issue, s.Parts)
		}
	}
	if split == nil {
		t.Fatalf("no session: %+v", rep.Top)
	}
	want := &FloorParts{System: 200, Tools: 800, MCP: 150, ClaudeMD: 400, Skills: 300, Brief: 500, Other: 250,
		ToolSizes:     []NamedTokens{{"Artifact", 500}, {"Bash", 300}, {"mcp__srv__query", 100}},
		Files:         []NamedTokens{{FileGlobal, 150}, {FileProject, 150}, {FileMemory, 100}},
		CharsPerToken: 2}
	if !reflect.DeepEqual(split.Parts, want) {
		t.Errorf("parts = %+v\nwant    %+v", split.Parts, want)
	}
	var sum int64
	for _, name := range FloorPartNames {
		sum += split.Parts.Part(name)
	}
	if sum != split.Floor {
		t.Errorf("parts add up to %d, floor %d", sum, split.Floor)
	}
	// The workers' stats: the one session with a split.
	if w := rep.Workers; w.PartsOf != 1 || w.Parts == nil || w.Parts.Tools != 800 || w.Parts.CharsPerToken != 0 {
		t.Errorf("workers parts = %d %+v", w.PartsOf, w.Parts)
	}
	// Numbers, tool names and kinds of file only: no text.
	b, _ := json.Marshal(rep)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "/work/p") {
		t.Error("message text or a path in the report")
	}
}

func TestMedianPartsTakeEachPartOnItsOwn(t *testing.T) {
	a := &FloorParts{System: 10, Skills: 0, ToolSizes: []NamedTokens{{"Artifact", 100}, {"Bash", 5}}}
	b := &FloorParts{System: 20, Skills: 50, ToolSizes: []NamedTokens{{"Bash", 7}}}
	c := &FloorParts{System: 30, Skills: 60, ToolSizes: []NamedTokens{{"Bash", 9}, {"Artifact", 90}}}
	got := medianParts([]*FloorParts{a, b, c})
	if got.System != 20 || got.Skills != 50 {
		t.Errorf("median = %+v", got)
	}
	// Artifact is in two of three (100, 0, 90): median 90; Bash 7.
	if want := []NamedTokens{{"Artifact", 90}, {"Bash", 7}}; !reflect.DeepEqual(got.ToolSizes, want) {
		t.Errorf("tools = %+v, want %+v", got.ToolSizes, want)
	}
	if medianParts(nil) != nil {
		t.Error("parts of no session")
	}
}
