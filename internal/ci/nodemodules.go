package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/shell"
)

// A worktree's node_modules is often a symlink to the main checkout's, so
// workers don't install the same packages once per worktree. A command
// that empties node_modules then empties the folder it links to: `npm ci`
// through such a link leaves the main checkout's node_modules empty (and
// removes the link), for every other worktree and the running app. So the
// code that runs a project's ci: commands and its worktree_setup: reads the
// command first and refuses it, with the fix, when it would do that.

// NodeModulesRefusal is a command refused because it would delete
// node_modules through a symlink.
type NodeModulesRefusal struct {
	Step   string // the part of the command that would delete, e.g. "npm ci"
	Link   string // the symlink, an absolute path
	Target string // where it points
	// Writes: the step adds, removes or updates packages (npm install <pkg>,
	// pnpm add) rather than deleting the folder: it would change the main
	// checkout's packages under every worktree that shares them.
	Writes bool
}

// Error is one line naming the link, its target and the fix.
func (r *NodeModulesRefusal) Error() string {
	if r.Writes {
		return fmt.Sprintf("refused: %q would change the packages in node_modules through the symlink %s -> %s, which other worktrees share; install this worktree's own first: issue-cli worktree deps --own (or rm %s and install), then run it again",
			r.Step, r.Link, r.Target, shell.Quote(r.Link))
	}
	return fmt.Sprintf("refused: %q would delete node_modules through the symlink %s -> %s, emptying the folder it links to; remove the link first: rm %s, then run it again",
		r.Step, r.Link, r.Target, shell.Quote(r.Link))
}

// segmentSep splits a command into the simple commands sh runs one after
// another: ; && || | & and newlines.
var segmentSep = regexp.MustCompile(`&&|\|\||[;|&\n]`)

// npmDeleting are the npm subcommands that remove node_modules before they
// install: ci and its aliases.
var npmDeleting = map[string]bool{"ci": true, "clean-install": true, "ic": true, "install-clean": true, "isntall-clean": true}

// npmWriting and pnpmWriting are the subcommands that change the packages
// whatever their arguments.
var (
	npmWriting  = map[string]bool{"uninstall": true, "remove": true, "rm": true, "un": true, "r": true, "unlink": true, "update": true, "up": true, "upgrade": true, "dedupe": true, "ddp": true}
	pnpmWriting = map[string]bool{"add": true, "remove": true, "rm": true, "uninstall": true, "un": true, "update": true, "up": true, "upgrade": true}
)

// CheckNodeModules returns a *NodeModulesRefusal when running command in
// dir would delete node_modules through a symlink, nil otherwise. It reads
// the command one simple command at a time: npm ci (and its aliases), npm
// install --force, pnpm prune, pnpm install --force, and rm -r of
// node_modules/ or node_modules/* delete through a link; rm node_modules
// or unlink node_modules (no trailing slash: that removes only the link)
// before them makes the rest safe; ln -s <target> node_modules makes a link
// the later steps count; cd <dir> moves where node_modules is looked for.
// A step it can't follow (a cd into $VAR, a make target that runs npm ci)
// is left as it is.
func CheckNodeModules(dir, command string) error {
	links := map[string]*string{} // dir -> link target ("" for none), read lazily
	linkIn := func(d string) string {
		if t, ok := links[d]; ok {
			return *t
		}
		target := ""
		p := filepath.Join(d, "node_modules")
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			// A broken link has nothing behind it to lose.
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				target = resolved
			}
		}
		links[d] = &target
		return target
	}
	set := func(d, target string) { links[d] = &target }

	cur := dir
	for _, seg := range segmentSep.Split(command, -1) {
		words := commandWords(seg)
		if len(words) == 0 {
			continue
		}
		name, args := words[0], words[1:]
		switch name {
		case "cd":
			if len(args) == 0 || strings.ContainsAny(args[0], "$`~") || args[0] == "-" {
				return nil // somewhere this check can't follow
			}
			if filepath.IsAbs(args[0]) {
				cur = filepath.Clean(args[0])
			} else {
				cur = filepath.Join(cur, args[0])
			}
		case "rm", "unlink":
			recursive, paths := rmArgs(name, args)
			for _, p := range paths {
				switch {
				case isNodeModules(p):
					set(cur, "") // the link (or the folder) goes, nothing through it
				case recursive && isNodeModulesContents(p):
					if t := linkIn(cur); t != "" {
						return &NodeModulesRefusal{Step: strings.TrimSpace(seg), Link: filepath.Join(cur, "node_modules"), Target: t}
					}
				}
			}
		case "ln":
			if target, ok := symlinkTo(args); ok {
				if !filepath.IsAbs(target) {
					target = filepath.Join(cur, target)
				}
				set(cur, filepath.Clean(target))
			}
		case "npm", "pnpm":
			d, deleting, writes := packageManager(name, args, cur)
			if deleting || writes {
				if t := linkIn(d); t != "" {
					return &NodeModulesRefusal{Step: strings.TrimSpace(seg), Link: filepath.Join(d, "node_modules"), Target: t, Writes: !deleting}
				}
			}
		}
	}
	return nil
}

// commandWords splits one simple command into words, quotes dropped, and
// skips what runs the real command: VAR=value assignments, env, nice,
// time, command, exec, sudo (and their flags).
func commandWords(seg string) []string {
	var words []string
	for _, w := range strings.Fields(seg) {
		words = append(words, strings.Trim(w, `'"`))
	}
	for len(words) > 0 {
		w := words[0]
		switch {
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "="):
			words = words[1:]
		case w == "env" || w == "time" || w == "command" || w == "exec" || w == "sudo" || w == "nice":
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				flag := words[0]
				words = words[1:]
				if w == "nice" && flag == "-n" && len(words) > 0 {
					words = words[1:]
				}
			}
		default:
			return words
		}
	}
	return nil
}

// rmArgs reads rm's (or unlink's) flags and paths.
func rmArgs(name string, args []string) (recursive bool, paths []string) {
	flags := true
	for _, a := range args {
		switch {
		case flags && a == "--":
			flags = false
		case flags && a == "--recursive":
			recursive = true
		case flags && strings.HasPrefix(a, "--"):
		case flags && strings.HasPrefix(a, "-") && len(a) > 1:
			if name == "rm" && strings.ContainsAny(a, "rR") {
				recursive = true
			}
		default:
			paths = append(paths, a)
		}
	}
	return recursive, paths
}

func trimDot(p string) string {
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	return p
}

// isNodeModules is node_modules itself: rm removes a link, not what it
// points to.
func isNodeModules(p string) bool { return trimDot(p) == "node_modules" }

// isNodeModulesContents is a path that goes through the link to the whole
// folder: node_modules/, node_modules/*, node_modules/.
func isNodeModulesContents(p string) bool {
	rest, ok := strings.CutPrefix(trimDot(p), "node_modules/")
	if !ok {
		return false
	}
	switch strings.Trim(rest, "/") {
	case "", "*", ".", ".*":
		return true
	}
	return false
}

// symlinkTo reads `ln -s[f] <target> node_modules` and returns the target.
func symlinkTo(args []string) (string, bool) {
	symbolic := false
	var paths []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if a == "--symbolic" || (!strings.HasPrefix(a, "--") && strings.Contains(a, "s")) {
				symbolic = true
			}
			continue
		}
		paths = append(paths, a)
	}
	if !symbolic || len(paths) != 2 || !isNodeModules(paths[1]) {
		return "", false
	}
	return paths[0], true
}

// packageManager reads an npm or pnpm call: the folder whose node_modules
// it works on (npm --prefix moves it), whether it deletes that
// node_modules first, and whether it changes the set of packages (an
// install with package names, add, remove, update). A bare install from an
// unchanged lock file writes next to nothing, so it is not counted.
func packageManager(name string, args []string, cur string) (dir string, deleting, writes bool) {
	dir = cur
	sub := ""
	force := false
	pkgs := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--force" || a == "-f":
			force = true
		case name == "npm" && a == "--prefix" && i+1 < len(args):
			dir = joinDir(cur, args[i+1])
			i++
		case name == "npm" && strings.HasPrefix(a, "--prefix="):
			dir = joinDir(cur, strings.TrimPrefix(a, "--prefix="))
		case strings.HasPrefix(a, "-"):
		case sub == "":
			sub = a
		default:
			pkgs++
		}
	}
	switch name {
	case "npm":
		deleting = npmDeleting[sub] || (force && (sub == "install" || sub == "i" || sub == "add"))
		writes = npmWriting[sub] || ((sub == "install" || sub == "i" || sub == "add") && pkgs > 0)
	default: // pnpm
		deleting = sub == "prune" || (force && (sub == "install" || sub == "i"))
		writes = pnpmWriting[sub] || ((sub == "install" || sub == "i") && pkgs > 0)
	}
	return dir, deleting, writes
}

func joinDir(cur, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(cur, p)
}
