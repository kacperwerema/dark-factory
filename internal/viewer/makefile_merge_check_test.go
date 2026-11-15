package viewer

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// mergeCheckRepo is a TempDir git repo on main holding the repo's Makefile
// and one clean file, committed.
func mergeCheckRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	makefile, err := os.ReadFile(repoPath("Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	dir := realTempDirRoot(t)
	affectedGit(t, dir, "init", "-q", "-b", "main")
	affectedGit(t, dir, "config", "maintenance.auto", "false")
	affectedGit(t, dir, "config", "gc.auto", "0")
	for name, body := range map[string]string{"Makefile": string(makefile), "notes.md": "# Notes\n\nNothing half merged.\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	affectedGit(t, dir, "add", ".")
	affectedGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// runValidate runs make validate with the given steps in dir: its stdout and
// stderr together, and whether it exited 0.
func runValidate(t *testing.T, dir string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command("make", append([]string{"--no-print-directory", "validate"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// A merge left open (MERGE_HEAD in the checkout's own git dir, a linked
// worktree's included) turns validate red at its first step.
func TestMergeCheckRefusesAnOpenMerge(t *testing.T) {
	dir := mergeCheckRepo(t)

	out, ok := runValidate(t, dir, "VALIDATE_STEPS=merge-check")
	if !ok || !strings.HasSuffix(strings.TrimSpace(out), "validate: PASS") {
		t.Fatalf("a clean repo: want validate: PASS:\n%s", out)
	}

	head := affectedGit(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, ".git", "MERGE_HEAD"), []byte(head+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok = runValidate(t, dir, "VALIDATE_STEPS=merge-check")
	if ok {
		t.Fatalf("MERGE_HEAD set: make validate exited 0:\n%s", out)
	}
	for _, want := range []string{"merge-check: unfinished merge", "validate: red so far: merge-check\n", "validate: FAIL (merge-check)"} {
		if !strings.Contains(out, want) {
			t.Errorf("MERGE_HEAD set: output lacks %q:\n%s", want, out)
		}
	}
	if err := os.Remove(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal(err)
	}

	// A linked worktree keeps its MERGE_HEAD in its own git dir, not the
	// shared one: the step reads the checkout's own.
	wt := filepath.Join(realTempDirRoot(t), "wt")
	affectedGit(t, dir, "worktree", "add", "-q", "-b", "work", wt)
	own := affectedGit(t, wt, "rev-parse", "--absolute-git-dir")
	if err := os.WriteFile(filepath.Join(own, "MERGE_HEAD"), []byte(head+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, ok := runValidate(t, wt, "VALIDATE_STEPS=merge-check"); ok || !strings.Contains(out, "validate: red so far: merge-check") {
		t.Errorf("MERGE_HEAD in the worktree's git dir: want red:\n%s", out)
	}
	if out, ok := runValidate(t, dir, "VALIDATE_STEPS=merge-check"); !ok {
		t.Errorf("the main checkout has no merge open, only the worktree: want green:\n%s", out)
	}
}

// A tracked file with a line that starts with a conflict marker turns the
// step red and is named file:line; a path on MERGE_CHECK_ALLOW is skipped.
func TestMergeCheckFindsConflictMarkers(t *testing.T) {
	dir := mergeCheckRepo(t)
	ours, theirs := strings.Repeat("<", 7), strings.Repeat(">", 7)
	conflicted := "# Notes\n\n" + ours + " HEAD\nmine\n" + strings.Repeat("=", 7) + "\ntheirs\n" + theirs + " main\n"
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "guide.md"), []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}
	// Untracked: the scan reads only what git tracks.
	if out, ok := runValidate(t, dir, "VALIDATE_STEPS=merge-check"); !ok {
		t.Fatalf("an untracked file with markers: want green:\n%s", out)
	}
	affectedGit(t, dir, "add", "docs/guide.md")

	out, ok := runValidate(t, dir, "VALIDATE_STEPS=merge-check")
	if ok {
		t.Fatalf("conflict markers: make validate exited 0:\n%s", out)
	}
	for _, want := range []string{"merge-check: conflict markers", "\ndocs/guide.md:3\n", "\ndocs/guide.md:7\n", "validate: FAIL (merge-check)"} {
		if !strings.Contains(out, want) {
			t.Errorf("conflict markers: output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "docs/guide.md:5") {
		t.Errorf("a ======= line alone is no marker (markdown underlines it):\n%s", out)
	}

	// A marker mid-line, or seven < with no space after, is text.
	if err := os.WriteFile(filepath.Join(dir, "docs", "guide.md"), []byte("see "+ours+" HEAD\n"+ours+"x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, ok := runValidate(t, dir, "VALIDATE_STEPS=merge-check"); !ok {
		t.Errorf("no line starts with a marker: want green:\n%s", out)
	}

	if err := os.WriteFile(filepath.Join(dir, "docs", "guide.md"), []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, ok := runValidate(t, dir, "VALIDATE_STEPS=merge-check", "MERGE_CHECK_ALLOW=docs/guide.md"); !ok {
		t.Errorf("the file is on MERGE_CHECK_ALLOW: want green:\n%s", out)
	}
}

// The step is cheap: under a second on this repo. On a loaded machine it is
// compared with a plain git grep timed in the same run.
func TestMergeCheckIsCheapOnThisRepo(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	timed := func(name string, args ...string) time.Duration {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repoPath()
		start := time.Now()
		if out, err := cmd.CombinedOutput(); err != nil && name == "make" {
			t.Fatalf("make merge-check on this repo is red:\n%s", out)
		}
		return time.Since(start)
	}
	took := timed("make", "--no-print-directory", "merge-check")
	if took < time.Second {
		return
	}
	plain := timed("git", "grep", "-n", "-I", "-E", "^no-such-line-anywhere$")
	if took > 3*plain+500*time.Millisecond {
		t.Errorf("make merge-check took %v (a plain git grep %v): want under 1s", took, plain)
	}
}

// CLAUDE.md's Validation paragraph names every VALIDATE_STEPS step, in the
// order they run, each as `make <step>` or (<step>).
func TestClaudeMdListsTheValidateSteps(t *testing.T) {
	makefile, err := os.ReadFile(repoPath("Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^VALIDATE_STEPS \?= (.*)$`).FindSubmatch(makefile)
	if m == nil {
		t.Fatal("no VALIDATE_STEPS ?= line in the Makefile")
	}
	doc, err := os.ReadFile(repoPath("CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(doc), "## Validation\n\n")
	if !ok {
		t.Fatal("CLAUDE.md has no ## Validation section")
	}
	para, _, _ := strings.Cut(after, "\n\n")
	at := 0
	for _, step := range strings.Fields(string(m[1])) {
		i := -1
		for _, form := range []string{"`make " + step + "`", "(" + step + ")", "(" + step + ";", "(" + step + ","} {
			if j := strings.Index(para[at:], form); j >= 0 && (i < 0 || j < i) {
				i = j
			}
		}
		if i < 0 {
			t.Errorf("CLAUDE.md's Validation paragraph doesn't name step %s as `make %s` or (%s) after the steps before it", step, step, step)
			continue
		}
		at += i
	}
}
