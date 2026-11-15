package tracker

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// countGitCalls turns the git call counter on for the test and returns a
// function that reads it: calls whose arguments start with prefix.
func countGitCalls(t *testing.T) func(prefix string) int {
	t.Helper()
	gitCalls.Lock()
	gitCalls.n = map[string]int{}
	gitCalls.Unlock()
	t.Cleanup(func() {
		gitCalls.Lock()
		gitCalls.n = nil
		gitCalls.Unlock()
	})
	return func(prefix string) int {
		gitCalls.Lock()
		defer gitCalls.Unlock()
		n := 0
		for args, c := range gitCalls.n {
			if strings.HasPrefix(args, prefix) {
				n += c
			}
		}
		return n
	}
}

// stackedRepo has three open branches, one stacked on another and two that
// share a file, so the scan runs merge-base, rev-list and diff.
func stackedRepo(t *testing.T) *gitRepo {
	t.Helper()
	r := newGitRepo(t)
	r.git("config", "maintenance.auto", "false")
	r.git("config", "gc.auto", "0")
	b := r.worktree("work/b", "main")
	r.writeIn(b, "shared.txt", "s\nb\n")
	r.commitIn(b, "b")
	c := r.worktree("work/c", "work/b")
	r.writeIn(c, "c.txt", "c\n")
	r.commitIn(c, "c")
	d := r.worktree("work/d", "main")
	r.writeIn(d, "shared.txt", "s\nd\n")
	r.commitIn(d, "d")
	return r
}

// A fresh process (memory cache empty) answers the branch scan's sha calls
// from the disk cache: no merge-base, rev-list or diff runs again.
func TestBranchScanReusesTheDiskCacheInANewProcess(t *testing.T) {
	r := stackedRepo(t)
	resetGitCacheMemory()
	t.Cleanup(resetGitCacheMemory)

	calls := countGitCalls(t)
	first := OpenBranches(r.root, "", nil)
	firstHot := HotFiles(r.root, first, true)
	if calls("merge-base") == 0 || calls("rev-list") == 0 || calls("diff") == 0 {
		t.Fatalf("cold scan ran no sha calls: merge-base %d, rev-list %d, diff %d", calls("merge-base"), calls("rev-list"), calls("diff"))
	}
	if len(firstHot) != 1 || firstHot[0].Path != "shared.txt" {
		t.Fatalf("hot files = %+v", firstHot)
	}
	if _, err := os.Stat(filepath.Join(r.root, ".git", gitCacheFile)); err != nil {
		t.Fatalf("disk cache not written: %v", err)
	}

	resetGitCacheMemory()
	calls = countGitCalls(t)
	second := OpenBranches(r.root, "", nil)
	secondHot := HotFiles(r.root, second, true)
	if n := calls("merge-base") + calls("rev-list") + calls("diff"); n != 0 {
		t.Errorf("warm scan in a new process ran %d sha calls, want 0", n)
	}
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(firstHot, secondHot) {
		t.Errorf("disk cache changed the answer:\n%+v\n%+v", first, second)
	}
}

// "No common ancestor" is an answer: kept on disk and not asked again.
func TestGitCacheKeepsNoCommonAncestor(t *testing.T) {
	r := newGitRepo(t)
	main := r.git("rev-parse", "HEAD")
	r.git("checkout", "-q", "--orphan", "other")
	r.write("o.txt", "o\n")
	r.commit("other")
	other := r.git("rev-parse", "HEAD")
	resetGitCacheMemory()
	t.Cleanup(resetGitCacheMemory)

	calls := countGitCalls(t)
	if _, err := gitCached(r.root, "merge-base", main, other); err == nil {
		t.Fatal("unrelated histories: want an error")
	}
	resetGitCacheMemory()
	// The swapped pair is the same question.
	if _, err := gitCached(r.root, "merge-base", other, main); err == nil {
		t.Fatal("cached answer: want the same error")
	}
	if n := calls("merge-base"); n != 1 {
		t.Errorf("merge-base ran %d times, want 1", n)
	}
}

// A failure that is not an answer (a missing object) is not cached.
func TestGitCacheDoesNotKeepOtherFailures(t *testing.T) {
	r := newGitRepo(t)
	resetGitCacheMemory()
	t.Cleanup(resetGitCacheMemory)
	head := r.git("rev-parse", "HEAD")
	missing := strings.Repeat("ab", 20)
	calls := countGitCalls(t)
	for i := 0; i < 2; i++ {
		if _, err := gitCached(r.root, "merge-base", head, missing); err == nil {
			t.Fatal("missing object: want an error")
		}
	}
	if n := calls("merge-base"); n != 2 {
		t.Errorf("merge-base ran %d times, want 2 (failure not cached)", n)
	}
}

// The scan reads main's sha from the one reading of the refs, however many
// branches it lists: no rev-parse of main at all.
func TestOpenBranchesReadsMainOnce(t *testing.T) {
	r := stackedRepo(t)
	resetGitCacheMemory()
	t.Cleanup(resetGitCacheMemory)
	calls := countGitCalls(t)
	if got := OpenBranches(r.root, "main", nil); len(got) != 3 {
		t.Fatalf("branches = %v", branchNames(got))
	}
	if n := calls("rev-parse --verify"); n != 0 {
		t.Errorf("%d rev-parse calls, want 0", n)
	}
	if n := calls("for-each-ref --format=%(refname)"); n != 1 {
		t.Errorf("refs read %d times, want 1", n)
	}
}

// A torn or foreign line in the file is skipped, the rest still read.
func TestGitDiskSkipsBadLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), gitCacheFile)
	good := `{"k":"rev-list\u0000--count\u0000x","o":"3\n"}`
	if err := os.WriteFile(path, []byte("{\"k\":\"torn\n"+good+"\nnot json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := loadGitDisk(path)
	if len(d.m) != 1 {
		t.Fatalf("entries = %v", d.m)
	}
	if a, ok := d.get("rev-list\x00--count\x00x"); !ok || a.out != "3\n" {
		t.Errorf("good line = %+v, %v", a, ok)
	}
}

func TestGitPersistableOnlyNamesShas(t *testing.T) {
	sha := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"merge-base", sha, other}, true},
		{[]string{"rev-list", "--count", sha + ".." + other}, true},
		{[]string{"rev-list", "--count", sha + ".." + other, "--", "path/main"}, true},
		{[]string{"diff", "--no-renames", "--numstat", sha, other}, true},
		{[]string{"merge-base", "main", other}, false},
		{[]string{"rev-list", "--count", "abc123.." + other}, false},
		{[]string{"show", sha + ":file"}, false},
		{[]string{"log", "-p", sha}, false},
		{[]string{"diff", "--name-only"}, false},
	} {
		if got := gitPersistable(c.args); got != c.want {
			t.Errorf("gitPersistable(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}
