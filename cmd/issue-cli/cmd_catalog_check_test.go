package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// catalogRepo makes a throwaway git repo shaped like issue-viewer: a catalog
// with one entry, a changelog with a release, and the entry's source file,
// committed on main.
func catalogRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(workflowupgrade.CatalogPath+"/health-tuning.yaml", "id: health-tuning\nversion: v0.53.0\ncategory: workbench\nsources: [\"internal/health/**\"]\n")
	write(workflowupgrade.ChangelogPath, "## Unreleased\n\n## v0.53.0 — 2026-10-23\n")
	write("internal/health/score.go", "package health\n")
	catalogRepoGit(t, dir, "init", "-q", "-b", "main")
	catalogRepoGit(t, dir, "add", "-A")
	catalogRepoGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func catalogRepoGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestCatalogCheckOnABranch(t *testing.T) {
	dir := catalogRepo(t)
	chdir(t, dir)
	proj := &tracker.Project{Name: "t", IssueDir: dir}

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runWorkflow(ctx, []string{"catalog-check"}); err != nil {
		t.Fatalf("on main: %v", err)
	}
	assertContains(t, stdout.String(), "skipped: not a work branch")

	catalogRepoGit(t, dir, "checkout", "-q", "-b", "work/x")
	if err := os.WriteFile(filepath.Join(dir, "internal/health/score.go"), []byte("package health\n\nconst x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	catalogRepoGit(t, dir, "commit", "-q", "-am", "change health")
	ctx, stdout, _ = newTestContext(proj, false)
	err := runCatalogCheck(ctx, nil)
	if err == nil {
		t.Fatalf("stale entry passed:\n%s", stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{"✗ catalog: this branch changes internal/health/score.go", "entry health-tuning is v0.53.0; the next release is v0.54.0", "issue-cli workflow catalog-bump health-tuning (sets version: v0.54.0", "\n    Catalog: n/a health-tuning <why>\n", "internal/health/score.go (matches internal/health/**)"} {
		assertContains(t, out, want)
	}
	assertContains(t, err.Error(), "counts only once committed")

	catalogRepoGit(t, dir, "commit", "-q", "--allow-empty", "-m", "note\n\nCatalog: n/a health-tuning only a constant")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runCatalogCheck(ctx, nil); err != nil {
		t.Fatalf("n/a trailer: %v\n%s", err, stdout.String())
	}
	assertContains(t, stdout.String(), "✓ catalog health-tuning: marked n/a: only a constant")

	ctx, stdout, _ = newTestContext(proj, true)
	if err := runCatalogCheck(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"state": "n/a"`) {
		t.Errorf("JSON: %s", stdout.String())
	}
}

func TestCatalogCheckOutsideTheRepo(t *testing.T) {
	dir := t.TempDir()
	catalogRepoGit(t, dir, "init", "-q")
	chdir(t, dir)
	ctx, _, _ := newTestContext(&tracker.Project{Name: "t", IssueDir: dir}, false)
	err := runCatalogCheck(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "dark-factory repository") {
		t.Fatalf("err = %v", err)
	}
}

func writeCatalogFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A work branch's worktree is checked even with --project naming the main
// checkout and before the branch has a commit: its uncommitted change counts.
func TestCatalogCheckInAWorktreeBeforeTheFirstCommit(t *testing.T) {
	dir := catalogRepo(t)
	wt := filepath.Join(realTempDir(t), "wt")
	catalogRepoGit(t, dir, "worktree", "add", "-q", "-b", "work/y", wt)
	chdir(t, wt)
	writeCatalogFile(t, wt, "internal/health/score.go", "package health\n\nconst y = 2\n")

	ctx, stdout, _ := newTestContext(&tracker.Project{Name: "t", IssueDir: dir}, false)
	if err := runWorkflow(ctx, []string{"catalog-check"}); err == nil {
		t.Fatalf("uncommitted change passed:\n%s", stdout.String())
	}
	out := stdout.String()
	if !strings.HasPrefix(out, "✗ catalog health-tuning: needs a bump or an n/a (changes internal/health/score.go (matches internal/health/**))\n") {
		t.Errorf("output doesn't start with the summary line:\n%s", out)
	}
	assertContains(t, out, "catalog-check failed: 1 of 1 entries and 0 of 0 templates need a bump or an n/a; 0 Catalog: lines mark nothing")
}

// A Catalog: line counts in a paragraph before Co-Authored-By and on a merge
// commit; a line that names no entry fails and shows the right form.
func TestCatalogCheckReadsTrailersAnywhere(t *testing.T) {
	dir := catalogRepo(t)
	chdir(t, dir)
	proj := &tracker.Project{Name: "t", IssueDir: dir}
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "work/z")
	writeCatalogFile(t, dir, "internal/health/score.go", "package health\n\nconst z = 3\n")
	catalogRepoGit(t, dir, "commit", "-q", "-am", "change health\n\nCatalog: n/a health-tuning a constant\n\nCo-Authored-By: t <t@t>")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCatalogCheck(ctx, nil); err != nil {
		t.Fatalf("trailer before Co-Authored-By: %v\n%s", err, stdout.String())
	}
	assertContains(t, stdout.String(), "✓ catalog health-tuning: marked n/a: a constant")

	// The same on a merge commit only.
	dir = catalogRepo(t)
	chdir(t, dir)
	proj = &tracker.Project{Name: "t", IssueDir: dir}
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "side")
	writeCatalogFile(t, dir, "internal/health/score.go", "package health\n\nconst s = 4\n")
	catalogRepoGit(t, dir, "commit", "-q", "-am", "side change")
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "work/m", "main")
	catalogRepoGit(t, dir, "merge", "-q", "--no-ff", "side", "-m", "chore(merge): side\n\nCatalog: n/a health-tuning from the side branch")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runCatalogCheck(ctx, nil); err != nil {
		t.Fatalf("trailer on a merge commit: %v\n%s", err, stdout.String())
	}

	// A free-text reason where the id goes.
	catalogRepoGit(t, dir, "commit", "-q", "--allow-empty", "-m", "note\n\nCatalog: n/a refactor only")
	ctx, stdout, _ = newTestContext(proj, false)
	err := runCatalogCheck(ctx, nil)
	if err == nil {
		t.Fatalf("bad trailer passed:\n%s", stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{`": no catalog entry "refactor"`, "the form is  Catalog: n/a <id>[,<id>...] <why>", "the entries this branch touches: health-tuning", "1 Catalog: lines mark nothing"} {
		assertContains(t, out, want)
	}
}

// Files main changed don't count while the branch merges main, the merge
// in progress included.
func TestCatalogCheckIgnoresFilesFromMain(t *testing.T) {
	dir := catalogRepo(t)
	chdir(t, dir)
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "work/n")
	writeCatalogFile(t, dir, "docs.md", "branch work\n")
	catalogRepoGit(t, dir, "add", "-A")
	catalogRepoGit(t, dir, "commit", "-q", "-m", "docs")
	catalogRepoGit(t, dir, "checkout", "-q", "main")
	writeCatalogFile(t, dir, "internal/health/score.go", "package health\n\nconst m = 5\n")
	catalogRepoGit(t, dir, "commit", "-q", "-am", "main moves on")
	catalogRepoGit(t, dir, "checkout", "-q", "work/n")
	catalogRepoGit(t, dir, "merge", "-q", "--no-commit", "--no-ff", "main")

	ctx, stdout, _ := newTestContext(&tracker.Project{Name: "t", IssueDir: dir}, false)
	if err := runCatalogCheck(ctx, nil); err != nil {
		t.Fatalf("main's file flagged during the merge: %v\n%s", err, stdout.String())
	}
	assertContains(t, stdout.String(), "catalog-check ok: this branch changes no catalog feature's sources")
}

// On a release/vX.Y branch only the commits after its newest release or
// candidate tag count: a published commit's bad Catalog: line and its files
// can't be reworded, and were checked when they were tagged.
func TestCatalogCheckOnAReleaseBranchReadsSinceItsLastTag(t *testing.T) {
	dir := catalogRepo(t)
	chdir(t, dir)
	proj := &tracker.Project{Name: "t", IssueDir: dir}
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "release/v0.53")
	writeCatalogFile(t, dir, "internal/health/score.go", "package health\n\nconst r = 1\n")
	catalogRepoGit(t, dir, "commit", "-q", "-am", "fix\n\nCatalog: n/a")
	catalogRepoGit(t, dir, "tag", "v0.53.0-rc.2")
	// A tag of another line on the branch doesn't count.
	catalogRepoGit(t, dir, "commit", "-q", "--allow-empty", "-m", "unrelated")
	catalogRepoGit(t, dir, "tag", "v0.52.9")

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCatalogCheck(ctx, nil); err != nil {
		t.Fatalf("tagged commit still read: %v\n%s", err, stdout.String())
	}
	assertContains(t, stdout.String(), "catalog-check ok: this branch changes no catalog feature's sources")

	// A patch after the tag is checked like any branch commit.
	writeCatalogFile(t, dir, "internal/health/score.go", "package health\n\nconst r = 2\n")
	catalogRepoGit(t, dir, "commit", "-q", "-am", "patch")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runCatalogCheck(ctx, nil); err == nil {
		t.Fatalf("stale patch after the tag passed:\n%s", stdout.String())
	}
	assertContains(t, stdout.String(), "✗ catalog health-tuning: needs a bump or an n/a")
	catalogRepoGit(t, dir, "commit", "-q", "--allow-empty", "-m", "note\n\nCatalog: n/a health-tuning a fix")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runCatalogCheck(ctx, nil); err != nil {
		t.Fatalf("n/a after the tag: %v\n%s", err, stdout.String())
	}

	// A release branch with no tag of its line yet keeps the merge-base.
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "release/v0.54", "main")
	catalogRepoGit(t, dir, "commit", "-q", "--allow-empty", "-m", "cut\n\nCatalog: n/a")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runCatalogCheck(ctx, nil); err == nil {
		t.Fatalf("untagged release branch skipped its commits:\n%s", stdout.String())
	}

	// A work branch reads from the merge-base even when main carries a tag.
	catalogRepoGit(t, dir, "checkout", "-q", "main")
	catalogRepoGit(t, dir, "tag", "v0.53.0")
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "work/r")
	catalogRepoGit(t, dir, "commit", "-q", "--allow-empty", "-m", "w\n\nCatalog: n/a")
	catalogRepoGit(t, dir, "tag", "v0.53.1")
	catalogRepoGit(t, dir, "commit", "-q", "--allow-empty", "-m", "w2")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runCatalogCheck(ctx, nil); err == nil {
		t.Fatalf("work branch stopped at a tag:\n%s", stdout.String())
	}
}

// The retro's case (20261031-202918): main moves on with a change to an
// entry's sources and its own Catalog: line after the branch was cut; the
// branch has no commits of its own and isn't blamed for main's: the check
// diffs against the merge base, not main's tip.
func TestCatalogCheckOnABranchBehindMain(t *testing.T) {
	dir := catalogRepo(t)
	chdir(t, dir)
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "work/behind")
	catalogRepoGit(t, dir, "checkout", "-q", "main")
	writeCatalogFile(t, dir, "internal/health/score.go", "package health\n\nconst m = 5\n")
	writeCatalogFile(t, dir, "internal/health/new.go", "package health\n\nconst n = 6\n")
	catalogRepoGit(t, dir, "add", "-A")
	catalogRepoGit(t, dir, "commit", "-q", "-m", "main moves on\n\nCatalog: n/a health-tuning refactor")
	catalogRepoGit(t, dir, "checkout", "-q", "work/behind")

	ctx, stdout, _ := newTestContext(&tracker.Project{Name: "t", IssueDir: dir}, true)
	if err := runCatalogCheck(ctx, nil); err != nil {
		t.Fatalf("a branch behind main was blamed for main's commits: %v\n%s", err, stdout.String())
	}
	var rep workflowupgrade.CheckReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if len(rep.Entries) != 0 || len(rep.Templates) != 0 || len(rep.BadTrailers) != 0 {
		t.Errorf("findings on a branch with no commits of its own: %+v", rep)
	}
}

// A commit that only moves an entry's source file, content unchanged,
// passes with no Catalog: line; a move that also edits it still asks.
func TestCatalogCheckPassesAPureRename(t *testing.T) {
	dir := catalogRepo(t)
	chdir(t, dir)
	proj := &tracker.Project{Name: "t", IssueDir: dir}
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "work/rename")
	catalogRepoGit(t, dir, "mv", "internal/health/score.go", "internal/health/scoring.go")
	catalogRepoGit(t, dir, "commit", "-q", "-m", "refactor: rename score.go")

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runCatalogCheck(ctx, nil); err != nil {
		t.Fatalf("pure rename failed: %v\n%s", err, stdout.String())
	}
	assertContains(t, stdout.String(), "catalog-check ok: this branch changes no catalog feature's sources")

	writeCatalogFile(t, dir, "internal/health/scoring.go", "package health\n\nconst z = 3\n")
	catalogRepoGit(t, dir, "commit", "-q", "-am", "edit after the move")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runCatalogCheck(ctx, nil); err == nil {
		t.Fatalf("rename with an edit passed:\n%s", stdout.String())
	}
	assertContains(t, stdout.String(), "✗ catalog health-tuning: needs a bump or an n/a")
}

// Several red entries end the output with one Catalog: n/a line naming
// every refactor-looking entry and one catalog-bump for the entry whose own
// symbol changed; each red entry names the source pattern it matched, and
// the clilog's record names the red entries.
func TestCatalogCheckEndsWithOneLineForEveryRedEntry(t *testing.T) {
	dir := catalogRepo(t)
	chdir(t, dir)
	writeCatalogFile(t, dir, workflowupgrade.CatalogPath+"/alpha.yaml", "id: alpha\nversion: v0.53.0\ncategory: workbench\nsources: [\"internal/health/*.go\"]\n")
	writeCatalogFile(t, dir, workflowupgrade.CatalogPath+"/beta.yaml", "id: beta\nversion: v0.53.0\ncategory: workbench\nsources: [\"internal/health/score.go#Score\"]\n")
	writeCatalogFile(t, dir, "internal/health/score.go", "package health\n\nfunc Score() int { return 1 }\n")
	catalogRepoGit(t, dir, "add", "-A")
	catalogRepoGit(t, dir, "commit", "-q", "-m", "more entries")
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "work/many")
	writeCatalogFile(t, dir, "internal/health/score.go", "package health\n\nfunc Score() int { return 2 }\n")

	logPath := filepath.Join(realTempDir(t), "clilog.jsonl")
	t.Setenv("ISSUE_CLI_LOG", logPath)
	ctx, stdout, _ := newTestContext(&tracker.Project{Name: "t", IssueDir: dir}, false)
	err := runCatalogCheck(ctx, nil)
	if err == nil {
		t.Fatalf("red entries passed:\n%s", stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"✗ catalog alpha: needs a bump or an n/a (changes internal/health/score.go (matches internal/health/*.go))",
		"✗ catalog beta: needs a bump or an n/a (changes internal/health/score.go (matches internal/health/score.go#Score))",
		"✗ catalog health-tuning: needs a bump or an n/a (changes internal/health/score.go (matches internal/health/**))",
		"→ looks like a feature change: issue-cli workflow catalog-bump beta ",
	} {
		assertContains(t, out, want)
	}
	if !strings.HasSuffix(out, "\n    Catalog: n/a alpha,health-tuning <why>\n") {
		t.Errorf("output doesn't end with one n/a line for every refactor-looking entry:\n%s", out)
	}
	if n := strings.Count(out, "Catalog: n/a "); n != 1 {
		t.Errorf("%d Catalog: n/a lines, want 1:\n%s", n, out)
	}
	if got := strings.Join(ctx.red, ","); got != "alpha,beta,health-tuning" {
		t.Errorf("ctx.red = %q", got)
	}

	newCallLog([]string{"workflow", "catalog-check"}, time.Now()).write(&invocation{ctx: ctx}, err, false)
	data, rerr := os.ReadFile(logPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	var rec struct {
		Red []string `json:"red"`
	}
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); jerr != nil {
		t.Fatalf("%v: %s", jerr, data)
	}
	if strings.Join(rec.Red, ",") != "alpha,beta,health-tuning" {
		t.Errorf("clilog red = %v (%s)", rec.Red, data)
	}
}

// Every red entry a broad glob matched is in the one n/a line, with no
// catalog-bump line.
func TestCatalogCheckAllRefactorEntriesShareOneLine(t *testing.T) {
	dir := catalogRepo(t)
	chdir(t, dir)
	writeCatalogFile(t, dir, workflowupgrade.CatalogPath+"/alpha.yaml", "id: alpha\nversion: v0.53.0\ncategory: workbench\nsources: [\"internal/health/*.go\"]\n")
	catalogRepoGit(t, dir, "add", "-A")
	catalogRepoGit(t, dir, "commit", "-q", "-m", "alpha")
	catalogRepoGit(t, dir, "checkout", "-q", "-b", "work/two")
	writeCatalogFile(t, dir, "internal/health/score.go", "package health\n\nconst two = 2\n")
	ctx, stdout, _ := newTestContext(&tracker.Project{Name: "t", IssueDir: dir}, false)
	if err := runCatalogCheck(ctx, nil); err == nil {
		t.Fatalf("red entries passed:\n%s", stdout.String())
	}
	out := stdout.String()
	if !strings.HasSuffix(out, "\n    Catalog: n/a alpha,health-tuning <why>\n") {
		t.Errorf("output doesn't end with one n/a line naming both:\n%s", out)
	}
	if strings.Contains(out, "looks like a feature change") {
		t.Errorf("a glob match reads as a feature change:\n%s", out)
	}
}
