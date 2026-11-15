package viewer

import (
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// gitBoardPassed is when every pass on a gitBoard happened. The review lists
// issues oldest first by pass date, in whole seconds, then newest file first:
// on the wall clock, a second ticking over before the last issue sent it
// alone to page 2 (TestYOLOReviewRunsNoGitPerEntry, red once at d5a9392).
var gitBoardPassed = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

var commentDate = regexp.MustCompile(`"date":"[^"]*"`)

// gitBoard is a project in a git repository with n issues in human-testing,
// cli/ht-00 … cli/ht-<n-1>, and one worktree on work/cli/ht-00 holding a
// guided check script for it. calls reports every git command run since
// the board was made, through a git on PATH that logs each call.
type gitBoard struct {
	proj  tracker.Project
	calls func() []string
}

func newGitBoard(t *testing.T, n int) gitBoard {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	for i := 0; i < n; i++ {
		path := filepath.Join(root, "issues", "CLI", fmt.Sprintf("ht-%02d.md", i))
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(fmt.Sprintf("---\ntitle: \"HT %02d\"\nstatus: \"human-testing\"\n---\n\nBody.\n\n## Decisions\n\n- **D-1** · 2026-10-30 · Keep the body short\n", i)), 0o644)
		if err := tracker.AddComment(path, 0, tracker.ApprovalComment("YOLO", tracker.YOLOHow("agent", "testing"), "2026-10-30", "human-testing"), "cli"); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		passed := `"date":"` + gitBoardPassed.Format(time.RFC3339) + `"`
		if err := os.WriteFile(path, commentDate.ReplaceAll(data, []byte(passed)), 0o644); err != nil {
			t.Fatal(err)
		}
		// One second apart, for a filesystem whose clock is too coarse to
		// tell these writes apart: the load's order is newest file first.
		at := gitBoardPassed.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	wfFile := filepath.Join(root, "workflow.yaml")
	os.WriteFile(wfFile, []byte("statuses:\n  - name: \"testing\"\n  - name: \"human-testing\"\n  - name: \"done\"\n"), 0o644)
	git := func(dir string, args ...string) {
		cmd := exec.Command(realGit, append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(root, "init", "-q", "-b", "main")
	git(root, "config", "maintenance.auto", "false")
	git(root, "config", "gc.auto", "0")
	git(root, "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, ".worktrees", "one")
	git(root, "worktree", "add", "-q", "-b", "work/cli/ht-00", wt)
	os.MkdirAll(filepath.Join(wt, "scripts", "checks"), 0o755)
	os.WriteFile(filepath.Join(wt, "scripts", "checks", "zero.sh"), []byte("#!/usr/bin/env bash\n# issue: cli/ht-00\n"), 0o644)

	bin := t.TempDir()
	log := filepath.Join(bin, "calls.log")
	// The log sits beside the stand-in, so its body is the same every run.
	standin.Write(t, filepath.Join(bin, "git"), "printf '%s\\n' \"$*\" >> \"${0%/*}/calls.log\"\nexec '"+realGit+"' \"$@\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return gitBoard{
		proj: tracker.Project{Name: "Git calls", Slug: "gitcalls", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: wfFile},
		calls: func() []string {
			data, _ := os.ReadFile(log)
			if s := strings.TrimSpace(string(data)); s != "" {
				return strings.Split(s, "\n")
			}
			return nil
		},
	}
}

// A board's order comes from its fixture, not from where the clock fell while
// it was written: the newest file first, every pass on one date.
func TestGitBoardOrderIgnoresTheClock(t *testing.T) {
	b := newGitBoard(t, 16)
	issues, err := tracker.LoadIssues(b.proj.IssueDir)
	if err != nil || len(issues) != 16 {
		t.Fatalf("issues = %d, %v", len(issues), err)
	}
	for i, issue := range issues {
		if want := fmt.Sprintf("cli/ht-%02d", 15-i); issue.Slug != want {
			t.Errorf("issue %d is %s, want %s", i, issue.Slug, want)
		}
		if got := issue.Comments()[0].Date; got != gitBoardPassed.Format(time.RFC3339) {
			t.Errorf("%s passed at %s", issue.Slug, got)
		}
	}
}

// TestWaitingOnOwnerRunsGitOncePerPassNotPerIssue: the waiting-on-you count
// runs on every page (sidebar, floor), and every issue in human-testing asks
// for its branch worktree. A board with 65 of them ran 65 `git worktree
// list` per page, 95% of its shelf page's 845 ms (performance audit
// 2026-11-03). One listing per pass, and the merged-into-main check only for
// the issue that has a worktree.
func TestWaitingOnOwnerRunsGitOncePerPassNotPerIssue(t *testing.T) {
	const n = 30
	b := newGitBoard(t, n)
	issues, err := tracker.LoadIssues(b.proj.IssueDir)
	if err != nil || len(issues) != n {
		t.Fatalf("issues = %d, %v", len(issues), err)
	}
	for pass := 0; pass < 2; pass++ {
		items := waitingOnOwner(&b.proj, issues)
		if len(items) != 1 || items[0].Kind != "checks" || items[0].Issue.Slug != "cli/ht-00" {
			t.Fatalf("pass %d: waiting = %+v, want ht-00's checks", pass, items)
		}
	}
	calls := b.calls()
	lists := 0
	for _, c := range calls {
		if strings.Contains(c, "worktree list") {
			lists++
		}
	}
	// One listing; the merged-into check is at most 3 calls a pass for the
	// one issue with a worktree.
	if lists > 1 || len(calls) > 1+2*3 {
		t.Fatalf("two passes over %d issues in human-testing ran git %d times (%d worktree lists):\n%s", n, len(calls), lists, strings.Join(calls, "\n"))
	}
}

// TestPagesRunNoGitPerIssue is the audit's guard (follow-up 10): a page's
// git calls must not grow with the number of issues. Each page is loaded on
// a board of 4 issues in human-testing and on one of 16; the second may run
// no more git than the first. The YOLO review is here too: the board's
// issues all wait in its queue (yolo_git_calls_test.go tests its branches).
func TestPagesRunNoGitPerIssue(t *testing.T) {
	pages := []string{
		"/p/gitcalls/shelf",
		"/p/gitcalls/needs-you",
		"/p/gitcalls/hall",
		"/p/gitcalls/company",
		"/p/gitcalls/status",
		"/p/gitcalls/yolo/review",
		"/p/gitcalls/issue/cli/ht-00",
		"/p/gitcalls/issue/cli/ht-00?tab=timeline",
		"/p/gitcalls/issue/cli/ht-00?tab=evidence",
		"/p/gitcalls/issue/cli/ht-00?tab=issue",
		"/needs-you",
		"/",
		"/crew",
		"/leads",
	}
	count := func(n int) map[string]int {
		b := newGitBoard(t, n)
		srv, err := NewServer([]tracker.Project{b.proj})
		if err != nil {
			t.Fatal(err)
		}
		h := srv.Routes()
		got := map[string]int{}
		for _, page := range pages {
			// Once to warm what a page keeps between requests, then counted.
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://127.0.0.1:8080"+page, nil))
			before := len(b.calls())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:8080"+page, nil))
			if rec.Code != 200 {
				t.Fatalf("%s: %d\n%s", page, rec.Code, rec.Body.String())
			}
			got[page] = len(b.calls()) - before
		}
		return got
	}
	small, big := count(4), count(16)
	for _, page := range pages {
		t.Logf("%s: git %d on 4 issues, %d on 16", page, small[page], big[page])
		if big[page] > small[page] {
			t.Errorf("%s ran git %d times on 4 issues and %d on 16: a git call per issue", page, small[page], big[page])
		}
	}
}
