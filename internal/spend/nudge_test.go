package spend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nudgeRun writes lines as a transcript and runs the hook on it, as
// running tool use id runs; it returns the hook's answer.
func nudgeRun(t *testing.T, state, running string, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := fmt.Sprintf(`{"session_id":"sess-1","transcript_path":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":%q}`, path, running)
	var out bytes.Buffer
	if err := ReadNudge(strings.NewReader(in), &out, state); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// twoCalls is an assistant message with two Bash calls in one turn.
func twoCalls(id string, at time.Time, a, b string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":%q,"content":[{"type":"tool_use","id":"tu-%s-a","name":"Bash","input":{"command":%q}},{"type":"tool_use","id":"tu-%s-b","name":"Bash","input":{"command":%q}}]}}`,
		at.Format(time.RFC3339Nano), id, id, a, id, b)
}

func TestReadNudgeAfterTwoSingleReadTurns(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 11, 23, 9, m, 0, 0, time.UTC) }
	state := t.TempDir()
	reads := []string{
		toolTurn("m1", "Bash", at(1), 100, `go build ./...`), toolResult("m1", at(1), false, "ok"),
		toolTurn("m2", "Bash", at(2), 100, `grep -rn foo internal`), toolResult("m2", at(2), false, "x"),
		toolTurn("m3", "Read", at(3), 100, "/repo/a.go"), toolResult("m3", at(3), false, "x"),
	}
	out := nudgeRun(t, state, "tu-m4", reads...)
	var doc struct {
		Out struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Out.Event != "PostToolUse" || doc.Out.Context != NudgeText {
		t.Fatalf("two single reads: want the nudge, got %q (%v)", out, err)
	}
	// Another call of the same running turn: that pair was nudged already.
	if out := nudgeRun(t, state, "tu-m4b", reads...); out != "" {
		t.Errorf("same pair again: want nothing, got %q", out)
	}

	for name, lines := range map[string][]string{
		"one read after a build": reads[:4],
		"last turn batched":      {reads[2], reads[3], twoCalls("m5", at(5), "cat a.go", "cat b.go")},
		"last turn writes":       {reads[2], reads[3], toolTurn("m5", "Bash", at(5), 100, `sed -i '' s/a/b/ a.go`)},
		"text-only turn between": {reads[2], `{"type":"assistant","message":{"id":"m9","content":[{"type":"text","text":"hm"}]}}`, reads[4]},
		// Claude Code wrote the running turn already: it is not finished,
		// so m3 has a build before it, not a read.
		"running turn written": {reads[0], reads[4], toolTurn("m4", "Bash", at(4), 100, `ls`)},
		// A subagent's turns are not the worker's.
		"sidechain":     {reads[2], strings.Replace(reads[4], `{"type":"assistant"`, `{"isSidechain":true,"type":"assistant"`, 1)},
		"no transcript": {},
	} {
		if name == "no transcript" {
			var out bytes.Buffer
			if err := ReadNudge(strings.NewReader(`{"session_id":"s","transcript_path":"/nowhere/x.jsonl"}`), &out, t.TempDir()); err != nil || out.Len() > 0 {
				t.Errorf("%s: %q %v", name, out.String(), err)
			}
			continue
		}
		if out := nudgeRun(t, t.TempDir(), "tu-m4", lines...); out != "" {
			t.Errorf("%s: want no nudge, got %q", name, out)
		}
	}
	// The next follow-on pair nudges again.
	more := append(append([]string(nil), reads...), toolTurn("m4", "Bash", at(4), 100, `git log -3`))
	if out := nudgeRun(t, state, "tu-m5", more...); !strings.Contains(out, "Read nudge") {
		t.Errorf("next pair: want the nudge, got %q", out)
	}
}
