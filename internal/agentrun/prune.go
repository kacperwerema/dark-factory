package agentrun

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/gitmain"
	"github.com/kacperwerema/dark-factory/internal/restrace"
)

// Removing issue worktrees: an issue's worktree is no use once its branch is
// on main, and hundreds of checked-out trees cost gigabytes and keep the
// file-system watcher busy. A worktree is removed only when every rule
// holds: its branch is on main, it is clean (untracked files count), no tmux
// pane's current path and no process's arguments are inside it, and (for a
// sweep, which knows nothing of issues) nothing at its top level or its git
// dir's HEAD changed for PruneIdle. Special trees (.ci, detached ones,
// branches outside work/) are never touched; a release tree goes once its
// line is published, a newer one is out and its branch has nothing past its
// tag (prune_release.go). `git worktree remove`
// without --force keeps the branch and refuses a dirty tree by itself too.

// PruneIdle is how long nothing in a worktree may have changed before a
// sweep removes it. A worker's branch still at its dispatch base is not on
// main (PruneInput.Base); the idle rule covers a branch whose base no
// stats record.
const PruneIdle = 3 * time.Hour

// Why a worktree is kept, and the word for one that goes.
const (
	TreeRemoved  = "removed"
	TreeDirty    = "dirty"
	TreeUnmerged = "unmerged"
	TreeLive     = "live"
	TreeRecent   = "recent"
	TreeSpecial  = "special"
)

// TreeVerdict is one worktree's place in a sweep: Kind is TreeRemoved (it
// goes, or went) or the reason it is kept, Why the words.
type TreeVerdict struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Kind   string `json:"kind"`
	Why    string `json:"why"`
}

// PruneInput is what a sweep reads besides git.
type PruneInput struct {
	// Root is the main checkout; Main the branch the work lands on.
	Root, Main string
	Now        time.Time
	// Idle is PruneIdle unless a test says otherwise; 0 skips the rule (an
	// issue at done: its session is closed, nothing new starts on it).
	Idle time.Duration
	// Live is every folder a pane is in and every process's arguments
	// (LiveDirs).
	Live []string
	// Base is a work branch's dispatch base ("" unknown, or nil): a branch
	// still at its base has no commits of its own and is not on main
	// (gitmain.WorkAt), whatever main contains.
	Base func(branch string) string
	// EmptyGoes lets a branch with no commits past its base go: its issue
	// is at its last status, so there is nothing to land (RemoveIssueTree).
	EmptyGoes bool
	// Tags are the repository's version tags (ReleaseTags): a release tree
	// goes only when its line is published. Sweep reads them when nil.
	Tags []string
}

// Tree is one linked worktree of a repository.
type Tree struct {
	Path, Branch string
	// Head is the tree's checked-out commit, "" when not read.
	Head     string
	Detached bool
	// Locked: git worktree lock holds it (Claude Code locks its subagent
	// trees); git worktree remove refuses it until unlocked.
	Locked bool
}

// ListTrees is the linked worktrees of the repository at root (the main
// checkout left out), from one `git worktree list --porcelain`.
func ListTrees(root string) ([]Tree, error) {
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var trees []Tree
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			trees = append(trees, Tree{Path: strings.TrimPrefix(line, "worktree ")})
		case len(trees) == 0:
		case strings.HasPrefix(line, "HEAD "):
			trees[len(trees)-1].Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			trees[len(trees)-1].Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "detached":
			trees[len(trees)-1].Detached = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			trees[len(trees)-1].Locked = true
		}
	}
	if len(trees) > 0 {
		trees = trees[1:] // the main checkout comes first
	}
	return trees, nil
}

// MergedBranches is every local branch whose head main contains, with that
// head, from one git call: the contains of gitmain.WorkAt for a sweep.
func MergedBranches(root, main string) (map[string]string, error) {
	out, err := exec.Command("git", "-C", root, "for-each-ref", "--merged=refs/heads/"+main, "--format=%(objectname) %(refname:short)", "refs/heads/").Output()
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref --merged %s: %w", main, err)
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if sha, b, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			m[b] = sha
		}
	}
	return m, nil
}

// LiveDirs is every tmux pane's current path and every process's arguments
// on this machine: a worktree named in one of them is in use. Vars so tests
// stub them.
var LiveDirs = func() []string {
	var live []string
	if out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_current_path}").Output(); err == nil {
		live = append(live, strings.Split(strings.TrimSpace(string(out)), "\n")...)
	}
	if out, err := exec.Command("ps", "-Ao", "args=").Output(); err == nil {
		live = append(live, strings.Split(string(out), "\n")...)
	}
	return live
}

// Judge is the rule for one worktree: the reason it is kept, or TreeRemoved
// when it may go. merged holds the branches on main with their heads
// (MergedBranches).
func Judge(in PruneInput, t Tree, merged map[string]string) TreeVerdict {
	v := TreeVerdict{Path: t.Path, Branch: t.Branch}
	keep := func(kind, why string) TreeVerdict { v.Kind, v.Why = kind, why; return v }
	line, release := releaseLine(in.Root, t)
	if why := special(in.Root, t); why != "" && !release {
		return keep(TreeSpecial, why)
	}
	if _, err := os.Stat(t.Path); err != nil {
		v.Kind, v.Why = TreeRemoved, "its folder is gone: git worktree prune drops the record"
		return v
	}
	if release {
		gone, kind, why := judgeRelease(in, t, line)
		if kind != "" {
			return keep(kind, why)
		}
		return idleAndClean(in, v, gone)
	}
	head, on := merged[t.Branch]
	if head == "" {
		head = t.Head
	}
	base := ""
	if in.Base != nil {
		base = in.Base(t.Branch)
	}
	gone := t.Branch + " is on " + in.Main + ", clean and idle"
	switch gitmain.WorkAt(head, base, func(string) bool { return on }) {
	case gitmain.WorkEmpty:
		if !in.EmptyGoes {
			return keep(TreeUnmerged, t.Branch+" has no commits past its dispatch base")
		}
		gone = t.Branch + " has no commits of its own and its issue is closed; clean"
	case gitmain.WorkOnMain:
	default:
		return keep(TreeUnmerged, t.Branch+" has commits not on "+in.Main)
	}
	return idleAndClean(in, v, gone)
}

// idleAndClean is the rest of Judge for a tree whose branch may go: kept
// when something live is inside it, it changed within in.Idle, or it is
// dirty; else TreeRemoved with gone as Why.
func idleAndClean(in PruneInput, v TreeVerdict, gone string) TreeVerdict {
	keep := func(kind, why string) TreeVerdict { v.Kind, v.Why = kind, why; return v }
	if hit := LiveIn(v.Path, in.Live); hit != "" {
		return keep(TreeLive, "in use: "+hit)
	}
	if in.Idle > 0 {
		if at, what := LastChange(v.Path); !at.IsZero() && in.Now.Sub(at) < in.Idle {
			return keep(TreeRecent, what+" changed "+ago(in.Now.Sub(at)))
		}
	}
	if out, err := exec.Command("git", "-C", v.Path, "status", "--porcelain").Output(); err != nil {
		return keep(TreeDirty, "git status failed: "+err.Error())
	} else if s := strings.TrimSpace(string(out)); s != "" {
		n := len(strings.Split(s, "\n"))
		return keep(TreeDirty, fmt.Sprintf("%d uncommitted or untracked file(s)", n))
	}
	v.Kind, v.Why = TreeRemoved, gone
	return v
}

// special says why a tree is never a sweep's: outside .worktrees/, a dot or
// release folder, detached, or a branch that isn't an issue's work/ branch.
func special(root string, t Tree) string {
	rel, err := filepath.Rel(filepath.Join(root, ".worktrees"), t.Path)
	switch {
	case err != nil || rel == "." || strings.HasPrefix(rel, ".."):
		return "not under .worktrees/"
	case strings.HasPrefix(rel, ".") || strings.HasPrefix(rel, "release"):
		return "a special worktree (" + rel + ")"
	case t.Detached || t.Branch == "":
		return "detached: not an issue's worktree"
	case !strings.HasPrefix(t.Branch, "work/"):
		return t.Branch + " is not a work/ branch"
	}
	return ""
}

// LiveIn is the first live line naming path or a folder inside it.
func LiveIn(path string, live []string) string {
	for _, line := range live {
		for i := 0; ; {
			j := strings.Index(line[i:], path)
			if j < 0 {
				break
			}
			end := i + j + len(path)
			if end == len(line) || line[end] == '/' || line[end] == ' ' || line[end] == '"' || line[end] == '\'' {
				return shorten(strings.TrimSpace(line))
			}
			i = end
		}
	}
	return ""
}

func shorten(s string) string {
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}

// LastChange is the newest modification time among the tree's top-level
// entries and its git dir's HEAD, and what it was.
func LastChange(path string) (time.Time, string) {
	var at time.Time
	what := ""
	see := func(p, name string) {
		if fi, err := os.Lstat(p); err == nil && fi.ModTime().After(at) {
			at, what = fi.ModTime(), name
		}
	}
	entries, _ := os.ReadDir(path)
	for _, e := range entries {
		see(filepath.Join(path, e.Name()), e.Name())
	}
	if gd := gitdir.Worktree(path); gd != "" {
		see(filepath.Join(gd, "HEAD"), "its HEAD")
	}
	return at, what
}

// RemoveTree removes the worktree at path (git refuses a dirty one; the
// branch stays) and prunes git's records of trees whose folder is gone.
func RemoveTree(root, path string) error {
	if out, err := exec.Command("git", "-C", root, "worktree", "remove", path).CombinedOutput(); err != nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return fmt.Errorf("git worktree remove %s: %s", path, outputOrErr(out, err))
		}
	}
	restrace.Stop(restrace.KindWorktree, 0, path)
	return PruneRecords(root)
}

// PruneRecords is `git worktree prune`: records of trees whose folder is
// gone are dropped, so the branch can be checked out again.
func PruneRecords(root string) error {
	if out, err := exec.Command("git", "-C", root, "worktree", "prune").CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree prune: %s", outputOrErr(out, err))
	}
	return nil
}

// Sweep judges every linked worktree of in.Root and, unless dryRun,
// removes the ones that may go. A removal git refuses is kept as dirty.
func Sweep(in PruneInput, dryRun bool) ([]TreeVerdict, error) {
	trees, err := ListTrees(in.Root)
	if err != nil {
		return nil, err
	}
	merged, err := MergedBranches(in.Root, in.Main)
	if err != nil {
		return nil, err
	}
	if in.Tags == nil {
		in.Tags = ReleaseTags(in.Root)
	}
	var out []TreeVerdict
	for _, t := range trees {
		v := Judge(in, t, merged)
		if v.Kind == TreeRemoved && !dryRun {
			if err := RemoveTree(in.Root, t.Path); err != nil {
				v.Kind, v.Why = TreeDirty, firstLine(err.Error())
			}
		}
		out = append(out, v)
	}
	if !dryRun {
		_ = PruneRecords(in.Root)
	}
	return out, nil
}

// RemoveIssueTree removes the worktree of an issue at its last status: path
// on branch, branch on main, clean, nothing live inside (no idle rule: its
// session is closed and nothing new starts on a closed issue). It returns
// the verdict; Kind is TreeRemoved when the tree went or was already gone
// ("" Why then means there was nothing to do).
func RemoveIssueTree(in PruneInput, path, branch string) TreeVerdict {
	if _, err := os.Stat(path); err != nil {
		return TreeVerdict{Path: path, Branch: branch, Kind: TreeRemoved}
	}
	merged, err := MergedBranches(in.Root, in.Main)
	if err != nil {
		return TreeVerdict{Path: path, Branch: branch, Kind: TreeUnmerged, Why: err.Error()}
	}
	in.Idle, in.EmptyGoes = 0, true
	head, _ := gitmain.Git(path, "rev-parse", "--verify", "-q", "HEAD")
	v := Judge(in, Tree{Path: path, Branch: branch, Head: head}, merged)
	if v.Kind != TreeRemoved {
		return v
	}
	if err := RemoveTree(in.Root, path); err != nil {
		v.Kind, v.Why = TreeDirty, firstLine(err.Error())
	}
	return v
}

// RemoveLandedTree removes the worktree of an issue that is still open
// but whose branch is on main with commits of its own: clean, nothing live
// inside, and (the caller's part) no live session for the issue. No idle
// rule, so the tree goes at the first tick after the merge; a branch still
// at its dispatch base is kept (nothing landed). The branch stays, so a
// re-dispatch makes the tree again.
func RemoveLandedTree(in PruneInput, path, branch string) TreeVerdict {
	if _, err := os.Stat(path); err != nil {
		return TreeVerdict{Path: path, Branch: branch, Kind: TreeRemoved}
	}
	merged, err := MergedBranches(in.Root, in.Main)
	if err != nil {
		return TreeVerdict{Path: path, Branch: branch, Kind: TreeUnmerged, Why: err.Error()}
	}
	if _, on := merged[branch]; !on {
		return TreeVerdict{Path: path, Branch: branch, Kind: TreeUnmerged, Why: branch + " is not on " + in.Main}
	}
	if !branchMoved(in.Root, branch) {
		return TreeVerdict{Path: path, Branch: branch, Kind: TreeUnmerged, Why: branch + " has no commits since it was made"}
	}
	in.Idle, in.EmptyGoes = 0, false
	head, _ := gitmain.Git(path, "rev-parse", "--verify", "-q", "HEAD")
	v := Judge(in, Tree{Path: path, Branch: branch, Head: head}, merged)
	if v.Kind != TreeRemoved {
		return v
	}
	if err := RemoveTree(in.Root, path); err != nil {
		v.Kind, v.Why = TreeDirty, firstLine(err.Error())
	}
	return v
}

// branchMoved reports whether branch got a commit (or a reset) after it was
// made, from its reflog: a branch cut from main is "on main" before any
// work, and that tree must not go. No reflog reads as not moved.
func branchMoved(root, branch string) bool {
	out, err := gitmain.Git(root, "rev-list", "--walk-reflogs", "--count", "refs/heads/"+branch)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	return err == nil && n >= 2
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// ago is d as "under a minute ago" or "2h26m ago".
func ago(d time.Duration) string {
	if d < time.Minute {
		return "under a minute ago"
	}
	return strings.TrimSuffix(d.Round(time.Minute).String(), "0s") + " ago"
}
