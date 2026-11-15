package tracker

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
)

// The repository's branch heads and main branch, read in one
// `git for-each-ref` and kept until a ref changes. A page or a command asks
// for them many times over (one issue's branch at a time, main for each):
// each ask was its own rev-parse and symbolic-ref (performance audit
// 2026-11-03, cause 8). The keeping is the same as checks.Worktrees: stats
// only, a stale answer impossible short of the filesystem's clock tick.

// refSnapshot is one reading of the refs. dirs are the folders under
// refs/heads when it was read: every ref update renames a lock file into
// one of them, or into a new folder that changes its parent.
type refSnapshot struct {
	fp         string
	dirs       []string
	heads      map[string]string // branch (refs/heads/ dropped) → sha
	originHead string            // the branch origin/HEAD names, "" for none
}

var refCache = struct {
	sync.Mutex
	m map[string]*refSnapshot
}{m: map[string]*refSnapshot{}}

// gitRefs is the repository's current refs. Only git failing is an error:
// ErrNoGit outside a repository, ErrGitTimeout on a stuck one.
func gitRefs(root string) (*refSnapshot, error) {
	common := ""
	if root != "" {
		common = gitdir.Common(root)
	}
	if common != "" {
		refCache.Lock()
		s := refCache.m[common]
		refCache.Unlock()
		if s != nil && s.fp == refsFingerprint(common, s.dirs) {
			return s, nil
		}
	}
	// The fingerprint comes before the read: a ref changing between them
	// leaves a fingerprint older than the snapshot, so it is read again.
	var dirs []string
	fp := ""
	if common != "" {
		dirs = refDirs(common)
		fp = refsFingerprint(common, dirs)
	}
	s := &refSnapshot{fp: fp, dirs: dirs, heads: map[string]string{}}
	out, err := runGit(root, "for-each-ref", "--format=%(refname)%09%(objectname)%09%(symref)", "refs/heads/", "refs/remotes/origin/HEAD")
	if err != nil {
		return nil, err
	}
	for _, line := range splitLines(out) {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		if name, ok := strings.CutPrefix(f[0], "refs/heads/"); ok {
			s.heads[name] = f[1]
		} else if name, ok := strings.CutPrefix(f[2], "refs/remotes/origin/"); ok {
			s.originHead = name
		}
	}
	// No folder means no loose refs store (a reftable repository): its
	// updates leave nothing here to stat, so nothing is kept.
	if common != "" && len(dirs) > 0 {
		refCache.Lock()
		refCache.m[common] = s
		refCache.Unlock()
	}
	return s, nil
}

// refDirs lists refs/heads and every folder under it.
func refDirs(common string) []string {
	var dirs []string
	_ = filepath.WalkDir(filepath.Join(common, "refs", "heads"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	return dirs
}

// refsFingerprint changes whenever the refs could: a loose ref written or
// deleted (its folder's time), a new folder (its parent's), refs packed
// (packed-refs) or origin's HEAD moved.
func refsFingerprint(common string, dirs []string) string {
	var b strings.Builder
	stamp := func(p string) {
		if fi, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s %d %d;", p, fi.Size(), fi.ModTime().UnixNano())
		} else {
			fmt.Fprintf(&b, "%s -;", p)
		}
	}
	stamp(filepath.Join(common, "packed-refs"))
	stamp(filepath.Join(common, "refs", "remotes", "origin", "HEAD"))
	for _, d := range dirs {
		stamp(d)
	}
	return b.String()
}

// BranchHead is the sha of the branch name (refs/heads/ dropped), "" when
// there is no such branch or no repository.
func BranchHead(root, name string) string {
	s, err := gitRefs(root)
	if err != nil {
		return ""
	}
	return s.heads[name]
}

// MainHead is the repo's main branch and its commit.
func MainHead(root string) (name, sha string, err error) {
	if name, err = MainBranch(root); err != nil {
		return "", "", err
	}
	if sha = BranchHead(root, name); sha != "" {
		return name, sha, nil
	}
	// The checkout's own branch when it has neither main nor master, or
	// "HEAD" when that is detached.
	out, err := runGit(root, "rev-parse", "--verify", "--quiet", name+"^{commit}")
	if err != nil {
		return "", "", err
	}
	return name, strings.TrimSpace(out), nil
}
