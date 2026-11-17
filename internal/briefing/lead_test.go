package briefing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestBuildDispatchPrompt_ChildOfEpic(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("UI/epic.md", "---\ntitle: \"Epic\"\nstatus: \"in progress\"\nbase: \"base/ui/epic\"\nlead: \"lead-ui-epic\"\n---\n\nEpic body.\n")
	write("UI/child.md", "---\ntitle: \"Child\"\nstatus: \"in progress\"\numbrella: \"ui/epic\"\n---\n\nChild body.\n")
	proj := &tracker.Project{Slug: "p", IssueDir: dir}
	issues, err := tracker.LoadIssues(dir)
	if err != nil {
		t.Fatal(err)
	}
	child := tracker.FindIssue(issues, "ui/child")
	enabled := true
	wf := &tracker.WorkflowConfig{Worktree: &enabled}

	dp := BuildDispatchPrompt(proj, child, wf, "/w", func(n string) bool { return n == "lead-ui-epic" || n == "lead-p" })
	if dp.Lead != "lead-p" || dp.Branch != "work/ui/child" {
		t.Fatalf("got %+v", dp)
	}
	for _, want := range []string{
		"You report to the project lead lead-p (live). Your issue is part of epic ui/epic.",
		"`issue-cli --project p comment \"question: ...\"`",
		"Don't message lead-p or any other agent (SendMessage) unless the human, in your chat, gives you permission or asks you to.",
		"Your address is agent-ui-child",
	} {
		if !strings.Contains(dp.Prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	for _, gone := range []string{"sub-lead", "## Base branch", "base/ui/epic", "you can also message", "message your lead"} {
		if strings.Contains(dp.Prompt, gone) {
			t.Errorf("prompt still mentions %q", gone)
		}
	}

	dp = BuildDispatchPrompt(proj, child, wf, "/w", nil)
	if !strings.Contains(dp.Prompt, "You report to the project lead lead-p (not running).") {
		t.Errorf("lead line missing:\n%s", dp.Prompt)
	}
}
