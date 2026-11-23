package viewer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// getReportBoth is the Report (the new look) and status.md.
func getReportBoth(t *testing.T, proj tracker.Project) (page, md string) {
	t.Helper()
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	resp, page := getResp(t, ts.URL+"/p/"+proj.Slug+"/status")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d:\n%s", resp.StatusCode, page)
	}
	_, md = getResp(t, ts.URL+"/p/"+proj.Slug+"/status.md")
	return page, md
}

func TestNewestPatchCut(t *testing.T) {
	tags := []string{"v0.52.0", "v0.52.1-rc.1", "v0.52.1", "v0.52.2-rc.1", "v0.52.2-rc.2", "v0.53.0-rc.1", "v0.51.3-rc.1"}
	if got := newestPatchCut(tags); got != "v0.52.2" {
		t.Errorf("newestPatchCut = %q, want v0.52.2 (v0.52.1 is released, v0.53.0 is no patch)", got)
	}
	if got := newestPatchCut([]string{"v0.52.1-rc.1", "v0.52.1"}); got != "" {
		t.Errorf("a released patch = %q", got)
	}
	// One version is supported: v0.63.0 is out, so v0.62.1 is no candidate.
	if got := newestPatchCut([]string{"v0.62.0", "v0.62.1-rc.1", "v0.63.0"}); got != "" {
		t.Errorf("a patch older than the newest published release = %q, want none", got)
	}
}

// stubPatch makes the repo hold release/v0.52 with a v0.52.1 candidate, its
// changelog text and the tip the build check last passed.
func stubPatch(t *testing.T, proj tracker.Project, changelog, built string) {
	t.Helper()
	origCut, origHas, origFile, origTip, origBuilt := gitPatchCut, gitHasBranch, gitBranchFile, gitTip, releaseBuiltTip
	stubNoRefs(t, nil)
	gitPatchCut = func(string) string { return "v0.52.1" }
	gitHasBranch = func(_, branch string) bool { return branch == "release/v0.52" }
	gitBranchFile = func(_, branch, path string) string {
		if branch != "release/v0.52" || path != "CHANGELOG.md" {
			t.Errorf("changelog read from %s:%s", branch, path)
		}
		return changelog
	}
	gitTip = func(string, string) string { return "8a847f5000" }
	releaseBuiltTip = func(string, string) string { return built }
	t.Cleanup(func() {
		gitPatchCut, gitHasBranch, gitBranchFile, gitTip, releaseBuiltTip = origCut, origHas, origFile, origTip, origBuilt
	})
	wt := filepath.Join(ciRootFor(&proj), ".worktrees", "release-v0.52")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A patch candidate on the existing release branch shows as soaking with
// its own checks: CI on main's head, the branch's changelog heading, the
// build on the branch tip, nothing picked after it.
func TestReleasePatchCandidateSoaks(t *testing.T) {
	proj := readyRCProject(t, rcBlock+"privacy:\n  code: snapshots\n  decided: x\n")
	stubCandidates(t, map[string]time.Time{"v0.52.1-rc.1": time.Now().Add(-3 * time.Hour)}, 0)
	stubPatch(t, proj, "# Changelog\n\n## v0.52.1 — 2026-10-31\n\n- fix\n\n## v0.52.0\n", "8a847f5000")
	stubValidated(t, "8a847f5000", "8a847f5000")
	page, md := getReportBoth(t, proj)
	wantAll(t, "page", page,
		`data-testid="status.release_wait.patch.state">v0.52.1-rc.1 soaking</span>`,
		`data-testid="status.release_wait.patch.check.validated" data-ok="true"`,
		`data-testid="status.release_wait.patch.check.ci" data-ok="true"`,
		`data-testid="status.release_wait.patch.check.changelog" data-ok="true"`,
		`data-testid="status.release_wait.patch.check.build" data-ok="true"`,
		`data-testid="status.release_wait.patch.check.soaked" data-ok="true"`,
		`issue-cli release publish v0.52.1`,
	)
	wantAll(t, "status.md", md, "- Patch v0.52.1 on release/v0.52: v0.52.1-rc.1 soaking since")

	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, body := getResp(t, ts.URL+"/p/"+proj.Slug+"/release.json")
	wantAll(t, "release.json", body, `"patch":{"version":"0.52.1","cut":"v0.52.1","rc":true,"ready":true,"soaking":true,"candidate":"v0.52.1-rc.1","branch":"release/v0.52"`)
}

// Without a build of the tip and with nothing in the changelog, the patch
// candidate is not ready and says why.
func TestReleasePatchCandidateNotReady(t *testing.T) {
	proj := readyRCProject(t, rcBlock)
	stubCandidates(t, map[string]time.Time{"v0.52.1-rc.1": time.Now().Add(-3 * time.Hour)}, 0)
	stubPatch(t, proj, "# Changelog\n\n## Unreleased\n\n## v0.52.0\n", "")
	page, md := getReportBoth(t, proj)
	wantAll(t, "page", page,
		`data-testid="status.release_wait.patch.state">not ready</span>`,
		`data-testid="status.release_wait.patch.check.changelog" data-ok="false"`,
		`data-testid="status.release_wait.patch.check.build" data-ok="false"`,
		`issue-cli release cut v0.52.1 runs them first`,
	)
	wantAll(t, "status.md", md, "- Patch v0.52.1 on release/v0.52: not ready: nothing under ## Unreleased")
}

// stubValidated makes release check's record validated and every ref
// (the candidate tag, the branch tip) resolve to at.
func stubValidated(t *testing.T, validated, at string) {
	t.Helper()
	origV, origTip := releaseValidatedTip, gitTip
	releaseValidatedTip = func(string, string) string { return validated }
	gitTip = func(string, string) string { return at }
	t.Cleanup(func() { releaseValidatedTip, gitTip = origV, origTip })
}

// patchOnBusyMain is a soaked patch candidate whose every other check is
// met, with local CI in state st and main's head 96abff0 (never tested).
func patchOnBusyMain(t *testing.T, st *ci.State, validated string) tracker.Project {
	t.Helper()
	proj := readyRCProject(t, rcBlock+"privacy:\n  code: snapshots\n  decided: x\n")
	if err := ci.WriteState(ciRootFor(&proj), st); err != nil {
		t.Fatal(err)
	}
	stubBranchState(t, &BranchState{Branch: "main", Upstream: "origin/main", Head: "96abff0000", HeadAt: time.Now()})
	stubCandidates(t, map[string]time.Time{"v0.52.1-rc.1": time.Now().Add(-3 * time.Hour)}, 0)
	stubPatch(t, proj, "# Changelog\n\n## v0.52.1 — 2026-10-31\n\n- fix\n\n## v0.52.0\n", "8a847f5000")
	stubValidated(t, validated, "8a847f5000")
	return proj
}

// The repro: workers land on main every few minutes, so main's head is
// never one local CI has tested. A patch ships the release branch, so it
// soaks (and publish reads it as soaking) once the candidate is validated
// and main is not red.
func TestPatchRowAllowsUntestedMainHead(t *testing.T) {
	proj := patchOnBusyMain(t, &ci.State{Branch: "main", State: ci.Green, GreenSha: "6788873000", GreenAt: time.Now()}, "8a847f5000")
	page, _ := getReportBoth(t, proj)
	wantAll(t, "page", page,
		`data-testid="status.release_wait.patch.state">v0.52.1-rc.1 soaking</span>`,
		`data-testid="status.release_wait.patch.check.ci" data-ok="true"`,
		`Local CI not red on main`,
		`data-testid="status.release_wait.patch.check.validated" data-ok="true"`,
	)
	// The 0.53 milestone has no release branch yet: its row still reads
	// main's head (it would cut from there); only the patch row changed.
}

// A known break on main still stops the patch, with the reason.
func TestPatchRowRefusesWhileMainIsRed(t *testing.T) {
	proj := patchOnBusyMain(t, &ci.State{Branch: "main", State: ci.Red, RedSince: "25526c7000", Failed: "make validate", Updated: time.Now()}, "8a847f5000")
	page, md := getReportBoth(t, proj)
	wantAll(t, "page", page,
		`data-testid="status.release_wait.patch.state">not ready</span>`,
		`data-testid="status.release_wait.patch.check.ci" data-ok="false"`,
	)
	wantAll(t, "status.md", md, "- Patch v0.52.1 on release/v0.52: not ready: main red since 25526c7: make validate")
}

// A candidate nobody validated (or one validated at an older commit) is
// not soaking: publish refuses and names release check.
func TestPatchRowRefusesAnUnvalidatedCandidate(t *testing.T) {
	green := &ci.State{Branch: "main", State: ci.Green, GreenSha: "96abff0000", GreenAt: time.Now()}
	for name, tc := range map[string]struct{ validated, detail string }{
		"never": {"", "not validated: issue-cli release check v0.52.1 runs local CI"},
		"stale": {"1111111000", "validated at 1111111, not at v0.52.1-rc.1: issue-cli release check v0.52.1"},
	} {
		t.Run(name, func(t *testing.T) {
			proj := patchOnBusyMain(t, green, tc.validated)
			page, md := getReportBoth(t, proj)
			wantAll(t, "page", page,
				`data-testid="status.release_wait.patch.state">not ready</span>`,
				`data-testid="status.release_wait.patch.check.validated" data-ok="false"`,
			)
			wantAll(t, "status.md", md, "- Patch v0.52.1 on release/v0.52: not ready: "+tc.detail)
		})
	}
}
