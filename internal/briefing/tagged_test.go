package briefing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// templateIssue is an issue at status in a checkout that has
// cmd/issue-cli/templates/, its body naming body.
func templateIssue(t *testing.T, status, body string) (*tracker.Project, *tracker.Issue) {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(filepath.Join(root, "cmd", "issue-cli", "templates", "departments", "sales-b2c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(issues, "fix.md")
	if err := os.WriteFile(path, []byte("---\ntitle: \"Wire payments\"\nstatus: \""+status+"\"\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := &tracker.Project{Slug: "proj", IssueDir: issues, WorkDir: root}
	return proj, &tracker.Issue{Slug: "factory/fix", Title: "Wire payments", Status: status, BodyRaw: body, FilePath: path}
}

// An in-progress briefing of an issue whose body names a file under
// cmd/issue-cli/templates/ carries the memories tagged template, whatever
// its title; at another status, or for an issue elsewhere, it doesn't.
func TestAnInProgressBriefingCarriesTheTemplateMemories(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	const pitfall = "Editing a bundled department template's file needs its earlier hash pinned in the same commit"
	add := func(proj *tracker.Project) string {
		t.Helper()
		r, err := memory.ProjectStore(proj.IssueDir).Add(memory.Draft{Text: pitfall, Kind: memory.KindPitfall, Scope: memory.ScopeProject, Tags: []string{"template"}}, memory.AddOptions{}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return r.Memory.ID
	}
	touches := "Add the provider line to `cmd/issue-cli/templates/departments/sales-b2c/role.md`.\n"

	wf := freshWorkflow(t, "in progress", 0)
	proj, issue := templateIssue(t, "in progress", touches)
	id := add(proj)
	dp := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil)
	if !strings.Contains(dp.Prompt, "For the template files this issue touches:") || !strings.Contains(dp.Prompt, id+" · pitfall: "+pitfall) {
		t.Fatalf("the template memory is missing from the in-progress briefing:\n%s", dp.Prompt)
	}
	if ids := StageMemoryIDs(proj, issue); len(ids) != 1 || ids[0] != id {
		t.Fatalf("stage memory ids = %v, want [%s]", ids, id)
	}

	for name, c := range map[string]struct{ status, body string }{
		"testing":   {"testing", touches},
		"elsewhere": {"in progress", "Fix `internal/x/run.go`.\n"},
	} {
		wf := freshWorkflow(t, c.status, 0)
		proj, issue := templateIssue(t, c.status, c.body)
		add(proj)
		if dp := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil); strings.Contains(dp.Prompt, pitfall) {
			t.Errorf("%s: the template memory reached the briefing:\n%s", name, dp.Prompt)
		}
	}
}
