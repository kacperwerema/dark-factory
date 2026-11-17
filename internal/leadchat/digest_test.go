package leadchat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A transcript with an owner message, a lead decision, a lead chatter line,
// a changing command, a read-only command and a tool result carrying a
// marker word: the digest keeps the first three kinds that matter and never
// the tool result.
const transcript = `{"type":"user","timestamp":"2026-11-15T10:00:00Z","message":{"content":"ship the board today, it matters"}}
{"type":"assistant","timestamp":"2026-11-15T10:01:00Z","message":{"content":[{"type":"text","text":"Decided: dispatch a/b first."},{"type":"tool_use","name":"Bash","input":{"command":"issue-cli dispatch a/b --quote \"ship the board\""}}]}}
{"type":"user","timestamp":"2026-11-15T10:01:05Z","message":{"content":[{"type":"tool_result","content":"SECRETMARKER dispatched fine"}]}}
{"type":"assistant","timestamp":"2026-11-15T10:02:00Z","message":{"content":[{"type":"text","text":"Looking at the files now."},{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]}}
{"type":"assistant","timestamp":"2026-11-15T10:03:00Z","isSidechain":true,"message":{"content":[{"type":"text","text":"sidechain decided"}]}}
`

func writeTranscript(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDigestKeepsWordsDecisionsCommandsNotToolOutput(t *testing.T) {
	es := ReadEntries(writeTranscript(t), time.Time{})
	d := Digest("lead-x", es, es[0].At, es[len(es)-1].At)
	for _, want := range []string{"owner: ship the board today", "lead: Decided: dispatch a/b first.", "command: issue-cli dispatch a/b"} {
		if !strings.Contains(d, want) {
			t.Errorf("digest misses %q:\n%s", want, d)
		}
	}
	for _, not := range []string{"SECRETMARKER", "Looking at the files", "ls -la", "sidechain"} {
		if strings.Contains(d, not) {
			t.Errorf("digest has %q:\n%s", not, d)
		}
	}
}

func TestDigestCapsLines(t *testing.T) {
	var es []Entry
	at := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	for i := 0; i < DigestMaxLines+50; i++ {
		es = append(es, Entry{At: at.Add(time.Duration(i) * time.Minute), Who: WhoOwner, Text: "hi"})
	}
	d := Digest("lead-x", es, at, at)
	if n := strings.Count(d, "\n- "); n != DigestMaxLines {
		t.Fatalf("%d lines, want %d", n, DigestMaxLines)
	}
	if !strings.Contains(d, "(50 older lines left out") {
		t.Fatal("no cut line")
	}
}

func TestDigestReadsBackAndSearchMatchesEveryWord(t *testing.T) {
	es := ReadEntries(writeTranscript(t), time.Time{})
	dir := t.TempDir()
	p := filepath.Join(dir, "2026-11-15-lead-x.digest.md")
	if err := os.WriteFile(p, []byte(Digest("lead-x", es, es[0].At, es[0].At)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Another lead whose name starts with this one's is not this lead's.
	_ = os.WriteFile(filepath.Join(dir, "2026-11-15-lead-x-seo.digest.md"), []byte("- 2026-11-15 10:00 owner: ship seo\n"), 0o644)
	files := DigestFiles(dir, "lead-x")
	if len(files) != 1 || files[0] != p {
		t.Fatalf("DigestFiles = %v", files)
	}
	back := ReadDigest(p)
	hits := Search([]string{"Ship", "board"}, map[string][]Entry{"digest": back, "transcript": es})
	if len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	if strings.Contains(HitLine(hits[0]), "SECRETMARKER") {
		t.Fatal("tool output in a hit")
	}
	if got := Search([]string{"SECRETMARKER"}, map[string][]Entry{"transcript": es}); len(got) != 0 {
		t.Fatalf("tool output found: %+v", got)
	}
	if got := Search(nil, map[string][]Entry{"transcript": es}); len(got) != 0 {
		t.Fatal("no words matches nothing")
	}
}
