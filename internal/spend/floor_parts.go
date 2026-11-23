package spend

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The floor by part: what a session's first turn is made of. Claude Code
// writes the first request into the transcript before the first answer:
// the api-request-shape record (the system prompt and each tool's
// definition), the api-request-blob records (the messages as sent) and the
// attachments they are built from (instructions: the CLAUDE.md files and
// MEMORY.md; skill_listing; mcp_instructions_delta), after the first user
// message (the brief). Each part is measured in characters and scaled to
// the first turn's measured tokens by the session's own ratio (floor over
// all characters, about 2.7 per token), so a part's tokens are an
// estimate, about ±10%; JSON tool schemas tokenize denser than prose.
// Only lengths, tool names and a file's kind are kept: no text.

// Floor part names, in the order they print.
const (
	PartSystem   = "system"
	PartTools    = "tools"
	PartMCP      = "mcp"
	PartClaudeMD = "claude_md"
	PartSkills   = "skills"
	PartBrief    = "brief"
	PartOther    = "other"
)

// FloorPartNames are the parts in print order.
var FloorPartNames = []string{PartSystem, PartTools, PartMCP, PartClaudeMD, PartSkills, PartBrief, PartOther}

// Kinds of instruction file in the claude_md part.
const (
	FileGlobal  = "global"  // ~/CLAUDE.md or ~/.claude/CLAUDE.md
	FileProject = "project" // the project's (or a parent folder's) CLAUDE.md
	FileMemory  = "memory"  // Claude Code's auto-memory MEMORY.md
)

// NamedTokens is one tool's or one kind of file's tokens.
type NamedTokens struct {
	Name   string `json:"name"`
	Tokens int64  `json:"tokens"`
}

// FloorParts is a first turn split by part, in estimated tokens.
type FloorParts struct {
	// System is the system prompt.
	System int64 `json:"system"`
	// Tools are the built-in tools' definitions (not MCP's).
	Tools int64 `json:"tools"`
	// MCP is the MCP servers' tool definitions and instructions (a
	// deferred MCP tool is a name only, in Other).
	MCP int64 `json:"mcp"`
	// ClaudeMD is the CLAUDE.md files and MEMORY.md; Files by kind.
	ClaudeMD int64 `json:"claude_md"`
	// Skills is the skill list.
	Skills int64 `json:"skills"`
	// Brief is the first user message: a lead's or worker's brief, or
	// what the owner typed.
	Brief int64 `json:"brief"`
	// Other is the rest of the first request: the agent types, deferred
	// tool names, hook output, session context, date, reminders.
	Other int64 `json:"other"`
	// ToolSizes is each tool's definition, biggest first (MCP tools too).
	ToolSizes []NamedTokens `json:"tool_sizes,omitempty"`
	// Files is the claude_md part by kind of file (global, project, memory).
	Files []NamedTokens `json:"files,omitempty"`
	// BriefKind is the brief's kind (lead, worker; "" for what the owner
	// typed), BriefSections the brief by section in brief order, adding
	// up to Brief (brief_sections.go); in a sum of sessions the median
	// over BriefSectionsOf sessions of the commonest kind.
	BriefKind       string        `json:"brief_kind,omitempty"`
	BriefSections   []NamedTokens `json:"brief_sections,omitempty"`
	BriefSectionsOf int           `json:"brief_sections_sessions,omitempty"`
	// CharsPerToken is the session's ratio; 0 in a sum of sessions.
	CharsPerToken float64 `json:"chars_per_token,omitempty"`
}

// Part is the tokens of the named part.
func (p *FloorParts) Part(name string) int64 {
	switch name {
	case PartSystem:
		return p.System
	case PartTools:
		return p.Tools
	case PartMCP:
		return p.MCP
	case PartClaudeMD:
		return p.ClaudeMD
	case PartSkills:
		return p.Skills
	case PartBrief:
		return p.Brief
	case PartOther:
		return p.Other
	}
	return 0
}

func (p *FloorParts) setPart(name string, v int64) {
	switch name {
	case PartSystem:
		p.System = v
	case PartTools:
		p.Tools = v
	case PartMCP:
		p.MCP = v
	case PartClaudeMD:
		p.ClaudeMD = v
	case PartSkills:
		p.Skills = v
	case PartBrief:
		p.Brief = v
	case PartOther:
		p.Other = v
	}
}

// floorChars is a first request's parts in characters, as read.
type floorChars struct {
	system int
	tools  []namedChars // in the order sent
	// blobs is the messages' text as sent; 0 when the transcript has no
	// blob records (an older Claude Code), then the attachments count.
	blobs     int
	brief     int
	briefKind string
	sections  []namedChars // the brief by section
	files     []namedChars
	claudeMD  int // the instructions attachment as rendered
	skills    int
	mcpInstr  int
	otherAtts int // every other attachment as rendered
}

type namedChars struct {
	name  string
	chars int
}

// mcpTool is a tool an MCP server brings.
func mcpTool(name string) bool { return strings.HasPrefix(name, "mcp__") }

// split scales the characters to floor tokens; nil without a request
// shape or a floor.
func (c *floorChars) split(floor int64) *FloorParts {
	if c == nil || floor <= 0 {
		return nil
	}
	var tools, mcp int
	for _, t := range c.tools {
		if mcpTool(t.name) {
			mcp += t.chars
		} else {
			tools += t.chars
		}
	}
	messages := c.blobs
	if messages == 0 {
		messages = c.brief + c.claudeMD + c.skills + c.mcpInstr + c.otherAtts
	}
	other := max(messages-c.brief-c.claudeMD-c.skills-c.mcpInstr, 0)
	total := c.system + tools + mcp + messages
	if total == 0 {
		return nil
	}
	ratio := float64(floor) / float64(total)
	tok := func(n int) int64 { return int64(float64(n)*ratio + 0.5) }
	p := &FloorParts{
		System: tok(c.system), Tools: tok(tools), MCP: tok(mcp + c.mcpInstr),
		ClaudeMD: tok(c.claudeMD), Skills: tok(c.skills), Brief: tok(c.brief), Other: tok(other),
		CharsPerToken: float64(total) / float64(floor),
	}
	for _, t := range c.tools {
		p.ToolSizes = append(p.ToolSizes, NamedTokens{t.name, tok(t.chars)})
	}
	sortNamed(p.ToolSizes)
	// Files share the rendered part by their content's length.
	var fileChars int
	byKind := map[string]int{}
	for _, f := range c.files {
		fileChars += f.chars
		byKind[f.name] += f.chars
	}
	for k, n := range byKind {
		if fileChars > 0 {
			p.Files = append(p.Files, NamedTokens{k, int64(float64(p.ClaudeMD)*float64(n)/float64(fileChars) + 0.5)})
		}
	}
	sortNamed(p.Files)
	p.BriefKind, p.BriefSections = c.briefKind, briefTokens(p.Brief, c.sections)
	if p.BriefSections == nil {
		p.BriefKind = ""
	}
	return p
}

func sortNamed(ns []NamedTokens) {
	sort.SliceStable(ns, func(i, j int) bool {
		if ns[i].Tokens != ns[j].Tokens {
			return ns[i].Tokens > ns[j].Tokens
		}
		return ns[i].Name < ns[j].Name
	})
}

// firstRequestRecords are the record types the first request is read
// from; any other line before the first answer is skipped unparsed.
var firstRequestRecords = [][]byte{
	[]byte(`"type":"api-request-shape"`), []byte(`"type":"api-request-blob"`),
	[]byte(`"type":"attachment"`), []byte(`"type":"user"`), []byte(`"type":"assistant"`),
}

// readFloorChars reads path's first request, up to its first answer; nil
// when the transcript has no request shape (an older Claude Code, or a
// session that never answered).
func readFloorChars(path string) *floorChars {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	c := &floorChars{}
	shape, user := false, false
	for sc.Scan() {
		line := sc.Bytes()
		known := false
		for _, t := range firstRequestRecords {
			if bytes.Contains(line, t) {
				known = true
				break
			}
		}
		if !known {
			continue
		}
		var rec struct {
			Type  string `json:"type"`
			Shape *struct {
				System []struct {
					Text string `json:"text"`
				} `json:"system"`
				Tools []json.RawMessage `json:"tools"`
			} `json:"shape"`
			Message *struct {
				Content json.RawMessage `json:"content"`
				Usage   json.RawMessage `json:"usage"`
			} `json:"message"`
			Attachment *struct {
				Type  string `json:"type"`
				Files []struct {
					Path    string `json:"path"`
					Type    string `json:"type"`
					Content string `json:"content"`
				} `json:"files"`
			} `json:"attachment"`
			Rendered []struct {
				Content string `json:"content"`
			} `json:"rendered"`
		}
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		switch rec.Type {
		case "assistant":
			if rec.Message != nil && len(rec.Message.Usage) > 0 && string(rec.Message.Usage) != "null" {
				if !shape {
					return nil
				}
				return c
			}
		case "api-request-shape":
			if shape || rec.Shape == nil {
				continue
			}
			shape = true
			for _, s := range rec.Shape.System {
				c.system += len(s.Text)
			}
			for _, raw := range rec.Shape.Tools {
				var t struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(raw, &t)
				c.tools = append(c.tools, namedChars{t.Name, len(raw)})
			}
		case "api-request-blob":
			if rec.Message != nil {
				c.blobs += textChars(rec.Message.Content)
			}
		case "user":
			if !user && rec.Message != nil {
				user = true
				text := messageText(rec.Message.Content)
				c.brief = len(text)
				c.briefKind, c.sections = splitBrief(text)
			}
		case "attachment":
			if rec.Attachment == nil {
				continue
			}
			n := 0
			for _, r := range rec.Rendered {
				n += len(r.Content)
			}
			switch rec.Attachment.Type {
			case "instructions":
				files := 0
				for _, fl := range rec.Attachment.Files {
					c.files = append(c.files, namedChars{fileKind(fl.Path, fl.Type), len(fl.Content)})
					files += len(fl.Content)
				}
				if n == 0 {
					n = files
				}
				c.claudeMD += n
			case "skill_listing":
				c.skills += n
			case "mcp_instructions_delta":
				c.mcpInstr += n
			default:
				c.otherAtts += n
			}
		}
	}
	return nil
}

// textChars is the length of the text in a message's content.
func textChars(raw json.RawMessage) int { return len(messageText(raw)) }

// messageText is the text in a message's content: a string, or the text of
// its blocks one after another.
func messageText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, bl := range blocks {
		b.WriteString(bl.Text)
	}
	return b.String()
}

// homeDir is the owner's home, for telling the global CLAUDE.md apart; a
// seam for tests.
var homeDir = func() string { h, _ := os.UserHomeDir(); return h }

// fileKind is an instruction file's kind: memory (auto-memory), global (in
// the home folder or ~/.claude) or project.
func fileKind(path, typ string) string {
	switch {
	case strings.Contains(strings.ToLower(typ), "mem"):
		return FileMemory
	case typ == "User":
		return FileGlobal
	}
	if h := homeDir(); h != "" {
		dir := filepath.Dir(path)
		if dir == h || dir == filepath.Join(h, ".claude") {
			return FileGlobal
		}
	}
	return FileProject
}

// medianParts is the per-part median over the sessions' splits (each part,
// tool and kind of file on its own, so they need not add up to the median
// floor); nil when no session has one.
func medianParts(all []*FloorParts) *FloorParts {
	if len(all) == 0 {
		return nil
	}
	out := &FloorParts{}
	for _, name := range FloorPartNames {
		vs := make([]int64, len(all))
		for i, p := range all {
			vs[i] = p.Part(name)
		}
		out.setPart(name, median(vs))
	}
	out.ToolSizes = medianNamed(all, func(p *FloorParts) []NamedTokens { return p.ToolSizes })
	out.Files = medianNamed(all, func(p *FloorParts) []NamedTokens { return p.Files })
	out.BriefSections, out.BriefKind, out.BriefSectionsOf = medianSections(all)
	return out
}

// medianNamed is each name's median over all (0 where a session lacks it),
// the names with a median above 0, biggest first.
func medianNamed(all []*FloorParts, of func(*FloorParts) []NamedTokens) []NamedTokens {
	names := map[string]bool{}
	for _, p := range all {
		for _, n := range of(p) {
			names[n.Name] = true
		}
	}
	var out []NamedTokens
	for name := range names {
		vs := make([]int64, len(all))
		for i, p := range all {
			for _, n := range of(p) {
				if n.Name == name {
					vs[i] += n.Tokens
				}
			}
		}
		if m := median(vs); m > 0 {
			out = append(out, NamedTokens{name, m})
		}
	}
	sortNamed(out)
	return out
}
