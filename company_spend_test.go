package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/bench"
	"github.com/michal-franc/issue-viewer/internal/spend"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// TestCompanySpend: each room shows its spend this week with the weeks and
// top issues; the group and HQ roll up; a budget shows as "x of y" and a
// bad one as unreadable; a box with nothing says so and names the command.
func TestCompanySpend(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	spend.ResetCache()
	// No prices: the owner's prices file and benchmarks stay out of it.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	projects := t.TempDir()
	old := spendProjectsDir
	spendProjectsDir = func() string { return projects }
	t.Cleanup(func() { spendProjectsDir = old })

	a.Departments = []tracker.Department{
		{Name: "GTM", Budget: &tracker.Budget{Err: "budget: is not a mapping"}},
		{Name: "Marketing", Systems: []string{"UI"}, Parent: "GTM", Budget: &tracker.Budget{Tokens: 1000, Period: "week"}},
		{Name: "Sales", Parent: "GTM"},
	}
	write := func(cwd, file string, lines ...string) {
		folder := bench.TranscriptFolders(projects, cwd)[0]
		os.MkdirAll(folder, 0o755)
		if err := os.WriteFile(filepath.Join(folder, file), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	line := func(id, in string) string {
		return `{"type":"assistant","timestamp":"` + at + `","message":{"id":"` + id + `","model":"m","usage":{"input_tokens":` + in + `}}}`
	}
	write(filepath.Join(a.Root(), ".worktrees", "ui/child"), "w.jsonl", line("a", "1200"))
	write(a.Root(), "ceo.jsonl", `{"type":"agent-name","agentName":"lead-agents"}`, line("b", "50"))

	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/company")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`data-testid="company.room.spend.box.marketing" data-spend-box="marketing"`,
		`1.2k tokens · cost unknown this week`,
		`1.2k of 1k tokens this week`, `wb-chip wb-chip--red" data-testid="company.room.spend.budget"`,
		`data-testid="company.room.spend.issue"><span class="wb-spend-slug">ui/child</span>`,
		`href="/p/agents/issue/ui/child"`,
		// The group rolls up Marketing; HQ everything plus the CEO.
		`data-spend-box="gtm"`, `incl. 2 departments`, `data-testid="company.room.spend.budget_error"`,
		`data-spend-box="hq"`, `1.2k tokens · cost unknown this week`, `incl. 4 boxes`,
		// Sales and Product spent nothing.
		`data-testid="company.room.spend.empty">No spend recorded yet · <code>issue-cli --project agents spend sales</code>`,
		`Estimates · Claude Code on this machine · never enforced`,
	)
	if !strings.Contains(html, `data-spend-box="product"`) {
		t.Error("Product's room has its spend block")
	}
}
