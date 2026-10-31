package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/checks"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// makeCheckScriptFixture is a project without worktrees (scripts run from
// its folder) and one issue at human-testing with Manual boxes.
func makeCheckScriptFixture(t *testing.T, workflow string) (*tracker.Project, string) {
	t.Helper()
	dir := t.TempDir()
	issues := filepath.Join(dir, "issues", "CLI")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	if workflow == "" {
		workflow = "statuses:\n  - name: \"testing\"\n  - name: \"human-testing\"\n"
	}
	wfPath := filepath.Join(dir, "workflow.yaml")
	os.WriteFile(wfPath, []byte(workflow), 0o644)
	body := `---
title: "lead tab"
status: "human-testing"
system: "CLI"
---

## Test Plan

### Automated
- [x] Tests pass

### Manual
- [x] Sandbox prepared
- [ ] The tab opens detached
- [ ] Spawning twice adds nothing

## Human Testing
- [ ] Owner checked it
`
	os.WriteFile(filepath.Join(issues, "lead-tab.md"), []byte(body), 0o644)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return &tracker.Project{Name: "t", Slug: "t", IssueDir: filepath.Join(dir, "issues"), WorkflowFile: wfPath, WorkDir: dir}, dir
}

const checkSlug = "cli/lead-tab"

func TestCheckScriptLibPrintsTheLibrary(t *testing.T) {
	ctx, out, _ := newTestContext(nil, false)
	if err := runCheckScript(ctx, []string{"--lib"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(checks.Lib) {
		t.Fatal("--lib doesn't print the embedded library")
	}
}

func TestCheckScriptNeedsASlug(t *testing.T) {
	ctx, _, _ := newTestContext(nil, false)
	err := runCheckScript(ctx, nil)
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "check-script --lib") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckScriptNoScriptsSaysHowToStartOne(t *testing.T) {
	proj, _ := makeCheckScriptFixture(t, "")
	ctx, _, _ := newTestContext(proj, false)
	err := runCheckScript(ctx, []string{checkSlug})
	var ce *exitCodeError
	if !errors.As(err, &ce) || ce.Code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	assertContains(t, ce.Msg, "no check scripts for cli/lead-tab")
	assertContains(t, ce.Msg, "issue-cli check-script cli/lead-tab --new <name>")
}

func TestCheckScriptNewWritesOneStepPerUntickedManualBox(t *testing.T) {
	proj, dir := makeCheckScriptFixture(t, "")
	ctx, out, _ := newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--new", "lead-tab"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scripts", "checks", "lead-tab.sh")
	assertContains(t, out.String(), "✓ Wrote "+path)
	assertContains(t, out.String(), "check-script cli/lead-tab --yes")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	assertContains(t, s, "# issue: cli/lead-tab\n")
	assertContains(t, s, "step TP3 'The tab opens detached' --box TP3")
	assertContains(t, s, "step TP4 'Spawning twice adds nothing' --box TP4")
	assertNotContains(t, s, "Sandbox prepared")
	assertNotContains(t, s, "Owner checked it")

	// A second --new with the same name refuses to overwrite.
	ctx, _, _ = newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--new", "lead-tab"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second --new: %v", err)
	}
	ctx, _, _ = newTestContext(proj, false)
	var ue *usageError
	if err := runCheckScript(ctx, []string{checkSlug, "--new", "Lead Tab"}); !errors.As(err, &ue) {
		t.Fatalf("bad name: %v", err)
	}
}

func writeCheckScript(t *testing.T, dir, name, slug, body string) {
	t.Helper()
	path := filepath.Join(dir, "scripts", "checks", name+".sh")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("#!/usr/bin/env bash\n# issue: "+slug+"\nsource \"${CHECKS_LIB:?}\"\n"+body), 0o755)
}

func TestCheckScriptListAndJSON(t *testing.T) {
	proj, dir := makeCheckScriptFixture(t, "")
	writeCheckScript(t, dir, "lead-tab", checkSlug, "summary\n")
	writeCheckScript(t, dir, "elsewhere", "cli/other", "summary\n")
	ctx, out, _ := newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "lead-tab  "+filepath.Join(dir, "scripts", "checks", "lead-tab.sh")+"  (no owner run yet)")
	assertNotContains(t, out.String(), "elsewhere")

	ctx, out, _ = newTestContext(proj, true)
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), `"name": "lead-tab"`)
	assertContains(t, out.String(), `"owner_run": false`)
}

func TestCheckScriptRunsInAMachineRunFromThePipe(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	proj, dir := makeCheckScriptFixture(t, "")
	writeCheckScript(t, dir, "lead-tab", checkSlug, `
echo "ENV slug=$CHECKS_SLUG project=$CHECKS_PROJECT root=$CHECKS_ROOT name=$CHECKS_NAME"
step TP3 "passes"
expect true fine
summary
`)
	writeCheckScript(t, dir, "broken", checkSlug, "step TP4 fails\nexpect false nope\nsummary\n")

	ctx, out, _ := newTestContext(proj, false)
	err := runCheckScript(ctx, []string{checkSlug, "lead-tab"})
	if err != nil {
		t.Fatalf("one passing script: %v\n%s", err, out)
	}
	assertContains(t, out.String(), "ENV slug=cli/lead-tab project=t root="+dir+" name=lead-tab")
	assertContains(t, out.String(), "Machine run")
	assertNotContains(t, out.String(), "broken")

	ctx, out, _ = newTestContext(proj, false)
	err = runCheckScript(ctx, []string{checkSlug, "--yes"})
	var ce *exitCodeError
	if !errors.As(err, &ce) || ce.Code != 1 || ce.Msg != "check script failed: broken" {
		t.Fatalf("err = %v\n%s", err, out)
	}
	assertContains(t, out.String(), "✓ TP3 pass")
	assertContains(t, out.String(), "✗ TP4 fail: nope")

	ctx, _, _ = newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "nope"}); !errors.As(err, &ce) || !strings.Contains(ce.Msg, "no check script nope") {
		t.Fatalf("unknown name: %v", err)
	}
}

func TestCheckScriptWithoutItsWorktree(t *testing.T) {
	proj, _ := makeCheckScriptFixture(t, "worktree: true\nstatuses:\n  - name: \"human-testing\"\n")
	ctx, _, _ := newTestContext(proj, false)
	err := runCheckScript(ctx, []string{checkSlug})
	if err == nil || !strings.Contains(err.Error(), "no worktree for cli/lead-tab") || !strings.Contains(err.Error(), "git worktree add") {
		t.Fatalf("err = %v", err)
	}
	// --list still answers (exit 1), so the mod's probe never hangs.
	ctx, _, _ = newTestContext(proj, false)
	var ce *exitCodeError
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); !errors.As(err, &ce) || ce.Code != 1 {
		t.Fatalf("--list without a worktree: %v", err)
	}
}

func TestManualBoxesStopAtTheNextHeading(t *testing.T) {
	body := "## Test Plan\n\n### Manual\n- [ ] one\n\n### Notes\n- [ ] not manual\n\n## Other\n- [ ] no\n"
	got := manualBoxes(body)
	if len(got) != 1 || got[0].Text != "one" {
		t.Fatalf("manualBoxes = %+v", got)
	}
}

func TestCheckScriptHintAtTestingAndHumanTesting(t *testing.T) {
	wf := "statuses:\n  - name: \"testing\"\n    prompt: \"Pass issue-cli check-script <slug> --yes.\"\n  - name: \"human-testing\"\n"
	proj, dir := makeCheckScriptFixture(t, wf)
	ctx, _, _ := newTestContext(proj, false)
	issue, _, err := findIssueOrErr(ctx, checkSlug)
	if err != nil {
		t.Fatal(err)
	}
	hint := func(status string) string {
		var b strings.Builder
		i := *issue
		i.Status = status
		printCheckScriptHint(&b, proj, proj.LoadWorkflowForIssue(&i), &i)
		return b.String()
	}
	assertContains(t, hint("human-testing"), "None yet.")
	assertContains(t, hint("human-testing"), "issue-cli check-script cli/lead-tab --new <name>")
	if got := hint("in progress"); got != "" {
		t.Fatalf("in progress: %q", got)
	}

	writeCheckScript(t, dir, "lead-tab", checkSlug, "summary\n")
	got := hint("testing")
	assertContains(t, got, "Scripts: lead-tab")
	assertContains(t, got, "issue-cli check-script cli/lead-tab --yes")
	assertNotContains(t, hint("human-testing"), "--yes")

	// A workflow without the feature says nothing.
	plain, _ := makeCheckScriptFixture(t, "")
	var b strings.Builder
	printCheckScriptHint(&b, plain, plain.LoadWorkflowForIssue(issue), issue)
	if b.Len() != 0 {
		t.Fatalf("without the feature: %q", b.String())
	}
}

func TestCheckScriptLibNeedsNoProject(t *testing.T) {
	var out, errw strings.Builder
	cfg := filepath.Join(t.TempDir(), "projects.yaml")
	os.WriteFile(cfg, []byte("projects:\n  - {name: a, slug: a, issues: /nonexistent/a}\n  - {name: b, slug: b, issues: /nonexistent/b}\n"), 0o644)
	if err := runTraced([]string{"--config", cfg, "check-script", "--lib"}, strings.NewReader(""), &out, &errw, &invocation{}); err != nil {
		t.Fatalf("--lib with an ambiguous project: %v %s", err, errw.String())
	}
	if out.String() != string(checks.Lib) {
		t.Fatal("--lib didn't print the library")
	}
}

// issueWorktree puts a git worktree on work/<slug> under dir, named freely:
// the workflow declares no worktrees, the workers make one anyway.
func issueWorktree(t *testing.T, dir, slug string) string {
	t.Helper()
	return worktreeOn(t, dir, "work/"+slug)
}

// worktreeOn puts a git worktree on branch under dir.
func worktreeOn(t *testing.T, dir, branch string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(dir, ".worktrees", "own-name")
	git("worktree", "add", "-q", "-b", branch, wt)
	resolved, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestCheckScriptUsesTheIssueBranchWorktreeWhenTheWorkflowHasNone(t *testing.T) {
	proj, dir := makeCheckScriptFixture(t, "")
	wt := issueWorktree(t, dir, checkSlug)

	ctx, out, _ := newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--new", "lead-tab"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Wrote "+filepath.Join(wt, "scripts", "checks", "lead-tab.sh"))
	if pathExists(filepath.Join(dir, "scripts", "checks", "lead-tab.sh")) {
		t.Fatal("--new wrote into the main checkout")
	}

	ctx, out, _ = newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); err != nil {
		t.Fatalf("--list: %v", err)
	}
	assertContains(t, out.String(), "lead-tab  "+filepath.Join(wt, "scripts", "checks", "lead-tab.sh"))
}

// A merged work/<slug> keeps its worktree, with the code and scripts it had
// when it branched: the checks run from the project folder (main) instead.
func TestCheckScriptRunsAMergedBranchFromTheProjectFolder(t *testing.T) {
	proj, dir := makeCheckScriptFixture(t, "")
	wt := issueWorktree(t, dir, checkSlug)
	git := func(in string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", in, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	old := filepath.Join(wt, "scripts", "checks", "lead-tab.sh")
	os.MkdirAll(filepath.Dir(old), 0o755)
	os.WriteFile(old, []byte(checks.Skeleton(checkSlug, "lead-tab", nil)), 0o755)
	git(wt, "add", "scripts")
	git(wt, "commit", "-q", "-m", "check script")
	git(dir, "merge", "-q", "--ff-only", "work/"+checkSlug)
	// main moves on and fixes the script; the worktree keeps the old copy.
	os.WriteFile(filepath.Join(dir, "scripts", "checks", "lead-tab.sh"), []byte(checks.Skeleton(checkSlug, "lead-tab", nil)+"# fixed\n"), 0o755)
	git(dir, "commit", "-q", "-am", "fix the script")

	ctx, out, _ := newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); err != nil {
		t.Fatalf("--list: %v", err)
	}
	assertContains(t, out.String(), "lead-tab  "+filepath.Join(dir, "scripts", "checks", "lead-tab.sh"))
	assertContains(t, out.String(), "work/"+checkSlug+" is merged into main")

	// --new still writes into the issue's worktree.
	ctx, out, _ = newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--new", "second"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Wrote "+filepath.Join(wt, "scripts", "checks", "second.sh"))

	// Uncommitted work there: the worktree is in use again.
	ctx, out, _ = newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); err != nil {
		t.Fatalf("--list: %v", err)
	}
	assertContains(t, out.String(), "lead-tab  "+old)
}

// The shipping step removes the scripts in a last commit before the merge:
// after it, check-script runs them from history, from the project folder.
func TestCheckScriptRunsAScriptTheMergeRemovedFromHistory(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	proj, dir := makeCheckScriptFixture(t, "")
	wt := issueWorktree(t, dir, checkSlug)
	git := func(in string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", in, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	writeCheckScript(t, wt, "lead-tab", checkSlug, `
echo "RAN from=$CHECKS_ROOT name=$CHECKS_NAME"
step TP3 "passes"
expect true fine
summary
`)
	writeCheckScript(t, wt, "other-issue", "cli/other", "echo OTHER\n")
	git(wt, "add", "scripts")
	git(wt, "commit", "-q", "-m", "check scripts")
	git(wt, "rm", "-q", "-r", "scripts")
	git(wt, "commit", "-q", "-m", "drop the check scripts before the merge")
	removedIn := git(wt, "rev-parse", "--short=7", "HEAD")
	// main moved meanwhile: a merge commit, which git log -- <path> would
	// simplify away (main never had the file) without --full-history.
	git(dir, "commit", "-q", "--allow-empty", "-m", "main moves on")
	git(dir, "merge", "-q", "--no-ff", "-m", "merge", "work/"+checkSlug)

	ctx, out, _ := newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); err != nil {
		t.Fatalf("--list: %v\n%s", err, out)
	}
	assertContains(t, out.String(), "lead-tab  scripts/checks/lead-tab.sh  (no owner run yet; from history: removed in "+removedIn+" before the merge)")
	assertNotContains(t, out.String(), "other-issue")

	ctx, out, _ = newTestContext(proj, true)
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), `"from": "`+removedIn)

	ctx, out, _ = newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--yes"}); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	assertContains(t, out.String(), "scripts/checks/lead-tab.sh from history (removed in "+removedIn+" before the merge): running it from "+dir)
	assertContains(t, out.String(), "RAN from="+dir+" name=lead-tab")
	assertContains(t, out.String(), "✓ TP3 pass")
	if !pathExists(filepath.Join(dir, ".agent-logs", "checks", "lead-tab")) {
		t.Fatal("the run record isn't in the project folder")
	}

	// With the branch gone, only main's history holds it.
	git(dir, "worktree", "remove", "--force", wt)
	git(dir, "branch", "-q", "-D", "work/"+checkSlug)
	ctx, out, _ = newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); err != nil {
		t.Fatalf("--list without the branch: %v\n%s", err, out)
	}
	assertContains(t, out.String(), "from history: removed in "+removedIn)

	// A script back on disk wins over history.
	writeCheckScript(t, dir, "lead-tab", checkSlug, "summary\n")
	ctx, out, _ = newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); err != nil {
		t.Fatal(err)
	}
	assertNotContains(t, out.String(), "from history")
}

// With the issue's worktree gone too, history is read from the project folder.
func TestCheckScriptFromHistoryWithoutItsWorktree(t *testing.T) {
	proj, dir := makeCheckScriptFixture(t, "worktree: true\nstatuses:\n  - name: \"human-testing\"\n")
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	writeCheckScript(t, dir, "lead-tab", checkSlug, "summary\n")
	git("add", "scripts")
	git("commit", "-q", "-m", "check script")
	git("rm", "-q", "-r", "scripts")
	git("commit", "-q", "-m", "drop it")

	ctx, out, _ := newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--list"}); err != nil {
		t.Fatalf("--list: %v\n%s", err, out)
	}
	assertContains(t, out.String(), "lead-tab  scripts/checks/lead-tab.sh")
}

// A system with a space ("Level Design") gives a slug with a space: it has no
// work/<slug> branch, so the worker's worktree is the one it runs from.
func TestCheckScriptFindsScriptsForASlugWithASpace(t *testing.T) {
	proj, dir := makeCheckScriptFixture(t, "")
	level := filepath.Join(dir, "issues", "Level Design")
	os.MkdirAll(level, 0o755)
	os.WriteFile(filepath.Join(level, "rock-sentinel.md"), []byte("---\ntitle: \"rock sentinel\"\nstatus: \"human-testing\"\nsystem: \"Level Design\"\n---\n"), 0o644)
	const slug = "level design/rock-sentinel"
	wt := worktreeOn(t, dir, "art/rock-sentinel")
	script := filepath.Join(wt, "scripts", "checks", "probe.sh")

	old := workingDir
	t.Cleanup(func() { workingDir = old })
	os.MkdirAll(filepath.Join(wt, "scripts"), 0o755)
	workingDir = func() (string, error) { return filepath.Join(wt, "scripts"), nil }
	ctx, out, _ := newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{slug, "--new", "probe"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ Wrote "+script)
	assertContains(t, out.String(), "issue-cli check-script 'level design/rock-sentinel' --yes")
	if pathExists(filepath.Join(dir, "scripts", "checks", "probe.sh")) {
		t.Fatal("--new wrote into the main checkout")
	}
	ctx, out, _ = newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{slug, "--list"}); err != nil {
		t.Fatalf("--list: %v", err)
	}
	assertContains(t, out.String(), "probe  "+script)

	// From the main checkout, a script there is found too.
	workingDir = func() (string, error) { return dir, nil }
	os.MkdirAll(filepath.Join(dir, "scripts", "checks"), 0o755)
	os.WriteFile(filepath.Join(dir, "scripts", "checks", "main.sh"), []byte(checks.Skeleton(slug, "main", nil)), 0o755)
	ctx, out, _ = newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{slug, "--list"}); err != nil {
		t.Fatalf("--list from the main checkout: %v", err)
	}
	assertContains(t, out.String(), "main  ")
}

func TestCheckScriptRunsWithBashFromCandidates(t *testing.T) {
	bash, err := checks.FindBash()
	if err != nil {
		t.Skip(err)
	}
	proj, dir := makeCheckScriptFixture(t, "")
	writeCheckScript(t, dir, "lead-tab", checkSlug, "echo \"BASH=$BASH_VERSION\"\nstep TP3 ok\nexpect true fine\nsummary\n")
	// A PATH whose bash is too old (or missing) doesn't stop the run.
	t.Setenv("PATH", t.TempDir()+string(os.PathListSeparator)+"/usr/bin:/bin")
	ctx, out, _ := newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--yes"}); err != nil {
		t.Fatalf("run: %v\n%s (bash %s)", err, out, bash)
	}
	assertNotContains(t, out.String(), "BASH=3.")
}

func TestHumanTestingWaitsForAPassingMachineRun(t *testing.T) {
	wf := "statuses:\n  - name: \"testing\"\n    prompt: \"Pass issue-cli check-script <slug> --yes.\"\n  - name: \"human-testing\"\n"
	proj, dir := makeCheckScriptFixture(t, wf)
	ctx, _, _ := newTestContext(proj, false)
	issue, _, err := findIssueOrErr(ctx, checkSlug)
	if err != nil {
		t.Fatal(err)
	}
	wfc := proj.LoadWorkflowForIssue(issue)
	gate := func(to string) *transitionProblem { return checkScriptGate(proj, wfc, issue, to) }

	if p := gate("human-testing"); p != nil {
		t.Fatalf("no scripts: %+v", p)
	}
	writeCheckScript(t, dir, "lead-tab", checkSlug, "summary\n")
	p := gate("human-testing")
	if p == nil || !strings.Contains(p.Message, "lead-tab: no machine run") || len(p.Fix) == 0 || !strings.Contains(p.Fix[0], "check-script cli/lead-tab --yes") {
		t.Fatalf("never run: %+v", p)
	}
	if p := gate("testing"); p != nil {
		t.Fatalf("another target: %+v", p)
	}
	rec := filepath.Join(dir, checks.RecordDir, "lead-tab")
	os.MkdirAll(rec, 0o755)
	os.WriteFile(filepath.Join(rec, "20261030T100000Z-machine.txt"), []byte("step TP3 fail boxes=TP3 note=nope\n"), 0o644)
	if p := gate("human-testing"); p == nil || !strings.Contains(p.Message, "lead-tab: the last machine run failed") {
		t.Fatalf("failed run: %+v", p)
	}
	os.WriteFile(filepath.Join(rec, "20261030T110000Z-machine.txt"), []byte("step TP3 pass boxes=TP3\n"), 0o644)
	if p := gate("human-testing"); p != nil {
		t.Fatalf("passed run: %+v", p)
	}

	// Without the feature there is no gate.
	plain, pdir := makeCheckScriptFixture(t, "")
	writeCheckScript(t, pdir, "lead-tab", checkSlug, "summary\n")
	if p := checkScriptGate(plain, plain.LoadWorkflowForIssue(issue), issue, "human-testing"); p != nil {
		t.Fatalf("without the feature: %+v", p)
	}
}

// A passing machine run ticks nothing and says which boxes are the owner's:
// the steps left for their eyes, and HT1.
func TestCheckScriptYesNamesTheOwnersBoxes(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	proj, dir := makeCheckScriptFixture(t, "")
	writeCheckScript(t, dir, "lead-tab", checkSlug, `
step TP3 "opens"
expect true fine
step TP4 "twice"
ask "One tab?"
summary
`)
	ctx, out, _ := newTestContext(proj, false)
	if err := runCheckScript(ctx, []string{checkSlug, "--yes"}); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	got := out.String()
	assertContains(t, got, "Machine run: 0 failed, 1 need the owner's eyes, 0 skipped. Left to the owner: TP4.")
	assertNotContains(t, got, "Nothing ticked")
	assertContains(t, got, "Machine run passed. Left to the owner on cli/lead-tab (never tick these yourself):")
	assertContains(t, got, `TP4  "Spawning twice adds nothing"  (guided check lead-tab step TP4: needs your eyes)`)
	assertContains(t, got, `HT1  "Owner checked it"  (Human Testing box)`)
	assertNotContains(t, got, `TP3  "The tab opens detached"`)
	body, _ := os.ReadFile(filepath.Join(proj.IssueDir, "CLI", "lead-tab.md"))
	assertNotContains(t, string(body), "[x] Owner checked it")
}
