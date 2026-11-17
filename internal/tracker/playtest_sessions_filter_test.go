package tracker

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// IssuePlaytestSessions parses only the session files that name one of its
// slugs: each worktree holds a copy of every session, and parsing them all
// made each prove call slow. PlaytestSessionFiles still reads them all.
func TestIssuePlaytestSessionsParseOnlyTheIssuesFiles(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "playtest", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	var others []string
	for i := 0; i < 20; i++ {
		p := filepath.Join(sessions, "other-"+strconv.Itoa(i)+".yaml")
		if err := os.WriteFile(p, []byte("issue: cli/other\nsteps:\n  - id: TP1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		others = append(others, p)
	}
	if err := os.WriteFile(filepath.Join(sessions, "mine.yaml"), []byte("session: mine\nissue: 'cli/mine'\nsteps:\n  - id: TP1\n  - id: TP2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := IssuePlaytestSessions(dir, "cli/mine")
	if len(got) != 1 || got[0].Session != "mine" || len(got[0].Steps) != 2 {
		t.Fatalf("sessions = %+v, want mine with two steps", got)
	}
	sessionCache.Lock()
	for _, p := range others {
		if _, ok := sessionCache.m[p]; ok {
			t.Errorf("%s was parsed: it names another issue", p)
		}
	}
	sessionCache.Unlock()

	if all := PlaytestSessionFiles(dir); len(all) != 21 {
		t.Errorf("PlaytestSessionFiles read %d, want all 21", len(all))
	}
	if got := IssuePlaytestSessions(dir, "cli/other", "cli/mine"); len(got) != 21 {
		t.Errorf("two slugs: %d sessions, want 21", len(got))
	}
}
