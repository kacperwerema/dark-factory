package briefing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// longSlug is a 140-character slug, as long as the ones that made the
// briefing repeat ~1k tokens of slug.
var longSlug = "cli/" + strings.Repeat("a-long-issue-title-", 8)[:136]

// The worker briefing names its slug at most three times (it was 29–33):
// the issue-cli commands on the worker's own issue leave it out, since the
// session has ISSUE_VIEWER_ISSUE_SLUG. Both the full and the stage briefing.
func TestBriefingNamesOwnSlugAtMostThrice(t *testing.T) {
	if len(longSlug) != 140 {
		t.Fatalf("slug is %d chars", len(longSlug))
	}
	enabled := true
	for _, fresh := range []bool{false, true} {
		wf := tracker.DefaultWorkflow()
		if fresh {
			wf = freshWorkflow(t, "in progress", 0)
		}
		wf.Worktree = &enabled
		dir := t.TempDir()
		path := filepath.Join(dir, "CLI", filepath.Base(longSlug)+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "## Design\n- [ ] first task\n\nRun `issue-cli check " + longSlug + " D1` when done.\n"
		if err := os.WriteFile(path, []byte("---\ntitle: \"Long\"\nstatus: \"in progress\"\n---\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		proj := &tracker.Project{Slug: "proj", IssueDir: dir}
		issue := &tracker.Issue{Slug: longSlug, Title: "Long", Status: "in progress", BodyRaw: body, FilePath: path}
		dp := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil)
		if fresh != (dp.Stage != nil) {
			t.Fatalf("fresh %v: stage = %+v", fresh, dp.Stage)
		}
		if n := strings.Count(dp.Prompt, longSlug); n > 3 {
			t.Errorf("fresh %v: the briefing names the slug %d times, want at most 3:\n%s", fresh, n, dp.Prompt)
		}
		for _, want := range []string{"You have been assigned this issue", tracker.OwnSlugNote, "issue-cli --project proj check <id>", "issue-cli --project proj transition --to"} {
			if !strings.Contains(dp.Prompt, want) {
				t.Errorf("fresh %v: the briefing misses %q", fresh, want)
			}
		}
	}
}
