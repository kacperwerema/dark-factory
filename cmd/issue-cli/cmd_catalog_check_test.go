package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	for _, want := range []string{"✗ catalog: this branch changes internal/health/score.go", "entry health-tuning is v0.53.0; the next release is v0.54.0", "issue-cli workflow catalog-bump health-tuning (sets version: v0.54.0", "Catalog: n/a health-tuning <why>"} {
		assertContains(t, out, want)
	}

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
	if !strings.HasPrefix(out, "✗ catalog health-tuning: needs a bump or an n/a (changes internal/health/score.go)\n") {
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
