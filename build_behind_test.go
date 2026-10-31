package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// behindRepo is a sandbox engine repo on main: v0.1.0 tagged with one
// Unreleased entry, then a release of it and two commits each adding an
// entry. It returns the repo and the describe of each commit after the tag.
func behindRepo(t *testing.T) (root string, describes []string) {
	t.Helper()
	root = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		return strings.TrimSpace(runGitT(t, root, append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "tag.gpgSign=false", "-c", "commit.gpgSign=false"}, args...)...))
	}
	changelog := func(body, msg string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte("# Changelog\n\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "CHANGELOG.md")
		git("commit", "-q", "-m", msg)
		describes = append(describes, git("describe", "--tags", "--always"))
	}
	git("init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(root, "issues"), 0o755)
	changelog("## Unreleased\n\n- Old entry the build has\n", "first")
	git("tag", "-a", "v0.1.0", "-m", "v0.1.0")
	describes = nil
	// The old entry is released between the build and main: still not new.
	changelog("## Unreleased\n\n- New A: the review count\n  wrapped onto a second line\n\n## v0.1.0 — 2026-10-30\n\n- Old entry the build has\n", "a")
	changelog("## Unreleased\n\n- New A: the review count\n  wrapped onto a second line\n- New B\n\n## v0.1.0 — 2026-10-30\n\n- Old entry the build has\n", "b")
	return root, describes
}

func TestBuildBehindOnASandboxRepo(t *testing.T) {
	root, describes := behindRepo(t)

	r := buildBehind(root, "", "", "v0.1.0")
	if r == nil {
		t.Fatal("a build at the tag, two commits behind main: no card")
	}
	if r.Ahead != 2 || r.Branch != "main" || r.Installed != "v0.1.0" || len(r.Sha) != 7 {
		t.Fatalf("row = %+v", r)
	}
	want := []string{"New A: the review count wrapped onto a second line", "New B"}
	if strings.Join(r.Entries, "|") != strings.Join(want, "|") {
		t.Fatalf("entries = %q, want %q", r.Entries, want)
	}
	if r.Install != "cd "+root+" && make install" || r.Changelog != "CHANGELOG.md" {
		t.Fatalf("install = %q, changelog = %q", r.Install, r.Changelog)
	}

	// One commit behind, with uncommitted changes in the build: from its base.
	r = buildBehind(root, "main", "CHANGELOG.md", describes[0]+"-dirty")
	if r == nil || r.Ahead != 1 || strings.Join(r.Entries, "|") != "New B" {
		t.Fatalf("one behind = %+v", r)
	}

	// At main, a build git can't resolve, a dev build, a missing branch: nothing.
	for _, v := range []string{describes[1], describes[1] + "-dirty", "dev", "", "v9.9.9-3-gdeadbee", "--output=x"} {
		if r := buildBehind(root, "main", "", v); r != nil {
			t.Errorf("version %q: want no card, got %+v", v, r)
		}
	}
	if r := buildBehind(root, "trunk", "", "v0.1.0"); r != nil {
		t.Errorf("no such branch: want no card, got %+v", r)
	}
}

func TestBuildBehindWithoutAChangelog(t *testing.T) {
	root, _ := behindRepo(t)
	r := buildBehind(root, "", "docs/NEWS.md", "v0.1.0")
	if r == nil || r.Ahead != 2 || r.Changelog != "" || r.Entries != nil {
		t.Fatalf("no changelog at that path: want the count alone, got %+v", r)
	}
	var b strings.Builder
	behindMarkdown(&b, r)
	if strings.Contains(b.String(), "Unreleased") || !strings.Contains(b.String(), "make install") {
		t.Fatalf("status.md = %q", b.String())
	}
}

func TestStatusShowsTheBuildBehindMainOnTheEngineOnly(t *testing.T) {
	root, _ := behindRepo(t)
	orig := Version
	Version = "v0.1.0"
	t.Cleanup(func() { Version = orig })

	wfFile := filepath.Join(root, "workflow.yaml")
	if err := os.WriteFile(wfFile, []byte("statuses:\n  - name: \"idea\"\n  - name: \"done\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := tracker.Project{Name: "Engine", Slug: "eng", Repo: "michal-franc/ai-workflow-engine", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: wfFile}
	other := tracker.Project{Name: "Other", Slug: "other", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: wfFile}
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{engine, other})
	defer ts.Close()

	_, page := getResp(t, ts.URL+"/p/eng/status")
	_, md := getResp(t, ts.URL+"/p/eng/status.md")
	wantAll(t, "page", page,
		`data-testid="status.behind"`,
		`installed v0.1.0 · main is 2 commits ahead`,
		`<div class="wb-rel-entry">New A: the review count wrapped onto a second line</div>`,
		`<div class="wb-rel-entry">New B</div>`,
		`data-testid="status.behind.install">cd `+root+` &amp;&amp; make install</code>`,
	)
	wantAll(t, "status.md", md,
		"## Installed build behind main\n",
		"- installed v0.1.0 · main is 2 commits ahead\n",
		"  - New A: the review count wrapped onto a second line\n  - New B\n",
		"- To get them: cd "+root+" && make install, then restart the viewer",
	)

	// Not the engine's own repo: nothing, though the same repo is behind.
	_, page = getResp(t, ts.URL+"/p/other/status")
	_, md = getResp(t, ts.URL+"/p/other/status.md")
	wantNone(t, "other page", page, `status.behind`)
	wantNone(t, "other status.md", md, "Installed build behind")

	// At main: nothing on the engine either.
	Version = strings.TrimSpace(runGitT(t, root, "describe", "--tags", "--always"))
	_, page = getResp(t, ts.URL+"/p/eng/status")
	_, md = getResp(t, ts.URL+"/p/eng/status.md")
	wantNone(t, "page at main", page, `status.behind`)
	wantNone(t, "status.md at main", md, "Installed build behind")
}
