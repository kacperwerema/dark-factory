package retro

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sweepFile(t *testing.T, dir, name, date string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# Workflow Retrospective\n\nIssue: cli/x\nDate: " + date + "\n\nBase workflow: fine\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sweepTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func names(es []*Entry) string {
	var out []string
	for _, e := range es {
		out = append(out, e.FileName)
	}
	return strings.Join(out, " ")
}

// Select reads the Date line, not the file's time (the owner's answers
// rewrite a post-mortem), holds both ends, and skips what is swept already.
func TestSelectWindowAndTopLevelOnly(t *testing.T) {
	dir := t.TempDir()
	sweepFile(t, dir, "b.md", "2026-11-02T12:00:00Z")
	sweepFile(t, dir, "a.md", "2026-11-01T12:00:00Z")
	sweepFile(t, dir, "c.md", "2026-11-03T12:00:00Z")
	sweepFile(t, filepath.Join(dir, ArchiveDir), "old.md", "2026-11-02T12:00:00Z")
	entries, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(Select(entries, dir, Window{})); got != "a.md b.md c.md" {
		t.Errorf("open window: %q", got)
	}
	w := Window{Since: sweepTime(t, "2026-11-02T12:00:00Z"), Until: sweepTime(t, "2026-11-02T12:00:00Z")}
	if got := names(Select(entries, dir, w)); got != "b.md" {
		t.Errorf("both ends held: %q", got)
	}
}

// Archive moves, keeps what it is told to, never overwrites, and its log
// line is the mark.
func TestArchiveMovesLogsAndMarks(t *testing.T) {
	dir := t.TempDir()
	sweepFile(t, dir, "move.md", "2026-11-01T12:00:00Z")
	sweepFile(t, dir, "keep.md", "2026-11-01T12:00:00Z")
	sweepFile(t, dir, "clash.md", "2026-11-01T12:00:00Z")
	sweepFile(t, filepath.Join(dir, ArchiveDir), "clash.md", "2026-10-01T12:00:00Z")
	if _, ok, err := LastSweep(dir); ok || err != nil {
		t.Fatalf("no sweep logged yet: ok %v, err %v", ok, err)
	}
	entries, _ := Load(dir)
	until := sweepTime(t, "2026-11-02T00:00:00Z")
	s, err := Archive(dir, Select(entries, dir, Window{Until: until}), func(e *Entry) bool { return e.FileName == "keep.md" },
		Sweep{At: until, By: "agent-x", Until: until})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.Moved, " ") != "move.md" || strings.Join(s.Waiting, " ") != "keep.md" || strings.Join(s.Clashes, " ") != "clash.md" {
		t.Errorf("sweep %+v", s)
	}
	for _, p := range []string{filepath.Join(dir, ArchiveDir, "move.md"), filepath.Join(dir, "keep.md"), filepath.Join(dir, "clash.md")} {
		if _, err := os.Stat(p); err != nil {
			t.Error(err)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, ArchiveDir, "clash.md")); !strings.Contains(string(raw), "2026-10-01") {
		t.Error("the archive's file was overwritten")
	}
	raw, err := os.ReadFile(LogPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) != 1 || !strings.Contains(lines[0], `"by":"agent-x"`) || !strings.Contains(lines[0], `"moved":["move.md"]`) {
		t.Errorf("log: %s", raw)
	}
	if mark, ok, err := LastSweep(dir); !ok || err != nil || !mark.Equal(until) {
		t.Errorf("mark %v, %v, %v", mark, ok, err)
	}
}

// The mark is the newest Until, whatever came last, and a torn line doesn't
// stop a sweep.
func TestLastSweepNewestUntilSkipsATornLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ArchiveDir), 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"until":"2026-11-03T00:00:00Z"}` + "\n" + `{"until":"2026-11-01T00:00:00Z"}` + "\n" + `{"until":"2026-11-0`
	if err := os.WriteFile(LogPath(dir), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	mark, ok, err := LastSweep(dir)
	if err != nil || !ok || !mark.Equal(sweepTime(t, "2026-11-03T00:00:00Z")) {
		t.Errorf("mark %v, %v, %v", mark, ok, err)
	}
}
