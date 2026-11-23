package privacy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
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
	t.Parallel()
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
	if got, want := strings.Join(res.Picked, " "), gitdir.ShortSHA(first)+" "+gitdir.ShortSHA(second); got != want {
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
	t.Parallel()
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
	for _, want := range []string{gitdir.ShortSHA(bad), "fix: main moved", "main.go", "release/v0.54 is as it was"} {
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
	t.Parallel()
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
// that needs an unpicked one is reported with the hint. Its go build runs
// with TestMain's GOTOOLCHAIN=local and GOFLAGS=-mod=mod.
func TestPickReportsABranchThatNoLongerBuilds(t *testing.T) {
	t.Parallel()
	r := newReleaseRepo(t)
	r.cfg = &tracker.PrivacyConfig{}
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
	t.Parallel()
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

// The v0.60.1 sequence: publish commits "vX is published" on main (only
// the changelog: a released heading, no ## Unreleased line), the fix's
// work branch is cut from main after it, and a pick names the move by sha
// beside the slug (the slug alone no longer lists it). Publish's move is
// skipped, so the branch keeps one ## v0.54.0 heading and the patch publishes.
func TestPickSkipsPublishsChangelogMove(t *testing.T) {
	t.Parallel()
	r := newReleaseRepo(t)
	if _, err := Setup(r.root, r.dev, r.cfg); err != nil {
		t.Fatal(err)
	}
	cut, err := Cut(r.root, "v0.54.0", "main", r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	if _, err := Publish(PublishInput{Root: r.root, Worktree: cut.Worktree, Cut: "v0.54.0", Cfg: r.cfg, Changelog: "CHANGELOG.md", Now: day}); err != nil {
		t.Fatal(err)
	}
	moved := run(t, r.root, "git", "rev-parse", "HEAD")
	if subj := run(t, r.root, "git", "log", "-1", "--format=%s", moved); subj != "docs(changelog): v0.54.0 is published" {
		t.Fatalf("main's head after publish = %q", subj)
	}
	r.land(t, "ui/patch", "patch.go", "package main // patch\n", "- fix P")

	res, err := Pick(r.root, "v0.54.1", []string{gitdir.ShortSHA(moved), "ui/patch"}, PickOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Picked) != 1 || len(res.Skipped) != 1 || res.Skipped[0] != gitdir.ShortSHA(moved) {
		t.Errorf("pick = %+v, want the fix picked and %s skipped", res, gitdir.ShortSHA(moved))
	}
	branchLog, _ := os.ReadFile(filepath.Join(cut.Worktree, "CHANGELOG.md"))
	if n := strings.Count(string(branchLog), "## v0.54.0"); n != 1 {
		t.Errorf("branch changelog has %d v0.54.0 headings:\n%s", n, branchLog)
	}
	if got := strings.Join(unreleasedLines(t, cut.Worktree), "|"); got != "- fix P" {
		t.Errorf("branch Unreleased = %q, want only the picked line", got)
	}

	if _, err := Cut(r.root, "v0.54.1", "main", r.cfg); err != nil {
		t.Fatal(err)
	}
	pub, err := Publish(PublishInput{Root: r.root, Worktree: cut.Worktree, Cut: "v0.54.1", Cfg: r.cfg, Changelog: "CHANGELOG.md", Now: day.Add(time.Hour), DryRun: true})
	if err != nil {
		t.Fatalf("patch publish dry run refused: %v", err)
	}
	if strings.TrimSpace(pub.Message) != "v0.54.1\n\n- fix P" {
		t.Errorf("patch message = %q", pub.Message)
	}

	// Picking the move by its sha skips it too.
	again, err := Pick(r.root, "v0.54.1", []string{moved}, PickOptions{})
	if err != nil || len(again.Picked) != 0 || len(again.Skipped) != 1 {
		t.Errorf("picking the move by sha = %+v, %v", again, err)
	}
}

// The v0.62.1 sequence: the changelog merges with merge=union (so a pick
// never conflicts on it), the release is published with its ## Unreleased
// renamed ## v0.54.0, and main lands a commit written before publish moved
// the released lines (its line goes above "- feature A" under
// ## Unreleased) plus one written after (in main's fresh ## Unreleased).
// Picking both puts their lines in one ## Unreleased on top, and the
// released section stays as at its tag. "tight" is the real changelog's
// layout (no blank line under ## Unreleased), where git's union merge
// wrote "## v0.62.0 — …" then a second "## Unreleased".
func TestPickUnderUnionMergeKeepsTheReleasedSection(t *testing.T) {
	t.Parallel()
	for _, layout := range []struct{ name, gap string }{{"blank", "\n\n"}, {"tight", "\n"}} {
		t.Run(layout.name, func(t *testing.T) {
			t.Parallel()
			r, cut := unionReleaseRepo(t, layout.gap)
			addLine := func(line string) {
				data, _ := os.ReadFile(filepath.Join(r.root, "CHANGELOG.md"))
				s := string(data)
				h := "## Unreleased" + layout.gap
				if !strings.Contains(s, h) {
					h = "## Unreleased\n\n"
				}
				write(t, filepath.Join(r.root, "CHANGELOG.md"), strings.Replace(s, h, h+line+"\n", 1))
			}
			// A work branch from main before publish: its line goes on
			// top of the lines v0.54.0 is about to release.
			run(t, r.root, "git", "checkout", "-q", "-b", "work/ui/early", "main")
			write(t, filepath.Join(r.root, "early.go"), "package main // early\n")
			addLine("- fix early")
			run(t, r.root, "git", "add", ".")
			run(t, r.root, "git", "commit", "-q", "-m", "fix: early\n\nRefs: ui/early")
			run(t, r.root, "git", "checkout", "-q", "main")
			day := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
			if _, err := Publish(PublishInput{Root: r.root, Worktree: cut, Cut: "v0.54.0", Cfg: r.cfg, Changelog: "CHANGELOG.md", Now: day}); err != nil {
				t.Fatal(err)
			}
			run(t, r.root, "git", "merge", "-q", "--no-ff", "work/ui/early", "-m", "chore(merge): land ui/early")
			run(t, r.root, "git", "branch", "-q", "-D", "work/ui/early")
			write(t, filepath.Join(r.root, "late.go"), "package main // late\n")
			addLine("- fix late")
			run(t, r.root, "git", "add", ".")
			run(t, r.root, "git", "commit", "-q", "-m", "fix: late\n\nRefs: ui/late")
			mainLog, _ := os.ReadFile(filepath.Join(r.root, "CHANGELOG.md"))
			if !strings.Contains(string(mainLog), "## v0.54.0 — 2026-11-15") || !strings.Contains(string(mainLog), "- fix late") {
				t.Fatalf("setup: main changelog:\n%s", mainLog)
			}
			tagged := run(t, r.root, "git", "show", "v0.54.0:CHANGELOG.md")

			res, err := Pick(r.root, "v0.54.1", []string{"ui/early", "ui/late"}, PickOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Picked) != 2 || !res.Changelog {
				t.Errorf("pick = %+v, want both commits, the changelog rewritten", res)
			}
			data, _ := os.ReadFile(filepath.Join(cut, "CHANGELOG.md"))
			branchLog := strings.TrimSpace(string(data))
			want := strings.TrimSpace(strings.Replace(tagged, "# Changelog\n\n", "# Changelog\n\n## Unreleased\n\n- fix late\n- fix early\n\n", 1))
			if branchLog != want {
				t.Errorf("branch changelog =\n%s\nwant\n%s", branchLog, want)
			}
			if st := run(t, cut, "git", "status", "--porcelain"); st != "" {
				t.Errorf("worktree left dirty: %q", st)
			}
		})
	}
}

// unionReleaseRepo is a release repo whose changelog merges with
// merge=union, its ## Unreleased lines gap below the heading, and
// release/v0.54 cut from it (with snapshots set up, so it can publish).
func unionReleaseRepo(t *testing.T, gap string) (releaseRepo, string) {
	t.Helper()
	r := newReleaseRepo(t)
	write(t, filepath.Join(r.root, ".gitattributes"), "CHANGELOG.md merge=union\n")
	write(t, filepath.Join(r.root, "CHANGELOG.md"), "# Changelog\n\n## Unreleased"+gap+"- feature A\n\n## v0.53.0 — 2026-10-23\n\n- old\n")
	r.commitMain(t, "", "", "", "chore: union-merge the changelog")
	if _, err := Setup(r.root, r.dev, r.cfg); err != nil {
		t.Fatal(err)
	}
	cut, err := Cut(r.root, "v0.54.0", "main", r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r, cut.Worktree
}

// A picked commit whose changelog line the branch already has (a fix
// applied by hand, then picked) commits nothing: it is reported as
// already on the branch, and the line is not written twice.
func TestPickWritesNoLineTheBranchAlreadyHas(t *testing.T) {
	t.Parallel()
	r, wt := pickRepo(t)
	write(t, filepath.Join(wt, "CHANGELOG.md"), strings.Replace(string(readFile(t, filepath.Join(wt, "CHANGELOG.md"))), "## Unreleased\n\n", "## Unreleased\n\n- fix B\n", 1))
	run(t, wt, "git", "commit", "-q", "-am", "docs(changelog): fix B by hand")
	path := filepath.Join(r.root, "CHANGELOG.md")
	write(t, path, strings.Replace(string(readFile(t, path)), "## Unreleased\n\n", "## Unreleased\n\n- fix B\n", 1))
	run(t, r.root, "git", "commit", "-q", "-m", "docs(changelog): fix B", "--", "CHANGELOG.md")
	fix := run(t, r.root, "git", "rev-parse", "HEAD")

	res, err := Pick(r.root, "v0.54.0", []string{fix}, PickOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Picked) != 0 || len(res.Already) != 1 {
		t.Errorf("pick = %+v, want the commit reported as already there", res)
	}
	if got := strings.Join(unreleasedLines(t, wt), "|"); got != "- fix B|- feature A" {
		t.Errorf("branch Unreleased = %q", got)
	}
}

// A branch whose changelog is already wrong (the stray ## Unreleased the
// v0.62.1 pick left under ## v0.54.0) fails the check after the next pick:
// the branch goes back where it was and the error names the lines.
func TestPickRefusesABranchWithAStrayUnreleased(t *testing.T) {
	t.Parallel()
	r, cut := unionReleaseRepo(t, "\n\n")
	day := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	if _, err := Publish(PublishInput{Root: r.root, Worktree: cut, Cut: "v0.54.0", Cfg: r.cfg, Changelog: "CHANGELOG.md", Now: day}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cut, "CHANGELOG.md")
	write(t, path, strings.Replace(string(readFile(t, path)), "## v0.54.0 — 2026-11-15\n", "## v0.54.0 — 2026-11-15\n## Unreleased\n- stray\n", 1))
	run(t, cut, "git", "commit", "-q", "-am", "docs(changelog): a stray heading")
	before := run(t, cut, "git", "rev-parse", "HEAD")
	fix := r.commitMain(t, "fix.go", "package main // fix\n", "- fix P", "fix: P")

	_, err := Pick(r.root, "v0.54.1", []string{fix}, PickOptions{})
	if err == nil {
		t.Fatal("pick onto a branch with a stray ## Unreleased passed")
	}
	for _, want := range []string{"2 ## Unreleased headings (lines 3, 8)", `## v0.54.0 — 2026-11-15 changed from its release: dropped "- feature A"`, "nothing picked"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if now := run(t, cut, "git", "rev-parse", "HEAD"); now != before {
		t.Errorf("branch moved to %s, want %s", now, before)
	}
	if st := run(t, cut, "git", "status", "--porcelain"); st != "" {
		t.Errorf("worktree left dirty: %q", st)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// workBranchWithMainMerge makes work/ui/fix as a run leaves it: its own
// fix (landed on main with Refs: ui/fix), a guided-check commit dropped
// before the merge to main, and a merge of main carrying another issue's
// commit. It returns the fix's commit on main.
func (r releaseRepo) workBranchWithMainMerge(t *testing.T) string {
	t.Helper()
	run(t, r.root, "git", "checkout", "-q", "-b", "work/ui/fix")
	r.commitMain(t, "check.sh", "echo check\n", "", "test(checks): guided check\n\nRefs: ui/fix")
	run(t, r.root, "git", "checkout", "-q", "main")
	r.commitMain(t, "other.go", "package main // other\n", "", "feat: other\n\nRefs: ui/other")
	run(t, r.root, "git", "checkout", "-q", "work/ui/fix")
	run(t, r.root, "git", "merge", "-q", "--no-ff", "main", "-m", "chore(merge): main into ui/fix")
	run(t, r.root, "git", "checkout", "-q", "main")
	return r.commitMain(t, "fix.go", "package main // fix\n", "", "fix: the fix\n\nRefs: ui/fix")
}

// A done issue whose work branch merged main (another issue's commit on
// it) and kept a check commit main never got: pick takes only the issue's
// Refs: commits on main, not the work branch, and the check then counts
// nothing unpicked.
func TestPickPrefersRefsOnMainOverAWorkBranch(t *testing.T) {
	t.Parallel()
	r, wt := pickRepo(t)
	fix := r.workBranchWithMainMerge(t)
	before, err := UnpickedIssues(r.root, "release/v0.54", "main", []string{"ui/fix"})
	if err != nil || strings.Join(before["ui/fix"], " ") != fix {
		t.Fatalf("before the pick, unpicked = %v, %v; want only %s", before, err, fix)
	}
	res, err := Pick(r.root, "v0.54.0", []string{"ui/fix"}, PickOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Picked, " "); got != gitdir.ShortSHA(fix) {
		t.Errorf("picked %q, want only the Refs commit %s", got, gitdir.ShortSHA(fix))
	}
	for _, f := range []string{"other.go", "check.sh"} {
		if _, err := os.Stat(filepath.Join(wt, f)); err == nil {
			t.Errorf("%s reached the release branch from work/ui/fix", f)
		}
	}
	after, err := UnpickedIssues(r.root, "release/v0.54", "main", []string{"ui/fix"})
	if err != nil || len(after["ui/fix"]) != 0 {
		t.Errorf("after the pick, unpicked = %v, %v; want none", after, err)
	}
	if _, ok := after["ui/fix"]; !ok {
		t.Error("a found issue with nothing left to pick is missing from the map")
	}
}

// Without a Refs: commit on main, the work branch is still read.
func TestUnpickedIssuesFallsBackToTheWorkBranch(t *testing.T) {
	t.Parallel()
	r, _ := pickRepo(t)
	run(t, r.root, "git", "checkout", "-q", "-b", "work/ui/wip")
	wip := r.commitMain(t, "wip.go", "package main // wip\n", "", "fix: wip")
	run(t, r.root, "git", "checkout", "-q", "main")
	got, err := UnpickedIssues(r.root, "release/v0.54", "main", []string{"ui/wip", "ui/none"})
	if err != nil || strings.Join(got["ui/wip"], " ") != wip {
		t.Errorf("unpicked = %v, %v; want work/ui/wip's %s", got, err, wip)
	}
	if _, ok := got["ui/none"]; ok {
		t.Errorf("a slug with neither Refs commits nor a branch is in the map: %v", got)
	}
}

// agentMsg is a commit message as agents write it: the Refs: line in its
// own paragraph before Co-Authored-By, so git's %(trailers) (the last
// paragraph only) never sees it.
func agentMsg(subject, slug string) string {
	return subject + "\n\nWhy it changed.\n\nRefs: " + slug + "\n\nCo-Authored-By: Agent <agent@example.com>"
}

// agentWorkBranch is work/ui/fix with the issue's two commits, a guided
// check script and its drop (all naming the slug as agents write it), and
// a merge of main that brought two other issues' commits onto it.
func (r releaseRepo) agentWorkBranch(t *testing.T) (own []string) {
	t.Helper()
	run(t, r.root, "git", "checkout", "-q", "-b", "work/ui/fix")
	own = append(own, r.commitMain(t, "fix.go", "package main // fix\n", "", agentMsg("fix: the fix", "ui/fix")))
	r.commitMain(t, "scripts/checks/fix-repro.sh", "echo check\n", "", agentMsg("test(checks): guided check", "ui/fix"))
	run(t, r.root, "git", "checkout", "-q", "main")
	r.commitMain(t, "other.go", "package main // other\n", "", agentMsg("feat: other", "ui/other"))
	r.commitMain(t, "more.go", "package main // more\n", "", agentMsg("feat: more", "ui/more"))
	run(t, r.root, "git", "checkout", "-q", "work/ui/fix")
	run(t, r.root, "git", "merge", "-q", "--no-ff", "main", "-m", "chore(merge): main into ui/fix")
	own = append(own, r.commitMain(t, "fix_test.go", "package main // test\n", "", agentMsg("test: the fix's test", "ui/fix")))
	run(t, r.root, "git", "rm", "-q", "scripts/checks/fix-repro.sh")
	run(t, r.root, "git", "commit", "-q", "-m", agentMsg("chore(checks): drop the check script before the merge", "ui/fix"))
	run(t, r.root, "git", "checkout", "-q", "main")
	return own
}

func shortJoin(shas []string) string {
	var out []string
	for _, s := range shas {
		out = append(out, gitdir.ShortSHA(s))
	}
	return strings.Join(out, " ")
}

// A work branch that merged main with two other issues' commits, its
// Refs: lines in their own paragraph: pick <slug> picks only the slug's 2
// commits, before the issue lands (from work/<slug>) and after (from main,
// the work branch still there), never the merge, main's commits or the
// guided check script and its drop.
func TestPickTakesOnlyTheSlugsCommitsFromAWorkBranchThatMergedMain(t *testing.T) {
	t.Parallel()
	for _, landed := range []bool{false, true} {
		name := "before landing"
		if landed {
			name = "landed, work branch kept"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, wt := pickRepo(t)
			own := r.agentWorkBranch(t)
			if landed {
				run(t, r.root, "git", "merge", "-q", "--ff-only", "work/ui/fix")
			}
			var out strings.Builder
			res, err := Pick(r.root, "v0.54.0", []string{"ui/fix"}, PickOptions{Out: &out})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := strings.Join(res.Picked, " "), shortJoin(own); got != want {
				t.Errorf("picked %q, want only the slug's 2 commits %q", got, want)
			}
			for _, f := range []string{"other.go", "more.go", "scripts/checks/fix-repro.sh"} {
				if _, err := os.Stat(filepath.Join(wt, f)); err == nil {
					t.Errorf("%s reached the release branch", f)
				}
			}
			for _, s := range own {
				if !strings.Contains(out.String(), gitdir.ShortSHA(s)) {
					t.Errorf("the list before the pick lacks %s:\n%s", gitdir.ShortSHA(s), out.String())
				}
			}
		})
	}
}

// A commit asked for under a slug that does not name it is refused before
// anything is picked, by sha.
func TestPickRefusesACommitThatDoesNotNameTheSlug(t *testing.T) {
	t.Parallel()
	r, _ := pickRepo(t)
	fix := r.commitMain(t, "fix.go", "package main // fix\n", "", agentMsg("fix: the fix", "ui/fix"))
	other := r.commitMain(t, "other.go", "package main // other\n", "", agentMsg("feat: other", "ui/other"))
	extras := foreignCommits(r.root, "main", "ui/fix", []string{fix, other})
	if strings.Join(extras, " ") != other {
		t.Errorf("extras = %v, want only %s", extras, other)
	}
	err := refuseForeign(r.root, "main", "ui/fix", []string{fix, other})
	if err == nil || !strings.Contains(err.Error(), gitdir.ShortSHA(other)) || !strings.Contains(err.Error(), "feat: other") {
		t.Errorf("refusal = %v, want it to name %s and its subject", err, gitdir.ShortSHA(other))
	}
}
