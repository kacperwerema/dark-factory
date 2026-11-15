// Package gitmain finds a repository's main checkout, the branch it works
// on and that branch's head: the three git questions local CI and health
// snapshots both start from. It knows nothing of either.
package gitmain

import (
	"bytes"
	"context"
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/gitdir"
	"os/exec"
	"strings"
	"time"
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
// dir.
func Root(dir string) string {
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

// Branch is configured (ci.branch in workflow.yaml) when set, else the
// branch checked out in the main checkout root.
func Branch(root, configured string) (string, error) {
	if b := strings.TrimSpace(configured); b != "" {
		return b, nil
	}
	out, err := Git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || out == "" {
		return "", fmt.Errorf("the main checkout %s has no branch checked out; set ci.branch in workflow.yaml", root)
	}
	return out, nil
}

// Head is the branch's current commit.
func Head(root, branch string) (string, error) {
	return Git(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
}
