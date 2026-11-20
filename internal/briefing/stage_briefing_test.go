package briefing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/memory"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// freshWorkflow is the bundled development workflow with status set to
// session: fresh.
func freshWorkflow(t *testing.T, status string, budget int) *tracker.WorkflowConfig {
	t.Helper()
	wf := tracker.DefaultWorkflow()
	s := wf.GetStatus(status)
	if s == nil {
		t.Fatalf("no status %q in the bundled workflow", status)
	}
	s.Session = tracker.SessionFresh
	s.BriefingBudget = budget
	return wf
}

func stageIssue(t *testing.T, status string) (*tracker.Project, *tracker.Issue) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fix.md")
	body := "SECRET-BODY-LINE: the brief the stage must not inline.\n\n## Design\n\nThe design.\n"
	if err := os.WriteFile(path, []byte("---\ntitle: \"Fix it\"\nstatus: \""+status+"\"\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := &tracker.Project{Slug: "proj", IssueDir: dir}
	return proj, &tracker.Issue{Slug: "api/fix", Title: "Fix it", Status: status, BodyRaw: body, FilePath: path, Priority: "high"}
}

func indexOf(xs []string, x string) int {
	for i, s := range xs {
		if s == x {
			return i
		}
	}
	return -1
}

// Unset and continue keep today's briefing; only fresh builds the stage one.
func TestSessionForDefaultsToContinue(t *testing.T) {
	wf := tracker.DefaultWorkflow()
	if wf.SessionFor("in progress") != tracker.SessionContinue || wf.IsFreshStatus("in progress") {
		t.Fatal("unset session is not continue")
	}
	wf.GetStatus("in progress").Session = tracker.SessionContinue
	proj, issue := stageIssue(t, "in progress")
	dp := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil)
	if dp.Stage != nil || !strings.Contains(dp.Prompt, "SECRET-BODY-LINE") {
		t.Fatalf("a continue status changed the briefing: stage=%+v", dp.Stage)
	}
	if wf.BriefingBudgetFor("in progress") != tracker.DefaultBriefingBudget {
		t.Fatalf("default budget = %d", wf.BriefingBudgetFor("in progress"))
	}
}

func TestStageBriefingIsPathsOnly(t *testing.T) {
	wf := freshWorkflow(t, "in progress", 0)
	enabled := true
	wf.Worktree = &enabled
	proj, issue := stageIssue(t, "in progress")
	dp := BuildDispatchPrompt(proj, issue, wf, "/w", nil)
	if dp.Stage == nil {
		t.Fatal("no stage briefing at a fresh status")
	}
	p := dp.Prompt
	if strings.Contains(p, "SECRET-BODY-LINE") {
		t.Fatal("the stage briefing inlines the issue body")
	}
	for _, want := range []string{
		"## You start this stage fresh",
		"no chat from earlier stages",
		issue.FilePath,
		"What the stage before wrote (in design): ## Idea, ## Design, ## Acceptance Criteria, ## Must not",
		filepath.Join("/w", ".worktrees", "api/fix", tracker.HandoffFile),
		"Your output is this stage's artifact: ## Implementation, ## Test Plan",
		"The gate out checks it (into testing):",
		"issue-cli --project proj transition --to testing --check <id> [<id>...] once the work is done: a refusal lists every unmet requirement",
		"## Worktree",
		"## Your lead",
		"Briefing: ",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("stage briefing misses %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "--dry-run lists") {
		t.Error("the stage briefing still sends the worker to a dry run to see what's missing")
	}
	if strings.Contains(p, "## Before you start") || strings.Contains(p, "## Issue metadata") {
		t.Error("the stage briefing carries the continue briefing's sections")
	}
}

func TestStageBriefingFoldsToBudget(t *testing.T) {
	wf := freshWorkflow(t, "in progress", 0)
	proj, issue := stageIssue(t, "in progress")
	wide := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil)
	if len(wide.Stage.Folded) != 0 {
		t.Fatalf("the default budget folded %v", wide.Stage.Folded)
	}
	// A budget just under the full briefing folds the first parts only, in
	// order; the never-folding parts stay.
	wf.GetStatus("in progress").BriefingBudget = wide.Stage.Tokens - 1
	tight := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil)
	if len(tight.Stage.Folded) == 0 {
		t.Fatalf("a budget under the briefing folded nothing: %+v", tight.Stage)
	}
	for i, name := range tight.Stage.Folded {
		if j := indexOf(tracker.StageFoldOrder, name); j < i {
			t.Fatalf("folded %v out of order %v", tight.Stage.Folded, tracker.StageFoldOrder)
		}
	}
	if tight.Stage.Tokens > wide.Stage.Tokens || tight.Stage.OverBudget {
		t.Fatalf("folding didn't shrink it under budget: %+v vs %+v", tight.Stage, wide.Stage)
	}
	// A budget nothing fits in folds every part and says over budget, and
	// still keeps the stop lines, the YOLO-free gate rules and the worktree.
	wf.GetStatus("in progress").BriefingBudget = 10
	tiny := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil)
	if !tiny.Stage.OverBudget || !strings.Contains(tiny.Prompt, ", over budget.") {
		t.Fatalf("not over budget at 10 tokens: %+v", tiny.Stage)
	}
	for _, keep := range []string{"## This stage", "Stop and ask the user", "This issue is your one task", "## Handoff"} {
		if !strings.Contains(tiny.Prompt, keep) {
			t.Errorf("folding dropped %q", keep)
		}
	}
	if got := tokens.Estimate(tiny.Prompt); got != tiny.Stage.Tokens {
		t.Errorf("recorded %d tokens, the text is %d", tiny.Stage.Tokens, got)
	}
}

// Under YOLO the stage's guidance carries the YOLO rule, folded or not.
func TestStageBriefingKeepsTheYOLORule(t *testing.T) {
	old := tracker.YOLOOn
	tracker.YOLOOn = func(string, string) bool { return true }
	t.Cleanup(func() { tracker.YOLOOn = old })
	wf := freshWorkflow(t, "in progress", 10)
	proj, issue := stageIssue(t, "in progress")
	dp := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil)
	if !strings.Contains(dp.Prompt, tracker.YOLORule(wf, issue.Slug)[:40]) || !strings.Contains(dp.Prompt, "YOLO is on") {
		t.Fatalf("the YOLO rule is missing:\n%s", dp.Prompt)
	}
}

// A memory --at the stage's status reaches the stage briefing; one at
// another status and a pinned one don't.
func TestStageBriefingShowsTheMemoriesAtItsStatus(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	wf := freshWorkflow(t, "testing", 0)
	proj, issue := stageIssue(t, "testing")
	store := memory.ProjectStore(proj.IssueDir)
	now := time.Now()
	for _, d := range []memory.Draft{
		{Text: "run the slow tier before testing hands over", Scope: memory.ScopeProject, Kind: "howto", At: "testing"},
		{Text: "merge main right before the fast-forward", Scope: memory.ScopeProject, Kind: "howto", At: "shipping"},
	} {
		if _, err := store.Add(d, memory.AddOptions{Mode: memory.ModeNew}, now); err != nil {
			t.Fatal(err)
		}
	}
	dp := BuildDispatchPrompt(proj, issue, wf, t.TempDir(), nil)
	if !strings.Contains(dp.Prompt, "run the slow tier before testing hands over") {
		t.Fatalf("the memory at testing is missing:\n%s", dp.Prompt)
	}
	if strings.Contains(dp.Prompt, "merge main right before the fast-forward") {
		t.Fatal("a memory at shipping reached the testing stage")
	}
	if ids := StageMemoryIDs(proj, issue); len(ids) != 1 {
		t.Fatalf("stage memory ids = %v", ids)
	}
}
