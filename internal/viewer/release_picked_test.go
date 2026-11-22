package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A pick whose changelog hunk was rewritten (release pick keeps only the
// branch's own ## Unreleased lines) has another patch id, so git cherry
// alone lists it as missing. Its "(cherry picked from commit <sha>)" line
// names the work-branch commit: the Every done fix check stays green.
func TestGitUnpickedRewrittenPick(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		return runGitT(t, root, append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgSign=false"}, args...)...)
	}
	write := func(file, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("a.go", "package a\n")
	write("CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n- feature A\n")
	git("add", ".")
	git("commit", "-q", "-m", "feat: A")
	git("branch", "release/v0.53")
	// Main moves on with an entry the branch never gets.
	write("CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n- feature B\n- feature A\n")
	git("commit", "-q", "-am", "feat: B")
	featB := strings.TrimSpace(git("rev-parse", "HEAD"))
	git("checkout", "-q", "-b", "work/ui/fix")
	write("a.go", "package a // fixed\n")
	write("CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n- the fix\n- feature B\n- feature A\n")
	git("commit", "-q", "-am", "fix: the fix\n\nRefs: ui/fix")
	fix := strings.TrimSpace(git("rev-parse", "HEAD"))

	if got := gitUnpicked(root, "release/v0.53", []string{"ui/fix"}); got["ui/fix"] != 1 {
		t.Fatalf("before the pick, unpicked = %v; want ui/fix: 1 (the fix; feat B is main's, on the work branch from its cut)", got)
	}

	// Feature B picked as it is (same patch), then the fix as release pick
	// does it: -x, the changelog hunk rewritten to the branch's own lines.
	git("checkout", "-q", "release/v0.53")
	git("cherry-pick", "-x", featB)
	write("CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n- feature A\n")
	git("commit", "-q", "-am", "chore: the branch keeps its own changelog")
	runGitT(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgSign=false", "cherry-pick", "-x", "--strategy-option=theirs", fix)
	write("CHANGELOG.md", "# Changelog\n\n## Unreleased\n\n- the fix\n- feature A\n")
	git("commit", "-q", "-a", "--amend", "--no-edit")
	if msg := git("log", "-1", "--format=%B"); !strings.Contains(msg, "(cherry picked from commit "+fix+")") {
		t.Fatalf("the pick has no -x line: %q", msg)
	}
	if cherry := git("cherry", "release/v0.53", "work/ui/fix"); !strings.Contains(cherry, "+ "+fix) {
		t.Fatalf("the repro needs git cherry to miss the pick, got %q", cherry)
	}

	if got := gitUnpicked(root, "release/v0.53", []string{"ui/fix"}); len(got) != 0 {
		t.Fatalf("after the pick, unpicked = %v; want none", got)
	}
	ms := &MilestoneRow{Version: "0.53", Branch: "release/v0.53"}
	ms.addPicked(root, []*tracker.Issue{{Slug: "ui/fix", Status: "done"}}, &canvasStatuses{final: "done"})
	if len(ms.Checks) != 1 || !ms.Checks[0].OK || len(ms.Unpicked) != 0 {
		t.Fatalf("picked check = %+v, unpicked %v; want green", ms.Checks, ms.Unpicked)
	}
}

// soakReportProject is the engine project with release candidates, rc.1 cut
// a day ago, and the given issue files (path → content) on its board.
func soakReportProject(t *testing.T, files map[string]string) tracker.Project {
	t.Helper()
	proj := readyRCProject(t, rcBlock)
	proj.Repo = "kacperwerema/dark-factory"
	for rel, content := range files {
		path := filepath.Join(proj.WorkDir, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stubCandidates(t, map[string]time.Time{"v0.53.0-rc.1": time.Now().Add(-26 * time.Hour)}, 0)
	return proj
}

// soakReport is an open cli-bug report from rc.1 in version, with body
// text and comments (JSON lines) after its report.
func soakReport(title, version, body string, comments ...string) string {
	s := "---\ntitle: \"" + title + "\"\nstatus: \"idea\"\nversion: \"" + version + "\"\nlabels:\n  - cli-bug\n---\n\n## Report\n\nBoom.\n\n- issue-cli: v0.53.0-rc.1 linux\n" + body
	if len(comments) > 0 {
		s += "\n<!-- issue-viewer-comments\n" + strings.Join(comments, "\n") + "\n-->\n"
	}
	return s
}

const leadMovedComment = `{"id":1,"block":0,"date":"2026-11-21T20:15:29+01:00","text":"lead: moved out of 0.53 to 0.54 on the owner's ok, lead chat: \"Ship the rest sooner\"","status":"open","source":"cli","author":"lead-rel"}`

// A report the owner moved out of the release (version past the
// milestone, the ok in a lead comment quoting the owner or a decision with
// the owner's words) doesn't hold the cut; the card lists it as moved out.
func TestSoakMovedOutReport(t *testing.T) {
	proj := soakReportProject(t, map[string]string{
		"issues/CLI/moved-by-lead.md": soakReport("Moved by lead", "0.54", "", leadMovedComment),
		"issues/CLI/moved-by-decision.md": soakReport("Moved by decision", "0.54",
			"\n## Decisions\n\n- **D-1** · 2026-11-21 · lead chat: moved out of 0.53 to 0.54, \"not in this one\"\n"),
	})
	page, md := getStatusBoth(t, proj)
	wantAll(t, "page", page,
		`data-testid="status.release_wait.check.reports" data-ok="true"`,
		`data-testid="status.release_wait.candidate.moved"`,
		`moved out: cli/moved-by-lead → 0.54`,
		`moved out: cli/moved-by-decision → 0.54`,
	)
	wantNone(t, "page", page, `candidate.report.cli/moved-by-lead`, `candidate.report.cli/moved-by-decision`)
	wantAll(t, "status.md", md, "- Moved out on the owner's ok: ")
	wantNone(t, "status.md", md, "Open reports from candidates")
}

// The same report still in the milestone holds the cut, and so does one
// moved with no owner's ok recorded (a bare version edit, a provisional
// decision): the check names what is missing.
func TestSoakReportNotMovedOutStillBlocks(t *testing.T) {
	for name, tc := range map[string]struct{ file, want string }{
		"still in 0.53": {soakReport("R", "0.53", "", leadMovedComment), "1 report open: cli/r\n"},
		"no ok":         {soakReport("R", "0.54", ""), "cli/r (version 0.54 without the owner's ok recorded"},
		"provisional": {soakReport("R", "0.54",
			"\n## Decisions\n\n- **D-1** · 2026-11-21 · YOLO, provisional: moved out to 0.54\n"), "cli/r (version 0.54 without the owner's ok recorded"},
	} {
		t.Run(name, func(t *testing.T) {
			proj := soakReportProject(t, map[string]string{"issues/CLI/r.md": tc.file})
			page, md := getStatusBoth(t, proj)
			wantAll(t, "page", page,
				`data-testid="status.release_wait.check.reports" data-ok="false"`,
				`data-testid="status.release_wait.candidate.report.cli/r"`,
			)
			wantNone(t, "page", page, `data-testid="status.release_wait.candidate.moved"`)
			wantAll(t, "status.md", md, strings.TrimSuffix(tc.want, "\n"))
		})
	}
}

// A done issue's work branch merged main (another issue's commit on it)
// and kept a guided-check commit dropped before its merge to main. The
// check reads the issue's Refs: commits on main: all picked is green
// whatever the work branch holds, one left unpicked is red.
func TestGitUnpickedReadsRefsOnMain(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		return strings.TrimSpace(runGitT(t, root, append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgSign=false"}, args...)...))
	}
	commit := func(file, msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, file), []byte(msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", file)
		git("commit", "-q", "-m", msg)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	commit("a.go", "feat: A")
	git("branch", "release/v0.53")
	git("checkout", "-q", "-b", "work/ui/fix")
	commit("check.sh", "test(checks): guided check\n\nRefs: ui/fix")
	git("checkout", "-q", "main")
	commit("other.go", "feat: other\n\nRefs: ui/other")
	git("checkout", "-q", "work/ui/fix")
	git("merge", "-q", "--no-ff", "main", "-m", "chore(merge): main into ui/fix")
	git("checkout", "-q", "main")
	fix1 := commit("fix1.go", "fix: part one\n\nRefs: ui/fix")
	fix2 := commit("fix2.go", "fix: part two\n\nRefs: ui/fix")

	check := func(want bool) {
		t.Helper()
		ms := &MilestoneRow{Version: "0.53", Branch: "release/v0.53"}
		ms.addPicked(root, []*tracker.Issue{{Slug: "ui/fix", Status: "done"}}, &canvasStatuses{final: "done"})
		if len(ms.Checks) != 1 || ms.Checks[0].OK != want {
			t.Fatalf("picked check = %+v, unpicked %v; want OK %v", ms.Checks, ms.Unpicked, want)
		}
	}
	git("checkout", "-q", "release/v0.53")
	git("cherry-pick", "-x", fix1)
	if got := gitUnpicked(root, "release/v0.53", []string{"ui/fix"}); got["ui/fix"] != 1 {
		t.Fatalf("one Refs commit left, unpicked = %v; want ui/fix: 1", got)
	}
	check(false)
	git("cherry-pick", "-x", fix2)
	if got := gitUnpicked(root, "release/v0.53", []string{"ui/fix"}); len(got) != 0 {
		t.Fatalf("all Refs commits picked, unpicked = %v; want none (work/ui/fix's merge and check commit don't count)", got)
	}
	check(true)
}
