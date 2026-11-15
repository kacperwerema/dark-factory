package viewer

import (
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestYOLOReviewRunsNoGitPerEntry is cause 7 of the audit, on a board whose
// entries each have a branch with its own commit: the review ran about 13
// git calls per entry. Once its entries are read, a page runs next to none,
// whatever the queue's length. A commit on one branch shows at once and
// costs that entry alone.
func TestYOLOReviewRunsNoGitPerEntry(t *testing.T) {
	run := func(n int) (warm, afterCommit int, page string) {
		b := newGitBoard(t, n)
		root := b.proj.WorkDir
		git := func(dir string, args ...string) {
			cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		wts := map[int]string{}
		for i := 1; i < n; i++ {
			wts[i] = filepath.Join(root, ".worktrees", fmt.Sprintf("w%02d", i))
			git(root, "worktree", "add", "-q", "-b", fmt.Sprintf("work/cli/ht-%02d", i), wts[i])
			git(wts[i], "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("work %d", i))
		}
		srv, err := NewServer([]tracker.Project{b.proj})
		if err != nil {
			t.Fatal(err)
		}
		h := srv.Routes()
		get := func() string {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:8080/p/gitcalls/yolo/review", nil))
			if rec.Code != 200 {
				t.Fatalf("%d\n%s", rec.Code, rec.Body.String())
			}
			return rec.Body.String()
		}
		get()
		before := len(b.calls())
		get()
		warm = len(b.calls()) - before
		if warm > 3 {
			t.Log(strings.Join(b.calls()[before:], "\n"))
		}

		if err := os.WriteFile(filepath.Join(wts[n-1], "new.go"), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(wts[n-1], "add", "new.go")
		git(wts[n-1], "commit", "-q", "-m", "a new commit")
		before = len(b.calls()) // the two above went through the logging git too
		page = get()
		return warm, len(b.calls()) - before, page
	}
	warmSmall, stepSmall, _ := run(4)
	warmBig, stepBig, page := run(16)
	t.Logf("warm: git %d on 4 entries, %d on 16; after a commit %d and %d", warmSmall, warmBig, stepSmall, stepBig)
	if warmBig > warmSmall || warmBig > 3 {
		t.Errorf("a warm review ran git %d times on 4 entries and %d on 16", warmSmall, warmBig)
	}
	if stepBig > stepSmall {
		t.Errorf("a commit on one branch cost %d git calls on 4 entries and %d on 16: each entry read again", stepSmall, stepBig)
	}
	if !strings.Contains(page, "2 commits · 1 file") {
		t.Errorf("the entry doesn't show its new commit:\n%s", page)
	}
}
