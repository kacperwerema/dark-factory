package spend

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The read nudge: a Claude Code PostToolUse hook in a lean worker's
// settings (agentrun.ReadNudgeHooks) runs issue-cli hook read-nudge after
// each tool call. When the worker's last two finished turns were each one
// read-only call (ReadOnlyCall), it adds NudgeText to the worker's context,
// once per such pair: the turn after could have gone in the one before.
//
// The running call's own message is not in the transcript while its tool
// runs (Claude Code writes it with its result), so the hook looks only at
// finished turns: it nudges one turn late, but needs no guess about the
// running turn's other calls.

// NudgeText is what the hook adds to the worker's context.
const NudgeText = "Read nudge: your last two turns were each one read-only call (grep, sed -n, cat, git, issue-cli show). Every turn re-reads the whole context: put every read a step needs in one message (parallel tool calls) or one command."

// nudgeTail is how much of the transcript's end the hook reads: the last
// two turns and their results.
const nudgeTail = 4 << 20

// hookInput is the part of Claude Code's hook input the nudge reads.
type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	ToolUseID      string `json:"tool_use_id"`
}

// ReadNudge reads a PostToolUse hook's input from in and writes the hook's
// answer to out: the nudge as additionalContext, or nothing. stateDir holds
// one file per session naming the turn last nudged for ("" = the temp
// dir's issue-cli-read-nudge). Any failure means no nudge: a hook must not
// stand in the worker's way.
func ReadNudge(in io.Reader, out io.Writer, stateDir string) error {
	var hi hookInput
	if err := json.NewDecoder(in).Decode(&hi); err != nil || hi.TranscriptPath == "" {
		return nil
	}
	turns := finishedTurns(hi.TranscriptPath, hi.ToolUseID)
	if len(turns) < 2 {
		return nil
	}
	prev, last := turns[len(turns)-2], turns[len(turns)-1]
	if !prev.singleRead() || !last.singleRead() {
		return nil
	}
	if stateDir == "" {
		stateDir = filepath.Join(os.TempDir(), "issue-cli-read-nudge")
	}
	state := filepath.Join(stateDir, safeName(hi.SessionID))
	if b, err := os.ReadFile(state); err == nil && strings.TrimSpace(string(b)) == last.id {
		return nil // this pair was nudged already (another call of the turn)
	}
	if os.MkdirAll(stateDir, 0o700) == nil {
		_ = os.WriteFile(state, []byte(last.id+"\n"), 0o600)
	}
	return json.NewEncoder(out).Encode(map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     "PostToolUse",
			"additionalContext": NudgeText,
		},
	})
}

// nudgeTurn is one finished assistant message: its tool calls and whether
// every one only reads.
type nudgeTurn struct {
	id       string
	calls    int
	readOnly bool
}

func (t nudgeTurn) singleRead() bool { return t.calls == 1 && t.readOnly }

// finishedTurns are the main thread's assistant messages in the end of the
// transcript at path, in order, leaving out the one holding the running
// call (running, when Claude Code wrote it already).
func finishedTurns(path, running string) []nudgeTurn {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	off := st.Size() - nudgeTail
	if off < 0 {
		off = 0
	}
	data := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(data, off); err != nil && err != io.EOF {
		return nil
	}
	if off > 0 { // the first line is cut: drop it
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	var order []string
	byID := map[string]*nudgeTurn{}
	skip := map[string]bool{}
	for _, b := range bytes.Split(data, []byte("\n")) {
		if !bytes.Contains(b, []byte(`"assistant"`)) {
			continue
		}
		var line struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				ID      string `json:"id"`
				Content []struct {
					Type  string `json:"type"`
					ID    string `json:"id"`
					Name  string `json:"name"`
					Input struct {
						Command string `json:"command"`
					} `json:"input"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(b, &line) != nil || line.Type != "assistant" || line.IsSidechain || line.Message.ID == "" {
			continue
		}
		id := line.Message.ID
		t := byID[id]
		if t == nil {
			t = &nudgeTurn{id: id, readOnly: true}
			byID[id] = t
			order = append(order, id)
		}
		for _, bl := range line.Message.Content {
			if bl.Type != "tool_use" {
				continue
			}
			if running != "" && bl.ID == running {
				skip[id] = true
			}
			t.calls++
			if !ReadOnlyCall(bl.Name, bl.Input.Command) {
				t.readOnly = false
			}
		}
	}
	var out []nudgeTurn
	for _, id := range order {
		if !skip[id] {
			out = append(out, *byID[id])
		}
	}
	return out
}

// safeName keeps a session id to a file name's safe characters.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}
