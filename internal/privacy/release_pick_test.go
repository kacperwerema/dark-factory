package privacy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// pickRepo is a release repo (no snapshots: nothing is pushed) with
// release/v0.54 cut from main and its worktree.
func pickRepo(t *testing.T) (releaseRepo, string) {
	t.Helper()
	r := newReleaseRepo(t)
	r.cfg = &tracker.PrivacyConfig{}
	cut, err := Cut(r.root, "v0.54.0", "main", r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r, cut.Worktree
}

// commitMain commits file (and a changelog line under ## Unreleased) on
// main with msg, and returns the commit.
func (r releaseRepo) commitMain(t *testing.T, file, text, changelogLine, msg string) string {
	t.Helper()
	if file != "" {
		write(t, filepath.Join(r.root, file), text)
	}
	if changelogLine != "" {
		data, _ := os.ReadFile(filepath.Join(r.root, "CHANGELOG.md"))
		s := strings.Replace(string(data), "## Unreleased\n\n", "## Unreleased\n\n"+changelogLine+"\n", 1)
		write(t, filepath.Join(r.root, "CHANGELOG.md"), s)
	}
	run(t, r.root, "git", "add", ".")
	run(t, r.root, "git", "commit", "-q", "-m", msg)
	return run(t, r.root, "git", "rev-parse", "HEAD")
}

func unreleasedLines(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	s, e, ok := section(string(data), func(h string) bool { return unreleasedRe.MatchString(h) })
	if !ok {
		t.Fatalf("no ## Unreleased in %s", data)
	}
	var out []string
	for _, l := range strings.Split(string(data[s:e]), "\n")[1:] {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// A done issue's work branch is gone: its commits are found on main by
// their Refs: trailer, oldest first, merges and other issues' commits
// skipped.
func TestPickFindsADoneIssuesCommitsByRefs(t *testing.T) {
	r, wt := pickRepo(t)
	first := r.commitMain(t, "fix.go", "package main // fix 1\n", "", "fix: part one\n\nRefs: ui/fix")
	r.commitMain(t, "other.go", "package main // other\n", "", "feat: other\n\nRefs: ui/fix-more")
	second := r.commitMain(t, "fix2.go", "package main // fix 2\n", "", "test: part two\n\nProves: AC1\nRefs: ui/fix")
	// A merge commit that names the issue too is skipped.
	run(t, r.root, "git", "checkout", "-q", "-b", "side", "HEAD~1")
	r.commitMain(t, "side.go", "package main // side\n", "", "feat: side")
	run(t, r.root, "git", "checkout", "-q", "main")
	run(t, r.root, "git", "merge", "-q", "--no-ff", "side", "-m", "chore(merge): side\n\nRefs: ui/fix")

	res, err := Pick(r.root, "v0.54.0", []string{"ui/fix"}, PickOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(res.Picked, " "), short(first)+" "+short(second); got != want {
		t.Errorf("picked %q, want %q (oldest first)", got, want)
	}
	if _, err := os.Stat(filepath.Join(wt, "other.go")); err == nil {
		t.Error("another issue's commit reached the branch")
	}
	if _, err := os.Stat(filepath.Join(wt, "side.go")); err == nil {
		t.Error("the merge reached the branch")
	}
	again, err := Pick(r.root, "v0.54.0", []string{"ui/fix"}, PickOptions{})
	if err != nil || len(again.Picked) != 0 || len(again.Already) != 1 {
		t.Errorf("second pick = %+v, %v", again, err)
	}
	if _, err := Pick(r.root, "v0.54.0", []string{"ui/none"}, PickOptions{}); err == nil || !strings.Contains(err.Error(), "Refs: ui/none") {
		t.Errorf("an unknown slug = %v", err)
	}
}

// A conflict names the commit and the file, and leaves the branch at its
// tip from before the call, even when an earlier commit of the same call
// applied.
func TestPickConflictNamesCommitAndFileAndKeepsTheBranch(t *testing.T) {
	r, wt := pickRepo(t)
	write(t, filepath.Join(wt, "main.go"), "package main // release only\n")
	run(t, wt, "git", "commit", "-q", "-am", "fix: release only")
	before := run(t, wt, "git", "rev-parse", "HEAD")

	ok := r.commitMain(t, "fine.go", "package main // fine\n", "", "fix: fine")
	bad := r.commitMain(t, "main.go", "package main // main moved\n", "", "fix: main moved")

	_, err := Pick(r.root, "v0.54.0", []string{ok, bad}, PickOptions{})
	if err == nil {
		t.Fatal("a conflicting pick passed")
	}
	for _, want := range []string{short(bad), "fix: main moved", "main.go", "release/v0.54 is as it was"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "hint:") {
		t.Errorf("error carries git's hint text: %q", err)
	}
	if after := run(t, wt, "git", "rev-parse", "HEAD"); after != before {
		t.Errorf("branch moved %s → %s", before, after)
	}
	if st := run(t, wt, "git", "status", "--porcelain"); st != "" {
		t.Errorf("worktree left dirty: %q", st)
	}
}

// Main's ## Unreleased has a line the release lacks (routing); the picked
// fix adds its own line next to it. The branch's Unreleased gets the fix's
// line and nothing else from main.
func TestPickKeepsTheChangelogToThePickedLines(t *testing.T) {
	r, wt := pickRepo(t)
	r.commitMain(t, "routing.go", "package main // routing\n", "- routing", "feat: routing")
	r.commitMain(t, "memory.go", "package main // memory\n", "- memory", "feat: memory")
	r.commitMain(t, "fix.go", "package main // fix\n", "- fix B", "fix: B\n\nRefs: ui/fix")

	res, err := Pick(r.root, "v0.54.0", []string{"ui/fix"}, PickOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Picked) != 1 || !res.Changelog {
		t.Errorf("pick = %+v, want one commit with its changelog conflict resolved", res)
	}
	if got, want := strings.Join(unreleasedLines(t, wt), "|"), "- fix B|- feature A"; got != want {
		t.Errorf("branch Unreleased = %q, want %q", got, want)
	}
	if st := run(t, wt, "git", "status", "--porcelain"); st != "" {
		t.Errorf("worktree left dirty: %q", st)
	}
}

// After a pick the release worktree is built and vetted; a picked commit
// that needs an unpicked one is reported with the hint.
func TestPickReportsABranchThatNoLongerBuilds(t *testing.T) {
	r := newReleaseRepo(t)
	r.cfg = &tracker.PrivacyConfig{}
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFLAGS", "-mod=mod")
	write(t, filepath.Join(r.root, "go.mod"), "module example.com/rel\n\ngo 1.21\n")
	write(t, filepath.Join(r.root, "main.go"), "package main\n\nfunc main() {}\n")
	r.commitMain(t, "", "", "", "build: go.mod")
	if _, err := Cut(r.root, "v0.54.0", "main", r.cfg); err != nil {
		t.Fatal(err)
	}
	helper := r.commitMain(t, "helper.go", "package main\n\nfunc helper() int { return 1 }\n", "", "feat: helper")
	use := r.commitMain(t, "use.go", "package main\n\nvar used = helper()\n", "", "fix: use the helper")

	res, err := Pick(r.root, "v0.54.0", []string{use}, PickOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.BuildFailed == "" || !strings.Contains(res.BuildFailed, "helper") {
		t.Fatalf("build failure not reported: %+v", res)
	}
	hint := res.BuildHint()
	if !strings.Contains(hint, "issue-cli release pick") {
		t.Errorf("hint = %q", hint)
	}

	res, err = Pick(r.root, "v0.54.0", []string{helper}, PickOptions{})
	if err != nil || res.BuildFailed != "" || !res.Built {
		t.Errorf("after picking the helper = %+v, %v", res, err)
	}
}

// A patch release goes on the published release's branch: cut refuses it
// before the branch exists, a fix picked after the publish opens a fresh
// ## Unreleased (no main entries), the owner's hand-written ## v0.54.1
// heading is kept, and publish puts one more snapshot on public.
func TestPatchReleaseCutPickPublish(t *testing.T) {
	r := newReleaseRepo(t)
	if _, err := Setup(r.root, r.dev, r.cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := Cut(r.root, "v0.54.1", "main", r.cfg); err == nil || !strings.Contains(err.Error(), "Cut v0.54.0 first") {
		t.Fatalf("a patch cut before the release = %v", err)
	}
	cut, err := Cut(r.root, "v0.54.0", "main", r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if _, err := Publish(PublishInput{Root: r.root, Worktree: cut.Worktree, Cut: "v0.54.0", Cfg: r.cfg, Changelog: "CHANGELOG.md", Now: day}); err != nil {
		t.Fatal(err)
	}
	r.commitMain(t, "later.go", "package main // later\n", "- later C", "feat: later")
	r.commitMain(t, "patch.go", "package main // patch\n", "- fix P", "fix: P\n\nRefs: ui/patch")

	res, err := Pick(r.root, "v0.54.1", []string{"ui/patch"}, PickOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Picked) != 1 {
		t.Errorf("pick = %+v", res)
	}
	if got := strings.Join(unreleasedLines(t, cut.Worktree), "|"); got != "- fix P" {
		t.Errorf("branch Unreleased = %q, want only the picked line", got)
	}
	// The owner names the heading by hand before the candidate.
	data, _ := os.ReadFile(filepath.Join(cut.Worktree, "CHANGELOG.md"))
	write(t, filepath.Join(cut.Worktree, "CHANGELOG.md"), strings.Replace(string(data), "## Unreleased", "## v0.54.1 — 2026-10-31", 1))
	run(t, cut.Worktree, "git", "commit", "-q", "-am", "docs(changelog): v0.54.1")

	rc, err := Cut(r.root, "v0.54.1", "main", r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Created || rc.Branch != "release/v0.54" || rc.Tag != "v0.54.1-rc.1" {
		t.Errorf("patch cut = %+v", rc)
	}
	pub, err := Publish(PublishInput{Root: r.root, Worktree: cut.Worktree, Cut: "v0.54.1", Cfg: r.cfg, Changelog: "CHANGELOG.md", Now: day.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if msg := run(t, r.root, "git", "--git-dir="+r.public, "log", "-1", "--format=%B", "main"); msg != "v0.54.1\n\n- fix P" {
		t.Errorf("public head message = %q", msg)
	}
	if !IsSnapshot(r.root, pub.Parent) {
		t.Error("the patch snapshot does not go on the v0.54.0 snapshot")
	}
	branchLog, _ := os.ReadFile(filepath.Join(cut.Worktree, "CHANGELOG.md"))
	if n := strings.Count(string(branchLog), "## v0.54.1"); n != 1 {
		t.Errorf("branch changelog has %d v0.54.1 headings:\n%s", n, branchLog)
	}
	mainLog, _ := os.ReadFile(filepath.Join(r.root, "CHANGELOG.md"))
	if !strings.Contains(string(mainLog), "## Unreleased\n\n- later C\n\n## v0.54.1 — 2026-10-31\n\n- fix P\n") {
		t.Errorf("main changelog:\n%s", mainLog)
	}
}
