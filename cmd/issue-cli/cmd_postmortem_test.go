package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/autostart"
	"github.com/michal-franc/issue-viewer/internal/postmortem"
	"github.com/michal-franc/issue-viewer/internal/retro"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const (
	pmBodySecret    = "the body says the rollout hit a flaky lock"
	pmCommentSecret = "progress: rewrote the lock twice"
	pmClilogSecret  = "issue-cli check cli/sample AC9 --na"
)

// postmortemFixture is a done-able issue whose workflow starts a
// post-mortem at done, with a comment, a clilog line and a worker retro
// the brief must point at but never copy.
func postmortemFixture(t *testing.T, start string) *tracker.Project {
	t.Helper()
	proj := makeDoneParkedFixture(t, "shipping", "", "postmortem:\n  start: "+start+"\n")
	root := filepath.Dir(proj.IssueDir)
	file := filepath.Join(proj.IssueDir, "CLI", "sample.md")
	raw, _ := os.ReadFile(file)
	os.WriteFile(file, append(raw, []byte("\n"+pmBodySecret+"\n")...), 0o644)
	if err := tracker.AddCommentBy(file, 0, pmCommentSecret, "cli", "agent-cli-sample"); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(root, ".agent-logs", "agent-cli-sample")
	os.MkdirAll(logs, 0o755)
	os.WriteFile(filepath.Join(logs, "agent-cli-sample.clilog"), []byte(pmClilogSecret+"\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "retros"), 0o755)
	os.WriteFile(filepath.Join(root, "retros", "20261031-000000-cli-sample.md"), []byte("# Workflow Retrospective\n\nIssue: cli/sample\n\nBase workflow: fine\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.WriteFile(filepath.Join(root, "docs", "workflow.md"), []byte("# Workflow\n"), 0o644)
	orig := autostart.ReadMemory
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 30, CanStart: true} }
	t.Cleanup(func() { autostart.ReadMemory = orig })
	return proj
}

func standInAgent(t *testing.T) *[]agentrun.Spec {
	t.Helper()
	var specs []agentrun.Spec
	orig := startAgent
	startAgent = func(_ *tracker.Project, spec agentrun.Spec) agentrun.Response {
		specs = append(specs, spec)
		return agentrun.Response{Status: "dispatched", Session: spec.Session, AttachCmd: "tmux attach -t " + spec.Session}
	}
	t.Cleanup(func() { startAgent = orig })
	return &specs
}

// AC1: the close step starts a fresh post-mortem agent with paths only.
func TestDoneStartsAFreshPostmortemAgentWithPathsOnly(t *testing.T) {
	proj := postmortemFixture(t, "auto")
	specs := standInAgent(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatalf("runTransition: %v", err)
	}
	if len(*specs) != 1 {
		t.Fatalf("started %d agents, want 1", len(*specs))
	}
	spec := (*specs)[0]
	if spec.Session != "postmortem-cli-sample" || !spec.Headless || spec.IssueSlug != "cli/sample" {
		t.Fatalf("spec: %+v", spec)
	}
	if spec.Worktree.Path != "" || spec.Worktree.Branch != "" {
		t.Fatalf("the post-mortem runs in the checkout, not a worktree: %+v", spec.Worktree)
	}
	root := filepath.Dir(proj.IssueDir)
	for _, want := range []string{
		filepath.Join(proj.IssueDir, "CLI", "sample.md"),
		filepath.Join(root, ".agent-logs", "agent-cli-sample"),
		filepath.Join(root, "retros", "20261031-000000-cli-sample.md"),
		"work/cli/sample", "postmortem write cli/sample", postmortem.TheTest,
	} {
		assertContains(t, spec.Prompt, want)
	}
	for _, secret := range []string{pmBodySecret, pmCommentSecret, "rewrote the lock", pmClilogSecret, "Base workflow: fine"} {
		assertNotContains(t, spec.Prompt, secret)
	}
	assertContains(t, stdout.String(), "✓ Post-mortem started: postmortem-cli-sample")
	comments, _ := tracker.LoadComments(filepath.Join(proj.IssueDir, "CLI", "sample.md"))
	if last := comments[len(comments)-1].Text; !strings.HasPrefix(last, "postmortem: started postmortem-cli-sample") {
		t.Fatalf("last comment: %q", last)
	}
}

func TestAskModeOnlyPrintsTheStart(t *testing.T) {
	proj := postmortemFixture(t, "ask")
	specs := standInAgent(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatal(err)
	}
	if len(*specs) != 0 {
		t.Fatalf("ask started an agent: %+v", *specs)
	}
	assertContains(t, stdout.String(), "issue-cli --project test postmortem start cli/sample --quote")
}

func TestPostmortemHoldsOnMemGuardAndOnAnExistingOne(t *testing.T) {
	proj := postmortemFixture(t, "auto")
	specs := standInAgent(t)
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 80, Note: "BLOCKED 80%"} }
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatal(err)
	}
	if len(*specs) != 0 {
		t.Fatal("started past mem-guard")
	}
	assertContains(t, stdout.String(), "Post-mortem not started: mem-guard says no (BLOCKED 80%)")

	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, CanStart: true} }
	root := filepath.Dir(proj.IssueDir)
	if _, err := postmortem.Save(filepath.Join(root, "retros"), postmortem.Meta{Issue: "cli/sample"}, "## Steps\nnone\n", ctx.now()); err != nil {
		t.Fatal(err)
	}
	issue, _, _ := findIssueOrErr(ctx, "cli/sample")
	wf, _ := issueWorkflow(ctx, issue)
	_, err := startPostmortem(ctx, issue, wf, postmortemStart{Auto: true})
	if err == nil || !strings.Contains(err.Error(), "has a post-mortem already") {
		t.Fatalf("second post-mortem: %v", err)
	}
	if _, err := startPostmortem(ctx, issue, wf, postmortemStart{Auto: true, Again: true}); err != nil {
		t.Fatalf("--again: %v", err)
	}
	if len(*specs) != 1 {
		t.Fatalf("--again started %d", len(*specs))
	}
}

func TestPostmortemStartByHandNeedsTheOwnersWords(t *testing.T) {
	proj := postmortemFixture(t, "ask")
	standInAgent(t)
	ctx, _, _ := newTestContext(proj, false)
	err := runPostmortem(ctx, []string{"start", "cli/sample"})
	if err == nil || !strings.Contains(err.Error(), "needs --quote") {
		t.Fatalf("no quote: %v", err)
	}
}

func TestPostmortemWriteRefusesAndSaves(t *testing.T) {
	proj := postmortemFixture(t, "ask")
	ctx, stdout, _ := newTestContext(proj, false)
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.md")
	sections := "## Steps\nx\n## Worked\nx\n## Did not work\nnone\n## Decisions\nnone\n## Known\nnone\n## Proposed actions\n\n"
	os.WriteFile(bad, []byte(sections+"- [ ] A1: Remember the lock\n  Place: memory pitfall\n  Text: The lock is flaky.\n"), 0o644)
	err := runPostmortem(ctx, []string{"write", "cli/sample", "--file", bad})
	if err == nil || !strings.Contains(err.Error(), "no Differently: line") {
		t.Fatalf("bad: %v", err)
	}
	root := filepath.Dir(proj.IssueDir)
	entries, _ := retro.Load(filepath.Join(root, "retros"))
	if len(postmortem.Of(entries, "")) != 0 {
		t.Fatal("a refused post-mortem was saved")
	}
	good := filepath.Join(dir, "good.md")
	os.WriteFile(good, []byte(sections+"- [ ] A1: Remember the lock\n  Differently: a fresh agent would retry the lock test before blaming the code\n  Place: memory pitfall\n  Text: The lock test is flaky under load.\n- [ ] A2: Workflow already says it\n  Differently: a fresh agent would read the local CI section first\n  Place: none docs/workflow.md#local-ci\n"), 0o644)
	if err := runPostmortem(ctx, []string{"write", "cli/sample", "--file", good}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "2 action(s) wait for the owner's one round: ")
	entries, _ = retro.Load(filepath.Join(root, "retros"))
	pms := postmortem.Of(entries, "cli/sample")
	if len(pms) != 1 || pms[0].Kind != "postmortem" || len(postmortem.Actions(pms[0].Body)) != 2 {
		t.Fatalf("saved: %+v", pms)
	}
	stdout.Reset()
	if err := runPostmortem(ctx, []string{"show", "cli/sample"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "A1 [memory pitfall] Remember the lock")
	assertContains(t, stdout.String(), "waits for the owner")
}
