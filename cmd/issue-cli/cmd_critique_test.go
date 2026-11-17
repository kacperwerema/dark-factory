package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/critique"
	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const critiqueWorkflow = `statuses:
  - name: "idea"
  - name: "in design"
  - name: "backlog"
critique:
  command: "cat \"$CRITIC_OUT\""
  timeout: 30s
transitions:
  - from: "idea"
    to: "in design"
    actions: []
  - from: "in design"
    to: "backlog"
    actions:
      - type: "validate"
        rule: "critique"
`

// critiqueProject is a sandbox project with one issue at in design behind
// a critique gate, run as its worker; the critic is a stand-in that prints
// the file $CRITIC_OUT names. mem-guard and the load reading are stubbed.
func critiqueProject(t *testing.T) (proj *tracker.Project, slug, issuePath, criticOut string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "issues", "Factory"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "workflow.yaml"), critiqueWorkflow)
	issuePath = filepath.Join(root, "issues", "Factory", "critic.md")
	mustWrite(t, issuePath, "---\ntitle: \"Critic\"\nstatus: \"in design\"\nsystem: \"Factory\"\nassignee: \"agent-x\"\ncreated: \"2026-10-31\"\n---\n\nSUMMARY-SENTINEL the worker's own framing.\n")
	slug = "factory/critic"
	criticOut = filepath.Join(t.TempDir(), "critic.txt")
	t.Setenv("CRITIC_OUT", criticOut)
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", slug)
	oldMem, oldLoad := autostart.ReadMemory, autostart.ReadLoad
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 40, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 1, Avg5: 1, Cores: 8} }
	// No real agent ever starts from a test: every agent reads as missing,
	// so only the stand-in command can run.
	oldInstalled := routing.Installed
	routing.Installed = func(string) bool { return false }
	t.Cleanup(func() { autostart.ReadMemory, autostart.ReadLoad, routing.Installed = oldMem, oldLoad, oldInstalled })
	proj = &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
	return proj, slug, issuePath, criticOut
}

// A blocks finding refuses the transition naming the finding; a new run
// without it clears the gate. A worker can't override.
func TestCritiqueBlocksHoldTheGateUntilANewRun(t *testing.T) {
	proj, slug, issuePath, criticOut := critiqueProject(t)

	// No run yet: show and the gate name the command.
	ctx, out, _ := newTestContext(proj, false)
	if err := runShow(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Critique: none from in design yet; the next gate wants one — issue-cli critique "+slug)
	ctx, _, _ = newTestContext(proj, false)
	err := runTransition(ctx, []string{slug, "--to", "backlog"})
	if err == nil || !strings.Contains(err.Error(), "issue-cli critique "+slug) {
		t.Fatalf("without a run: %v", err)
	}

	mustWrite(t, criticOut, "blocks issues/Factory/critic.md:9 — no case for an empty list\nshould issues/Factory/critic.md:9 — cite the decision\nblocks something vague\nCoverage: read 1 of 1 files\nNot read: none\n")
	ctx, out, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatalf("critique: %v", err)
	}
	assertContains(t, out.String(), "critique: in design → backlog, holds")
	assertContains(t, out.String(), "blocks issues/Factory/critic.md:9 — no case for an empty list")
	assertContains(t, out.String(), "blocks (malformed: no path:line) blocks something vague")
	assertContains(t, out.String(), "The gate in design → backlog holds")
	assertContains(t, readFile(t, issuePath), "critique: in design → backlog, holds")

	ctx, _, _ = newTestContext(proj, false)
	err = runTransition(ctx, []string{slug, "--to", "backlog"})
	if err == nil || !strings.Contains(err.Error(), "blocks issues/Factory/critic.md:9 — no case for an empty list") || !strings.Contains(err.Error(), "something vague") {
		t.Fatalf("with blocks: %v", err)
	}

	ctx, _, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--override", "--quote", "ship it"}); err == nil || !strings.Contains(err.Error(), "the owner's") {
		t.Fatalf("a worker overrode: %v", err)
	}

	// Fixed: a new run without blocks clears the gate.
	mustWrite(t, criticOut, "should issues/Factory/critic.md:9 — cite the decision\nnote issues/Factory/critic.md:1 — fine\nCoverage: read 1 of 1 files\n")
	ctx, out, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ No blocks finding")
	assertContains(t, out.String(), "1 should finding above")
	ctx, _, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{slug, "--to", "backlog"}); err != nil {
		t.Fatalf("after a clear run: %v", err)
	}
	ctx, out, _ = newTestContext(proj, false)
	if err := runShow(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Critique: in design → backlog clear (0 blocks, 1 should, 1 note)")
	runs, _ := critique.Load(proj.IssueDir, slug)
	if len(runs) != 2 || runs[1].Verdict() != "clear" {
		t.Fatalf("runs = %+v", runs)
	}
}

// The owner's shell overrides a holding run; the gate then passes.
func TestCritiqueOwnerOverride(t *testing.T) {
	proj, slug, _, criticOut := critiqueProject(t)
	mustWrite(t, criticOut, "blocks a.go:1 — wrong\nCoverage: all\n")
	ctx, _, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("TMUX", "")
	ctx, _, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--override"}); err == nil {
		t.Fatal("override without --quote was recorded")
	}
	ctx, out, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--override", "--quote", "known, ship it"}); err != nil {
		t.Fatalf("owner override: %v", err)
	}
	assertContains(t, out.String(), "overridden")
	ctx, _, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{slug, "--to", "backlog"}); err != nil {
		t.Fatalf("after override: %v", err)
	}
}

// A critic that fails, or prints no Coverage line, holds the gate.
func TestCritiqueFailedRunHolds(t *testing.T) {
	proj, slug, _, criticOut := critiqueProject(t)
	mustWrite(t, criticOut, "looks fine to me\n")
	ctx, out, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "no Coverage: line")
	t.Setenv("CRITIC_OUT", filepath.Join(t.TempDir(), "missing"))
	ctx, out, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "failed")
	ctx, _, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{slug, "--to", "backlog"}); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("failed run let the gate pass: %v", err)
	}
}

// mem-guard saying no starts nothing and records nothing.
func TestCritiqueRefusedOnMemory(t *testing.T) {
	proj, slug, _, criticOut := critiqueProject(t)
	mustWrite(t, criticOut, "Coverage: all\n")
	autostart.ReadMemory = func() autostart.Memory {
		return autostart.Memory{Found: true, Used: 80, Note: "BLOCKED 80%"}
	}
	ctx, _, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err == nil || !strings.Contains(err.Error(), "mem-guard") {
		t.Fatalf("err = %v", err)
	}
	if runs, _ := critique.Load(proj.IssueDir, slug); len(runs) != 0 {
		t.Fatalf("a run was recorded: %+v", runs)
	}
}

// The built brief carries paths only: the issue file, the diff file, the
// changed files, the settled rules; never the body's or a comment's text.
func TestCritiqueBriefCarriesPathsOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	proj, slug, issuePath, _ := critiqueProject(t)
	root := proj.WorkDir
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(root, "init", "-q", "-b", "main")
	for _, d := range []string{"docs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(root, "docs", "departments.md"), "## Settled rules\n")
	mustWrite(t, filepath.Join(root, ".gitignore"), "issues/\n.worktrees/\n")
	git(root, "add", ".")
	git(root, "commit", "-q", "-m", "init")
	wt := filepath.Join(root, ".worktrees", slug)
	git(root, "worktree", "add", "-q", "-b", "work/"+slug, wt)
	if err := os.MkdirAll(filepath.Join(wt, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(wt, "internal", "x.go"), "package x\n")
	mustWrite(t, filepath.Join(wt, "docs", "guide.md"), "# Guide\n")
	git(wt, "add", ".")
	git(wt, "commit", "-q", "-m", "work")

	ctx, _, _ := newTestContext(proj, false)
	if err := runComment(ctx, []string{slug, "--text", "progress: COMMENT-SENTINEL I believe it is done"}); err != nil {
		t.Fatal(err)
	}
	ctx, out, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--brief"}); err != nil {
		t.Fatal(err)
	}
	brief := out.String()
	for _, want := range []string{"Assume they are wrong", issuePath, ".git-critique/branch.diff", "internal/x.go", "docs/guide.md  (doc changed by the branch)", "docs/departments.md  (settled rules)", "(relative to " + wt + ")"} {
		assertContains(t, brief, want)
	}
	for _, never := range []string{"SUMMARY-SENTINEL", "COMMENT-SENTINEL", "I believe"} {
		if strings.Contains(brief, never) {
			t.Errorf("the brief carries %q:\n%s", never, brief)
		}
	}
	// --brief starts nothing and writes no diff.
	if _, err := os.Stat(filepath.Join(wt, ".git-critique")); err == nil {
		t.Error("--brief wrote the diff file")
	}
}

// critiqueStandIns replaces the critic's CLIs with stand-in scripts (no
// real agent ever starts) and reports both as installed. scripts maps an
// agent to its script body; each run appends the agent to calls.
func critiqueStandIns(t *testing.T, scripts map[string]string) (calls string) {
	t.Helper()
	bin := t.TempDir()
	calls = filepath.Join(bin, "calls")
	paths := map[string]string{}
	for agent, body := range scripts {
		p := filepath.Join(bin, agent+"-standin")
		standin.Write(t, p, "cat >/dev/null\necho "+agent+" >> \"${0%/*}/calls\"\n"+body)
		paths[agent] = p
	}
	oldArgv, oldInstalled := critiqueArgv, routing.Installed
	critiqueArgv = func(agent string) ([]string, error) {
		if p, ok := paths[agent]; ok {
			return []string{p}, nil
		}
		return nil, os.ErrNotExist
	}
	routing.Installed = func(a string) bool { _, ok := paths[a]; return ok }
	t.Cleanup(func() { critiqueArgv, routing.Installed = oldArgv, oldInstalled })
	return calls
}

// codexLimitStandIn prints codex's real usage-limit message (from the
// fixture's first run) and exits 1. The reset is a date five days after
// reset's base, so the rest is still ahead whenever the test runs.
func codexLimitStandIn(reset time.Time) string {
	return `echo "ERROR: You’ve hit your usage limit. Visit https://chatgpt.com/settings/usage to purchase more credits or try again at ` + reset.Format("Jan 2") + `th, ` + reset.Format("2006 3:04 PM") + `." >&2
exit 1
`
}

// A critic that hits its usage limit marks that subscription resting and
// runs once more on the other CLI; the run recorded is the fallback's.
func TestCritiqueUsageLimitFallsBackOnce(t *testing.T) {
	proj, slug, issuePath, _ := critiqueProject(t)
	mustWrite(t, filepath.Join(proj.WorkDir, "workflow.yaml"), strings.Replace(critiqueWorkflow, "  command: \"cat \\\"$CRITIC_OUT\\\"\"\n", "", 1))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	reset := time.Now().AddDate(0, 0, 5)
	calls := critiqueStandIns(t, map[string]string{
		"codex":  codexLimitStandIn(reset),
		"claude": "printf 'should issues/Factory/critic.md:9,12 — cite the decision\\nCoverage: read 1 of 1\\nNot read: none\\n'\n",
	})
	ctx, out, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatalf("critique: %v", err)
	}
	if got := strings.Fields(readFile(t, calls)); strings.Join(got, ",") != "codex,claude" {
		t.Fatalf("critics run = %v, want codex then claude", got)
	}
	assertContains(t, out.String(), "codex hit its usage limit (resting until")
	assertContains(t, out.String(), "✓ No blocks finding")
	st, _ := routing.Load(routing.Dir())
	if r, resting := st.Resting("codex", ctx.Now()); !resting || r.Until.Year() != reset.Year() || r.Until.Month() != reset.Month() || r.Until.Day() != reset.Day() {
		t.Fatalf("codex rest = %+v (%v)", r, resting)
	}
	runs, _ := critique.Load(proj.IssueDir, slug)
	if len(runs) != 1 || runs[0].Agent != "claude" || runs[0].Failed || !strings.Contains(runs[0].Fallback, "ran on claude") {
		t.Fatalf("runs = %+v", runs)
	}
	if w := runs[0].Report.Findings[0].Where(); w != "issues/Factory/critic.md:9,12" {
		t.Errorf("where = %s", w)
	}
	assertContains(t, readFile(t, issuePath), "codex hit its usage limit")

	// Both at their limits: one fallback only, then the run is failed.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	calls = critiqueStandIns(t, map[string]string{
		"codex":  codexLimitStandIn(reset),
		"claude": fmt.Sprintf("echo \"Claude AI usage limit reached|%d\"\nexit 1\n", reset.Unix()),
	})
	ctx, out, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(readFile(t, calls)); strings.Join(got, ",") != "codex,claude" {
		t.Fatalf("critics run = %v, want one fallback", got)
	}
	runs, _ = critique.Load(proj.IssueDir, slug)
	if last := runs[len(runs)-1]; !last.Failed || last.Agent != "claude" {
		t.Fatalf("last = %+v", last)
	}
	assertContains(t, out.String(), "claude hit its usage limit too")
	assertContains(t, out.String(), "The gate in design → backlog holds")
	st, _ = routing.Load(routing.Dir())
	if _, resting := st.Resting("claude", ctx.Now()); !resting {
		t.Error("claude's limit was not marked")
	}

	// An agent chosen by --agent is marked but not swapped.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	calls = critiqueStandIns(t, map[string]string{"codex": codexLimitStandIn(time.Now().AddDate(0, 0, 5)), "claude": "echo 'Coverage: all'\n"})
	ctx, out, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--agent", "codex"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(readFile(t, calls)); strings.Join(got, ",") != "codex" {
		t.Fatalf("critics run = %v, want codex only", got)
	}
	assertContains(t, out.String(), "codex was chosen by --agent, so no other agent is tried")

	// A finished run that quotes a limit message (the work it read has
	// one) is not a limit: nothing rests, nothing reruns.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	calls = critiqueStandIns(t, map[string]string{
		"codex":  "echo 'blocks a.md:3 — quotes: You’ve hit your usage limit. Try again at Nov 5th, 2026 8:47 AM.'\necho 'ERROR: You’ve hit your usage limit. Visit https://chatgpt.com/settings/usage or try again at Nov 5th, 2026 8:47 AM.'\n",
		"claude": "echo 'Coverage: all'\n",
	})
	ctx, _, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(readFile(t, calls)); strings.Join(got, ",") != "codex" {
		t.Fatalf("critics run = %v, want codex only", got)
	}
	st, _ = routing.Load(routing.Dir())
	if _, resting := st.Resting("codex", ctx.Now()); resting {
		t.Error("a quoted limit in a finished run marked codex resting")
	}
}

// critiqueByAgents is critiqueProject with no critique: command: and both
// agents as stand-ins that report clear, in a fresh subscriptions folder.
func critiqueByAgents(t *testing.T) (proj *tracker.Project, slug, issuePath, calls string) {
	t.Helper()
	proj, slug, issuePath, _ = critiqueProject(t)
	mustWrite(t, filepath.Join(proj.WorkDir, "workflow.yaml"), strings.Replace(critiqueWorkflow, "  command: \"cat \\\"$CRITIC_OUT\\\"\"\n", "", 1))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	clear := "printf 'Coverage: read 1 of 1\\nNot read: none\\n'\n"
	return proj, slug, issuePath, critiqueStandIns(t, map[string]string{"codex": clear, "claude": clear})
}

func recordWorker(t *testing.T, issuePath, session, agent string) {
	t.Helper()
	if err := tracker.RecordDispatch(issuePath, tracker.Dispatch{At: time.Now(), Session: session, Agent: agent}); err != nil {
		t.Fatal(err)
	}
}

// The critic is the other agent than the one on the dispatch record, in
// both directions, whatever routing would pick now; a resting or missing
// other falls back to the worker's agent and the run says why.
func TestCritiqueRunsOnTheOtherAgentThanTheWorker(t *testing.T) {
	run := func(t *testing.T, proj *tracker.Project, slug string, args ...string) (*critique.Run, string) {
		t.Helper()
		ctx, out, _ := newTestContext(proj, false)
		if err := runCritique(ctx, append([]string{slug}, args...)); err != nil {
			t.Fatalf("critique: %v", err)
		}
		runs, _ := critique.Load(proj.IssueDir, slug)
		return runs[len(runs)-1], out.String()
	}

	t.Run("claude wrote it, codex reads it", func(t *testing.T) {
		proj, slug, issuePath, calls := critiqueByAgents(t)
		recordWorker(t, issuePath, tracker.AgentSessionName(slug), "claude")
		r, out := run(t, proj, slug)
		if strings.TrimSpace(readFile(t, calls)) != "codex" || r.Agent != "codex" || r.Worker != "claude" || r.SameAgent || r.Why != "not the worker's claude" {
			t.Fatalf("calls %q, run %+v", readFile(t, calls), r)
		}
		assertContains(t, out, "Critic: codex (not the worker's claude)")
	})

	// Routing's default here is claude: only the record says codex wrote it.
	t.Run("codex wrote it, claude reads it", func(t *testing.T) {
		proj, slug, issuePath, calls := critiqueByAgents(t)
		recordWorker(t, issuePath, tracker.AgentSessionName(slug), "codex")
		r, _ := run(t, proj, slug)
		if strings.TrimSpace(readFile(t, calls)) != "claude" || r.Agent != "claude" || r.Worker != "codex" || r.SameAgent {
			t.Fatalf("calls %q, run %+v", readFile(t, calls), r)
		}
	})

	t.Run("the other rests: the same agent, with the reason", func(t *testing.T) {
		proj, slug, issuePath, calls := critiqueByAgents(t)
		recordWorker(t, issuePath, tracker.AgentSessionName(slug), "claude")
		if _, err := routing.Mark(routing.Dir(), routing.Limit{Agent: "codex", Until: time.Now().AddDate(0, 0, 2), KnownReset: true}, proj.Slug, "agent-x", time.Now()); err != nil {
			t.Fatal(err)
		}
		r, _ := run(t, proj, slug)
		if strings.TrimSpace(readFile(t, calls)) != "claude" || r.Agent != "claude" || r.Worker != "claude" || !r.SameAgent {
			t.Fatalf("calls %q, run %+v", readFile(t, calls), r)
		}
		assertContains(t, r.Why, "codex resting until")
		assertContains(t, r.Why, "on the worker's claude")
		assertContains(t, critique.CommentText(r), "same agent as the writer: codex resting until")
	})

	t.Run("the other not installed: the same agent, with the reason", func(t *testing.T) {
		proj, slug, issuePath, calls := critiqueByAgents(t)
		recordWorker(t, issuePath, tracker.AgentSessionName(slug), "claude")
		routing.Installed = func(a string) bool { return a == "claude" }
		r, _ := run(t, proj, slug)
		if strings.TrimSpace(readFile(t, calls)) != "claude" || !r.SameAgent || !strings.Contains(r.Why, "codex not installed") {
			t.Fatalf("calls %q, run %+v", readFile(t, calls), r)
		}
	})

	t.Run("no dispatch record: routing's pick, said to be a guess", func(t *testing.T) {
		proj, slug, _, calls := critiqueByAgents(t)
		r, _ := run(t, proj, slug)
		if strings.TrimSpace(readFile(t, calls)) != "codex" || r.Worker != "" || r.Why != "not routing's pick claude, no dispatch record" {
			t.Fatalf("calls %q, run %+v", readFile(t, calls), r)
		}
	})

	t.Run("a launch in another session is not the worker", func(t *testing.T) {
		proj, slug, issuePath, calls := critiqueByAgents(t)
		recordWorker(t, issuePath, tracker.AgentSessionName(slug), "codex")
		recordWorker(t, issuePath, "postmortem-critic", "claude")
		r, _ := run(t, proj, slug)
		if strings.TrimSpace(readFile(t, calls)) != "claude" || r.Worker != "codex" {
			t.Fatalf("calls %q, run %+v", readFile(t, calls), r)
		}
	})

	t.Run("--agent wins, and the run still names the worker", func(t *testing.T) {
		proj, slug, issuePath, calls := critiqueByAgents(t)
		recordWorker(t, issuePath, tracker.AgentSessionName(slug), "claude")
		r, _ := run(t, proj, slug, "--agent", "claude")
		if strings.TrimSpace(readFile(t, calls)) != "claude" || r.Worker != "claude" || !r.SameAgent || r.Why != "--agent" {
			t.Fatalf("calls %q, run %+v", readFile(t, calls), r)
		}
	})

	t.Run("critique: agent: wins", func(t *testing.T) {
		proj, slug, issuePath, calls := critiqueByAgents(t)
		recordWorker(t, issuePath, tracker.AgentSessionName(slug), "claude")
		wfPath := filepath.Join(proj.WorkDir, "workflow.yaml")
		mustWrite(t, wfPath, strings.Replace(readFile(t, wfPath), "critique:\n", "critique:\n  agent: claude\n", 1))
		r, _ := run(t, proj, slug)
		if strings.TrimSpace(readFile(t, calls)) != "claude" || r.Why != "critique: agent:" {
			t.Fatalf("calls %q, run %+v", readFile(t, calls), r)
		}
	})

	// The other agent meets its limit during the run: the one fallback lands
	// on the worker's agent, and the run says it is the same one.
	t.Run("a limit met during the run lands on the worker's agent", func(t *testing.T) {
		proj, slug, issuePath, _ := critiqueByAgents(t)
		calls := critiqueStandIns(t, map[string]string{
			"codex":  codexLimitStandIn(time.Now().AddDate(0, 0, 5)),
			"claude": "printf 'Coverage: all\\n'\n",
		})
		recordWorker(t, issuePath, tracker.AgentSessionName(slug), "claude")
		r, _ := run(t, proj, slug)
		if got := strings.Fields(readFile(t, calls)); strings.Join(got, ",") != "codex,claude" {
			t.Fatalf("critics run = %v", got)
		}
		if r.Agent != "claude" || !r.SameAgent || r.Why != "codex at its usage limit" {
			t.Fatalf("run %+v", r)
		}
	})

	// A command may run anything: the run names no CLI.
	t.Run("critique: command: names no agent", func(t *testing.T) {
		proj, slug, issuePath, criticOut := critiqueProject(t)
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		mustWrite(t, criticOut, "Coverage: all\n")
		recordWorker(t, issuePath, tracker.AgentSessionName(slug), "claude")
		r, _ := run(t, proj, slug)
		if r.Agent != "" || r.Worker != "claude" || r.SameAgent || r.Why != "critique: command:" {
			t.Fatalf("run %+v", r)
		}
	})
}

// Each held run is one red round of the critique check, even at one
// branch head, and the same finding each time makes each one count; the
// fourth posts the owner's question, and then neither the
// critic nor the transition runs again until the owner resolves it.
func TestHeldCritiqueRunsAreRedRoundsAndTheFourthAsksTheOwner(t *testing.T) {
	proj, slug, issuePath, criticOut := critiqueProject(t)
	mustWrite(t, criticOut, "blocks issues/Factory/critic.md:9,12 — no case for an empty list\nCoverage: read 1 of 1\nNot read: none\n")
	// A failed run is not a round.
	t.Setenv("CRITIC_OUT", filepath.Join(t.TempDir(), "missing"))
	ctx, _, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRITIC_OUT", criticOut)
	for i := 1; i <= 3; i++ {
		ctx, out, _ := newTestContext(proj, false)
		if err := runCritique(ctx, []string{slug}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		assertContains(t, out.String(), fmt.Sprintf("The critique check is red %d of 4 — round %d: ", i, i))
	}
	ctx, out, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "The critique check is red 4 of 4: it is a question for the owner now")
	body := readFile(t, issuePath)
	assertContains(t, body, "question: red rounds: critique in design → backlog — red 4 of 4")
	assertContains(t, body, "1 blocks: blocks issues/Factory/critic.md:9,12 — no case for an empty list")

	ctx, out, _ = newTestContext(proj, false)
	if err := runRounds(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "critique")
	assertContains(t, out.String(), "red 4 of 4")

	ctx, _, _ = newTestContext(proj, false)
	err := runCritique(ctx, []string{slug})
	if err == nil || !strings.Contains(err.Error(), "critique in design → backlog is red 4 of 4: it is a question for the owner now") {
		t.Fatalf("fifth critique: %v", err)
	}
	if runs, _ := critique.Load(proj.IssueDir, slug); len(runs) != 5 {
		t.Fatalf("runs = %d, want 5 (the fifth not started)", len(runs))
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runTransition(ctx, []string{slug, "--to", "backlog"}); err == nil || !strings.Contains(err.Error(), "question for the owner") {
		t.Fatalf("transition: %v", err)
	}
}

// A run keeps the stand-in critic's stdout byte for byte in
// .critique/raw/<run id>.txt, and the run record names that file.
func TestCritiqueRunKeepsTheRawOutput(t *testing.T) {
	proj, slug, _, criticOut := critiqueProject(t)
	stdout := "Thinking about the diff first.\n\nblocks issues/Factory/critic.md:9 — no case for an empty list\nnote issues/Factory/critic.md:1 — fine\n  trailing prose the parser drops\nCoverage: read 1 of 1 files\n"
	mustWrite(t, criticOut, stdout)
	ctx, _, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	runs, err := critique.Load(proj.IssueDir, slug)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs: %v, %d", err, len(runs))
	}
	run := runs[0]
	if want := ".critique/raw/" + run.ID + ".txt"; run.Raw != want {
		t.Fatalf("record names %q, want %q", run.Raw, want)
	}
	if got := readFile(t, filepath.Join(proj.IssueDir, filepath.FromSlash(run.Raw))); got != stdout {
		t.Fatalf("raw file = %q\nwant %q", got, stdout)
	}
	if len(run.RawRedacted) != 0 {
		t.Errorf("nothing to redact, got %v", run.RawRedacted)
	}
}

// A secret in the critic's output is redacted in the kept file by the
// memory's scan, and the record names the kind.
func TestCritiqueRawOutputIsScrubbed(t *testing.T) {
	proj, slug, _, criticOut := critiqueProject(t)
	mustWrite(t, criticOut, "blocks issues/Factory/critic.md:9 — the fixture holds password=hunter22\nCoverage: read 1 of 1 files\n")
	ctx, _, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	runs, _ := critique.Load(proj.IssueDir, slug)
	raw := readFile(t, filepath.Join(proj.IssueDir, filepath.FromSlash(runs[0].Raw)))
	if strings.Contains(raw, "hunter22") || !strings.Contains(raw, "password=[redacted: password or token value]") {
		t.Fatalf("raw not scrubbed: %q", raw)
	}
	if len(runs[0].RawRedacted) != 1 {
		t.Errorf("RawRedacted = %v", runs[0].RawRedacted)
	}
}

// --reparse parses a run's kept raw output with the current parser: a
// run recorded by an older parser that kept only a lead-in line ("Some
// cases are not covered:") shows its bullet cases again, and nothing new
// is recorded.
func TestCritiqueReparseUsesTheCurrentParser(t *testing.T) {
	proj, slug, _, _ := critiqueProject(t)
	raw := "Read everything.\n\nshould issues/Factory/critic.md:9 — Some cases are not covered:\n- an empty list\n- a list of one\nCoverage: read 1 of 1 files\n"
	old := critique.Report{Coverage: "read 1 of 1 files", Findings: []critique.Finding{{Severity: critique.Should, Path: "issues/Factory/critic.md", Line: 9, Text: "Some cases are not covered:"}}}
	run := &critique.Run{Slug: slug, From: "in design", To: "backlog", At: time.Date(2026, 10, 31, 15, 35, 32, 0, time.UTC), Agent: "claude", Report: old}
	if err := critique.RecordWithRaw(proj.IssueDir, run, raw); err != nil {
		t.Fatal(err)
	}
	want := critique.Parse(raw)
	if !strings.Contains(want.Findings[0].Text, "a list of one") {
		t.Fatalf("the current parser lost the lead-in's cases: %+v", want.Findings)
	}

	ctx, out, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--reparse", run.ID}); err != nil {
		t.Fatal(err)
	}
	cur := *run
	cur.Report = want
	got := out.String()
	assertContains(t, got, "Current parser:\n"+critique.CommentText(&cur))
	assertContains(t, got, "Recorded:\n"+critique.CommentText(run))
	assertContains(t, got, "Differs from the recorded report. Nothing was recorded.")
	if runs, _ := critique.Load(proj.IssueDir, slug); len(runs) != 1 {
		t.Fatalf("reparse recorded a run: %d runs", len(runs))
	}

	ctx, _, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--reparse", "nope"}); err == nil || !strings.Contains(err.Error(), run.ID) {
		t.Fatalf("unknown id: %v", err)
	}
	// A run from before raw outputs were kept says so.
	if err := critique.Record(proj.IssueDir, &critique.Run{Slug: slug, From: "in design", To: "backlog", At: run.At.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	runs, _ := critique.Load(proj.IssueDir, slug)
	ctx, _, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--reparse", runs[1].ID}); err == nil || !strings.Contains(err.Error(), "no raw output kept") {
		t.Fatalf("old run: %v", err)
	}
}

// byHandWorkflow has a gate on one edge only, so the issue in design has
// none out of its status, and the block keeps the stand-in critic.
const byHandWorkflow = `statuses:
  - name: "in design"
  - name: "testing"
  - name: "done"
critique:
  command: "cat \"$CRITIC_OUT\""
  timeout: 30s
transitions:
  - from: "in design"
    to: "testing"
    actions: []
  - from: "testing"
    to: "done"
    actions:
      - type: "validate"
        rule: "critique"
`

// A run from a status with no gate writes its record and its comment, holds
// nothing, counts no red round, says so, and has nothing to override. A
// later gate out of another status still wants a run of its own.
func TestCritiqueRunsByHandWithoutAGate(t *testing.T) {
	proj, slug, issuePath, criticOut := critiqueProject(t)
	mustWrite(t, proj.WorkflowFile, byHandWorkflow)
	mustWrite(t, criticOut, "blocks internal/x.go:42 — nil on an empty list\nCoverage: all\n")

	ctx, out, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--brief"}); err != nil {
		t.Fatalf("--brief: %v", err)
	}
	brief := out.String()
	if !strings.Contains(brief, "no gate") || strings.Contains(brief, "gate in design →") || strings.Contains(brief, "the gate holds") {
		t.Fatalf("the brief should name no gate:\n%s", brief)
	}
	if runs, _ := critique.Load(proj.IssueDir, slug); len(runs) != 0 {
		t.Fatalf("--brief recorded a run: %+v", runs)
	}

	for round := 1; round <= 5; round++ {
		ctx, out, _ = newTestContext(proj, false)
		if err := runCritique(ctx, []string{slug}); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		text := out.String()
		for _, want := range []string{"no gate", "holds nothing", "testing → done"} {
			if !strings.Contains(text, want) {
				t.Errorf("round %d: output misses %q:\n%s", round, want, text)
			}
		}
		for _, no := range []string{"The gate", "gate in design", "passes", "critique check is"} {
			if strings.Contains(text, no) {
				t.Errorf("round %d: output says %q:\n%s", round, no, text)
			}
		}
	}

	runs, _ := critique.Load(proj.IssueDir, slug)
	if len(runs) != 5 {
		t.Fatalf("runs = %d, want 5", len(runs))
	}
	run := runs[4]
	if !run.ByHand() || run.From != "in design" || run.To != "" || run.Verdict() != "findings" {
		t.Fatalf("run = %+v (verdict %s)", run, run.Verdict())
	}
	comments, _ := tracker.LoadComments(issuePath)
	last := comments[len(comments)-1].Text
	if !strings.Contains(last, "critique: in design, by hand (no gate), findings") || strings.Contains(last, "→") {
		t.Fatalf("comment = %q", last)
	}
	if strings.Contains(strings.Join(commentTexts(comments), "\n"), "red rounds") {
		t.Fatalf("a by-hand run became a red-round question: %v", commentTexts(comments))
	}

	// issue-cli show's line and --override both read the record.
	wf, err := tracker.LoadWorkflow(proj.WorkflowFile)
	if err != nil {
		t.Fatal(err)
	}
	issue := &tracker.Issue{Slug: slug, Status: "in design"}
	if line := critiqueLine(proj.IssueDir, wf, issue); !strings.Contains(line, "in design, by hand (no gate) findings") {
		t.Fatalf("critiqueLine = %q", line)
	}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	ctx, out, _ = newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug, "--override", "--quote", "ok"}); err != nil {
		t.Fatalf("--override: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to override") {
		t.Fatalf("--override output: %s", out.String())
	}

	// The gate out of testing is not cleared by a run from in design.
	if err := critique.GateError(critique.Latest(runs, slug, "testing"), slug, "testing"); err == nil {
		t.Fatal("a by-hand run from another status cleared a gate")
	}
}

func commentTexts(comments []tracker.Comment) []string {
	var out []string
	for _, c := range comments {
		out = append(out, c.Text)
	}
	return out
}

// The doc's "This project runs it on" line names exactly the edges
// workflow.yaml gates, on any type's path, and says the gate is off when
// there are none; workflow.yaml's declined list agrees.
func TestCritiqueDocMatchesTheWorkflow(t *testing.T) {
	wf, err := tracker.LoadWorkflow("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	gated := map[string]bool{}
	add := func(edges []tracker.WorkflowTransition) {
		for _, e := range edges {
			gated["`"+e.From+" → "+e.To+"`"] = true
		}
	}
	add(wf.CritiqueEdges())
	for _, name := range wf.TypeNames() {
		add(wf.ForType(name).CritiqueEdges())
	}
	declined := false
	for _, id := range wf.Features.Declined {
		declined = declined || id == "critique-gate"
	}

	raw, err := os.ReadFile("../../docs/Workflow/critique.md")
	if err != nil {
		t.Fatal(err)
	}
	const lead = "This project runs it on "
	var line string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, lead) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("docs/Workflow/critique.md has no line starting %q", lead)
	}
	if len(gated) == 0 {
		if !strings.HasPrefix(line, lead+"no edge") {
			t.Fatalf("workflow.yaml gates no edge, the doc says: %s", line)
		}
		if !declined {
			t.Fatal("no edge has the critique rule but critique-gate is not declined in workflow.yaml")
		}
		if !strings.Contains(line, "declined") {
			t.Fatalf("the doc's line should say the gate is declined: %s", line)
		}
		return
	}
	if declined {
		t.Fatal("critique-gate is declined in workflow.yaml but an edge has the critique rule")
	}
	sentence, _, _ := strings.Cut(line, ". ")
	for edge := range gated {
		if !strings.Contains(sentence, edge) {
			t.Errorf("workflow.yaml gates %s, the doc's line doesn't: %s", edge, line)
		}
	}
	if n := strings.Count(sentence, "→"); n != len(gated) {
		t.Errorf("the doc's line names %d edges, workflow.yaml gates %d: %s", n, len(gated), line)
	}
}

// A new run records how long the critic took, and its comment says so; a
// run recorded before took_ms was kept reads "—", never 0.
func TestCritiqueRecordsTook(t *testing.T) {
	proj, slug, issuePath, criticOut := critiqueProject(t)
	mustWrite(t, criticOut, "note issues/Factory/critic.md:1 — fine\nCoverage: read 1 of 1 files\n")
	ctx, out, _ := newTestContext(proj, false)
	if err := runCritique(ctx, []string{slug}); err != nil {
		t.Fatal(err)
	}
	runs, err := critique.Load(proj.IssueDir, slug)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs: %v, %d", err, len(runs))
	}
	if runs[0].TookMS <= 0 {
		t.Fatalf("took_ms = %d, want > 0", runs[0].TookMS)
	}
	want := "; took " + runs[0].TookText()
	assertContains(t, out.String(), want)
	assertContains(t, readFile(t, issuePath), want)

	old := &critique.Run{Slug: slug, From: "in design", To: "backlog"}
	if old.TookText() != "—" || strings.Contains(critique.CommentText(old), "took") {
		t.Fatalf("an old run: %q, %q", old.TookText(), critique.CommentText(old))
	}
	if _, ok := old.Took(); ok {
		t.Fatal("an old run reads as timed")
	}
}
