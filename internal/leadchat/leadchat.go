// Package leadchat reads what the owner and a lead said to each other, for
// the nightly lead flush's pack: from the lead's Claude Code transcript, or
// from the text of its pane. It reads and writes no state of its own.
package leadchat

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

// Transcripts finds the Claude Code session files leadchat reads. The caller
// passes one built on the transcript readers it already has (issue-cli's
// lead flush: bench's folders and session files, spend's agent names), so
// leadchat imports neither.
type Transcripts interface {
	// Sessions are the top-level session transcripts of sessions run in cwd,
	// under projectsDir.
	Sessions(projectsDir, cwd string) []string
	// AgentName is the agent name a transcript records, "" for none.
	AgentName(path string) string
}

// Caps on what a pack carries: the worker reads it whole.
const (
	MaxMessages = 300
	MaxChars    = 600
)

// Message is one thing said in the chat. Who is "owner" or "lead".
type Message struct {
	At   time.Time
	Who  string
	Text string
}

// FromTranscripts is what agent said and heard since since, in the
// transcripts of sessions run in cwd under projectsDir, oldest first and
// capped to the newest MaxMessages. Tool calls, tool results, subagents and
// the harness's own lines (system reminders, command wrappers) are left out.
func FromTranscripts(src Transcripts, projectsDir, cwd, agent string, since time.Time) []Message {
	var out []Message
	for _, top := range src.Sessions(projectsDir, cwd) {
		st, err := os.Stat(top)
		if err != nil || st.ModTime().Before(since) || src.AgentName(top) != agent {
			continue
		}
		out = append(out, readFile(top, since)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if len(out) > MaxMessages {
		out = out[len(out)-MaxMessages:]
	}
	return out
}

func readFile(path string, since time.Time) []Message {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Message
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var line struct {
			Type        string `json:"type"`
			Timestamp   string `json:"timestamp"`
			IsMeta      bool   `json:"isMeta"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.IsMeta || line.IsSidechain {
			continue
		}
		who := map[string]string{"user": "owner", "assistant": "lead"}[line.Type]
		if who == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, line.Timestamp)
		if err != nil || at.Before(since) {
			continue
		}
		if text := said(line.Message.Content); text != "" {
			out = append(out, Message{At: at, Who: who, Text: trim(text)})
		}
	}
	return out
}

// said is a message's words: a plain string, or its text blocks. A line the
// harness wrote opens with a tag and is nobody's words.
func said(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) != nil {
			return ""
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		text = strings.Join(parts, " ")
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<") {
		return ""
	}
	return text
}

// trim puts text on one line and cuts it at MaxChars.
func trim(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > MaxChars {
		return string(r[:MaxChars]) + "…"
	}
	return text
}
