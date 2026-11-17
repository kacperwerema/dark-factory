package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// funcSource is the source of the named top-level function in file.
func funcSource(t *testing.T, file, name string) string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name && fn.Recv == nil {
			return string(src[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset])
		}
	}
	t.Fatalf("%s has no func %s", file, name)
	return ""
}

// "Is this issue's work on main" has one rule, gitmain.WorkAt / OnMain: the
// queue's --merged blockers, its done marking, the worktree prune (Judge,
// behind Sweep and RemoveIssueTree) and the lead tick (which prunes through
// pruneInput, so it must hand prune the dispatch bases) all ask it, and
// none keeps a containment test of its own.
func TestOneBranchOnMainPredicate(t *testing.T) {
	sites := []struct {
		file, fn string
		want     string
	}{
		{"cmd_queue.go", "issueWorkOnMain", "gitmain.OnMain("},
		{"cmd_queue.go", "queueBranchSHA", "gitmain.WorkAt("},
		{"../../internal/agentrun/prune.go", "Judge", "gitmain.WorkAt("},
		{"cmd_worktrees.go", "pruneInput", "Base: pruneBases("},
		{"cmd_lead_tick.go", "leadTickWorktrees", "pruneInput(ctx)"},
	}
	for _, s := range sites {
		body := funcSource(t, s.file, s.fn)
		if !strings.Contains(body, s.want) {
			t.Errorf("%s %s doesn't call %s: every on-main question goes through gitmain.WorkAt", s.file, s.fn, s.want)
		}
		for _, own := range []string{"--is-ancestor", "--merged", "EqualFold("} {
			if strings.Contains(body, own) {
				t.Errorf("%s %s has its own %q: ask gitmain.WorkAt (Contains, or a set read in one call) instead", s.file, s.fn, own)
			}
		}
	}

	yes := func(string) bool { return true }
	no := func(string) bool { return false }
	for _, c := range []struct {
		head, base string
		contains   func(string) bool
		want       gitmain.Work
	}{
		{"", "abc1234", yes, gitmain.WorkNone},
		{"abc1234def", "abc1234def", yes, gitmain.WorkEmpty},
		{"ABC1234DEF", "abc1234", yes, gitmain.WorkEmpty},
		{"abc1234def", "", yes, gitmain.WorkOnMain},
		{"abc1234def", "fff0000", yes, gitmain.WorkOnMain},
		{"abc1234def", "fff0000", no, gitmain.WorkAhead},
		{"abc1234def", "abc", yes, gitmain.WorkOnMain}, // too short to name it
	} {
		if got := gitmain.WorkAt(c.head, c.base, c.contains); got != c.want {
			t.Errorf("WorkAt(%q, %q) = %d, want %d", c.head, c.base, got, c.want)
		}
	}
}

// A work branch still at its dispatch base has no commits of its own: main
// containing it says nothing, so it is not on main for the queue's
// --merged blocker, its done marking, a sweep or the lead tick. A closed
// issue's empty tree still goes (nothing to land).
func TestEmptyBranchIsNotOnMain(t *testing.T) {
	proj, root, base, _ := queueMergedProject(t)
	queueGit(t, root, "config", "user.email", "t@example.com")
	queueGit(t, root, "config", "user.name", "t")
	queueGit(t, root, "branch", "-f", "work/base", "main")
	issue := loadLeadIssue(t, proj, "base")
	if err := tracker.RecordDispatch(issue.FilePath, tracker.Dispatch{At: time.Now(), Session: "s", BaseBranch: "main", BaseSHA: base}); err != nil {
		t.Fatal(err)
	}

	if sha, ok := issueWorkOnMain(root, "", issue); ok {
		t.Errorf("queue blocker: empty branch on main at %s", sha)
	}
	if _, where := queueBranchSHA(root, "base", "", base); where != branchEmpty {
		t.Errorf("queue done: where = %d, want branchEmpty", where)
	}
	if sha, note := queueShipSHA(root, "base", "", base); sha != "" || note != "" {
		t.Errorf("queue ship sha = %q, note %q; want neither", sha, note)
	}
	// Without the base the old answer stands: main contains it.
	if _, where := queueBranchSHA(root, "base", "", ""); where != branchOnMain {
		t.Errorf("no base known: where = %d, want branchOnMain", where)
	}

	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(real, ".worktrees", "base")
	queueGit(t, root, "worktree", "add", "-q", tree, "work/base")
	ctx, _, _ := newTestContext(proj, false)
	in, err := pruneInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in.Now, in.Live = time.Now().Add(2*agentrun.PruneIdle), nil
	vs, err := agentrun.Sweep(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Kind != agentrun.TreeUnmerged || !strings.Contains(vs[0].Why, "no commits past its dispatch base") {
		t.Errorf("sweep (worktrees prune, the lead tick's daily one): %+v, want kept unmerged", vs)
	}
	if v := agentrun.RemoveIssueTree(in, tree, "work/base"); v.Kind != agentrun.TreeRemoved || !strings.Contains(v.Why, "no commits of its own") {
		t.Errorf("closed issue's empty tree: %+v, want removed", v)
	}
}

// One order names the main branch: gitmain.Branch (ci.branch first),
// tracker.MainBranch and agentrun.MainBranch agree, and no other code
// spells the candidate list.
func TestMainBranchResolvedOnce(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  string
	}{
		{"main, checkout on a feature branch", func(t *testing.T, dir string) {
			queueGit(t, dir, "checkout", "-q", "-b", "feature")
		}, "main"},
		{"origin HEAD on develop", func(t *testing.T, dir string) {
			queueGit(t, dir, "branch", "develop")
			queueGit(t, dir, "update-ref", "refs/remotes/origin/develop", "main")
			queueGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
		}, "develop"},
		{"master only", func(t *testing.T, dir string) {
			queueGit(t, dir, "branch", "-m", "main", "master")
		}, "master"},
		{"trunk only", func(t *testing.T, dir string) {
			queueGit(t, dir, "branch", "-m", "main", "trunk")
		}, "trunk"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			queueGit(t, dir, "init", "-q", "-b", "main")
			queueGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
			c.setup(t, dir)
			g, err := gitmain.Branch(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			tr, err := tracker.MainBranch(dir)
			if err != nil {
				t.Fatal(err)
			}
			a := agentrun.MainBranch(dir)
			if g != c.want || tr != c.want || a != c.want {
				t.Errorf("gitmain %q, tracker %q, agentrun %q; want %q", g, tr, a, c.want)
			}
			if b, _ := gitmain.Branch(dir, "release"); b != "release" {
				t.Errorf("ci.branch release: %q", b)
			}
		})
	}

	// The candidate list lives in gitmain.PickBranch only.
	var spelled []string
	for _, dir := range []string{"../../internal", "."} {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			src, _ := os.ReadFile(p)
			s := string(src)
			if strings.Contains(s, `"main", "master"`) && !strings.HasSuffix(filepath.ToSlash(p), "internal/gitmain/gitmain.go") {
				spelled = append(spelled, p)
			}
			return nil
		})
	}
	if len(spelled) > 0 {
		t.Errorf("these files spell the main-branch candidates themselves: %v; call gitmain.Branch, tracker.MainBranch or gitmain.PickBranch", spelled)
	}
}
