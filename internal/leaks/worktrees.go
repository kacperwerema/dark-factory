package leaks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/procs"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// KindWorktree is a registered git worktree nothing uses any more, outside
// the trees `issue-cli worktrees prune` sweeps (.worktrees/).
const KindWorktree = "worktree"

// How long a tree must sit untouched before it counts: a worker's own
// scratchpad tree in use changes more often than that, and Claude Code's
// subagent trees are kept for a day in case their session comes back.
const (
	TempTreeIdle     = time.Hour
	SubagentTreeIdle = 24 * time.Hour
)

// worktrees are the registered worktrees of each root that nothing uses:
//   - temp: outside the main checkout, in a temp or scratchpad folder
//     (procs.TempPlace), its folder gone or untouched for TempTreeIdle;
//   - subagent: under <root>/.claude/worktrees/, its branch on main,
//     untouched for SubagentTreeIdle.
//
// A tree a pane or process is inside (live) is never one.
func worktrees(roots []string, now time.Time, live []string) []Finding {
	var out []Finding
	for _, root := range roots {
		trees, err := agentrun.ListTrees(root)
		if err != nil {
			continue
		}
		var merged map[string]string
		onMain := func(branch string) bool {
			if merged == nil {
				merged = map[string]string{}
				if main, err := gitmain.Branch(root, ""); err == nil {
					merged, _ = agentrun.MergedBranches(root, main)
				}
			}
			_, ok := merged[branch]
			return ok && branch != ""
		}
		subRoot := filepath.Join(root, ".claude", "worktrees")
		for _, t := range trees {
			temp := !inside(root, t.Path) && procs.TempPlace(t.Path)
			sub := inside(subRoot, t.Path) && t.Path != subRoot
			if !temp && !sub {
				continue
			}
			if _, err := os.Stat(t.Path); err != nil {
				out = append(out, Finding{
					Kind:        KindWorktree,
					What:        treeName(t),
					Why:         "a registered worktree whose folder is gone",
					StopCommand: "git -C " + shell.Quote(root) + " worktree prune",
					stop:        func() error { return agentrun.PruneRecords(root) },
				})
				continue
			}
			if agentrun.LiveIn(t.Path, live) != "" {
				continue
			}
			at, _ := agentrun.LastChange(t.Path)
			age := now.Sub(at)
			var why string
			switch {
			case temp && age >= TempTreeIdle:
				why = "a worktree of " + root + " in a temp or scratchpad folder, untouched for " + Age(age)
			case sub && !temp && age >= SubagentTreeIdle && onMain(t.Branch):
				why = "a subagent's worktree whose branch " + t.Branch + " is on main, untouched for " + Age(age)
			default:
				continue
			}
			out = append(out, treeFinding(root, t, age, why))
		}
	}
	return out
}

func treeFinding(root string, t Tree, age time.Duration, why string) Finding {
	cmd := "git -C " + shell.Quote(root) + " worktree remove " + shell.Quote(t.Path)
	if t.Locked {
		cmd = "git -C " + shell.Quote(root) + " worktree unlock " + shell.Quote(t.Path) + " && " + cmd
		why += " (locked)"
	}
	if t.Branch != "" {
		why += "; the branch stays"
	}
	return Finding{
		Kind: KindWorktree, Age: age,
		What:        treeName(t),
		Why:         why,
		StopCommand: cmd,
		stop:        func() error { return removeTree(root, t) },
	}
}

// Tree is agentrun's worktree record.
type Tree = agentrun.Tree

// removeTree removes t without force: a dirty tree stays (git refuses it,
// and a locked one is unlocked only once git status is empty).
func removeTree(root string, t Tree) error {
	out, err := exec.Command("git", "-C", t.Path, "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("git status in %s: %w", t.Path, err)
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return fmt.Errorf("%s has %d uncommitted or untracked file(s): left as it is", t.Path, len(strings.Split(s, "\n")))
	}
	if t.Locked {
		if out, err := exec.Command("git", "-C", root, "worktree", "unlock", t.Path).CombinedOutput(); err != nil {
			return fmt.Errorf("git worktree unlock %s: %s", t.Path, strings.TrimSpace(string(out)))
		}
	}
	return agentrun.RemoveTree(root, t.Path)
}

func treeName(t Tree) string {
	switch {
	case t.Branch != "":
		return t.Path + " (" + t.Branch + ")"
	case t.Head != "":
		return t.Path + " (detached at " + gitdir.ShortSHA(t.Head) + ")"
	}
	return t.Path
}

// inside: p is dir or a path under it.
func inside(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// projectRoots are the main checkouts of projects, each once: the work dir,
// else the issues dir's parent, read up to its git main checkout.
func projectRoots(projects []*tracker.Project) []string {
	seen := map[string]bool{}
	var roots []string
	for _, p := range projects {
		dir := p.WorkDir
		if dir == "" && p.IssueDir != "" {
			if abs, err := filepath.Abs(p.IssueDir); err == nil {
				dir = filepath.Dir(abs)
			}
		}
		if dir == "" {
			continue
		}
		root := gitmain.Root(dir)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	return roots
}
