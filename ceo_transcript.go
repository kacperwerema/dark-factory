package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// The CEO chat reads the conversation from the lead's Claude Code transcript
// when it can find it: ~/.claude/projects/<cwd slug>/<session>.jsonl, one
// JSON record per line. The transcript has whole messages (the pane has them
// wrapped at the terminal's width), every tool call with its id, and each
// AskUserQuestion's answer, so the chat can tell answered questions from open
// ones. The layout is Claude Code's and undocumented: records and blocks this
// file doesn't know are skipped, and with no turns out of it the chat falls
// back to the pane (parseCEOPane).

// CEOBubble is one CEO message, its markdown rendered.
type CEOBubble struct {
	HTML template.HTML `json:"html"`
	// Markdown is the message as written.
	Markdown string `json:"markdown"`
}

// CEOToolCall is one tool call in a CEO turn: the tool's name and a short
// label (its description, command or file).
type CEOToolCall struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
}

// CEOQuestion is one AskUserQuestion call: up to four questions asked
// together, answered once its tool result is in the transcript. Claude
// Code writes the call itself only after the answer, so an open one is
// read from the pane (CEOPaneAsk).
type CEOQuestion struct {
	ID        string        `json:"id"`
	Questions []CEOAskEntry `json:"questions"`
	Answered  bool          `json:"answered"`
}

// CEOAskEntry is one question of an AskUserQuestion call.
type CEOAskEntry struct {
	Header      string         `json:"header"`
	Question    string         `json:"question"`
	MultiSelect bool           `json:"multi_select"`
	Options     []CEOAskOption `json:"options"`
	// Answer is what was chosen, as the tool result has it ("A, B" for
	// several), once answered.
	Answer string `json:"answer,omitempty"`
}

// CEOAskOption is one option of a question.
type CEOAskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// CEOOpenQuestion is one question the CEO waits on: an AskUserQuestion
// (Kind "ask", ID its tool call) or a sentence ending in "?" in its last
// message that the owner hasn't replied to (Kind "sentence").
type CEOOpenQuestion struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	ID   string `json:"id,omitempty"`
}

// CEOTranscript is what the chat reads out of one transcript.
type CEOTranscript struct {
	Path  string    `json:"path"`
	Turns []CEOTurn `json:"turns"`
	// Older says earlier turns were cut: the file's head wasn't read, or
	// there were more than the chat shows.
	Older bool `json:"older"`
}

const (
	// ceoTranscriptTail is how much of a transcript's end is read.
	ceoTranscriptTail = 4 << 20
	// ceoTranscriptTurns is how many turns the chat shows.
	ceoTranscriptTurns = 60
	// ceoTranscriptScan is how many of a folder's newest transcripts are
	// looked at for the lead's name.
	ceoTranscriptScan = 20
	// ceoTranscriptProbe is how much of a transcript's head and tail are
	// read for the lead's name.
	ceoTranscriptProbe = 64 << 10
)

// claudeProjectsDir is where Claude Code keeps transcripts; a seam for tests.
var claudeProjectsDir = func() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// claudeSlugChars are what Claude Code turns into "-" in a cwd's folder name.
var claudeSlugChars = regexp.MustCompile(`[^A-Za-z0-9]`)

// claudeSlugMax is the longest folder name Claude Code keeps whole.
const claudeSlugMax = 200

// leadTranscript is the newest transcript under the cwd's folder whose
// agent-name or custom-title record is session; ok is false when there is
// none.
func leadTranscript(cwd, session string) (string, bool) {
	root := claudeProjectsDir()
	if root == "" || cwd == "" || session == "" {
		return "", false
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	slug := claudeSlugChars.ReplaceAllString(cwd, "-")
	pattern := filepath.Join(root, slug, "*.jsonl")
	if len(slug) > claudeSlugMax {
		// A long cwd's folder is its first 200 characters and a hash.
		pattern = filepath.Join(root, slug[:claudeSlugMax]+"-*", "*.jsonl")
	}
	matches, _ := filepath.Glob(pattern)
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil {
			files = append(files, file{m, st.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > ceoTranscriptScan {
		files = files[:ceoTranscriptScan]
	}
	needles := [][]byte{
		[]byte(`"agentName":` + jsonString(session)),
		[]byte(`"customTitle":` + jsonString(session)),
	}
	for _, f := range files {
		if transcriptNames(f.path, needles) {
			return f.path, true
		}
	}
	return "", false
}

// transcriptNames reports whether the head or tail of path holds one of
// needles.
func transcriptNames(path string, needles [][]byte) bool {
	fh, err := os.Open(path)
	if err != nil {
		return false
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return false
	}
	offsets := []int64{0}
	if st.Size() > ceoTranscriptProbe {
		offsets = append(offsets, st.Size()-ceoTranscriptProbe)
	}
	buf := make([]byte, ceoTranscriptProbe)
	for _, off := range offsets {
		n, _ := fh.ReadAt(buf, off)
		for _, needle := range needles {
			if bytes.Contains(buf[:n], needle) {
				return true
			}
		}
	}
	return false
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// transcriptCache keeps the last read of each transcript until its size or
// modification time changes: the page polls every few seconds.
var transcriptCache = struct {
	sync.Mutex
	m map[string]cachedTranscript
}{m: map[string]cachedTranscript{}}

type cachedTranscript struct {
	size int64
	mod  time.Time
	t    CEOTranscript
}

// readLeadTranscript reads path's turns, from the cache when the file is
// unchanged.
func readLeadTranscript(path string) (CEOTranscript, error) {
	st, err := os.Stat(path)
	if err != nil {
		return CEOTranscript{}, err
	}
	transcriptCache.Lock()
	c, ok := transcriptCache.m[path]
	transcriptCache.Unlock()
	if ok && c.size == st.Size() && c.mod.Equal(st.ModTime()) {
		return c.t.clone(), nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return CEOTranscript{}, err
	}
	defer fh.Close()
	var r io.Reader = fh
	cut := false
	if st.Size() > ceoTranscriptTail {
		if _, err := fh.Seek(st.Size()-ceoTranscriptTail, io.SeekStart); err != nil {
			return CEOTranscript{}, err
		}
		cut = true
	}
	t := parseTranscript(r, cut, ceoTranscriptTurns)
	t.Path = path
	transcriptCache.Lock()
	transcriptCache.m[path] = cachedTranscript{size: st.Size(), mod: st.ModTime(), t: t}
	transcriptCache.Unlock()
	return t.clone(), nil
}

// clone copies the turns and their questions, so a caller changing them
// doesn't change the cached read.
func (t CEOTranscript) clone() CEOTranscript {
	turns := make([]CEOTurn, len(t.Turns))
	for i, turn := range t.Turns {
		turn.Text = append([]string(nil), turn.Text...)
		if len(turn.Asks) > 0 {
			asks := make([]*CEOQuestion, len(turn.Asks))
			for j, q := range turn.Asks {
				c := *q
				asks[j] = &c
			}
			turn.Asks = asks
		}
		turns[i] = turn
	}
	t.Turns = turns
	return t
}

// transcriptRecord is the part of a transcript line the chat reads.
type transcriptRecord struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	IsCompact   bool   `json:"isCompactSummary"`
	Origin      *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message *struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

// transcriptBlock is one content block of a message.
type transcriptBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

// parseTranscript reads transcript lines into turns, keeping the last
// maxTurns. cut says r starts mid-file: its first line is partial.
func parseTranscript(r io.Reader, cut bool, maxTurns int) CEOTranscript {
	out := CEOTranscript{Turns: []CEOTurn{}, Older: cut}
	asks := map[string]*CEOQuestion{}
	// lastMsg is the message id of the CEO's newest bubble, so blocks of
	// one message (written as one record each) join one bubble.
	lastMsg := ""
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	first := true
	for sc.Scan() {
		line := sc.Bytes()
		if first && cut {
			first = false
			continue
		}
		first = false
		// Bookkeeping records (api-request-blob, mode, agent-name …) start
		// with their type; messages start with parentUuid.
		if bytes.HasPrefix(line, []byte(`{"type":"`)) && !bytes.HasPrefix(line, []byte(`{"type":"user"`)) && !bytes.HasPrefix(line, []byte(`{"type":"assistant"`)) {
			continue
		}
		var rec transcriptRecord
		if json.Unmarshal(line, &rec) != nil || rec.Message == nil || rec.IsSidechain {
			continue
		}
		switch rec.Type {
		case "user":
			blocks, text := transcriptContent(rec.Message.Content)
			for _, b := range blocks {
				if b.Type == "tool_result" {
					if q := asks[b.ToolUseID]; q != nil {
						q.Answered = true
						fillAnswers(q, rec.ToolUseResult)
					}
				}
			}
			if rec.IsMeta || rec.IsCompact || (rec.Origin != nil && rec.Origin.Kind != "human") {
				continue
			}
			if text = ownerText(text); text != "" {
				out.Turns = append(out.Turns, CEOTurn{From: "you", Text: []string{text}})
				lastMsg = ""
			}
		case "assistant":
			blocks, _ := transcriptContent(rec.Message.Content)
			for _, b := range blocks {
				switch b.Type {
				case "text":
					text := strings.TrimSpace(b.Text)
					if text == "" {
						continue
					}
					cur := ceoTurn(&out.Turns)
					if n := len(cur.Bubbles); n > 0 && lastMsg != "" && lastMsg == rec.Message.ID {
						cur.Bubbles[n-1].Markdown += "\n\n" + text
					} else {
						cur.Bubbles = append(cur.Bubbles, CEOBubble{Markdown: text})
					}
					lastMsg = rec.Message.ID
				case "tool_use":
					cur := ceoTurn(&out.Turns)
					if b.Name == "AskUserQuestion" {
						if q := askOf(b); q != nil {
							asks[q.ID] = q
							cur.Asks = append(cur.Asks, q)
						}
						lastMsg = ""
						continue
					}
					cur.Tools = append(cur.Tools, b.Name)
					cur.Calls = append(cur.Calls, CEOToolCall{Name: b.Name, Label: toolLabel(b.Input)})
					lastMsg = ""
				}
			}
		}
	}
	if len(out.Turns) > maxTurns {
		out.Turns, out.Older = out.Turns[len(out.Turns)-maxTurns:], true
	}
	for i := range out.Turns {
		for j := range out.Turns[i].Bubbles {
			b := &out.Turns[i].Bubbles[j]
			b.HTML = template.HTML(ceoMarkdown(b.Markdown))
		}
	}
	return out
}

// transcriptContent reads a message's content: a plain string, or blocks
// (whose text blocks are joined into text).
func transcriptContent(raw json.RawMessage) ([]transcriptBlock, string) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return nil, s
	}
	var blocks []transcriptBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil, ""
	}
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" {
			texts = append(texts, b.Text)
		}
	}
	return blocks, strings.Join(texts, "\n")
}

var (
	// ownerTags are blocks Claude Code adds to what the owner typed.
	ownerTags = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	// pasteTags wrap a paste; the text inside stays.
	pasteTags = regexp.MustCompile(`</?pasted_content[^>]*>`)
)

// ownerText is the owner's message as typed: Claude Code's reminders cut,
// paste tags dropped, and "" for command echoes and interrupt notes.
func ownerText(text string) string {
	text = ownerTags.ReplaceAllString(text, "")
	text = strings.TrimSpace(pasteTags.ReplaceAllString(text, ""))
	for _, p := range []string{"<command-", "<local-command-", "[Request interrupted", "Caveat: "} {
		if strings.HasPrefix(text, p) {
			return ""
		}
	}
	return text
}

// askOf reads an AskUserQuestion call; nil when it has no questions.
func askOf(b transcriptBlock) *CEOQuestion {
	var in struct {
		Questions []struct {
			Question    string         `json:"question"`
			Header      string         `json:"header"`
			MultiSelect bool           `json:"multiSelect"`
			Options     []CEOAskOption `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(b.Input, &in) != nil || len(in.Questions) == 0 {
		return nil
	}
	q := &CEOQuestion{ID: b.ID}
	for _, e := range in.Questions {
		q.Questions = append(q.Questions, CEOAskEntry{Header: e.Header, Question: e.Question, MultiSelect: e.MultiSelect, Options: e.Options})
	}
	return q
}

// fillAnswers copies the chosen answers out of an AskUserQuestion's tool
// result ({answers: {question: answer}}).
func fillAnswers(q *CEOQuestion, raw json.RawMessage) {
	var res struct {
		Answers map[string]string `json:"answers"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return
	}
	for i := range q.Questions {
		q.Questions[i].Answer = res.Answers[q.Questions[i].Question]
	}
}

// toolLabel is a tool call's short label: the first of its description,
// command, file, pattern, URL or prompt, on one line, at most 80 runes.
func toolLabel(raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	for _, k := range []string{"description", "command", "file_path", "path", "pattern", "url", "query", "prompt", "skill"} {
		if s, ok := in[k].(string); ok && strings.TrimSpace(s) != "" {
			s = strings.Join(strings.Fields(s), " ")
			if r := []rune(s); len(r) > 80 {
				s = string(r[:79]) + "…"
			}
			return s
		}
	}
	return ""
}

// ceoMarkdownRenderer renders the CEO's messages: GitHub-flavoured markdown
// (tables, task lists, strikethrough, links), raw HTML dropped and unsafe
// link URLs blanked (goldmark's default without WithUnsafe).
var ceoMarkdownRenderer = goldmark.New(goldmark.WithExtensions(extension.GFM))

func ceoMarkdown(text string) string {
	var buf bytes.Buffer
	if err := ceoMarkdownRenderer.Convert([]byte(text), &buf); err != nil {
		return template.HTMLEscapeString(text)
	}
	return buf.String()
}

// openCEOQuestions is what the CEO waits on the owner for: the question
// widget on its pane, else any AskUserQuestion in turns without an answer,
// then the sentences ending in "?" in its last message when the owner
// hasn't written since. Answered questions are never in it. Needs-you and
// lead.json read it.
func openCEOQuestions(turns []CEOTurn, ask *CEOPaneAsk) []CEOOpenQuestion {
	out := []CEOOpenQuestion{}
	switch {
	case ask != nil && ask.Review:
		out = append(out, CEOOpenQuestion{Kind: "ask", Text: "Submit your answers to the CEO's questions"})
	case ask != nil:
		out = append(out, CEOOpenQuestion{Kind: "ask", Text: ask.Question})
	default:
		for _, t := range turns {
			for _, q := range t.Asks {
				if !q.Answered {
					out = append(out, CEOOpenQuestion{Kind: "ask", Text: q.Questions[0].Question, ID: q.ID})
				}
			}
		}
	}
	if n := len(turns); n > 0 && turns[n-1].From == "ceo" {
		last := turns[n-1]
		var text string
		if b := len(last.Bubbles); b > 0 {
			text = last.Bubbles[b-1].Markdown
		} else if p := len(last.Text); p > 0 {
			text = last.Text[p-1]
		}
		for _, q := range leadQuestions(text, leadChatQuestions) {
			out = append(out, CEOOpenQuestion{Kind: "sentence", Text: q})
		}
	}
	return out
}
