package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// lintTestProject is makeSimpleProject (one undated issue) with a
// workflow.yaml; extra is appended to a valid one.
func lintTestProject(t *testing.T, extra string) *Context {
	t.Helper()
	proj, _, _ := makeSimpleProject(t, "idea")
	wf := filepath.Join(filepath.Dir(proj.IssueDir), "workflow.yaml")
	content := "template: custom\nstatuses:\n  - name: idea\n    description: raw\ntransitions:\n  - from: idea\n    to: idea\n    actions: []\n" + extra
	if err := os.WriteFile(wf, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	proj.WorkflowFile = wf
	ctx, _, _ := newTestContext(proj, false)
	return ctx
}

func stdout(ctx *Context) string { return ctx.Stdout.(fmt.Stringer).String() }

func TestHealthLintPrintsWarningsWithFixes(t *testing.T) {
	ctx := lintTestProject(t, "bord:\n  columns: [idea]\n")
	if err := runHealth(ctx, []string{"lint"}); err != nil {
		t.Fatalf("findings are warnings: err = %v, want exit 0", err)
	}
	out := stdout(ctx)
	assertContains(t, out, "== health lint: ")
	assertContains(t, out, "undated cli/sample: no created: date")
	assertContains(t, out, "  fix: issue-cli set-meta cli/sample --key created --value ")
	assertContains(t, out, `schema workflow.yaml:9: unknown key "bord" at the top level`)
	assertContains(t, out, `  fix: did you mean "board"?`)
	assertContains(t, out, "2 warnings; the health score's lint component counts them")
}

func TestHealthLintCheckFlag(t *testing.T) {
	ctx := lintTestProject(t, "bord:\n  columns: [idea]\n")
	if err := runHealth(ctx, []string{"lint", "--check", "schema"}); err != nil {
		t.Fatal(err)
	}
	if out := stdout(ctx); strings.Contains(out, "undated") || !strings.Contains(out, "1 warning;") {
		t.Errorf("--check schema ran other checks:\n%s", out)
	}
}

func TestHealthLintUnknownCheckIsUsage(t *testing.T) {
	ctx := lintTestProject(t, "")
	err := runHealth(ctx, []string{"lint", "--check", "schema,nope"})
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "checks: undated, workflow") {
		t.Fatalf("err = %v, want a usage error listing the checks", err)
	}
}

func TestHealthLintList(t *testing.T) {
	ctx := lintTestProject(t, "")
	if err := runHealth(ctx, []string{"lint", "--list"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"undated", "workflow", "schema", "template", "placeholders", "sections", "departments"} {
		assertContains(t, stdout(ctx), "  "+name+" ")
	}
}

func TestHealthLintJSON(t *testing.T) {
	ctx := lintTestProject(t, "")
	ctx.JSONOutput = true
	if err := runHealth(ctx, []string{"lint"}); err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Findings []struct{ Check, Where, Message, Fix string }
		Warnings int
	}
	if err := json.Unmarshal([]byte(stdout(ctx)), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Warnings != 1 || len(rep.Findings) != 1 || rep.Findings[0].Check != "undated" || rep.Findings[0].Where != "cli/sample" {
		t.Errorf("report = %+v", rep)
	}
}

func TestHealthLintCapsWarningsPerCheck(t *testing.T) {
	ctx := lintTestProject(t, "")
	for i := 0; i < lintWarnCap+3; i++ {
		p := filepath.Join(ctx.Project.IssueDir, "CLI", fmt.Sprintf("extra-%d.md", i))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("---\ntitle: \"extra %d\"\nstatus: idea\n---\n", i)), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runHealth(ctx, []string{"lint"}); err != nil {
		t.Fatal(err)
	}
	out := stdout(ctx)
	if n := strings.Count(out, "\nundated "); n != lintWarnCap {
		t.Errorf("printed %d undated warnings, want %d", n, lintWarnCap)
	}
	assertContains(t, out, "… 4 more undated warnings: issue-cli health lint --check undated --all")
	assertContains(t, out, "24 warnings;")
}

func TestTopLevelLintIsGone(t *testing.T) {
	if _, ok := commandRegistry["lint"]; ok {
		t.Error("issue-cli lint is registered; lint lives under issue-cli health lint")
	}
}

// TestLintShasFixPrintsEachSwap: one issue naming two pre-amend SHAs gets
// both swapped in one write, each swap printed with its subject, then the
// check's count on the rewritten board; --fix without --check shas is a
// usage error.
func TestLintShasFixPrintsEachSwap(t *testing.T) {
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_DATE": "2026-11-01T10:00:00Z", "GIT_COMMITTER_DATE": "2026-11-01T10:00:00Z"} {
		t.Setenv(k, v)
	}
	ctx := lintTestProject(t, "")
	root := ctx.Project.Root()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root, "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	n := 0
	commit := func(msg string) (before, landed string) {
		t.Helper()
		for _, amend := range []bool{false, true} {
			n++
			os.WriteFile(filepath.Join(root, "a.txt"), []byte(strings.Repeat("x", n)), 0o644)
			git("add", "a.txt")
			if amend {
				git("commit", "-q", "--amend", "--no-edit")
				landed = git("rev-parse", "--short=7", "HEAD")
			} else {
				git("commit", "-q", "-m", msg)
				before = git("rev-parse", "--short=7", "HEAD")
			}
		}
		return before, landed
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "first")
	b1, l1 := commit("feat: one\n\nIssue: cli/done")
	b2, l2 := commit("fix: two\n\nIssue: cli/done")

	issuePath := filepath.Join(ctx.Project.IssueDir, "CLI", "done.md")
	if err := os.WriteFile(issuePath, []byte("---\ntitle: \"done\"\nstatus: \"done\"\ncreated: \"2026-11-01\"\n---\n\n## Implementation\nCommits "+b1+" and "+b2+".\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := runHealth(ctx, []string{"lint", "--fix"})
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--fix takes one check it can fix: --check shas") {
		t.Fatalf("--fix alone: err = %v, want a usage error", err)
	}

	ctx, _, _ = newTestContext(ctx.Project, false)
	if err := runHealth(ctx, []string{"lint", "--check", "shas", "--fix"}); err != nil {
		t.Fatal(err)
	}
	out := stdout(ctx)
	assertContains(t, out, "✓ cli/done: "+b1+" → "+l1+"  feat: one\n")
	assertContains(t, out, "✓ cli/done: "+b2+" → "+l2+"  fix: two\n")
	assertContains(t, out, "2 refs swapped in 1 issue, 0 refs kept")
	assertContains(t, out, "health lint --check shas now: 0 warnings")
	raw, _ := os.ReadFile(issuePath)
	assertContains(t, string(raw), "Commits "+l1+" and "+l2+".")

	// Nothing left: a second run swaps nothing and writes nothing.
	ctx, _, _ = newTestContext(ctx.Project, false)
	if err := runHealth(ctx, []string{"lint", "--check", "shas", "--fix"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout(ctx), "0 refs swapped in 0 issues, 0 refs kept")
	if again, _ := os.ReadFile(issuePath); string(again) != string(raw) {
		t.Errorf("second run rewrote the issue:\n%s", again)
	}
}

// lintCandidateFiles writes a candidate projects.yaml for ctx's project
// (Product runs one job naming brief) and a copy of its live workflow
// file into dir; it returns their paths.
func lintCandidateFiles(t *testing.T, ctx *Context, dir, brief string) (wf, projects string) {
	t.Helper()
	raw, err := os.ReadFile(ctx.Project.WorkflowFile)
	if err != nil {
		t.Fatal(err)
	}
	wf = filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(wf, raw, 0644); err != nil {
		t.Fatal(err)
	}
	projects = filepath.Join(t.TempDir(), "projects.yaml")
	cfg := fmt.Sprintf("projects:\n  - name: %s\n    slug: %s\n    issues: %s\n    workflow: %s\n    departments:\n      - name: Product\n        jobs:\n          - id: z\n            every: day\n            brief: %s\n",
		ctx.Project.Name, ctx.Project.Slug, ctx.Project.IssueDir, ctx.Project.WorkflowFile, brief)
	if err := os.WriteFile(projects, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	return wf, projects
}

// lintRun runs health lint with args on a fresh output buffer.
func lintRun(t *testing.T, ctx *Context, args ...string) string {
	t.Helper()
	c, _, _ := newTestContext(ctx.Project, false)
	if err := runHealth(c, append([]string{"lint"}, args...)); err != nil {
		t.Fatalf("health lint %v: %v", args, err)
	}
	return stdout(c)
}

func TestLintCandidateWorkflowReportsNewWarning(t *testing.T) {
	ctx := lintTestProject(t, "")
	before := lintRun(t, ctx)
	wf, projects := lintCandidateFiles(t, ctx, t.TempDir(), "jobs/nope.md")
	out := lintRun(t, ctx, "--workflow", wf, "--projects", projects)
	assertContains(t, out, "== health lint --workflow "+wf+" --projects "+projects+": test (")
	assertContains(t, out, "+ departments projects.yaml (Product): job z: brief jobs/nope.md does not exist")
	assertContains(t, out, "1 added, 0 removed against the live files")
	if strings.Contains(out, "undated cli/sample") {
		t.Errorf("a warning both runs share was printed:\n%s", out)
	}
	if after := lintRun(t, ctx); after != before || strings.Contains(after, "jobs/nope.md") {
		t.Errorf("the live lint changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// A fixed candidate removes the warning the live file has.
	ctx2 := lintTestProject(t, "bord:\n  columns: [idea]\n")
	fixed := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(fixed, []byte("template: custom\nstatuses:\n  - name: idea\n    description: raw\ntransitions:\n  - from: idea\n    to: idea\n    actions: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out = lintRun(t, ctx2, "--workflow", fixed, "--check", "schema")
	assertContains(t, out, `- schema workflow.yaml:9: unknown key "bord" at the top level`)
	assertContains(t, out, "0 added, 1 removed against the live files (live 1 warning, candidate 0 warnings)")
}

func TestLintCandidateEqualPrintsNothing(t *testing.T) {
	ctx := lintTestProject(t, "bord:\n  columns: [idea]\n")
	wf, _ := lintCandidateFiles(t, ctx, t.TempDir(), "")
	// An inserted comment moves every line: still no difference.
	raw, _ := os.ReadFile(wf)
	if err := os.WriteFile(wf, append([]byte("# candidate\n"), raw...), 0644); err != nil {
		t.Fatal(err)
	}
	out := lintRun(t, ctx, "--workflow", wf)
	assertContains(t, out, "✓ the candidate adds and removes no warnings")
	assertContains(t, out, "0 added, 0 removed against the live files")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "+ ") || strings.HasPrefix(line, "- ") {
			t.Errorf("an equal candidate printed a difference: %q\n%s", line, out)
		}
	}
	c, _, _ := newTestContext(ctx.Project, true)
	if err := runHealth(c, []string{"lint", "--workflow", wf}); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Added, Removed []any
	}
	if err := json.Unmarshal([]byte(stdout(c)), &d); err != nil || d.Added == nil || len(d.Added)+len(d.Removed) != 0 {
		t.Errorf("--json = %s (err %v), want empty added and removed", stdout(c), err)
	}
}

func TestLintCandidateResolvesBriefsInItsCheckout(t *testing.T) {
	ctx := lintTestProject(t, "")
	live := ctx.Project.Root()
	if err := os.Mkdir(filepath.Join(live, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	// The worktree: a .git file, the brief the branch adds.
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: "+filepath.Join(live, ".git", "worktrees", "x")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tree, "jobs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "jobs", "weekly.md"), []byte("# weekly\n"), 0644); err != nil {
		t.Fatal(err)
	}
	wf, projects := lintCandidateFiles(t, ctx, tree, "jobs/weekly.md")
	out := lintRun(t, ctx, "--workflow", wf, "--projects", projects, "--check", "departments")
	if strings.Contains(out, "does not exist") {
		t.Errorf("a brief the candidate's checkout holds was reported missing:\n%s", out)
	}
	assertContains(t, out, "✓ the candidate adds and removes no warnings")
	// Without the worktree's workflow file the root stays live: missing.
	out = lintRun(t, ctx, "--projects", projects, "--check", "departments")
	assertContains(t, out, "+ departments projects.yaml (Product): job z: brief jobs/weekly.md does not exist")

	err := runHealth(ctx, []string{"lint", "--workflow", wf, "--check", "shas", "--fix"})
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--fix writes the live board") {
		t.Errorf("--fix with --workflow: err = %v, want a usage error", err)
	}
}
