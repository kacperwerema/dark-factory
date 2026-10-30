package wfedit

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

// saveMu keeps two saves from interleaving their write and commit.
var saveMu sync.Mutex

// Saved is a written and committed edit.
type Saved struct {
	Commit  string
	Subject string
}

// Save writes p's new text and commits that one file on main in the repo
// holding it, the subject naming the station and what changed, the body
// quoting the owner's reason. It refuses an empty reason, a preview with
// errors or no change, a file that moved since the preview (ErrStale), a
// checkout not on main (or master), and a file with uncommitted changes
// already. When the commit fails the file goes back to its old text.
func Save(p *Preview, reason string) (*Saved, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, errors.New("say why in the reason box: the commit quotes it")
	}
	if !p.OK() {
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf("the edit has problems, nothing was written: %s", strings.Join(p.Errors, "; "))
		}
		return nil, errors.New("the edit changes nothing")
	}
	saveMu.Lock()
	defer saveMu.Unlock()

	raw, err := os.ReadFile(p.File)
	if err != nil {
		return nil, err
	}
	if hashOf(raw) != p.Hash {
		return nil, ErrStale
	}
	dir := filepath.Dir(p.File)
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not in a git repository, so the edit can't be committed: %w", p.File, err)
	}
	branch, err := git(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil || (branch != "main" && branch != "master") {
		if branch == "" {
			branch = "a detached HEAD"
		}
		return nil, fmt.Errorf("the checkout at %s is on %s, not main: switch it to main, then save again", top, branch)
	}
	rel, err := filepath.Rel(realPath(top), realPath(p.File))
	if err != nil {
		return nil, err
	}
	if out, _ := git(top, "ls-files", "--", rel); out == "" {
		return nil, fmt.Errorf("%s is not tracked in %s: commit it once by hand, then save again", rel, top)
	}
	if out, _ := git(top, "status", "--porcelain", "--", rel); out != "" {
		return nil, fmt.Errorf("%s has uncommitted changes: commit or discard them, then save again", rel)
	}

	info, err := os.Stat(p.File)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(p.File, p.New, info.Mode().Perm()); err != nil {
		return nil, err
	}
	subject := fmt.Sprintf("workflow(%s): %s: %s", filepath.Base(p.File), p.Station, p.Summary)
	body := fmt.Sprintf("Owner: %q\n\nEdited from the hall's workflow editor (issue-viewer).", reason)
	if _, err := git(top, "commit", "-q", "-m", subject, "-m", body, "--", rel); err != nil {
		if rerr := writeAtomic(p.File, raw, info.Mode().Perm()); rerr != nil {
			return nil, fmt.Errorf("commit failed (%v) and restoring %s failed too: %w", err, rel, rerr)
		}
		return nil, fmt.Errorf("commit failed, %s is back as it was: %w", rel, err)
	}
	sha, _ := git(top, "rev-parse", "--short", "HEAD")
	return &Saved{Commit: sha, Subject: subject}, nil
}

func git(dir string, args ...string) (string, error) {
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

// realPath resolves links (on a Mac /var is /private/var), so a path and
// git's toplevel compare.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
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
