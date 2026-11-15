package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/retro"
	"github.com/kacperwerema/dark-factory/internal/tracker"
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
	// No real pane to watch: the launch is a stand-in that keeps running.
	origBare := agentrun.BareShell
	agentrun.BareShell = func(string) bool { return false }
	t.Cleanup(func() { agentrun.BareShell = origBare })
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
		// No git repo in the fixture, so no branch: the commits by slug.
		`log --stat --grep cli/sample main`, "postmortem write cli/sample", postmortem.TheTest,
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

// stubPostmortemSession stands in for tmux around the post-mortem's
// session: live says whether it exists, bare whether its pane is only a
// shell; killing it ends both and is counted.
func stubPostmortemSession(t *testing.T, live, bare *bool) *int {
	t.Helper()
	kills := 0
	origHas, origBare, origKill := agentrun.HasSession, agentrun.BareShell, killAgentSession
	agentrun.HasSession = func(string) bool { return *live }
	agentrun.BareShell = func(string) bool { return *live && *bare }
	killAgentSession = func(_ *tracker.Project, name string) error {
		if name != "postmortem-cli-sample" {
			t.Errorf("killed %q", name)
		}
		kills++
		*live, *bare = false, false
		return nil
	}
	t.Cleanup(func() { agentrun.HasSession, agentrun.BareShell, killAgentSession = origHas, origBare, origKill })
	return &kills
}

func lastPostmortemComment(t *testing.T, proj *tracker.Project) string {
	t.Helper()
	comments, _ := tracker.LoadComments(filepath.Join(proj.IssueDir, "CLI", "sample.md"))
	for i := len(comments) - 1; i >= 0; i-- {
		if strings.HasPrefix(comments[i].Text, "postmortem:") {
			return comments[i].Text
		}
	}
	return ""
}

// AC3: an action marked overtaken isn't open any more in list and show,
// and list names the owner's page while anything waits.
func TestPostmortemListAndShowStopCountingAnOvertakenAction(t *testing.T) {
	proj := postmortemFixture(t, "ask")
	ctx, stdout, _ := newTestContext(proj, false)
	root := filepath.Dir(proj.IssueDir)
	sections := "## Steps\nx\n## Worked\nx\n## Did not work\nnone\n## Decisions\nnone\n## Known\nnone\n## Proposed actions\n\n"
	file := filepath.Join(t.TempDir(), "pm.md")
	os.WriteFile(file, []byte(sections+"- [ ] A1: Remember the lock\n  Differently: a fresh agent would retry the lock test before blaming the code\n  Place: memory pitfall\n  Text: The lock test is flaky under load.\n"), 0o644)
	if err := runPostmortem(ctx, []string{"write", "cli/sample", "--file", file}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runPostmortem(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "1 action(s), 1 waiting")
	assertContains(t, stdout.String(), "/p/"+proj.Slug+"/postmortems")

	entries, _ := retro.Load(filepath.Join(root, "retros"))
	if _, err := postmortem.Overtake(postmortem.Of(entries, "cli/sample")[0].FilePath, "A1", "8a2b70ed", "owner", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runPostmortem(ctx, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "1 action(s), 0 waiting")
	if strings.Contains(stdout.String(), "/postmortems") {
		t.Errorf("list names the page with nothing waiting:\n%s", stdout.String())
	}
	stdout.Reset()
	if err := runPostmortem(ctx, []string{"show", "cli/sample"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "overtaken: 8a2b70ed")
}

// With postmortem: agent: unset, the start runs routing's agent (claude
// with no routing:), never an empty command; agent: still wins.
func TestPostmortemRoutesTheDefaultAgent(t *testing.T) {
	proj := postmortemFixture(t, "auto")
	specs := standInAgent(t)
	ctx, _, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatal(err)
	}
	if len(*specs) != 1 {
		t.Fatalf("started %d", len(*specs))
	}
	spec := (*specs)[0]
	if spec.AgentType != "claude" || spec.Route == nil || spec.Route.Agent != "claude" {
		t.Fatalf("agent %q route %+v", spec.AgentType, spec.Route)
	}
	launch := agentrun.LaunchCommand(proj, spec.AgentType, spec.Session, "/x/dispatch-prompt.txt")
	if !strings.HasPrefix(launch, "claude -n postmortem-cli-sample ") {
		t.Fatalf("launch command: %q", launch)
	}

	issue, _, _ := findIssueOrErr(ctx, "cli/sample")
	wf, _ := issueWorkflow(ctx, issue)
	wf.Postmortem.Agent = "codex"
	if _, err := startPostmortem(ctx, issue, wf, postmortemStart{Auto: true, Again: true}); err != nil {
		t.Fatal(err)
	}
	if got := (*specs)[1].AgentType; got != "codex" {
		t.Fatalf("agent: codex started %q", got)
	}
}

// A launch whose agent is gone at once is a failure (agentrun.Start's, which
// removes the session): no "started" comment, the transition says why it
// didn't start.
func TestPostmortemLaunchThatDiesIsAFailure(t *testing.T) {
	proj := postmortemFixture(t, "auto")
	var specs []agentrun.Spec
	orig := startAgent
	startAgent = func(_ *tracker.Project, spec agentrun.Spec) agentrun.Response {
		specs = append(specs, spec)
		return agentrun.Response{Status: "error", Session: spec.Session, Steps: []agentrun.Step{
			{Name: "Agent running", Status: "error", Detail: "claude exited right after the launch; the session was removed"}}}
	}
	t.Cleanup(func() { startAgent = orig })
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("started %d", len(specs))
	}
	assertContains(t, stdout.String(), "Post-mortem not started: starting postmortem-cli-sample failed: claude exited right after the launch")
	assertNotContains(t, stdout.String(), "✓ Post-mortem started")
	if c := lastPostmortemComment(t, proj); c != "" {
		t.Fatalf("comment after a dead launch: %q", c)
	}
}

// An empty agent never reaches tmux: Start refuses it, so a caller that
// forgets to route gets an error, not an idle shell.
func TestPostmortemEmptyAgentIsAFailure(t *testing.T) {
	proj := postmortemFixture(t, "auto")
	orig := startAgent
	startAgent = func(p *tracker.Project, spec agentrun.Spec) agentrun.Response {
		spec.AgentType = ""
		return agentrun.Start(p, spec)
	}
	t.Cleanup(func() { startAgent = orig })
	ctx, _, _ := newTestContext(proj, false)
	issue, _, _ := findIssueOrErr(ctx, "cli/sample")
	wf, _ := issueWorkflow(ctx, issue)
	resp, err := startPostmortem(ctx, issue, wf, postmortemStart{Auto: true})
	if err == nil || resp.Status != "error" {
		t.Fatalf("empty agent: %v %+v", err, resp)
	}
	if c := lastPostmortemComment(t, proj); c != "" {
		t.Fatalf("comment after a refused launch: %q", c)
	}
	if agentrun.HasSession(agentrun.Target(proj, "postmortem-cli-sample")) {
		t.Fatal("a session was left")
	}
}

// postmortem start treats a session whose pane is a bare shell as dead: it
// removes it and starts; a session with its agent running still holds.
func TestPostmortemBareShellSessionIsDead(t *testing.T) {
	proj := postmortemFixture(t, "ask")
	specs := standInAgent(t)
	live, bare := true, true
	kills := stubPostmortemSession(t, &live, &bare)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runPostmortem(ctx, []string{"start", "cli/sample", "--quote", "write the post-mortem"}); err != nil {
		t.Fatalf("bare shell held the start: %v", err)
	}
	if *kills != 1 || len(*specs) != 1 {
		t.Fatalf("kills %d, starts %d", *kills, len(*specs))
	}
	assertContains(t, stdout.String(), "✓ Post-mortem started: postmortem-cli-sample")

	live, bare = true, false
	err := runPostmortem(ctx, []string{"start", "cli/sample", "--quote", "again", "--again"})
	if err == nil || !strings.Contains(err.Error(), "postmortem-cli-sample is running already") {
		t.Fatalf("running agent: %v", err)
	}
	if *kills != 1 || len(*specs) != 1 {
		t.Fatalf("a running post-mortem was touched: kills %d, starts %d", *kills, len(*specs))
	}
}

// privateTmux starts a tmux server on its own socket under /tmp (the temp
// dir is too deep for a socket) and returns the socket; the server is
// killed by that socket with TMUX removed at cleanup.
func privateTmux(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "pmx-")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s")
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", sock, "kill-server")
		kill.Env = envWithout("TMUX")
		_ = kill.Run()
		_ = os.RemoveAll(dir)
	})
	return sock
}

func envWithout(key string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, key+"=") {
			env = append(env, kv)
		}
	}
	return env
}

// AC1: postmortem write run inside its own session closes that session
// once the post-mortem is saved; a write from any other session leaves it.
func TestPostmortemWriteClosesItsOwnSession(t *testing.T) {
	proj := postmortemFixture(t, "ask")
	sock := privateTmux(t)
	tm := func(args ...string) (string, error) {
		cmd := exec.Command("tmux", append([]string{"-S", sock}, args...)...)
		cmd.Env = envWithout("TMUX")
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	for _, name := range []string{"postmortem-cli-sample", "agent-cli-sample"} {
		if _, err := tm("new-session", "-d", "-s", name, "sleep 600"); err != nil {
			t.Fatalf("new-session %s: %v", name, err)
		}
	}
	pane := func(name string) string {
		p, err := tm("display-message", "-p", "-t", "="+name+":", "#{pane_id}")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	live := func(name string) bool {
		_, err := tm("has-session", "-t", "="+name)
		return err == nil
	}
	file := filepath.Join(t.TempDir(), "pm.md")
	os.WriteFile(file, []byte("## Steps\nx\n## Worked\nx\n## Did not work\nnone\n## Decisions\nnone\n## Known\nnone\n## Proposed actions\n\nnone\n"), 0o644)
	ctx, stdout, _ := newTestContext(proj, false)
	orig := currentTmuxNames
	currentTmuxNames = tmuxNames // the real reading, on the private server
	t.Cleanup(func() { currentTmuxNames = orig })

	// From the worker's session: nothing closes.
	t.Setenv("TMUX", sock+",1,0")
	t.Setenv("TMUX_PANE", pane("agent-cli-sample"))
	if err := runPostmortem(ctx, []string{"write", "cli/sample", "--file", file}); err != nil {
		t.Fatal(err)
	}
	assertNotContains(t, stdout.String(), "closes")

	// From its own session: the session goes after the result.
	stdout.Reset()
	later := time.Now().Add(time.Minute) // a second post-mortem, its own file name
	ctx.Now = func() time.Time { return later }
	t.Setenv("TMUX_PANE", pane("postmortem-cli-sample"))
	if err := runPostmortem(ctx, []string{"write", "cli/sample", "--file", file}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "This session (postmortem-cli-sample) closes now.")
	deadline := time.Now().Add(15 * time.Second)
	for live("postmortem-cli-sample") {
		if time.Now().After(deadline) {
			t.Fatal("postmortem-cli-sample is still open after its post-mortem was written")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !live("agent-cli-sample") {
		t.Fatal("another session was closed")
	}
}

// AC2: a live session whose post-mortem is written is finished, not
// running: start --again removes it and starts.
func TestPostmortemStartAgainAfterAWrittenOneIsNotHeld(t *testing.T) {
	proj := postmortemFixture(t, "ask")
	specs := standInAgent(t)
	live, bare := false, false
	kills := stubPostmortemSession(t, &live, &bare)
	ctx, _, _ := newTestContext(proj, false)
	if err := runPostmortem(ctx, []string{"start", "cli/sample", "--quote", "write the post-mortem"}); err != nil {
		t.Fatal(err)
	}
	live = true // the agent sits at its prompt
	file := filepath.Join(t.TempDir(), "pm.md")
	os.WriteFile(file, []byte("## Steps\nx\n## Worked\nx\n## Did not work\nnone\n## Decisions\nnone\n## Known\nnone\n## Proposed actions\n\nnone\n"), 0o644)
	if err := runPostmortem(ctx, []string{"write", "cli/sample", "--file", file}); err != nil {
		t.Fatal(err)
	}
	if err := runPostmortem(ctx, []string{"start", "cli/sample", "--quote", "once more", "--again"}); err != nil {
		t.Fatalf("a finished session held the start: %v", err)
	}
	if *kills != 1 || len(*specs) != 2 {
		t.Fatalf("kills %d, starts %d", *kills, len(*specs))
	}
	// Started again and not written yet: that one is running.
	live = true
	err := runPostmortem(ctx, []string{"start", "cli/sample", "--quote", "and again", "--again"})
	if err == nil || !strings.Contains(err.Error(), "is running already") {
		t.Fatalf("a running post-mortem: %v", err)
	}
}
