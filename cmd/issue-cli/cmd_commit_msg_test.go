package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// knownCatalogIDs is this checkout's catalog as the hook reads it, with
// parked-status pinned at the last release (not bumped) and
// assignee-gate pinned past it (bumped), whatever this branch did to them.
func knownCatalogIDs(t *testing.T) workflowupgrade.TrailerCatalog {
	t.Helper()
	cat := commitMsgCatalog(filepath.Join("..", ".."))
	if cat.Versions == nil || cat.LastRelease == "" {
		t.Fatal("no catalog or no last release in this checkout")
	}
	for _, id := range []string{"parked-status", "assignee-gate"} {
		if _, ok := cat.Versions[id]; !ok {
			t.Fatalf("catalog has no %s entry: pick another known id for these tests", id)
		}
	}
	cat.Versions["parked-status"] = cat.LastRelease
	cat.Versions["assignee-gate"] = workflowupgrade.NextRelease(cat.LastRelease)
	return cat
}

const commitSubject = "fix(workflow): something\n\nBody text.\n\n"

func TestCommitMsgProblems(t *testing.T) {
	known := knownCatalogIDs(t)
	for _, tc := range []struct {
		name, lines string
		want        string // "" = no problem
	}{
		{"colon after the id", "Catalog: n/a parked-status: only a refactor", `a colon after the entry id "parked-status"`},
		{"bare n/a", "Catalog: n/a", "names no entry: when the change touches no catalog entry leave the Catalog: line out"},
		{"colon after n/a", "Catalog: n/a: parked-status why", "a colon after n/a"},
		{"unknown id (wrapped prose)", "Catalog: n/a line per entry in the docs", `no catalog entry "line"`},
		{"an id without n/a, not bumped", "Catalog: parked-status", "names parked-status as bumped, but parked-status is at"},
		{"an id without n/a, bumped", "Catalog: assignee-gate the gate's new prompt", ""},
		{"bumped and not", "Catalog: assignee-gate,parked-status", "run issue-cli workflow catalog-bump parked-status;"},
		{"no entry, no n/a", "Catalog: none", `no catalog entry "none"`},
		{"package path", "Proves: AC1 internal/memory TestX", `"internal/memory" is a path`},
		{"path with flags", "Proves: AC1 TestX, cmd/issue-cli --project dark-factory TestY", `"cmd/issue-cli" is a path`},
		{"slug#AC", "Proves: workflow/some-slug#AC2 TestX", "the issue slug doesn't go in a Proves: line"},
		{"session/step", "Proves: AC3 lead-tab/TP4", `"lead-tab/TP4" is a <session>/<step>`},
		{"bare step id", "Proves: AC1 TP4", `"TP4" is a box or playtest step id`},
		{"no criterion", "Proves: TestX", "names no criterion"},
		{"well formed", "Catalog: n/a parked-status the change is a refactor\nProves: AC1 TestX", ""},
		{"several and a subtest", "Proves: AC1, AC3 TestA TestB/sub_case TestFoo2", ""},
		{"commit link", "Proves: AC2", ""},
		{"comment lines are git's", "# Proves: AC1 internal/memory TestX", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(CommitMsgProblems(commitSubject+tc.lines+"\n", known), "\n")
			if tc.want == "" {
				if got != "" {
					t.Fatalf("want no problem, got:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, got)
			}
		})
	}
}

// commitThroughHook installs the commit-msg check in a fresh repo (with
// this checkout's catalog), puts issue-cli on PATH as this test binary,
// and commits msg through git: the hook git runs, not a call of ours.
func commitThroughHook(t *testing.T, msg string) (string, error) {
	t.Helper()
	return commitFilesThroughHook(t, msg, nil)
}

// commitFilesThroughHook is commitThroughHook with files (path → content)
// written into the repo and staged beside the catalog.
func commitFilesThroughHook(t *testing.T, msg string, extra map[string]string) (string, error) {
	t.Helper()
	_, out, err := commitRepoThroughHook(t, msg, extra)
	return out, err
}

// commitRepoThroughHook is commitFilesThroughHook that also returns the repo.
func commitRepoThroughHook(t *testing.T, msg string, extra map[string]string) (repo, out string, err error) {
	t.Helper()
	return commitRepoThroughHookCLI(t, msg, extra, selfCLI)
}

// selfCLI is the body of an issue-cli stand-in that runs this test binary
// as issue-cli. The test binary's path changes with each build: it comes
// from the environment, so the stand-in's body stays the same.
const selfCLI = "ISSUE_CLI_TEST_AS_CLI=1 exec \"$ISSUE_CLI_TEST_SELF\" \"$@\"\n"

// commitRepoThroughHookCLI is commitRepoThroughHook with cli as the body of
// the issue-cli on PATH, the one the hook runs.
func commitRepoThroughHookCLI(t *testing.T, msg string, extra map[string]string, cli string) (repo, out string, err error) {
	t.Helper()
	repo = t.TempDir()
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
		// The machine's global config (a core.hooksPath) stays out.
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := git("init", "-q"); err != nil {
		t.Fatal(out)
	}
	srcDir := filepath.Join("..", "..", filepath.FromSlash(workflowupgrade.CatalogPath))
	files, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, filepath.FromSlash(workflowupgrade.CatalogPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range files {
		b, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// An existing hook is kept and runs first.
	hooks := filepath.Join(repo, ".git", "hooks")
	os.MkdirAll(hooks, 0o755)
	standin.Write(t, filepath.Join(hooks, "commit-msg"), "echo earlier-hook-ran >&2\n")
	path, kept, err := installCommitMsgHook(repo)
	if err != nil || !kept || !strings.HasSuffix(path, filepath.Join(".git", "hooks", "commit-msg")) {
		t.Fatalf("install: %s kept=%v %v", path, kept, err)
	}
	if _, _, err := installCommitMsgHook(repo); err != nil {
		t.Fatalf("a second install replaces our own hook: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	t.Setenv("ISSUE_CLI_TEST_SELF", self)
	standin.Write(t, filepath.Join(bin, "issue-cli"), cli)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	for rel, body := range extra {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := git("add", "-A"); err != nil {
		t.Fatal(out)
	}
	msgFile := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(msgFile, []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = git("commit", "-q", "-F", msgFile)
	return repo, out, err
}

func TestCommitMsgHookRefusesAColonAfterTheCatalogID(t *testing.T) {
	out, err := commitThroughHook(t, commitSubject+"Catalog: n/a parked-status: why\n")
	if err == nil {
		t.Fatalf("commit went through:\n%s", out)
	}
	for _, want := range []string{"earlier-hook-ran", "commit refused", `a colon after the entry id "parked-status"`, `write "Catalog: n/a parked-status <why>", no colon`} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

func TestCommitMsgHookRefusesAPathInProves(t *testing.T) {
	out, err := commitThroughHook(t, commitSubject+"Proves: AC1 internal/memory TestX\n")
	if err == nil {
		t.Fatalf("commit went through:\n%s", out)
	}
	if !strings.Contains(out, `"internal/memory" is a path`) {
		t.Errorf("want the path named in:\n%s", out)
	}
}

func TestCommitMsgHookPassesWellFormedLines(t *testing.T) {
	out, err := commitThroughHook(t, commitSubject+"Catalog: n/a parked-status a refactor, nothing projects adopt\nProves: AC1 TestX\n")
	if err != nil {
		t.Fatalf("commit refused: %v\n%s", err, out)
	}
	if strings.Contains(out, "commit refused") {
		t.Fatalf("hook complained:\n%s", out)
	}
}

// A bare Catalog: <id> passes the hook when the working tree's catalog has
// the entry past the changelog's last release (a catalog-bump), and is
// refused with the catalog-bump to run when it isn't.
func TestCommitMsgAcceptsABareIDForABumpedEntry(t *testing.T) {
	msg := commitSubject + "Catalog: parked-status the parked column's new prompt\n"
	out, err := commitFilesThroughHook(t, msg, map[string]string{workflowupgrade.ChangelogPath: "## Unreleased\n\n## v0.0.1 — 2026-01-01\n"})
	if err != nil || strings.Contains(out, "commit refused") {
		t.Fatalf("a bumped entry's id refused: %v\n%s", err, out)
	}
	out, err = commitFilesThroughHook(t, msg, map[string]string{workflowupgrade.ChangelogPath: "## v99.0.0 — 2026-01-01\n"})
	if err == nil {
		t.Fatalf("an entry not bumped went through:\n%s", out)
	}
	for _, want := range []string{"names parked-status as bumped, but parked-status is at", "not newer than the last release v99.0.0", "issue-cli workflow catalog-bump parked-status;", "Catalog: n/a parked-status <why>"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// The line catalog-na recorded goes into the next commit through the hook,
// and the record is dropped.
func TestCommitMsgHookAddsTheRecordedNALine(t *testing.T) {
	repo, out, err := commitRepoThroughHook(t, commitSubject+"Co-Authored-By: T <t@example.com>\n", map[string]string{".git/" + catalogNAFile: "n/a parked-status a refactor\n"})
	if err != nil {
		t.Fatalf("commit refused: %v\n%s", err, out)
	}
	if !strings.Contains(out, "note: added Catalog: n/a parked-status a refactor to the message (recorded with issue-cli workflow catalog-na)") {
		t.Errorf("no note:\n%s", out)
	}
	body, err := exec.Command("git", "-C", repo, "log", "-1", "--format=%B").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\nCatalog: n/a parked-status a refactor\n") {
		t.Errorf("the commit lacks the line:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", catalogNAFile)); err == nil {
		t.Error("the record outlived the commit that got it")
	}
}

// An issue-cli on PATH older than catalog-na accepts the message and
// leaves the recorded line out: the hook adds it itself, so the commit
// gets it whichever build the hook runs. Both older forms: one that knows
// commit-msg but not the record, and one that knows neither.
func TestCommitMsgHookAddsTheRecordedNALineWhenTheCLIPredatesIt(t *testing.T) {
	for name, cli := range map[string]string{
		"commit-msg without the record": "exit 0\n",
		"no commit-msg":                 "echo \"unknown workflow subcommand: commit-msg\" >&2; exit 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			repo, out, err := commitRepoThroughHookCLI(t, commitSubject+"Body para.\n\nCo-Authored-By: T <t@example.com>\n", map[string]string{".git/" + catalogNAFile: "n/a parked-status a refactor\n"}, cli)
			if err != nil {
				t.Fatalf("commit refused: %v\n%s", err, out)
			}
			if !strings.Contains(out, "note: added Catalog: n/a parked-status a refactor to the message (recorded with issue-cli workflow catalog-na; the issue-cli on PATH didn't add it: make install)") {
				t.Errorf("no note:\n%s", out)
			}
			body, err := exec.Command("git", "-C", repo, "log", "-1", "--format=%B").Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(body), "Catalog: n/a parked-status a refactor") != 1 {
				t.Errorf("want the line once in the commit:\n%s", body)
			}
			if _, err := os.Stat(filepath.Join(repo, ".git", catalogNAFile)); err == nil {
				t.Error("the record outlived the commit that got it")
			}
		})
	}
}

// onWorkBranch makes dir a git repo (when it isn't one) on the issue's
// work/cli/sample branch and runs the test from there, as the hook does.
func onWorkBranch(t *testing.T, dir string) {
	t.Helper()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		git("init", "-q", "-b", "main")
		git("commit", "-q", "--allow-empty", "-m", "init")
	}
	git("checkout", "-q", "-b", "work/cli/sample")
	t.Chdir(dir)
}

func commitMsgFile(t *testing.T, msg string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(p, []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// (1) An accepted Proves: line naming a test says the criterion stays
// unproven until prove --run posts the tests: report.
func TestCommitMsgAcceptedProvesPointsAtProveRun(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	onWorkBranch(t, filepath.Dir(proj.WorkflowFile))
	ctx, _, stderr := newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+"Proves: AC1, AC2 TestCountShows\n")}); err != nil {
		t.Fatalf("well-formed Proves: refused: %v\n%s", err, stderr)
	}
	for _, id := range []string{"AC1", "AC2"} {
		assertContains(t, stderr.String(), "note: "+id+" stays unproven until `issue-cli prove cli/sample --run` runs TestCountShows and posts a tests: comment")
	}

	// Proves: AC1 alone links the commit: nothing waits on prove --run.
	ctx, _, stderr = newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+"Proves: AC1\n")}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "prove") {
		t.Errorf("a commit-only Proves: line got a prove --run note:\n%s", stderr)
	}
}

// (3) On an issue with no ## Acceptance criteria, a Proves: line's only
// problem is that it exists: the refusal says so once and names no
// playtest step, since the issue has no criteria to put on one. An
// accepted line gets the same note.
func TestCommitMsgNoCriteriaOneNote(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	proj, _, _ := makeSimpleProject(t, "in progress")
	onWorkBranch(t, filepath.Dir(proj.IssueDir))
	for _, line := range []string{"Proves: B1", "Proves: I1 TP1", "Proves: B1 TestX"} {
		ctx, _, stderr := newTestContext(proj, false)
		if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+line+"\n")}); err == nil {
			t.Fatalf("%s accepted:\n%s", line, stderr)
		}
		out := stderr.String()
		assertContains(t, out, "commit refused")
		if strings.Contains(out, "on step") || strings.Contains(out, "proves: [AC1]") {
			t.Errorf("%s: a playtest-step hint on an issue with no criteria:\n%s", line, out)
		}
		note := "cli/sample has no ## Acceptance criteria: no Proves: line is needed here"
		if n := strings.Count(out, note); n != 1 {
			t.Errorf("%s: the drop-it note %d times, want once:\n%s", line, n, out)
		}
		assertContains(t, out, "issue-cli check cli/sample <id>")
	}

	// A Catalog: problem beside it is still named.
	ctx, _, stderr := newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+"Catalog: n/a parked-status: why\nProves: B1\n")}); err == nil {
		t.Fatal("bad Catalog: accepted")
	}
	assertContains(t, stderr.String(), `a colon after the entry id "parked-status"`)

	ctx, _, stderr = newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+"Proves: AC1 TestX\n")}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stderr.String(), "cli/sample has no ## Acceptance criteria: no Proves: line is needed here")
	if strings.Contains(stderr.String(), "stays unproven") {
		t.Errorf("no criteria, yet a prove --run note:\n%s", stderr)
	}

	// docs/Workflow/proof.md says what a brief's box takes.
	doc, err := os.ReadFile(filepath.Join(repo, "docs", "Workflow", "proof.md"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(doc), "A brief's boxes (`B1`) are ticked with `issue-cli check <slug> B1`, never a `Proves:` line")
}

// On an issue with criteria, a playtest step id in a trailer still gets
// the step hint: there the step can carry proves: [AC1].
func TestCommitMsgStepHintWithCriteria(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	onWorkBranch(t, filepath.Dir(proj.WorkflowFile))
	ctx, _, stderr := newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+"Proves: AC1 TP4\n")}); err == nil {
		t.Fatalf("a step id in Proves: accepted:\n%s", stderr)
	}
	out := stderr.String()
	assertContains(t, out, "proves: [AC1] on step TP4 in its session file")
	if strings.Contains(out, "no Proves: line is needed") {
		t.Errorf("an issue with criteria got the no-criteria note:\n%s", out)
	}
}

// requireCommitReference adds the commits_reference_issue rule to the
// fixture's workflow, as the bundled shipping edge has it.
func requireCommitReference(t *testing.T, proj *tracker.Project) {
	t.Helper()
	data, err := os.ReadFile(proj.WorkflowFile)
	if err != nil {
		t.Fatal(err)
	}
	rule := "      - type: validate\n        rule: \"commits_reference_issue\"\n"
	if err := os.WriteFile(proj.WorkflowFile, append(data, rule...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitOnBranch makes a real commit with msg on the checkout's branch.
func commitOnBranch(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", msg)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

const refsLine = "    Refs: cli/sample"

// The hint runs until a commit references the issue: the message at hand
// or one already on the branch, by a Refs: line or by the subject.
func TestCommitMsgHintsRefsUntilACommitHasIt(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	requireCommitReference(t, proj)
	dir := filepath.Dir(proj.WorkflowFile)
	onWorkBranch(t, dir)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "") // a session not dispatched for it: the note
	hint := func(msg string) string {
		t.Helper()
		ctx, _, stderr := newTestContext(proj, false)
		if err := runCommitMsg(ctx, []string{commitMsgFile(t, msg)}); err != nil {
			t.Fatalf("message refused: %v\n%s", err, stderr)
		}
		return stderr.String()
	}

	out := hint(commitSubject)
	assertContains(t, out, "no commit on work/cli/sample references the issue yet")
	assertContains(t, out, "\n"+refsLine+"\n")

	// The message at hand carries it.
	if out := hint(commitSubject + "Refs: cli/sample\n"); out != "" {
		t.Errorf("a message with Refs: got:\n%s", out)
	}
	if out := hint("chore(merge): main into cli/sample\n"); out != "" {
		t.Errorf("a subject naming the slug got:\n%s", out)
	}
	// A longer slug, a commented line and a commit without it don't count.
	if out := hint(commitSubject + "Refs: cli/sample-more\n# Refs: cli/sample\n"); !strings.Contains(out, refsLine) {
		t.Errorf("another issue's Refs: or a comment stopped the hint:\n%s", out)
	}
	commitOnBranch(t, dir, "feat: a commit that names no issue")
	if out := hint(commitSubject); !strings.Contains(out, refsLine) {
		t.Errorf("a commit naming no issue stopped the hint:\n%s", out)
	}

	// One commit on the branch has it: the hint stops for good.
	commitOnBranch(t, dir, "feat: more\n\nRefs: cli/sample")
	if out := hint(commitSubject); out != "" {
		t.Errorf("the branch has a Refs: commit, yet:\n%s", out)
	}
}

// A workflow without the rule never asks for the line, and a branch that
// isn't an issue's has nothing to name.
func TestCommitMsgNoRefsHintWithoutTheRule(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	dir := filepath.Dir(proj.WorkflowFile)
	onWorkBranch(t, dir)
	ctx, _, stderr := newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject)}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "Refs:") {
		t.Errorf("no commits_reference_issue rule, yet:\n%s", stderr)
	}

	requireCommitReference(t, proj)
	t.Chdir(dir)
	cmd := exec.Command("git", "-C", dir, "checkout", "-q", "-b", "work/cli/no-such-issue")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	ctx, _, stderr = newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject)}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "Refs:") {
		t.Errorf("a branch with no issue got a hint:\n%s", stderr)
	}
}

// The refusal is about the bad line alone: the hint waits for a message
// that goes through.
func TestCommitMsgRefusalHasNoRefsHint(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	requireCommitReference(t, proj)
	onWorkBranch(t, filepath.Dir(proj.WorkflowFile))
	ctx, _, stderr := newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+"Proves: AC1 internal/memory TestX\n")}); err == nil {
		t.Fatal("bad Proves: line accepted")
	}
	if strings.Contains(stderr.String(), "Refs:") {
		t.Errorf("a refusal carries the hint:\n%s", stderr)
	}
}

// An unquoted vitest title in a Proves: line: the note names the whole
// title and the quoted form; a list of Go tests gets no such note.
func TestCommitMsgUnquotedTitleNoteNamesTheTitle(t *testing.T) {
	proj, _, _ := makeProofFixture(t, "2026-10-23")
	onWorkBranch(t, filepath.Dir(proj.WorkflowFile))
	ctx, _, stderr := newTestContext(proj, false)
	title := "kuchnia z daniami prawie tylko na obiad nie ma kafelka"
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+"Proves: AC1 "+title+"\n")}); err != nil {
		t.Fatalf("Proves: with an unquoted title refused: %v\n%s", err, stderr)
	}
	assertContains(t, stderr.String(), `quote it to be sure (Proves: AC1 "`+title+`")`)
	assertContains(t, stderr.String(), "note: AC1 stays unproven until `issue-cli prove cli/sample --run` runs \""+title+"\" and posts")

	ctx, _, stderr = newTestContext(proj, false)
	if err := runCommitMsg(ctx, []string{commitMsgFile(t, commitSubject+"Proves: AC1 TestA TestB\n")}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "quote it") {
		t.Errorf("a list of Go tests got the title note:\n%s", stderr)
	}
}
