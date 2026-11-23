package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/privacy"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// stubNoRefs makes gitNoRefs return list for any branch.
func stubNoRefs(t *testing.T, list []privacy.NoRefsCommit) {
	t.Helper()
	orig := gitNoRefs
	gitNoRefs = func(string, string, string) []privacy.NoRefsCommit { return list }
	t.Cleanup(func() { gitNoRefs = orig })
}

// A release branch, and on main after it a .go fix with no Refs: line and
// a changelog-only commit: gitNoRefs (git itself) names the fix alone, and
// the card (both looks), status.md and release.json list it with its sha,
// subject and the command that picks it. It is no failing check: the
// release stays ready to cut.
func TestReleaseCardListsMainCommitsWithoutRefs(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		return strings.TrimSpace(runGitT(t, root, append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgSign=false"}, args...)...))
	}
	commit := func(file, text, msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", file)
		git("commit", "-q", "-m", msg)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	commit("a.go", "package a\n", "feat: A\n\nRefs: ui/a")
	commit("CHANGELOG.md", "# Changelog\n\n## Unreleased\n", "docs(changelog): start")
	git("branch", "release/v0.53")
	fix := commit("fix.go", "package a\n\nvar fixed = true\n", "test(cli): the fixture uses a placeholder home")
	commit("CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n- fixed\n", "docs(changelog): fixed")
	list := gitNoRefs(root, "release/v0.53", "CHANGELOG.md")
	if len(list) != 1 || list[0].SHA != fix {
		t.Fatalf("gitNoRefs = %+v; want only the .go fix %s", list, fix)
	}
	short, pick := list[0].Short(), "issue-cli release pick "+list[0].Short()

	proj := readyRCProject(t, rcBlock+"privacy:\n  code: snapshots\n  decided: x\n")
	stubCandidates(t, map[string]time.Time{"v0.53.0-rc.1": time.Now().Add(-5 * time.Hour)}, 1)
	stubReleaseBranch(t, nil)
	stubNoRefs(t, list)
	page, md := getStatusBoth(t, proj)
	wantAll(t, "page", page,
		`data-testid="status.release_wait.norefs"`,
		`Not on release/v0.53, no Refs: line · 1`,
		`data-testid="status.release_wait.norefs.commit.`+short+`"`,
		`the fixture uses a placeholder home`,
		pick,
		`data-testid="status.release_wait.state">ready to cut v0.53.0-rc.2</span>`,
	)
	wantNone(t, "page", page, `status.release_wait.check.norefs`)
	wantAll(t, "status.md", md, "Not on release/v0.53, no Refs: line", "    - "+short+" test(cli): the fixture uses a placeholder home: "+pick+"\n")
	report, _ := getReportBoth(t, proj)
	wantAll(t, "report", report, `data-testid="status.release_wait.norefs.commit.`+short+`"`, pick)

	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, body := getResp(t, ts.URL+"/p/"+proj.Slug+"/release.json")
	wantAll(t, "release.json", body, `"no_refs":[{"sha":"`+fix+`","subject":"test(cli): the fixture uses a placeholder home"}]`)
}

// The patch row lists them too: v0.65.3 was a patch.
func TestReleasePatchRowListsMainCommitsWithoutRefs(t *testing.T) {
	proj := readyRCProject(t, rcBlock+"privacy:\n  code: snapshots\n  decided: x\n")
	stubCandidates(t, map[string]time.Time{"v0.52.1-rc.1": time.Now().Add(-3 * time.Hour)}, 0)
	stubPatch(t, proj, "# Changelog\n\n## v0.52.1 — 2026-10-31\n\n- fix\n\n## v0.52.0\n", "8a847f5000")
	stubNoRefs(t, []privacy.NoRefsCommit{{SHA: "79b84240aaaabbbbccccddddeeeeffff00001111", Subject: "test(cli): fixture"}})
	page, md := getReportBoth(t, proj)
	wantAll(t, "page", page,
		`data-testid="status.release_wait.patch.norefs"`,
		`data-testid="status.release_wait.patch.norefs.commit.79b8424"`,
		`issue-cli release pick 79b8424`,
	)
	wantAll(t, "status.md", md, "- Patch v0.52.1 on release/v0.52", "    - 79b8424 test(cli): fixture: issue-cli release pick 79b8424\n")
}
