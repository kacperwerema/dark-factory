// Package gitfile writes one file and commits only that file on main: the
// hall's workflow editor (internal/wfedit) and the post-mortem's agreed
// file edits (internal/postmortem) both save through it.
package gitfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// mu keeps two commits from interleaving their write and commit.
var mu sync.Mutex

// ErrStale is a file that changed since its old text was read.
var ErrStale = errors.New("the file changed since it was read")

// Commit writes data to file and commits that one file in the repo holding
// it, on main (or master), with subject and body. old is the text the
// change was made from: when the file no longer holds it, nothing is
// written (ErrStale). It refuses a checkout not on main, an untracked file
// (unless create is set and the file is new) and a file with uncommitted
// changes. When the commit fails the file goes back to old. It returns the
// short sha.
func Commit(file string, old, data []byte, create bool, subject, body string) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	raw, err := os.ReadFile(file)
	exists := err == nil
	switch {
	case err != nil && !(create && errors.Is(err, os.ErrNotExist)):
		return "", err
	case exists && !bytes.Equal(raw, old):
		return "", ErrStale
	}
	dir := filepath.Dir(file)
	if !exists {
		dir = existingDir(dir)
	}
	top, err := Git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%s is not in a git repository, so the edit can't be committed: %w", file, err)
	}
	branch, err := Git(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil || (branch != "main" && branch != "master") {
		if branch == "" {
			branch = "a detached HEAD"
		}
		return "", fmt.Errorf("the checkout at %s is on %s, not main: switch it to main, then save again", top, branch)
	}
	rel, err := filepath.Rel(RealPath(top), filepath.Join(RealPath(existingDir(filepath.Dir(file))), relTail(file)))
	if err != nil {
		return "", err
	}
	if exists {
		if out, _ := Git(top, "ls-files", "--", rel); out == "" {
			return "", fmt.Errorf("%s is not tracked in %s: commit it once by hand, then save again", rel, top)
		}
		if out, _ := Git(top, "status", "--porcelain", "--", rel); out != "" {
			return "", fmt.Errorf("%s has uncommitted changes: commit or discard them, then save again", rel)
		}
	}
	perm := os.FileMode(0o644)
	if info, err := os.Stat(file); err == nil {
		perm = info.Mode().Perm()
	}
	if !exists {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return "", err
		}
	}
	if err := WriteAtomic(file, data, perm); err != nil {
		return "", err
	}
	restore := func() error {
		if !exists {
			return os.Remove(file)
		}
		return WriteAtomic(file, raw, perm)
	}
	if !exists {
		if _, err := Git(top, "add", "--", rel); err != nil {
			if rerr := restore(); rerr != nil {
				return "", fmt.Errorf("git add failed (%v) and removing %s failed too: %w", err, rel, rerr)
			}
			return "", err
		}
	}
	if _, err := Git(top, "commit", "-q", "-m", subject, "-m", body, "--", rel); err != nil {
		if !exists {
			Git(top, "reset", "-q", "--", rel)
		}
		if rerr := restore(); rerr != nil {
			return "", fmt.Errorf("commit failed (%v) and restoring %s failed too: %w", err, rel, rerr)
		}
		return "", fmt.Errorf("commit failed, %s is back as it was: %w", rel, err)
	}
	sha, _ := Git(top, "rev-parse", "--short", "HEAD")
	return sha, nil
}

// existingDir is dir or its nearest parent that exists.
func existingDir(dir string) string {
	for {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// relTail is file's path below the nearest existing folder.
func relTail(file string) string {
	base := existingDir(filepath.Dir(file))
	rel, err := filepath.Rel(base, file)
	if err != nil {
		return filepath.Base(file)
	}
	return rel
}

// Git runs git -C dir args and returns its trimmed output.
func Git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

// RealPath resolves links (on a Mac /var is /private/var), so a path and
// git's toplevel compare.
func RealPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// WriteAtomic writes data to path through a temp file and a rename.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
