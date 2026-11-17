package briefing

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestBuildDispatchPrompt(t *testing.T) {
	enabled := true
	wf := &tracker.WorkflowConfig{Worktree: &enabled}
	proj := &tracker.Project{Slug: "proj", TmuxSession: "work"}
	issue := &tracker.Issue{Slug: "api/fix", Title: "Fix", Status: "in progress", BodyRaw: "Body text."}

	dp := BuildDispatchPrompt(proj, issue, wf, "/w", nil)
	if dp.Worktree != filepath.Join("/w", ".worktrees", "api/fix") || dp.Branch != "work/api/fix" {
		t.Fatalf("worktree = %q branch = %q", dp.Worktree, dp.Branch)
	}
	if dp.Session != "work:agent-api-fix" || dp.Slug != "api/fix" || dp.Status != "in progress" {
		t.Fatalf("got %+v", dp)
	}
	if !strings.HasPrefix(dp.Prompt, tracker.ShortenOwnSlug(tracker.BuildAgentPrompt(proj, issue, wf, dp.Worktree, dp.Branch), issue.Slug)) {
		t.Fatal("Prompt does not start with BuildAgentPrompt for the resolved worktree")
	}
	if dp.Lead != "lead-proj" || !strings.Contains(dp.Prompt, "## Your lead\n\nYou report to the project lead lead-proj (not running).") {
		t.Fatalf("lead = %q, prompt missing the lead section: %q", dp.Lead, dp.Prompt)
	}
	// Workers message the lead only on the human's word (owner, 2026-10-24).
	if !strings.Contains(dp.Prompt, "Don't message lead-proj or any other agent (SendMessage) unless the human, in your chat, gives you permission or asks you to") || !strings.Contains(dp.Prompt, "Your address is agent-api-fix") {
		t.Fatalf("prompt missing the no-messaging line: %q", dp.Prompt)
	}
	if !strings.Contains(dp.Prompt, "issue-cli --project proj ") || !strings.Contains(dp.Prompt, "## Worktree") || !strings.Contains(dp.Prompt, "XDG_STATE_HOME=<sandbox>/state issue-viewer") {
		t.Fatalf("prompt missing --project injection or worktree section")
	}
	// One task, then stop: the worker never picks the next issue.
	if !strings.Contains(dp.Prompt, "This issue is your one task: at its last status, or a gate that waits on the human, stop. Don't run next, start, claim or move another issue, or start other agents.") {
		t.Fatalf("prompt missing the one-task rule: %q", dp.Prompt)
	}
	// The one exception: closing duplicates of its own issue, said as issue-cli close says it.
	if !strings.Contains(dp.Prompt, tracker.CloseRule+" (issue-cli --project proj close <dup> --duplicate-of \"$ISSUE_VIEWER_ISSUE_SLUG\")") ||
		!strings.Contains(dp.Prompt, "except a child issue your lead asks for") {
		t.Fatalf("prompt missing the close rule: %q", dp.Prompt)
	}

	noWT := BuildDispatchPrompt(nil, issue, nil, "/w", nil)
	if noWT.Worktree != "" || noWT.Branch != "" || noWT.Session != "agent-api-fix" || strings.Contains(noWT.Prompt, "## Worktree") {
		t.Fatalf("without worktree: %+v", noWT)
	}
}

// A worker's briefing never sends it to the viewer's HTTP endpoints: the
// built-in workflow's prompts name issue-cli commands only.
func TestBuildDispatchPrompt_NoRawHTTP(t *testing.T) {
	wf := tracker.DefaultWorkflow()
	proj := &tracker.Project{Slug: "proj"}
	for _, st := range wf.GetStatusOrder() {
		issue := &tracker.Issue{Slug: "api/fix", Title: "Fix", Status: st, BodyRaw: "Body text."}
		prompt := BuildDispatchPrompt(proj, issue, wf, "/w", nil).Prompt
		for _, raw := range []string{"curl", "/status.md", "/status.json", "/dispatch", "http://", "https://"} {
			if strings.Contains(prompt, raw) {
				t.Errorf("status %q: prompt mentions %q", st, raw)
			}
		}
	}
}

// The worktree leaves issues/ and departments/ out, so the Worktree section
// names both folders by absolute path: a department issue's
// departments/<key>/ (where brand.md and icp.md are written) and the issues
// tree (retro sweep 2026-10-31).
func TestBuildDispatchPrompt_WorktreeNamesDepartmentFolder(t *testing.T) {
	enabled := true
	wf := &tracker.WorkflowConfig{Worktree: &enabled}
	proj := leadDeptProject(t, "")
	issue := &tracker.Issue{Slug: "seo/brand", System: "SEO", Title: "Brand", Status: "research", BodyRaw: "Body."}

	dp := BuildDispatchPrompt(proj, issue, wf, proj.Root(), nil)
	wt := dp.Prompt[strings.Index(dp.Prompt, "## Worktree"):]
	deptDir := filepath.Join(proj.Root(), "departments", "marketing") + string(filepath.Separator)
	if !filepath.IsAbs(deptDir) || !strings.Contains(wt, "Your department's files are in "+deptDir+" ") {
		t.Fatalf("Worktree section does not name %s:\n%s", deptDir, wt)
	}
	if !strings.Contains(wt, "The issues tree is at "+proj.IssueDir+" ") {
		t.Fatalf("Worktree section does not name the issues tree %s:\n%s", proj.IssueDir, wt)
	}

	// A Product issue gets the issues tree but no department line.
	prod := &tracker.Issue{Slug: "api/fix", System: "API", Title: "Fix", Status: "in progress"}
	if p := BuildDispatchPrompt(proj, prod, wf, proj.Root(), nil).Prompt; strings.Contains(p, "Your department's files") || !strings.Contains(p, "The issues tree is at ") {
		t.Fatalf("Product issue's Worktree section is wrong:\n%s", p)
	}
}

// A brief naming a file under issues/ reads with its absolute path, since
// the worktree the agent runs in has no issues/ folder.
func TestBuildDispatchPrompt_BriefIssuePathsAbsolute(t *testing.T) {
	enabled := true
	wf := &tracker.WorkflowConfig{Worktree: &enabled}
	proj := leadDeptProject(t, "")
	issue := &tracker.Issue{Slug: "factory/x", System: "Factory", Title: "X", Status: "in progress",
		BodyRaw: "Read `issues/Factory/designs/x.md` first, then ./issues/Factory/y.md.\nThe issues/ tree and my-issues/z.md stay as written."}

	p := BuildDispatchPrompt(proj, issue, wf, proj.Root(), nil).Prompt
	for _, want := range []string{
		"Read `" + filepath.Join(proj.IssueDir, "Factory", "designs", "x.md") + "` first",
		"then " + filepath.Join(proj.IssueDir, "Factory", "y.md") + ".",
		"The issues/ tree and my-issues/z.md stay as written.",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(p, "`issues/Factory/designs/x.md`") {
		t.Error("relative issues/ path left in the brief")
	}

	// Without a worktree the agent runs in the main checkout: the body is unchanged.
	noWT := BuildDispatchPrompt(proj, issue, nil, proj.Root(), nil).Prompt
	if !strings.Contains(noWT, "Read `issues/Factory/designs/x.md` first") {
		t.Error("body rewritten without a worktree")
	}

	// An already absolute path is left alone.
	abs := filepath.Join(proj.IssueDir, "Factory", "a.md")
	if got := tracker.AbsIssuePaths(proj, "see "+abs); got != "see "+abs {
		t.Errorf("absolute path rewritten: %q", got)
	}
}

// A project without worktree: true gets no worktree from dispatch, so its
// briefing names issue-cli worktree add, which makes the branch and folder
// prove and check-script look for and runs the project's setup (workers
// there made their own by hand, under other names and with no node_modules). A
// worktree: true project and a folder outside git get no such section.
func TestBriefingNamesWorktreeAdd(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	proj := &tracker.Project{Slug: "proj", WorkDir: root}
	issue := &tracker.Issue{Slug: "api/fix", Title: "Fix", Status: "in progress", BodyRaw: "Body text."}

	prompt := BuildDispatchPrompt(proj, issue, &tracker.WorkflowConfig{}, root, nil).Prompt
	for _, want := range []string{
		"## Branch",
		"issue-cli --project proj worktree add api/fix",
		"it makes .worktrees/api/fix on work/api/fix from main, runs the project's worktree_setup",
		"prove, check-script and the commit-message hint look for work/api/fix first",
		"issue-cli --project proj set-meta api/fix --key branch --value <your-branch>",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "git worktree add") {
		t.Errorf("the Branch section still names the raw git worktree add (no setup):\n%s", prompt)
	}

	enabled := true
	if p := BuildDispatchPrompt(proj, issue, &tracker.WorkflowConfig{Worktree: &enabled}, root, nil).Prompt; strings.Contains(p, "## Branch") {
		t.Errorf("worktree: true briefing has a ## Branch section:\n%s", p)
	}
	outside := &tracker.Project{Slug: "proj", WorkDir: t.TempDir()}
	if p := BuildDispatchPrompt(outside, issue, &tracker.WorkflowConfig{}, outside.WorkDir, nil).Prompt; strings.Contains(p, "## Branch") {
		t.Errorf("a folder outside git got a ## Branch section:\n%s", p)
	}
}
