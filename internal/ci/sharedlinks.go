package ci

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// A worker's worktree links the main checkout's node_modules, the root's
// and each workspace package's (packages/web/node_modules), so it installs
// nothing while its lock file is main's. An install run in such a tree
// writes through every link: npm ci at the root empties the root's and each
// workspace's node_modules, so through the links it empties the main
// checkout's (the 2026-11-19 23:39 incident in a workspace project, where
// the old check read only <dir>/node_modules). PrepareInstall runs before
// every command ship, local CI's ci.run, prove --run and worktree_setup
// run: while every linked folder's lock file and package.json are main's
// it skips a whole-tree install (there is nothing to install); otherwise,
// when the command installs, it first turns each link into the tree's own
// copy of what it points to (a clone on APFS, no download) and the
// install then works on that copy.

// sharedLinksDepth is how deep under the tree's root a node_modules link
// is looked for: the root and workspace packages (packages/web is depth 2).
const sharedLinksDepth = 3

// SharedLink is one node_modules symlink in a tree.
type SharedLink struct {
	Rel    string // its folder relative to the tree's root, "." for the root
	Link   string // the link, absolute
	Target string // what it resolves to
}

// TreeRoot is the checkout that holds dir: the nearest folder from dir up
// with a .git (a worktree's is a file); dir itself when there is none.
func TreeRoot(dir string) string {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			return filepath.Clean(dir)
		}
	}
}

// SharedLinks is every node_modules symlink under root that resolves to a
// folder, at most sharedLinksDepth deep, skipping hidden folders,
// node_modules and vendor. A broken link has nothing behind it to lose.
func SharedLinks(root string) []SharedLink {
	var links []SharedLink
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		abs := filepath.Join(root, rel)
		p := filepath.Join(abs, "node_modules")
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			if t, err := filepath.EvalSymlinks(p); err == nil {
				if ti, err := os.Stat(t); err == nil && ti.IsDir() {
					links = append(links, SharedLink{Rel: rel, Link: p, Target: t})
				}
			}
		}
		if depth >= sharedLinksDepth {
			return
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return
		}
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() || strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor" {
				continue
			}
			walk(filepath.Join(rel, n), depth+1)
		}
	}
	walk(".", 0)
	sort.Slice(links, func(i, j int) bool { return links[i].Rel < links[j].Rel })
	return links
}

// lockUnder is the lock file that rules rel: the nearest one from rel up
// to root, relative to root; "" for none.
func lockUnder(root, rel string) string {
	for d := rel; ; d = filepath.Dir(d) {
		if name := LockFile(filepath.Join(root, d)); name != "" {
			return filepath.Join(d, name)
		}
		if d == "." || d == "/" || d == "" {
			return ""
		}
	}
}

// linkUnchanged reports whether the folder holding link l in the tree
// root has the same lock file and package.json as the folder its target
// sits in, in the checkout the target belongs to: what was installed there
// is what an install here would install.
func linkUnchanged(root string, l SharedLink) bool {
	otherDir := filepath.Dir(l.Target)
	otherRoot := otherDir
	if l.Rel != "." {
		for range strings.Split(filepath.ToSlash(l.Rel), "/") {
			otherRoot = filepath.Dir(otherRoot)
		}
		if filepath.Join(otherRoot, l.Rel) != otherDir {
			return false // the link points somewhere laid out differently
		}
	}
	lock := lockUnder(root, l.Rel)
	if lock == "" || !sameBytes(filepath.Join(root, lock), filepath.Join(otherRoot, lock), false) {
		return false
	}
	return sameBytes(filepath.Join(root, l.Rel, "package.json"), filepath.Join(otherDir, "package.json"), true)
}

// sameBytes: both files read and equal; with bothMissing, two missing
// files count as equal too.
func sameBytes(a, b string, bothMissing bool) bool {
	ab, errA := os.ReadFile(a)
	bb, errB := os.ReadFile(b)
	if errA != nil || errB != nil {
		return bothMissing && os.IsNotExist(errA) && os.IsNotExist(errB)
	}
	return bytes.Equal(ab, bb)
}

// yarnBunInstalling are the yarn and bun subcommands that install or
// change packages ("" is a bare yarn).
var yarnBunInstalling = map[string]bool{"": true, "install": true, "i": true, "add": true, "remove": true, "rm": true, "upgrade": true, "up": true, "update": true}

// Installs reports whether command runs a package manager that writes
// node_modules in any of its simple commands: an install (npm ci, npm
// install, pnpm install, yarn, bun install), or one that adds, removes or
// updates packages. A command it can't read into (a make target that runs
// npm ci) is not counted.
func Installs(command string) bool {
	for _, seg := range segmentSep.Split(command, -1) {
		words := commandWords(seg)
		if len(words) == 0 {
			continue
		}
		name, args := words[0], words[1:]
		switch name {
		case "npm", "pnpm":
			_, deleting, writes := packageManager(name, args, ".")
			if deleting || writes || installSubcommands[name][firstWord(args)] {
				return true
			}
		case "yarn", "bun":
			if yarnBunInstalling[firstWord(args)] {
				return true
			}
		}
	}
	return false
}

// firstWord is the first argument that is no flag, "" for none.
func firstWord(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// PrepareInstall readies the tree holding dir for command. In a tree with
// node_modules links (at any depth) and a command that installs: when
// command is a whole-tree install (IsInstall) and every linked folder's
// lock file and package.json are those of what it links to, skip is true
// and note says why; otherwise each link becomes the tree's own copy
// (OwnCopy) and note names them. Then, and in every other case, it returns
// CheckNodeModules' refusal, if any (rm -r node_modules/* through a link).
func PrepareInstall(dir, command string) (skip bool, note string, err error) {
	root := TreeRoot(dir)
	links := SharedLinks(root)
	if len(links) == 0 || !Installs(command) {
		return false, "", CheckNodeModules(dir, command)
	}
	unchanged := true
	for _, l := range links {
		if !linkUnchanged(root, l) {
			unchanged = false
			break
		}
	}
	if unchanged && IsInstall(command) {
		return true, fmt.Sprintf("skipped: %s: node_modules is shared with the main checkout (%s) and %s and package.json are unchanged, so there is nothing to install",
			command, linkNames(links), lockUnder(root, links[0].Rel)), nil
	}
	var owned []string
	for _, l := range links {
		how, err := OwnCopy(l.Link)
		if err != nil {
			return false, strings.Join(owned, "; "), fmt.Errorf("refused: %q would install through the node_modules link %s -> %s, and its own copy failed: %w", command, l.Link, l.Target, err)
		}
		owned = append(owned, fmt.Sprintf("%s is its own now (%s of %s)", relName(l.Rel), how, l.Target))
	}
	note = "before " + command + ": " + strings.Join(owned, "; ")
	return false, note, CheckNodeModules(dir, command)
}

func relName(rel string) string {
	if rel == "." {
		return "node_modules"
	}
	return filepath.Join(rel, "node_modules")
}

func linkNames(links []SharedLink) string {
	names := make([]string, len(links))
	for i, l := range links {
		names[i] = relName(l.Rel)
	}
	return strings.Join(names, ", ")
}

// cloneCommand is how OwnCopy copies a folder: a clone where the file
// system has them (APFS on macOS, reflinks on Linux), so the copy takes no
// new data and no download. A test swaps it.
var cloneCommand = func(src, dst string) (how string, err error) {
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("cp", "-c", "-R", src, dst).CombinedOutput(); err == nil {
			return "a clone", nil
		} else if rmErr := os.RemoveAll(dst); rmErr != nil {
			return "", fmt.Errorf("cp -c: %s; %w", strings.TrimSpace(string(out)), rmErr)
		}
		if out, err := exec.Command("cp", "-pR", src, dst).CombinedOutput(); err != nil {
			return "", fmt.Errorf("cp -pR: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return "a copy", nil
	}
	if out, err := exec.Command("cp", "-a", "--reflink=auto", src, dst).CombinedOutput(); err != nil {
		return "", fmt.Errorf("cp -a: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return "a copy (a reflink where the file system has them)", nil
}

// OwnCopy replaces the node_modules symlink link with a copy of the folder
// it points to: copied beside it first, then the link removed (never what
// it points to) and the copy moved in. Symlinks inside are copied as links:
// npm's are relative (node_modules/@app/web -> ../../packages/web), so they
// then point into this tree. how says how it was copied.
func OwnCopy(link string) (how string, err error) {
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "", err
	}
	tmp := link + ".df-own"
	if err := os.RemoveAll(tmp); err != nil {
		return "", err
	}
	if how, err = cloneCommand(target, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Remove(link); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, link); err != nil {
		return "", err
	}
	_ = os.Remove(filepath.Join(link, StampName)) // the copy was not installed here
	return how, nil
}
