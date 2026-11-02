package wfedit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
