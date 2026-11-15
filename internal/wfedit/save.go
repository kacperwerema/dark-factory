package wfedit

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kacperwerema/dark-factory/internal/gitfile"
)

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
	raw, err := os.ReadFile(p.File)
	if err != nil {
		return nil, err
	}
	if hashOf(raw) != p.Hash {
		return nil, ErrStale
	}
	subject := fmt.Sprintf("workflow(%s): %s: %s", filepath.Base(p.File), p.Station, p.Summary)
	body := fmt.Sprintf("Owner: %q\n\nEdited from the hall's workflow editor (issue-viewer).", reason)
	sha, err := gitfile.Commit(p.File, raw, p.New, false, subject, body)
	if errors.Is(err, gitfile.ErrStale) {
		return nil, ErrStale
	}
	if err != nil {
		return nil, err
	}
	return &Saved{Commit: sha, Subject: subject}, nil
}

// writeMu keeps two in-place writes from interleaving.
var writeMu sync.Mutex

// Write writes p's new text in place without git: for a file git does not
// track (a department workflow kept out of the repository). Like Save it
// refuses a preview with errors or no change and a file that moved since
// the preview (ErrStale). The text goes in aside and is renamed over, then
// is read back and parsed; when that fails the old text goes back. It never
// runs git, so it commits and stages nothing.
func Write(p *Preview) error {
	if !p.OK() {
		if len(p.Errors) > 0 {
			return fmt.Errorf("the edit has problems, nothing was written: %s", strings.Join(p.Errors, "; "))
		}
		return errors.New("the edit changes nothing")
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	raw, err := os.ReadFile(p.File)
	if err != nil {
		return err
	}
	if hashOf(raw) != p.Hash {
		return ErrStale
	}
	perm := os.FileMode(0o644)
	if info, err := os.Stat(p.File); err == nil {
		perm = info.Mode().Perm()
	}
	if err := gitfile.WriteAtomic(p.File, p.New, perm); err != nil {
		return err
	}
	back, err := os.ReadFile(p.File)
	if err == nil && !bytes.Equal(back, p.New) {
		err = errors.New("the file read back differs from what was written")
	}
	if err == nil {
		_, err = Open(p.File, p.Station)
	}
	if err != nil {
		if rerr := gitfile.WriteAtomic(p.File, raw, perm); rerr != nil {
			return fmt.Errorf("the read-back failed (%v) and restoring %s failed too: %w", err, p.File, rerr)
		}
		return fmt.Errorf("the read-back failed, %s is back as it was: %w", p.File, err)
	}
	return nil
}
