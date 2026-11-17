// Package gitmain finds a repository's main checkout, the branch it works
// on and that branch's head: the three git questions local CI and health
// snapshots both start from. It knows nothing of either.
package gitmain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"github.com/kacperwerema/dark-factory/internal/recent"
)

// Timeout bounds each git call.
var Timeout = 30 * time.Second

// Git runs git -C dir with args under Timeout and returns its trimmed
// stdout; the error carries git's stderr.
func Git(dir string, args ...string) (string, error) {
	return GitWithin(Timeout, dir, args...)
}

// GitWithin is Git under its own time limit.
func GitWithin(limit time.Duration, dir string, args ...string) (string, error) {
	return gitRun(limit, dir, "", args...)
}

// GitInput is Git with stdin: one call for a batch (cat-file
// --batch-check, rev-list --stdin) instead of one per item.
func GitInput(dir, stdin string, args ...string) (string, error) {
	return gitRun(Timeout, dir, stdin, args...)
}

func gitRun(limit time.Duration, dir, stdin string, args ...string) (string, error) {
	if gitdir.NoRepo(dir, args...) {
		return "", fmt.Errorf("git %s: not a git repository", strings.Join(args, " "))
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Root is the main checkout of the repository dir belongs to (the first
// entry of `git worktree list`), so state kept there lives in one place even
// when a project's workdir is a worktree or a subdirectory. Falls back to
// dir. While readings are kept (package recent) one dir's answer stands.
//
// A checkout's top folder is answered from its files (rootFromFiles): one
// show called Root three times, each a git process that took seconds on a
// loaded machine (cli/issue-cli-show-takes-under-a-second-on-this-board).
func Root(dir string) string {
	return roots.Get(dir, func() string { return readRoot(dir) })
}

var roots recent.Readings[string, string]

func readRoot(dir string) string {
	if root := rootFromFiles(dir); root != "" {
		return root
	}
	out, err := Git(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return dir
	}
	for _, line := range strings.Split(out, "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			return path
		}
	}
	return dir
}

// rootFromFiles is the main checkout of a non-bare repository whose top
// folder is dir: the folder holding its shared .git dir, as `git worktree
// list` names it. "" when the files don't say (a subdirectory, a bare
// repository, core.worktree or core.bare in its config): Root asks git.
func rootFromFiles(dir string) string {
	common := gitdir.Common(dir)
	if common == "" || filepath.Base(common) != ".git" {
		return ""
	}
	cfg, err := os.ReadFile(filepath.Join(common, "config"))
	if err != nil || coreMovesCheckout(string(cfg)) {
		return ""
	}
	// extensions.worktreeConfig: the main checkout's own settings.
	if cfg, err := os.ReadFile(filepath.Join(common, "config.worktree")); err == nil && coreMovesCheckout(string(cfg)) {
		return ""
	}
	return filepath.Dir(common)
}

// coreMovesCheckout reports whether a git config file's [core] section sets
// worktree, or bare to true: then the main checkout isn't the folder holding
// .git.
func coreMovesCheckout(cfg string) bool {
	core := false
	for _, line := range strings.Split(cfg, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			name := strings.ToLower(strings.TrimSpace(strings.Trim(line, "[]")))
			core = name == "core"
			continue
		}
		if !core {
			continue
		}
		key, val, _ := strings.Cut(line, "=")
		key, val = strings.ToLower(strings.TrimSpace(key)), strings.ToLower(strings.TrimSpace(val))
		if key == "worktree" || (key == "bare" && val != "false" && val != "no" && val != "off" && val != "0") {
			return true
		}
	}
	return false
}

// Branch is the branch the project's work lands on: configured (ci.branch
// in workflow.yaml) when set, else PickBranch's order over the repository's
// refs. It is the one resolver: tracker.MainBranch and agentrun.MainBranch
// pick by the same order (TestMainBranchResolvedOnce).
func Branch(root, configured string) (string, error) {
	if b := strings.TrimSpace(configured); b != "" {
		return b, nil
	}
	out, err := Git(root, "for-each-ref", "--format=%(refname)%00%(symref)", "refs/heads/main", "refs/heads/master", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("the main checkout %s: %w", root, err)
	}
	heads, originHead := map[string]bool{}, ""
	for _, line := range strings.Split(out, "\n") {
		ref, sym, _ := strings.Cut(line, "\x00")
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			heads[name] = true
		} else if name, ok := strings.CutPrefix(sym, "refs/remotes/origin/"); ok {
			originHead = name
		}
	}
	if originHead != "" && !heads[originHead] {
		if _, err := Git(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+originHead); err == nil {
			heads[originHead] = true
		}
	}
	checkout, _ := Git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if b := PickBranch(originHead, func(name string) bool { return heads[name] }, checkout); b != "" {
		return b, nil
	}
	return "", fmt.Errorf("the main checkout %s has no branch checked out; set ci.branch in workflow.yaml", root)
}

// PickBranch is the main branch's order with no ci.branch: origin's HEAD,
// else main, else master (each only when it is a local branch: has says),
// else the main checkout's branch; "" when none is known.
func PickBranch(originHead string, has func(name string) bool, checkout string) string {
	for _, name := range []string{originHead, "main", "master"} {
		if name != "" && has(name) {
			return name
		}
	}
	if checkout == "HEAD" {
		return ""
	}
	return strings.TrimSpace(checkout)
}

// Head is the branch's current commit.
func Head(root, branch string) (string, error) {
	return Git(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
}

// HasRemote reports whether the repository dir belongs to has a remote.
// An error (dir is no repository, git failed) means it can't tell, which a
// caller must not read as "no remote".
func HasRemote(dir string) (bool, error) {
	out, err := Git(dir, "remote")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}
