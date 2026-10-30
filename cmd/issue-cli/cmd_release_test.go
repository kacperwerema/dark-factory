package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/privacy"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

const snapshotsBlock = "privacy:\n  code: snapshots\n  decided: \"2026-10-24, owner: \\\"private dev\\\"\"\n"

// releaseViewer serves release.json with body.
func releaseViewer(t *testing.T, body string) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/release.json") {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestReleaseCutRefusesUnlessTheCardSaysReady(t *testing.T) {
	proj, _, _ := makePrivacyFixture(t, snapshotsBlock)
	proj.ViewerURL = releaseViewer(t, `{"milestone":{"version":"0.54","cut":"v0.54.0","rc":true,"checks":[{"key":"issues","text":"Every 0.54 issue done","detail":"","ok":false},{"key":"ci","text":"Local CI green","detail":"main red since abc","ok":false}]}}`)
	ctx, _, _ := newTestContext(proj, false)
	err := runRelease(ctx, []string{"cut", "v0.54.0"})
	if err == nil || !strings.Contains(err.Error(), "does not say ready to cut v0.54.0") || !strings.Contains(err.Error(), "✗ Local CI green: main red since abc") {
		t.Errorf("err = %v", err)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runRelease(ctx, []string{"cut", "v0.55.0"}); err == nil || !strings.Contains(err.Error(), "the milestone to cut is v0.54.0") {
		t.Errorf("wrong version = %v", err)
	}
}

func TestReleaseCutTagsTheCandidateOnTheBranch(t *testing.T) {
	proj, root, _ := makePrivacyFixture(t, "")
	proj.ViewerURL = releaseViewer(t, `{"milestone":{"version":"0.54","cut":"v0.54.0","rc":true,"ready":false,"cut_next":"v0.54.0-rc.1","checks":[]}}`)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runRelease(ctx, []string{"cut", "0.54.0"}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, ".worktrees", "release-v0.54")
	assertContains(t, stdout.String(), "✓ Created release/v0.54 from main, worktree "+wt)
	assertContains(t, stdout.String(), "✓ Tagged v0.54.0-rc.1 on release/v0.54 (local; never push a candidate tag)")
	if strings.Contains(stdout.String(), "Pushed") {
		t.Error("without code: snapshots the branch must not be pushed")
	}
	if got := gitOut(t, root, "rev-parse", "v0.54.0-rc.1^{commit}"); got != gitOut(t, root, "rev-parse", "release/v0.54") {
		t.Error("the candidate is not on the branch tip")
	}

	// pick finds the only release worktree by itself.
	gitIn(t, root, "checkout", "-q", "-b", "work/ui/a")
	os.WriteFile(filepath.Join(root, "fix.go"), []byte("package main\n"), 0o644)
	gitIn(t, root, "add", "fix.go")
	gitIn(t, root, "commit", "-q", "-m", "fix")
	gitIn(t, root, "checkout", "-q", "main")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runRelease(ctx, []string{"pick", "ui/a"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "✓ Picked 1 commit onto release/v0.54")
}

// Before release setup origin is still the public repo: cut refuses, says
// why and how to switch, and pushes nothing. After setup it pushes the branch
// to origin, now the dev repo.
func TestReleaseCutPushesOnlyAfterTheSwitch(t *testing.T) {
	proj, root, _ := makePrivacyFixture(t, snapshotsBlock)
	proj.ViewerURL = releaseViewer(t, `{"milestone":{"version":"0.54","cut":"v0.54.0","rc":true,"ready":false,"cut_next":"v0.54.0-rc.1","checks":[]}}`)
	base := filepath.Dir(root)
	public, dev := filepath.Join(base, "public.git"), filepath.Join(base, "dev.git")
	gitIn(t, base, "init", "-q", "--bare", "-b", "main", public)
	gitIn(t, base, "init", "-q", "--bare", "-b", "main", dev)
	gitIn(t, root, "remote", "add", "origin", public)
	gitIn(t, root, "push", "-q", "origin", "main")
	before := gitOut(t, root, "ls-remote", public)

	ctx, stdout, _ := newTestContext(proj, false)
	err := runRelease(ctx, []string{"cut", "v0.54.0"})
	if err == nil {
		t.Fatal("cut before the switch succeeded")
	}
	assertContains(t, err.Error(), "not cutting v0.54.0: privacy.code is snapshots, but there is no public remote, so origin ("+public+") may still be the public repo and nothing is pushed to it")
	assertContains(t, err.Error(), "issue-cli release setup --dev <private dev repo url>")
	if after := gitOut(t, root, "ls-remote", public); after != before {
		t.Errorf("origin (public) got refs: %q -> %q", before, after)
	}
	if gitOut(t, root, "branch", "--list", "release/*") != "" || gitOut(t, root, "tag", "-l") != "" {
		t.Error("the refused cut left a branch or tag")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout.String())
	}

	if _, err := privacy.Setup(root, dev, &tracker.PrivacyConfig{Code: "snapshots"}); err != nil {
		t.Fatal(err)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runRelease(ctx, []string{"cut", "v0.54.0"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "✓ Pushed release/v0.54 to origin (the dev repo)")
	assertContains(t, gitOut(t, root, "ls-remote", dev), "refs/heads/release/v0.54")
	if after := gitOut(t, root, "ls-remote", public); after != before {
		t.Errorf("public got refs after the switch: %q -> %q", before, after)
	}
}

func TestReleasePublishRefusals(t *testing.T) {
	proj, _, _ := makePrivacyFixture(t, "")
	ctx, _, _ := newTestContext(proj, false)
	if err := runRelease(ctx, []string{"publish", "v0.54.0"}); err == nil || !strings.Contains(err.Error(), "publishes by pushing main") {
		t.Errorf("without snapshots = %v", err)
	}
	if err := runRelease(ctx, []string{"setup", "--dev", "/x"}); err == nil || !strings.Contains(err.Error(), "the owner has not chosen private development") {
		t.Errorf("setup without snapshots = %v", err)
	}

	proj, _, _ = makePrivacyFixture(t, snapshotsBlock)
	proj.ViewerURL = releaseViewer(t, `{"milestone":{"version":"0.54","cut":"v0.54.0","rc":true,"soaking":false,"candidate":"v0.54.0-rc.1","checks":[{"key":"soaked","text":"Nothing picked onto release/v0.54 after v0.54.0-rc.1","detail":"1 commit since","ok":false}]}}`)
	ctx, _, _ = newTestContext(proj, false)
	err := runRelease(ctx, []string{"publish", "v0.54.0"})
	if err == nil || !strings.Contains(err.Error(), "does not say a v0.54.0 candidate is soaking") || !strings.Contains(err.Error(), "1 commit since") {
		t.Errorf("not soaking = %v", err)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runRelease(ctx, []string{"setup"}); err == nil || !strings.Contains(err.Error(), "needs --dev <url>") {
		t.Errorf("setup without --dev = %v", err)
	}
}

func TestReleasePublishDryRunShowsThePublicCommit(t *testing.T) {
	proj, root, _ := makePrivacyFixture(t, snapshotsBlock+"workbench:\n  changelog: CHANGELOG.md\n")
	os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte("# Changelog\n\n## Unreleased\n\n- feature A\n"), 0o644)
	gitIn(t, root, "add", "CHANGELOG.md")
	gitIn(t, root, "commit", "-q", "-m", "docs: changelog")
	public := filepath.Join(filepath.Dir(root), "public.git")
	gitIn(t, filepath.Dir(root), "init", "-q", "--bare", "-b", "main", public)
	gitIn(t, root, "remote", "add", "public", public)
	gitIn(t, root, "push", "-q", "public", "main")
	gitIn(t, root, "config", "issue-cli.publicbase", gitOut(t, root, "rev-parse", "main"))
	cut, err := privacy.Cut(root, "v0.54.0", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	prev, _ := os.Getwd()
	if err := os.Chdir(cut.Worktree); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	proj.ViewerURL = releaseViewer(t, `{"milestone":{"version":"0.54","cut":"v0.54.0","rc":true,"soaking":false,"candidate":"v0.54.0-rc.1","checks":[{"key":"soaked","text":"Soaked a day","detail":"2h so far","ok":false}]}}`)
	before := gitOut(t, root, "ls-remote", public)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runRelease(ctx, []string{"publish", "v0.54.0", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	assertContains(t, out, "Dry run: nothing was committed, tagged or pushed.")
	assertContains(t, out, "the old history recorded at setup: v0.54.0 goes on top of it")
	assertContains(t, out, "  | v0.54.0\n  | \n  | - feature A\n")
	assertContains(t, out, "CHANGELOG.md's ## Unreleased renamed to ## v0.54.0")
	assertContains(t, out, "The real run would refuse now:")
	assertContains(t, out, "2h so far")
	if after := gitOut(t, root, "ls-remote", public); after != before {
		t.Errorf("public moved: %q -> %q", before, after)
	}
	if gitOut(t, root, "tag", "-l", "v0.54.0") != "" {
		t.Error("the dry run tagged v0.54.0")
	}

	// The real run refuses on the card, and points at the dry run.
	ctx, _, _ = newTestContext(proj, false)
	if err := runRelease(ctx, []string{"publish", "v0.54.0"}); err == nil || !strings.Contains(err.Error(), "See what it would publish: issue-cli --project test release publish v0.54.0 --dry-run") {
		t.Errorf("real publish = %v", err)
	}
}
