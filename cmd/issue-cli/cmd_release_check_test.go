package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/privacy"
)

// stubReleaseMem makes mem-guard check say blocked or not.
func stubReleaseMem(t *testing.T, blocked bool) {
	t.Helper()
	orig := releaseMemCheck
	releaseMemCheck = func() (bool, bool, string) { return true, blocked, "used 80%" }
	t.Cleanup(func() { releaseMemCheck = orig })
}

// release check runs ci.run in the release worktree and records the commit
// that passed; a failure clears the record; a dirty tree or a blocked
// mem-guard runs nothing.
func TestReleaseCheckValidatesTheCandidate(t *testing.T) {
	// The command writes where it ran, so the test sees it ran in the worktree.
	proj, root, _ := makePrivacyFixture(t, "ci:\n  run:\n    - \"pwd > \\\"$RELEASE_CHECK_LOG\\\"; test ! -e fail\"\n")
	log := filepath.Join(realTempDir(t), "ran")
	t.Setenv("RELEASE_CHECK_LOG", log)
	stubReleaseMem(t, false)
	res, err := privacy.Cut(root, "v0.54.0", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runRelease(ctx, []string{"check", "v0.54.0"}); err != nil {
		t.Fatal(err)
	}
	tip := gitOut(t, root, "rev-parse", "release/v0.54")
	if got := privacy.ValidatedTip(root, "release/v0.54"); got != tip {
		t.Errorf("validated = %q, want the branch tip %q", got, tip)
	}
	if ran, _ := os.ReadFile(log); strings.TrimSpace(string(ran)) != res.Worktree {
		t.Errorf("ran in %q, want the release worktree %q", ran, res.Worktree)
	}
	assertContains(t, stdout.String(), "✓ Validated release/v0.54 at ")

	// Uncommitted changes: the tree is not the commit that ships.
	os.WriteFile(filepath.Join(res.Worktree, "main.go"), []byte("package main // dirty\n"), 0o644)
	ctx, _, _ = newTestContext(proj, false)
	if err := runRelease(ctx, []string{"check", "v0.54.0"}); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("dirty = %v", err)
	}
	gitIn(t, res.Worktree, "checkout", "--", "main.go")

	stubReleaseMem(t, true)
	ctx, _, _ = newTestContext(proj, false)
	if err := runRelease(ctx, []string{"check", "v0.54.0"}); err == nil || !strings.Contains(err.Error(), "mem-guard check is blocked") {
		t.Errorf("blocked = %v", err)
	}
	stubReleaseMem(t, false)

	os.WriteFile(filepath.Join(res.Worktree, "fail"), []byte("x"), 0o644)
	gitIn(t, res.Worktree, "add", "fail")
	gitIn(t, res.Worktree, "commit", "-q", "-m", "break")
	ctx, _, _ = newTestContext(proj, false)
	if err := runRelease(ctx, []string{"check", "v0.54.0"}); err == nil || !strings.Contains(err.Error(), "the candidate is not validated") {
		t.Errorf("failing check = %v", err)
	}
	if got := privacy.ValidatedTip(root, "release/v0.54"); got != "" {
		t.Errorf("a failed check left the record %q", got)
	}

	ctx, _, _ = newTestContext(proj, false)
	if err := runRelease(ctx, []string{"check", "v0.55.0"}); err == nil || !strings.Contains(err.Error(), "no release worktree") {
		t.Errorf("no worktree = %v", err)
	}
}

// cut names release check as the next step.
func TestReleaseCutSaysHowToValidate(t *testing.T) {
	proj, _, _ := makePrivacyFixture(t, "")
	proj.ViewerURL = releaseViewer(t, `{"milestone":{"version":"0.54","cut":"v0.54.0","rc":true,"ready":false,"cut_next":"v0.54.0-rc.1","checks":[]}}`)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runRelease(ctx, []string{"cut", "v0.54.0"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Validate the candidate (publish reads it): issue-cli release check v0.54.0 runs local CI's commands in the release worktree and records it with v0.54.0-rc.1.")
}
