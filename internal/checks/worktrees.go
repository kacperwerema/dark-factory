package checks

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
)

// worktree is one entry of `git worktree list --porcelain`: its folder and
// the branch checked out there (refs/heads/ dropped), "" when detached.
type worktree struct {
	Path, Branch string
}

// listCache keeps each repository's worktree listing, keyed by its common
// git dir, until the fingerprint of its worktree records changes. A pass
// over a board asks for one issue's worktree at a time (the waiting-on-you
// count, once per issue in human-testing): without it each ask was its own
// `git worktree list`.
var listCache = struct {
	sync.Mutex
	m map[string]cachedList
}{m: map[string]cachedList{}}

type cachedList struct {
	fp    string
	trees []worktree
}

// Worktrees maps each branch checked out in a worktree of the repository at
// root (refs/heads/ dropped, so work/<slug>) to that worktree's folder. One
// `git worktree list` per change to the repository's worktrees: adding,
// removing, moving one or switching its branch.
func Worktrees(root string) map[string]string {
	m := map[string]string{}
	for _, w := range worktreeList(root) {
		if w.Branch != "" {
			m[w.Branch] = w.Path
		}
	}
	return m
}

func worktreeList(root string) []worktree {
	if root == "" {
		return nil
	}
	common := gitdir.Common(root)
	fp := ""
	if common != "" {
		fp = worktreesFingerprint(common)
		listCache.Lock()
		c, ok := listCache.m[common]
		listCache.Unlock()
		if ok && c.fp == fp {
			return c.trees
		}
	}
	if common == "" && gitdir.Outside(root) {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	trees := parseWorktrees(string(out))
	if common != "" {
		listCache.Lock()
		listCache.m[common] = cachedList{fp: fp, trees: trees}
		listCache.Unlock()
	}
	return trees
}

func parseWorktrees(out string) []worktree {
	var trees []worktree
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			trees = append(trees, worktree{Path: p})
		} else if b, ok := strings.CutPrefix(line, "branch refs/heads/"); ok && len(trees) > 0 {
			trees[len(trees)-1].Branch = b
		}
	}
	return trees
}

// worktreesFingerprint changes whenever the listing could: the main
// checkout's HEAD, the worktrees/ folder (one added or pruned) and each
// linked worktree's HEAD (its branch) and gitdir (its folder), by size and
// modification time. Stats only: far cheaper than the git call it saves.
func worktreesFingerprint(common string) string {
	var b strings.Builder
	stamp := func(p string) {
		if fi, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s %d %d;", p, fi.Size(), fi.ModTime().UnixNano())
		} else {
			fmt.Fprintf(&b, "%s -;", p)
		}
	}
	stamp(filepath.Join(common, "HEAD"))
	dir := filepath.Join(common, "worktrees")
	stamp(dir)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		stamp(filepath.Join(dir, e.Name(), "HEAD"))
		stamp(filepath.Join(dir, e.Name(), "gitdir"))
	}
	return b.String()
}
