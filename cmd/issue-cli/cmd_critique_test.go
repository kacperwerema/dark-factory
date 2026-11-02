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
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 1, Cores: 8} }
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
		mustWrite(t, p, "#!/bin/sh\ncat >/dev/null\necho "+agent+" >> \""+calls+"\"\n"+body)
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
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
