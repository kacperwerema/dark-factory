package viewer

// The owner's sessions kept across a viewer restart (docs/API/security.md,
// "Owner sign-in"). Only the SHA-256 of each session token is written, with
// its expiry and the browser's user agent, to one 0600 file in a 0700 folder
// under the state dir ($XDG_STATE_HOME/ai-workflow-engine/owner/). The file
// is read once at start (keepIn), expired sessions dropped, and rewritten
// whole (temp file + rename) on every sign-in, sign-out and expiry. A file
// others may read or write is not trusted: the viewer starts signed out.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kacperwerema/dark-factory/internal/jsonfile"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// ownerSessionsFile is where the owner's viewer keeps its sessions.
func ownerSessionsFile() string {
	return filepath.Join(viewerstate.Dir(), "owner", "sessions.json")
}

type ownerSession struct {
	Exp time.Time
	UA  string
}

type ownerSessionRecord struct {
	Hash    string    `json:"hash"`
	Expires time.Time `json:"expires"`
	UA      string    `json:"user_agent,omitempty"`
}

type ownerSessionFile struct {
	Sessions []ownerSessionRecord `json:"sessions"`
}

// keepIn loads the sessions in file (dropping expired ones) and writes every
// later change there. A missing file is no sessions; a file with group or
// other permission bits is ignored with an error, and replaced on the next
// sign-in.
func (o *ownerAuth) keepIn(file string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.file = file
	fi, err := os.Stat(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("owner sessions %s: mode %v, want 0600: not loaded, sign in again", file, fi.Mode().Perm())
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var f ownerSessionFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("owner sessions %s: %w", file, err)
	}
	now, dropped := o.now(), false
	for _, s := range f.Sessions {
		if len(s.Hash) != 64 || !now.Before(s.Expires) {
			dropped = true
			continue
		}
		o.sessions[s.Hash] = ownerSession{Exp: s.Expires, UA: s.UA}
	}
	if dropped {
		return o.saveLocked()
	}
	return nil
}

// saveLocked writes the sessions to o.file; o.mu is held. No file, no write.
func (o *ownerAuth) saveLocked() error {
	if o.file == "" {
		return nil
	}
	f := ownerSessionFile{Sessions: []ownerSessionRecord{}}
	for h, s := range o.sessions {
		f.Sessions = append(f.Sessions, ownerSessionRecord{Hash: h, Expires: s.Exp, UA: s.UA})
	}
	dir := filepath.Dir(o.file)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return jsonfile.Write(o.file, f, 0o600)
}

// save writes the sessions and reports a failure on the viewer's stderr:
// the session still works until the next restart.
func (o *ownerAuth) saveOrWarn() {
	if err := o.saveLocked(); err != nil {
		fmt.Fprintf(os.Stderr, "owner sign-in: could not keep the sessions in %s: %v (a restart will sign you out)\n", o.file, err)
	}
}
