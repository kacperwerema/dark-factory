package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const claudeLimitLine = "You've hit your 5-hour limit · resets 5pm (Europe/Warsaw)"

// takeoverProject is makeRoutingProject with high dispatched on the given
// words and renames recorded, not run.
func takeoverProject(t *testing.T) (*tracker.Project, *[]string) {
	t.Helper()
	proj := makeRoutingProject(t)
	if err := tracker.AddCommentBy(filepath.Join(proj.IssueDir, "high.md"), 0, tracker.DispatchComment("start high", "CEO chat", "2026-11-05"), "cli", "lead-proj"); err != nil {
		t.Fatal(err)
	}
	var renames []string
	orig := agentrun.RenameSession
	agentrun.RenameSession = func(_ *tracker.Project, from, to string) error {
		renames = append(renames, from+" → "+to)
		return nil
	}
	t.Cleanup(func() { agentrun.RenameSession = orig })
	return proj, &renames
}

func TestTakeover_StartsTheOtherAgentOnTheIssuesWords(t *testing.T) {
	proj, renames := takeoverProject(t)
	typed := stubPanes(t, map[string]string{"agent-high": claudeLimitLine + "\n"})
	spec := captureStart(t, "dispatched")
	ctx, stdout, _ := newTestContext(proj, false)

	if err := runDispatch(ctx, []string{"high", "--takeover"}); err != nil {
		t.Fatal(err)
	}
	if spec.AgentType != "codex" || spec.Session != "agent-high" {
		t.Fatalf("started %q as %q", spec.AgentType, spec.Session)
	}
	if !strings.HasPrefix(spec.Prompt, "## You are taking this branch over") || !strings.Contains(spec.Prompt, "HANDOFF.md") {
		t.Fatalf("prompt: %.200s", spec.Prompt)
	}
	if strings.Join(*renames, ",") != "agent-high → limited-high" {
		t.Fatalf("renames: %v", *renames)
	}
	if len(*typed) != 0 {
		t.Fatalf("typed into %v", *typed)
	}
	assertContains(t, stdout.String(), `dispatch: "start high" (takeover from claude at its usage limit,`)
	last, _ := autostart.LastStart(autostart.SharedDir())
	if last == nil || last.Slug != "high" {
		t.Fatalf("the machine's budget has no start: %+v", last)
	}
}

// A takeover's second limit is the owner's: the tick says so and the command
// refuses, whether the replacement or the first session is showing it.
func TestTakeover_ASecondLimitStartsNoThirdWorker(t *testing.T) {
	proj, renames := takeoverProject(t)
	stubPanes(t, map[string]string{"agent-high": claudeLimitLine + "\n"})
	spec := captureStart(t, "dispatched")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDispatch(ctx, []string{"high", "--takeover"}); err != nil {
		t.Fatal(err)
	}
	*spec, *renames = agentrun.Spec{}, nil

	stubPanes(t, map[string]string{"agent-high": "■ You’ve hit your usage limit. Try again at 6:30 PM.\n"})
	ctx, stdout, _ := newTestContext(proj, false)
	printTakeovers(ctx)
	assertContains(t, stdout.String(), "taken over once already: no third worker")
	if strings.Contains(stdout.String(), "--takeover") {
		t.Fatalf("the tick offers a third worker: %s", stdout)
	}
	err := runDispatch(ctx, []string{"high", "--takeover"})
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "taken over once already") {
		t.Fatalf("second takeover: %v", err)
	}
	if spec.Session != "" || len(*renames) != 0 {
		t.Fatalf("second takeover started %+v, renamed %v", spec, *renames)
	}
}

func TestTakeover_APaneThatIsNotStoppedStartsNothing(t *testing.T) {
	for name, pane := range map[string]string{
		"no limit":           "⏺ Working on it\n",
		"mid-turn":           claudeLimitLine + "\n✶ Thinking… (esc to interrupt)\n",
		"goes on by itself":  "Usage limit reached · continuing automatically\n",
		"limit scrolled out": claudeLimitLine + "\n" + strings.Repeat("line\n", 20),
	} {
		t.Run(name, func(t *testing.T) {
			proj, renames := takeoverProject(t)
			stubPanes(t, map[string]string{"agent-high": pane})
			spec := captureStart(t, "dispatched")
			ctx, stdout, _ := newTestContext(proj, false)
			printTakeovers(ctx)
			if stdout.Len() != 0 {
				t.Fatalf("tick said: %s", stdout)
			}
			err := runDispatch(ctx, []string{"high", "--takeover"})
			if exitOf(err) != 1 || !strings.Contains(err.Error(), "shows no usage limit") {
				t.Fatalf("takeover: %v", err)
			}
			if spec.Session != "" || len(*renames) != 0 {
				t.Fatalf("started %+v, renamed %v", spec, *renames)
			}
		})
	}
}

func TestTakeover_WorkersAreRefused(t *testing.T) {
	proj, renames := takeoverProject(t)
	stubPanes(t, map[string]string{"agent-high": claudeLimitLine + "\n"})
	spec := captureStart(t, "dispatched")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "low")
	ctx, _, _ := newTestContext(proj, false)
	err := runDispatch(ctx, []string{"high", "--takeover"})
	if err == nil || !strings.Contains(err.Error(), "workers can't dispatch") {
		t.Fatalf("a worker's takeover: %v", err)
	}
	if spec.Session != "" || len(*renames) != 0 {
		t.Fatalf("started %+v, renamed %v", spec, *renames)
	}
}

func TestTakeover_Holds(t *testing.T) {
	limit := map[string]string{"agent-high": claudeLimitLine + "\n"}
	t.Run("the other agent rests", func(t *testing.T) {
		proj, _ := takeoverProject(t)
		// Its own limit shows too, so both subscriptions rest.
		stubPanes(t, map[string]string{"agent-high": claudeLimitLine + "\n", "agent-low": "■ You’ve hit your usage limit. Try again at 11:59 PM.\n"})
		ctx, _, _ := newTestContext(proj, false)
		err := runDispatch(ctx, []string{"high", "--takeover"})
		if exitOf(err) != 1 || !strings.Contains(err.Error(), "no other agent to take it over") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("mem-guard says no", func(t *testing.T) {
		proj, renames := takeoverProject(t)
		stubPanes(t, limit)
		autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 80, Note: "BLOCKED"} }
		ctx, _, _ := newTestContext(proj, false)
		err := runDispatch(ctx, []string{"high", "--takeover"})
		if exitOf(err) != 1 || !strings.Contains(err.Error(), "mem-guard says no") || len(*renames) != 0 {
			t.Fatalf("got %v, renamed %v", err, *renames)
		}
	})
	t.Run("a start a minute ago", func(t *testing.T) {
		proj, renames := takeoverProject(t)
		stubPanes(t, limit)
		ctx, _, _ := newTestContext(proj, false)
		if err := autostart.WithBudget(autostart.SharedDir(), func(*autostart.Start) (*autostart.Start, error) {
			return &autostart.Start{Project: "other", Slug: "x", At: ctx.now().Add(-time.Minute)}, nil
		}); err != nil {
			t.Fatal(err)
		}
		err := runDispatch(ctx, []string{"high", "--takeover"})
		if exitOf(err) != 1 || !strings.Contains(err.Error(), "gap 5m") || len(*renames) != 0 {
			t.Fatalf("got %v, renamed %v", err, *renames)
		}
	})
	t.Run("no words", func(t *testing.T) {
		proj, _ := takeoverProject(t)
		stubPanes(t, map[string]string{"agent-low": claudeLimitLine + "\n"})
		ctx, _, _ := newTestContext(proj, false)
		err := runDispatch(ctx, []string{"low", "--takeover"})
		if exitOf(err) != 1 || !strings.Contains(err.Error(), "no dispatch: words") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestTakeover_AFailedLaunchRenamesTheSessionBack(t *testing.T) {
	proj, renames := takeoverProject(t)
	stubPanes(t, map[string]string{"agent-high": claudeLimitLine + "\n"})
	captureStart(t, "error")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDispatch(ctx, []string{"high", "--takeover"}); err == nil {
		t.Fatal("a failed launch reported success")
	}
	if strings.Join(*renames, ",") != "agent-high → limited-high,limited-high → agent-high" {
		t.Fatalf("renames: %v", *renames)
	}
	if tracker.TakenOver(issueComments(t, proj, "high")) {
		t.Fatal("a failed takeover spent the issue's one")
	}
}

func TestTakeover_FlagsThatDontApply(t *testing.T) {
	proj, _ := takeoverProject(t)
	for _, extra := range [][]string{{"--quote", "x"}, {"--agent", "codex"}, {"--force"}, {"--reprompt"}, {"--autostart"}} {
		ctx, _, _ := newTestContext(proj, false)
		if err := runDispatch(ctx, append([]string{"high", "--takeover"}, extra...)); exitOf(err) != 2 {
			t.Errorf("%v: %v", extra, err)
		}
	}
}

// The tick names the session left running once its issue closed.
func TestTakeover_TickNamesALeftSessionOfAClosedIssue(t *testing.T) {
	proj, _ := takeoverProject(t)
	high := filepath.Join(proj.IssueDir, "high.md")
	raw, _ := os.ReadFile(high)
	mustWrite(t, high, strings.Replace(string(raw), `status: "backlog"`, `status: "done"`, 1))
	stubPanes(t, map[string]string{"limited-high": "whatever\n"})
	ctx, stdout, _ := newTestContext(proj, false)
	printTakeovers(ctx)
	assertContains(t, stdout.String(), "limited-high was left running at its limit and high is closed: close it: ")
	assertContains(t, stdout.String(), "tmux kill-session -t '=limited-high:'")
}

// standIn writes an executable agent into dir.
func standIn(t *testing.T, dir, name, body string) {
	t.Helper()
	standin.Write(t, filepath.Join(dir, name), body)
}

// The whole thing on a private tmux server with stand-in CLIs: claude stops
// at its limit, the takeover starts codex in the same worktree with
// HANDOFF.md in its prompt, the claude pane stays alive and untouched, and
// codex's own limit starts nothing more.
func TestTakeover_StandInsOnARealTmuxServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	realHas, realList := agentrun.HasSession, agentrun.ListSessions
	proj := makeRoutingProject(t)
	agentrun.HasSession, agentrun.ListSessions = realHas, realList

	sockDir, err := os.MkdirTemp("/tmp", "tko-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sock := filepath.Join(sockDir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
		kill := exec.Command("tmux", "-S", sock, "kill-server")
		kill.Env = envWithout("TMUX")
		_ = kill.Run()
		_ = os.RemoveAll(sockDir)
	})
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", sockDir)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", t.TempDir())
	bin, rec := t.TempDir(), t.TempDir()
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("REC", rec)
	origTimeout, origSettle := agentrun.PromptDeliveryTimeout, agentrun.LaunchSettle
	agentrun.PromptDeliveryTimeout, agentrun.LaunchSettle = 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { agentrun.PromptDeliveryTimeout, agentrun.LaunchSettle = origTimeout, origSettle })

	standIn(t, bin, "claude", `pwd > "$REC/claude.pwd"
echo "`+claudeLimitLine+`"
exec sleep 600
`)
	standIn(t, bin, "codex", `pwd > "$REC/codex.pwd"
printf '%s\n' "$@" > "$REC/codex.args.tmp"
mv "$REC/codex.args.tmp" "$REC/codex.args"
echo "■ You’ve hit your usage limit. Try again at 11:59 PM."
exec sleep 600
`)

	root, err := filepath.EvalSymlinks(proj.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	proj.WorkDir, proj.IssueDir, proj.WorkflowFile = root, filepath.Join(root, "issues"), filepath.Join(root, "workflow.yaml")
	proj.Terminal = "none"
	raw, _ := os.ReadFile(proj.WorkflowFile)
	mustWrite(t, proj.WorkflowFile, string(raw)+"worktree: true\n")
	mustWrite(t, filepath.Join(root, "README"), "hello\n")
	gitIn(t, root, "init", "-q", "-b", "main")
	gitIn(t, root, "add", "README")
	gitIn(t, root, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "maintenance.auto=false", "-c", "gc.auto=0", "commit", "-q", "-m", "first")
	origStart := startAgent
	startAgent = agentrun.Start // this one test runs the real launcher, on stand-ins
	t.Cleanup(func() { startAgent = origStart })

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDispatch(ctx, []string{"low", "--agent", "claude", "--quote", "start low"}); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stdout)
	}
	waitForFile(t, filepath.Join(rec, "claude.pwd"))
	limitSeen := func(session string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if pane, err := agentrun.CapturePane("=" + session + ":"); err == nil && strings.Contains(pane, "hit your") {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s never showed its limit", session)
	}
	limitSeen("agent-low")

	ctx, stdout, _ = newTestContext(proj, false)
	printTakeovers(ctx)
	assertContains(t, stdout.String(), "agent-low (claude) hit its usage limit: codex takes it over: issue-cli --project proj dispatch low --takeover")
	if err := runDispatch(ctx, []string{"low", "--takeover"}); err != nil {
		t.Fatalf("takeover: %v\n%s", err, stdout)
	}
	waitForFile(t, filepath.Join(rec, "codex.args"))

	args, _ := os.ReadFile(filepath.Join(rec, "codex.args"))
	claudeDir, _ := os.ReadFile(filepath.Join(rec, "claude.pwd"))
	codexDir, _ := os.ReadFile(filepath.Join(rec, "codex.pwd"))
	if len(claudeDir) == 0 || string(claudeDir) != string(codexDir) {
		t.Fatalf("claude in %q, codex in %q", claudeDir, codexDir)
	}
	dir := strings.TrimSpace(string(codexDir))
	assertContains(t, string(args), "Read "+dir+"/HANDOFF.md first")
	assertContains(t, string(args), "work/low")
	if !agentrun.HasSession("=limited-low:") {
		t.Fatal("the limited session is gone")
	}
	if pane, _ := agentrun.CapturePane("=limited-low:"); !strings.Contains(pane, "hit your 5-hour limit") {
		t.Fatalf("the limited pane changed: %q", pane)
	}
	if got := agentrun.SessionAgent(proj, "agent-low"); got != "codex" {
		t.Fatalf("agent-low runs %q", got)
	}

	limitSeen("agent-low")
	ctx, stdout, _ = newTestContext(proj, false)
	err = runDispatch(ctx, []string{"low", "--takeover"})
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "taken over once already") {
		t.Fatalf("a third worker: %v", err)
	}
	var workers int
	for _, s := range agentrun.ListSessions() {
		if strings.HasPrefix(s.Name, "agent-") || strings.HasPrefix(s.Name, "limited-") {
			workers++
		}
	}
	if workers != 2 {
		t.Fatalf("%d sessions, want the two", workers)
	}

	// HANDOFF.md can't be committed by accident from the worktree.
	mustWrite(t, filepath.Join(dir, "HANDOFF.md"), "done: x\n")
	status := exec.Command("git", "-C", dir, "status", "--porcelain")
	if out, err := status.CombinedOutput(); err != nil || strings.Contains(string(out), "HANDOFF") {
		t.Fatalf("git status: %v: %s", err, out)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}
