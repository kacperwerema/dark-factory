package agentrun

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/ci"
)

// Shared dependencies: a worktree that installs its own node_modules writes
// tens of thousands of files, and on macOS each goes into the fseventsd
// journal. A new worktree cut from main has main's lock file, so it can use
// the main checkout's packages as they are: ShareDeps links each
// node_modules folder the main checkout has into the worktree while the
// lock file it was installed from is the same in both. A worker whose
// branch changes the dependencies runs `issue-cli worktree deps --own`
// (ci.CheckNodeModules refuses package writes through the link and says
// so).

// depsMaxDepth is how deep under the checkout a node_modules is looked for:
// the root and workspace packages (packages/web/node_modules is depth 2).
const depsMaxDepth = 3

// DepLink is one node_modules ShareDeps saw: Dir is its folder relative to
// the checkout ("." for the root), State what it found or did.
type DepLink struct {
	Dir   string
	State string
	// Lock is the lock file that decides, relative to the checkout.
	Lock string
	// Err is why a DepFailed folder was not made.
	Err string
}

// The DepLink states.
const (
	DepLinked   = "linked"    // ShareDeps made the link
	DepShared   = "shared"    // a link to the main checkout's was already there
	DepOwn      = "own"       // the worktree has its own node_modules folder
	DepLockDiff = "lock-diff" // the lock file differs: nothing linked
	DepOther    = "other"     // a link somewhere else, left as it is
	DepCopied   = "copied"    // CopyDeps made the worktree's own copy
	DepFailed   = "failed"    // the link or copy could not be made (Err)
)

// MainDepDirs is every folder (relative, "." for the root) under main that
// has a node_modules folder of its own, at most depsMaxDepth deep, skipping
// hidden folders, .worktrees and node_modules itself.
func MainDepDirs(main string) []string {
	var dirs []string
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		abs := filepath.Join(main, rel)
		if fi, err := os.Lstat(filepath.Join(abs, "node_modules")); err == nil && fi.IsDir() {
			dirs = append(dirs, rel)
		}
		if depth >= depsMaxDepth {
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
	sort.Strings(dirs)
	return dirs
}

// lockFor is the lock file that rules dir (relative): the nearest one from
// dir up to the checkout's root, relative to the checkout; "" for none.
func lockFor(root, dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if name := ci.LockFile(filepath.Join(root, d)); name != "" {
			return filepath.Join(d, name)
		}
		if d == "." || d == "/" || d == "" {
			return ""
		}
	}
}

func sameFile(a, b string) bool {
	ab, err1 := os.ReadFile(a)
	bb, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && bytes.Equal(ab, bb)
}

// ShareDeps links, in the worktree wt, each node_modules folder the main
// checkout main has, where wt has the same folder with a package.json, no
// node_modules of its own, and the same lock file as main. It never
// removes or replaces anything. When a link would show as untracked (a
// .gitignore that says node_modules/ ignores folders, not links), it adds
// node_modules to the repository's info/exclude.
func ShareDeps(main, wt string) []DepLink {
	return placeDeps(main, wt, DepLinked, func(target, link string) error {
		return os.Symlink(target, link)
	})
}

// CopyDeps is ShareDeps for worktree_deps: own: where ShareDeps would link,
// it makes the worktree's own copy of the main checkout's folder instead (a
// clone on APFS: no download, and an install in the worktree writes only
// into its copy, never into the main checkout's).
func CopyDeps(main, wt string) []DepLink {
	return placeDeps(main, wt, DepCopied, func(target, link string) error {
		_, err := ci.CloneDir(target, link)
		return err
	})
}

func placeDeps(main, wt, done string, place func(target, link string) error) []DepLink {
	var out []DepLink
	placed := false
	for _, d := range MainDepDirs(main) {
		if _, err := os.Stat(filepath.Join(wt, d, "package.json")); err != nil {
			continue
		}
		lock := lockFor(main, d)
		if lock == "" {
			continue // nothing says the packages fit this tree
		}
		link := filepath.Join(wt, d, "node_modules")
		target := filepath.Join(main, d, "node_modules")
		dl := DepLink{Dir: d, Lock: lock}
		if fi, err := os.Lstat(link); err == nil {
			switch {
			case fi.Mode()&os.ModeSymlink == 0:
				dl.State = DepOwn
			case pointsTo(link, target):
				dl.State = DepShared
			default:
				dl.State = DepOther
			}
			out = append(out, dl)
			continue
		}
		if !sameFile(filepath.Join(main, lock), filepath.Join(wt, lock)) {
			dl.State = DepLockDiff
			out = append(out, dl)
			continue
		}
		if err := place(target, link); err != nil {
			dl.State, dl.Err = DepFailed, err.Error()
			out = append(out, dl)
			continue
		}
		dl.State, placed = done, true
		out = append(out, dl)
	}
	if placed {
		excludeLinks(wt)
	}
	return out
}

// MissingDeps is each folder (relative) the main checkout has a
// node_modules for, ruled by a lock file, where the worktree wt has the
// package.json but no node_modules: a tree whose scripts can't run yet.
func MissingDeps(main, wt string) []string {
	var missing []string
	for _, d := range MainDepDirs(main) {
		if _, err := os.Stat(filepath.Join(wt, d, "package.json")); err != nil {
			continue
		}
		if lockFor(main, d) == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(wt, d, "node_modules")); err != nil {
			missing = append(missing, depName(d))
		}
	}
	return missing
}

func pointsTo(link, target string) bool {
	got, err1 := filepath.EvalSymlinks(link)
	want, err2 := filepath.EvalSymlinks(target)
	return err1 == nil && err2 == nil && got == want
}

// excludeLinks adds node_modules (no slash: it matches a link too) to the
// repository's shared info/exclude when git would list a link as
// untracked: an untracked file makes the tree dirty, and a dirty tree is
// never removed after its branch lands.
func excludeLinks(wt string) {
	out, err := exec.Command("git", "-C", wt, "status", "--porcelain", "--untracked-files=all").Output()
	if err != nil || !untrackedLink(string(out)) {
		return
	}
	common, err := exec.Command("git", "-C", wt, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return
	}
	exclude := filepath.Join(strings.TrimSpace(string(common)), "info", "exclude")
	_ = os.MkdirAll(filepath.Dir(exclude), 0o755)
	prev, _ := os.ReadFile(exclude)
	add := "node_modules\n"
	if len(prev) > 0 && !bytes.HasSuffix(prev, []byte("\n")) {
		add = "\n" + add
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString("# dark-factory: worktrees link node_modules (issue-cli worktree deps)\n" + add)
}

func untrackedLink(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		if p, ok := strings.CutPrefix(line, "?? "); ok && filepath.Base(strings.TrimSuffix(p, "/")) == "node_modules" {
			return true
		}
	}
	return false
}

// DepsStep is ShareDeps' result as one dispatch step, ok in every case:
// a tree that can't share installs its own, as before.
func DepsStep(main string, links []DepLink) (Step, bool) {
	if len(links) == 0 {
		return Step{}, false
	}
	var linked, shared, copied, diff, failed []string
	for _, l := range links {
		switch l.State {
		case DepCopied:
			copied = append(copied, depName(l.Dir))
		case DepFailed:
			failed = append(failed, depName(l.Dir)+" ("+l.Err+")")
		case DepLinked:
			linked = append(linked, depName(l.Dir))
		case DepShared:
			shared = append(shared, depName(l.Dir))
		case DepLockDiff:
			diff = append(diff, depName(l.Dir)+" ("+l.Lock+" differs)")
		}
	}
	var parts []string
	if len(linked) > 0 {
		parts = append(parts, fmt.Sprintf("linked %s to the main checkout's (%s), lock file unchanged", strings.Join(linked, ", "), main))
	}
	if len(copied) > 0 {
		parts = append(parts, fmt.Sprintf("made %s this worktree's own copy of the main checkout's (%s), lock file unchanged", strings.Join(copied, ", "), main))
	}
	if len(shared) > 0 {
		parts = append(parts, "already shared: "+strings.Join(shared, ", "))
	}
	if len(failed) > 0 {
		parts = append(parts, "not made, install in the worktree: "+strings.Join(failed, ", "))
	}
	if len(diff) > 0 {
		parts = append(parts, "not shared, install in the worktree: "+strings.Join(diff, ", "))
	}
	if len(parts) == 0 {
		return Step{}, false
	}
	name := "Shared node_modules: " + strings.Join(parts, "; ")
	if len(linked)+len(shared) > 0 {
		name += "; before you add, remove or update a package: issue-cli worktree deps --own"
	}
	return Step{Name: name, Status: "ok"}, true
}

func depName(dir string) string {
	if dir == "." {
		return "node_modules"
	}
	return filepath.Join(dir, "node_modules")
}

// OwnDeps turns each node_modules link in wt that points into main into
// wt's own copy of it (ci.OwnCopy: a clone on APFS, the link removed, never
// what it points to) and returns the folders, relative, it copied. The
// caller then installs over the copy, which writes only what changed.
func OwnDeps(main, wt string) ([]string, error) {
	var owned []string
	for _, d := range MainDepDirs(main) {
		link := filepath.Join(wt, d, "node_modules")
		fi, err := os.Lstat(link)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 || !pointsTo(link, filepath.Join(main, d, "node_modules")) {
			continue
		}
		if _, err := ci.OwnCopy(link); err != nil {
			return owned, err
		}
		owned = append(owned, d)
	}
	return owned, nil
}

// InstallCommand is the whole-tree install for the lock file in dir, ""
// when it has none.
func InstallCommand(dir string) string {
	switch ci.LockFile(dir) {
	case "package-lock.json", "npm-shrinkwrap.json":
		return "npm ci"
	case "pnpm-lock.yaml":
		return "pnpm install --frozen-lockfile"
	case "yarn.lock":
		return "yarn install"
	case "bun.lock", "bun.lockb":
		return "bun install"
	}
	return ""
}
