package leadchat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTranscripts is a fixed list of session files and the agent each names.
type fakeTranscripts struct {
	files map[string]string // path -> agent name
	asked []string
}

func (f *fakeTranscripts) Sessions(projectsDir, cwd string) []string {
	f.asked = append(f.asked, projectsDir+"|"+cwd)
	var out []string
	for p := range f.files {
		out = append(out, p)
	}
	return out
}

func (f *fakeTranscripts) AgentName(path string) string { return f.files[path] }

func TestFromTranscriptsReadsOnlyTheLeadsSessions(t *testing.T) {
	dir := t.TempDir()
	since := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	write := func(name string, lines ...string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	lead := write("lead.jsonl",
		`{"type":"assistant","timestamp":"2026-11-07T10:05:00Z","message":{"content":[{"type":"text","text":"On it."}]}}`,
		`{"type":"user","timestamp":"2026-11-07T10:00:00Z","message":{"content":"Plan the night"}}`,
		`{"type":"user","timestamp":"2026-11-06T10:00:00Z","message":{"content":"yesterday"}}`,
		`{"type":"user","timestamp":"2026-11-07T10:01:00Z","message":{"content":"<system-reminder>x</system-reminder>"}}`,
		`{"type":"user","timestamp":"2026-11-07T10:02:00Z","isSidechain":true,"message":{"content":"subagent"}}`)
	other := write("worker.jsonl", `{"type":"user","timestamp":"2026-11-07T11:00:00Z","message":{"content":"not the lead's"}}`)
	src := &fakeTranscripts{files: map[string]string{lead: "lead-p", other: "agent-x"}}

	got := FromTranscripts(src, "/projects", "/repo", "lead-p", since)
	var said []string
	for _, m := range got {
		said = append(said, m.Who+": "+m.Text)
	}
	if want := "owner: Plan the night|lead: On it."; strings.Join(said, "|") != want {
		t.Errorf("said = %q, want %q", strings.Join(said, "|"), want)
	}
	if len(src.asked) != 1 || src.asked[0] != "/projects|/repo" {
		t.Errorf("Sessions asked with %q, want the projects dir and cwd passed through", src.asked)
	}
}
