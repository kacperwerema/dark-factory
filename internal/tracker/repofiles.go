package tracker

import (
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
)

// RepoFiles lists the files under root as slash paths relative to it: the
// files git tracks or would track there (nothing ignored: no .worktrees/ or
// node_modules), or, when root is not a git checkout, a walk that skips
// dot-folders. The code map and the lint paths check match
// workbench.systems paths against it.
func RepoFiles(root string) []string {
	if root == "" {
		return nil
	}
	if out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output(); err == nil {
		var files []string
		for _, f := range strings.Split(string(out), "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
		return files
	}
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil && d.Type().IsRegular() {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	return files
}
