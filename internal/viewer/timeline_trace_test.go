package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/timeline"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// AC6: the Timeline tab's By stage table, one row per status visit, tokens
// from the worktree's transcript, "—" for what nothing recorded and a line
// naming it.
func TestTimelineByStage(t *testing.T) {
	proj, tmp := setupTestProject(t)
	proj.WorkDir = tmp
	issuePath := filepath.Join(proj.IssueDir, "bug-in-login.md")
	stats := `{"transitions":[{"from":"in design","to":"in progress","ts":"2026-10-22T10:30:00Z"}],
"dispatches":[{"at":"2026-10-22T10:00:00Z","session":"agent-bug-in-login","agent":"claude"}]}`
	if err := os.WriteFile(tracker.StatsSidecarPath(issuePath), []byte(stats), 0o644); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(tmp, ".agent-logs", "agent-bug-in-login")
	os.MkdirAll(logDir, 0o755)
	lines := `{"args":["--project","test-project","transition","bug-in-login","--to","in progress"],"ts":"2026-10-22T10:29:00Z","exit":1,"dur_ms":90}
{"args":["--project","test-project","transition","bug-in-login","--to","in progress"],"ts":"2026-10-22T10:30:00Z","exit":0,"dur_ms":200}
`
	if err := os.WriteFile(filepath.Join(logDir, "agent-bug-in-login.clilog"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	projects := t.TempDir()
	worktree := filepath.Join(timeline.ProjectRoot(&proj), ".worktrees", "bug-in-login")
	folder := bench.TranscriptFolders(projects, worktree)[0]
	os.MkdirAll(folder, 0o755)
	const marker = "TRANSCRIPTMARKERTEXT"
	msg := `{"type":"assistant","sessionId":"s-1","timestamp":"2026-10-22T10:10:00Z","message":{"id":"m1","model":"claude-x","usage":{"input_tokens":1200,"output_tokens":300,"cache_read_input_tokens":5000},"content":[{"type":"text","text":"` + marker + `"}]}}
`
	if err := os.WriteFile(filepath.Join(folder, "s-1.jsonl"), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	old := traceProjectsDir
	traceProjectsDir = projects
	defer func() { traceProjectsDir = old }()

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, page := getBody(t, ts.URL+"/p/test-project/issue/bug-in-login?tab=timeline")
	if n := strings.Count(page, `data-testid="timeline.stage"`); n != 2 {
		t.Fatalf("%d stage rows, want 2 (in design, in progress)", n)
	}
	for _, want := range []string{`data-testid="timeline.stages"`, `data-status="in design"`, "1.2k / 300 / 5k", "claude (claude-x)",
		"→ in progress ✗ 2 tries", "trace bug-in-login", `data-testid="timeline.stages.note"`, "briefings (sizes unknown)"} {
		if !strings.Contains(page, want) {
			t.Errorf("By stage: missing %q", want)
		}
	}
	if strings.Contains(page, "transcripts (tokens unknown)") {
		t.Error("the transcript was read, yet the note says it was not")
	}
	if strings.Contains(page, marker) {
		t.Error("transcript text reached the page")
	}
}

func TestStageRowsUnknowns(t *testing.T) {
	rows, note := stageRows(nil, []missingSource{{Name: "Transcripts"}, {Name: "CLI log"}})
	if len(rows) != 0 || note != "Not read: transcripts (tokens unknown), the CLI log (calls and first tries unknown)." {
		t.Errorf("rows %v note %q", rows, note)
	}
}
