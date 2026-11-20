package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// A JavaScript install (npm ci, pnpm install) deletes and rewrites tens of
// thousands of files under node_modules, and on macOS every one of them
// goes into the fseventsd journal. Local CI keeps one worktree between
// runs, so an install whose lock file has not changed since the last green
// one has nothing to do: the runner skips it (InstallSkip) and says so in
// the log. go.sum and Cargo.lock need nothing of the kind: go and cargo
// keep their modules in a cache outside the checkout.

// StampName is the file a green install leaves in node_modules: the key it
// was installed for. A wiped node_modules takes its stamp with it.
const StampName = ".df-install-stamp"

// LockFiles are the JavaScript lock files, in the order they are looked for.
var LockFiles = []string{"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb"}

// LockFile is the first lock file in dir, "" when it has none.
func LockFile(dir string) string {
	for _, name := range LockFiles {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && fi.Mode().IsRegular() {
			return name
		}
	}
	return ""
}

// installSubcommands are, per package manager, the subcommands that install
// the whole tree from the lock file ("" is a bare `yarn`).
var installSubcommands = map[string]map[string]bool{
	"npm":  {"ci": true, "clean-install": true, "ic": true, "install-clean": true, "install": true, "i": true},
	"pnpm": {"install": true, "i": true},
	"yarn": {"": true, "install": true},
	"bun":  {"install": true, "i": true},
}

// IsInstall reports whether command is one whole-tree install and nothing
// else: a package manager, an install subcommand and flags, no package
// names (npm install foo adds a package) and no shell around it (a chain,
// a pipe, a variable, quotes).
func IsInstall(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, ";&|$`<>()'\"\\\n") {
		return false
	}
	f := strings.Fields(command)
	subs, ok := installSubcommands[f[0]]
	if !ok {
		return false
	}
	sub, rest := "", f[1:]
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		sub, rest = rest[0], rest[1:]
	}
	if !subs[sub] {
		return false
	}
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") {
			return false
		}
	}
	return true
}

// installKey is what an install in dir depends on: its lock file and
// package.json bytes, the command and the node version. "" when dir has no
// lock file: such an install is never skipped.
func installKey(dir, command string) string {
	lock := LockFile(dir)
	if lock == "" {
		return ""
	}
	lb, err := os.ReadFile(filepath.Join(dir, lock))
	if err != nil {
		return ""
	}
	pb, _ := os.ReadFile(filepath.Join(dir, "package.json"))
	h := sha256.New()
	for _, part := range [][]byte{lb, pb, []byte(strings.Join(strings.Fields(command), " ")), []byte(nodeVersion(dir))} {
		h.Write(part)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// nodeVersion is `node --version` in dir, "" without node; a test swaps it.
var nodeVersion = func(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "node", "--version")
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func stampPath(dir string) string { return filepath.Join(dir, "node_modules", StampName) }

// InstallSkip says whether running command in dir may be skipped: it is a
// whole-tree install (IsInstall), dir has a lock file, and node_modules
// holds the stamp of a green install with the same key. why is the log
// line; key is what WriteInstallStamp records after the install runs
// green ("" when command is no install, or dir has no lock file).
func InstallSkip(dir, command string) (skip bool, why, key string) {
	if !IsInstall(command) {
		return false, "", ""
	}
	if key = installKey(dir, command); key == "" {
		return false, "", ""
	}
	nm, err := os.Lstat(filepath.Join(dir, "node_modules"))
	if err != nil || !nm.IsDir() {
		return false, "", key
	}
	got, err := os.ReadFile(stampPath(dir))
	if err != nil || strings.TrimSpace(string(got)) != key {
		return false, "", key
	}
	return true, fmt.Sprintf("skipped: %s, package.json, the command and node are as at the last green install, so node_modules is kept (issue-cli ci run --reinstall installs again)", LockFile(dir)), key
}

// WriteInstallStamp records key in dir's node_modules after a green install.
func WriteInstallStamp(dir, key string) error {
	if key == "" {
		return nil
	}
	if fi, err := os.Lstat(filepath.Join(dir, "node_modules")); err != nil || !fi.IsDir() {
		return nil // nothing installed here, or a link: no stamp in someone else's folder
	}
	return os.WriteFile(stampPath(dir), []byte(key+"\n"), 0o644)
}

// ForgetInstalls removes the install stamps in the CI worktree of root, so
// the next run installs again (issue-cli ci run --reinstall). It returns
// how many it removed.
func ForgetInstalls(root string) (int, error) {
	wt := WorktreePath(root)
	n := 0
	err := filepath.WalkDir(wt, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch {
			case p == wt:
				return nil
			case d.Name() == "node_modules":
				if _, err := os.Stat(filepath.Join(p, StampName)); err == nil {
					if err := os.Remove(filepath.Join(p, StampName)); err != nil {
						return err
					}
					n++
				}
				return filepath.SkipDir
			case strings.HasPrefix(d.Name(), "."):
				return filepath.SkipDir
			}
		}
		return nil
	})
	return n, err
}
