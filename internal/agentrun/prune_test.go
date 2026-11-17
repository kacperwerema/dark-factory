package agentrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// pruneRepo is a temp repository on main with one commit; its folder is the
// resolved temp dir (macOS's /var is a link: git prints /private/var).
func pruneRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pruneGit(t, dir, "init", "-q", "-b", "main")
	pruneGit(t, dir, "config", "user.email", "t@example.com")
	pruneGit(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneGit(t, dir, "add", "a.txt")
	pruneGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func pruneGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// addTree adds .worktrees/<slug> on work/<slug> from main.
func addTree(t *testing.T, root, slug string) string {
	t.Helper()
	p := filepath.Join(root, ".worktrees", slug)
	pruneGit(t, root, "worktree", "add", "-q", "-b", "work/"+slug, p, "main")
	return p
}

// later is a clock past the idle window, so only the rule under test keeps
// a tree.
func pruneIn(root string, live []string) PruneInput {
	return PruneInput{Root: root, Main: "main", Now: time.Now().Add(2 * PruneIdle), Idle: PruneIdle, Live: live}
}

func verdictOf(t *testing.T, vs []TreeVerdict, path string) TreeVerdict {
	t.Helper()
	for _, v := range vs {
		if v.Path == path {
			return v
		}
	}
	t.Fatalf("no verdict for %s in %+v", path, vs)
	return TreeVerdict{}
}

func TestSweepRemovesOnlyMergedCleanIdleTrees(t *testing.T) {
	root := pruneRepo(t)
	gone := addTree(t, root, "sys/gone")
	dirty := addTree(t, root, "dirty")
	if err := os.WriteFile(filepath.Join(dirty, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ahead := addTree(t, root, "ahead")
	if err := os.WriteFile(filepath.Join(ahead, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneGit(t, ahead, "add", "b.txt")
	pruneGit(t, ahead, "commit", "-q", "-m", "work")
	ciTree := filepath.Join(root, ".worktrees", ".ci")
	pruneGit(t, root, "worktree", "add", "-q", "--detach", ciTree, "main")
	release := filepath.Join(root, ".worktrees", "release-v0.1")
	pruneGit(t, root, "worktree", "add", "-q", "-b", "release/v0.1", release, "main")

	dry, err := Sweep(pruneIn(root, nil), true)
	if err != nil {
		t.Fatal(err)
	}
	if v := verdictOf(t, dry, gone); v.Kind != TreeRemoved {
		t.Fatalf("merged clean idle tree: %+v, want removed", v)
	}
	if _, err := os.Stat(gone); err != nil {
		t.Fatalf("--dry-run removed %s", gone)
	}

	vs, err := Sweep(pruneIn(root, nil), false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{gone: TreeRemoved, dirty: TreeDirty, ahead: TreeUnmerged, ciTree: TreeSpecial, release: TreeSpecial}
	for path, kind := range want {
		if v := verdictOf(t, vs, path); v.Kind != kind {
			t.Errorf("%s: %+v, want %s", path, v, kind)
		}
	}
	if _, err := os.Stat(gone); err == nil {
		t.Errorf("%s still on disk", gone)
	}
	for _, p := range []string{dirty, ahead, ciTree, release} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("kept tree %s is gone", p)
		}
	}
	if pruneGit(t, root, "branch", "--list", "work/sys/gone") == "" {
		t.Error("the removed tree's branch was deleted; it must stay")
	}
	if strings.Contains(pruneGit(t, root, "worktree", "list"), gone) {
		t.Error("git still lists the removed tree")
	}
}

func TestSweepKeepsATreeALivePaneIsIn(t *testing.T) {
	isolateTmux(t)
	root := pruneRepo(t)
	p := addTree(t, root, "busy")
	tmuxRun(t, "new-session", "-d", "-s", "agent-busy", "-c", filepath.Join(p), "/bin/sh")
	var live []string
	for i := 0; i < 50; i++ { // the pane's path shows once its shell runs
		live = LiveDirs()
		if LiveIn(p, live) != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	vs, err := Sweep(pruneIn(root, live), false)
	if err != nil {
		t.Fatal(err)
	}
	if v := verdictOf(t, vs, p); v.Kind != TreeLive {
		t.Fatalf("tree with a pane in it: %+v, want live", v)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("the live tree was removed")
	}
}

func TestSweepKeepsARecentTree(t *testing.T) {
	root := pruneRepo(t)
	p := addTree(t, root, "fresh")
	in := pruneIn(root, nil)
	in.Now = time.Now()
	vs, err := Sweep(in, false)
	if err != nil {
		t.Fatal(err)
	}
	if v := verdictOf(t, vs, p); v.Kind != TreeRecent || !strings.HasSuffix(v.Why, "changed under a minute ago") {
		t.Fatalf("just-made tree: %+v, want recent, changed under a minute ago", v)
	}
	if got := ago(146*time.Minute + 20*time.Second); got != "2h26m ago" {
		t.Errorf("ago = %q", got)
	}
}

func TestLiveInMatchesWholePathsOnly(t *testing.T) {
	p := "/r/.worktrees/a"
	for line, want := range map[string]bool{
		"/r/.worktrees/a":                  true,
		"/r/.worktrees/a/sub":              true,
		"go test -C /r/.worktrees/a ./...": true,
		"/r/.worktrees/ab":                 false,
		"/r/.worktrees/a-b/x":              false,
	} {
		if got := LiveIn(p, []string{line}) != ""; got != want {
			t.Errorf("LiveIn(%q) = %v, want %v", line, got, want)
		}
	}
}

// A shipped issue's tree goes without the idle rule, and refuses while dirty.
func TestRemoveIssueTreeSkipsTheIdleRuleButNotTheOthers(t *testing.T) {
	root := pruneRepo(t)
	p := addTree(t, root, "shipped")
	in := pruneIn(root, nil)
	in.Now = time.Now()
	if err := os.WriteFile(filepath.Join(p, "left.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := RemoveIssueTree(in, p, "work/shipped"); v.Kind != TreeDirty {
		t.Fatalf("dirty shipped tree: %+v, want dirty", v)
	}
	os.Remove(filepath.Join(p, "left.txt"))
	if v := RemoveIssueTree(PruneInput{Root: root, Main: "main", Now: time.Now(), Live: []string{p}}, p, "work/shipped"); v.Kind != TreeLive {
		t.Fatalf("shipped tree with a live pane: %+v, want live", v)
	}
	if v := RemoveIssueTree(in, p, "work/shipped"); v.Kind != TreeRemoved {
		t.Fatalf("clean shipped tree: %+v, want removed", v)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("still on disk")
	}
}

// A removed tree comes back on the next dispatch, on its branch, even when
// its folder was deleted by hand and git still holds its record.
func TestEnsureWorktreeRecreatesARemovedTree(t *testing.T) {
	root := pruneRepo(t)
	p := addTree(t, root, "again")
	if err := RemoveTree(root, p); err != nil {
		t.Fatal(err)
	}
	wt := tracker.WorktreeSpec{Path: p, Branch: "work/again"}
	if _, steps, ok := EnsureWorktree(root, wt, nil); !ok {
		t.Fatalf("recreate after remove: %+v", steps)
	}
	if got := pruneGit(t, p, "rev-parse", "--abbrev-ref", "HEAD"); got != "work/again" {
		t.Fatalf("recreated on %q", got)
	}
	// Deleted by hand: the record stays until a prune.
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if _, steps, ok := EnsureWorktree(root, wt, nil); !ok {
		t.Fatalf("recreate after rm -rf: %+v", steps)
	}
	if _, err := os.Stat(filepath.Join(p, "a.txt")); err != nil {
		t.Fatal("recreated tree has no files")
	}
}

// addRelease adds .worktrees/release-<line> on release/<line> from main and
// tags each of tags on it.
func addRelease(t *testing.T, root, line string, tags ...string) string {
	t.Helper()
	p := filepath.Join(root, ".worktrees", "release-"+line)
	pruneGit(t, root, "worktree", "add", "-q", "-b", "release/"+line, p, "main")
	for _, tag := range tags {
		pruneGit(t, root, "tag", tag, "release/"+line)
	}
	return p
}

func TestPruneRemovesAPublishedReleaseWorktree(t *testing.T) {
	root := pruneRepo(t)
	old := addRelease(t, root, "v0.1", "v0.1.0-rc.1", "v0.1.0")
	patched := addRelease(t, root, "v0.2", "v0.2.0", "v0.2.1")
	dirty := addRelease(t, root, "v0.3", "v0.3.0")
	if err := os.WriteFile(filepath.Join(dirty, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	picked := addRelease(t, root, "v0.4", "v0.4.0")
	if err := os.WriteFile(filepath.Join(picked, "fix.txt"), []byte("fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneGit(t, picked, "add", "fix.txt")
	pruneGit(t, picked, "commit", "-q", "-m", "fix: picked")
	pruneGit(t, picked, "tag", "v0.4.1-rc.1")
	unpublished := addRelease(t, root, "v0.9", "v0.9.0-rc.1")
	skipped := addRelease(t, root, "v0.7", "v0.7.0-rc.2") // left for v0.8
	newest := addRelease(t, root, "v0.8", "v0.8.0")
	live := addRelease(t, root, "v0.0", "v0.0.3")

	vs, err := Sweep(pruneIn(root, []string{"zsh " + live}), false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		old:         {TreeRemoved, "v0.1.0 is published"},
		patched:     {TreeRemoved, "v0.2.1 is published"},
		dirty:       {TreeDirty, "uncommitted"},
		picked:      {TreeUnmerged, "1 commit(s) past v0.4.0"},
		unpublished: {TreeSpecial, "v0.9 is not published"},
		skipped:     {TreeRemoved, "v0.7 was never published and v0.8 is out"},
		newest:      {TreeSpecial, "v0.8 is the newest published release"},
		live:        {TreeLive, "in use"},
	}
	for path, w := range want {
		v := verdictOf(t, vs, path)
		if v.Kind != w[0] || !strings.Contains(v.Why, w[1]) {
			t.Errorf("%s: %+v, want %s with %q", filepath.Base(path), v, w[0], w[1])
		}
		_, statErr := os.Stat(path)
		if (w[0] == TreeRemoved) != os.IsNotExist(statErr) {
			t.Errorf("%s: on disk = %v after a %s verdict", filepath.Base(path), statErr == nil, w[0])
		}
	}
	if pruneGit(t, root, "branch", "--list", "release/v0.1") == "" {
		t.Error("the removed release tree's branch was deleted; it must stay")
	}
}
