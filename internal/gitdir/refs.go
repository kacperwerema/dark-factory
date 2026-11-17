package gitdir

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RefsStamp is a stamp of everything a ref-only git answer depends on, read
// with stats alone: the size and time of the shared git dir's HEAD,
// packed-refs and config, of a linked worktree's own HEAD, and of every
// folder under refs/ (heads, tags, remotes). Every ref update renames a lock
// file into one of those folders, a new folder changes its parent's time,
// packing rewrites packed-refs and git config rewrites config, so the stamp
// changes whenever a branch, tag, remote ref, HEAD or the config could have.
// newest is the youngest time in it: a caller keeps nothing read while it
// is younger than the filesystem's clock tick could hide. ok is false
// outside a checkout's top folder, or for a repository with no loose refs
// folder (reftable): there is nothing to stat there.
func RefsStamp(root string) (stamp string, newest time.Time, ok bool) {
	common := Common(root)
	if common == "" {
		return "", time.Time{}, false
	}
	var b strings.Builder
	add := func(p string, fi os.FileInfo) {
		fmt.Fprintf(&b, "%s %d %d;", p, fi.Size(), fi.ModTime().UnixNano())
		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	files := []string{"HEAD", "packed-refs", "config"}
	paths := make([]string, 0, len(files)+1)
	for _, f := range files {
		paths = append(paths, filepath.Join(common, f))
	}
	if wt := Worktree(root); wt != "" {
		paths = append(paths, filepath.Join(wt, "HEAD"))
	}
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			add(p, fi)
		} else {
			fmt.Fprintf(&b, "%s -;", p)
		}
	}
	dirs := 0
	_ = filepath.WalkDir(filepath.Join(common, "refs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			add(p, fi)
			dirs++
		}
		return nil
	})
	if dirs == 0 {
		return "", time.Time{}, false
	}
	return b.String(), newest, true
}
