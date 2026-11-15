// Package gitdir finds a checkout's shared git dir from its files, with no
// git call, for the caches that stat what's in there instead of asking git.
package gitdir

import (
	"os"
	"path/filepath"
	"strings"
)

// Common is the repository's shared git dir for a checkout's top folder,
// read from the files: <root>/.git when it is a folder, else the gitdir its
// .git file names and that dir's commondir. "" when root isn't a checkout's
// top folder: a caller then keeps nothing.
func Common(root string) string {
	dotgit := filepath.Join(root, ".git")
	fi, err := os.Stat(dotgit)
	if err != nil {
		return ""
	}
	if fi.IsDir() {
		return resolved(dotgit)
	}
	gitdir := pointed(root)
	if gitdir == "" {
		return ""
	}
	cd, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(cd))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	return resolved(common)
}

// Worktree is the git dir of a linked worktree, the one its .git file names
// (<common>/worktrees/<name>). "" when root's .git is a folder or missing.
func Worktree(root string) string {
	if gitdir := pointed(root); gitdir != "" {
		return resolved(gitdir)
	}
	return ""
}

// pointed is the gitdir root's .git file names, "" when .git is no such file.
func pointed(root string) string {
	data, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return ""
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	return gitdir
}

func resolved(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}
