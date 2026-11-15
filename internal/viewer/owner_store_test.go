package viewer

// The owner's sessions kept across a viewer restart (owner_store.go): a
// signed-in browser stays signed in, a signed-out or expired one doesn't, and
// the file holds only hashes, 0600.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gated reports whether the owner gate refused cookie c on the first O route.
func gated(srv *Server, c *http.Cookie) bool {
	rec := ownerDo(srv, ownerRoutes[0].method, ownerRoutes[0].path, ownerRoutes[0].body, nil, c)
	return rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), ownerRefused)
}

// restarted is a new viewer over the same sessions file.
func restarted(t *testing.T, file string, now func() time.Time) *Server {
	t.Helper()
	srv, _ := ownerTestServer(t)
	if now != nil {
		srv.owner.now = now
	}
	if err := srv.owner.keepIn(file); err != nil {
		t.Fatalf("keepIn: %v", err)
	}
	return srv
}

func TestOwnerSessionSurvivesRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "owner", "sessions.json")
	first := restarted(t, file, nil)
	c := pairOwner(t, first)
	if gated(first, c) {
		t.Fatal("the signed-in browser is refused before the restart")
	}

	second := restarted(t, file, nil)
	if gated(second, c) {
		t.Fatal("the signed-in browser is refused after a restart")
	}

	// Signing out removes the session from the file too.
	rec := ownerDo(second, "POST", "/owner/signout", "", map[string]string{"Origin": "http://127.0.0.1:8080"}, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("signout = %d", rec.Code)
	}
	third := restarted(t, file, nil)
	if !gated(third, c) {
		t.Fatal("a signed-out session came back after a restart")
	}
}

func TestOwnerSessionExpiredDroppedAtRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "owner", "sessions.json")
	start := time.Now()
	first := restarted(t, file, func() time.Time { return start })
	c := pairOwner(t, first)

	later := start.Add(ownerSessionTTL + time.Hour)
	second := restarted(t, file, func() time.Time { return later })
	if !gated(second, c) {
		t.Fatal("an expired session is still valid after a restart")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), hashToken(c.Value)) {
		t.Errorf("the expired session is still in the file: %s", b)
	}
}

func TestOwnerSessionFileHoldsNoTokenAnd0600(t *testing.T) {
	file := filepath.Join(t.TempDir(), "owner", "sessions.json")
	srv := restarted(t, file, nil)
	c := pairOwner(t, srv)
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), c.Value) {
		t.Fatal("the session file holds the raw token")
	}
	if !strings.Contains(string(b), hashToken(c.Value)) {
		t.Errorf("the session file has no hash of the session: %s", b)
	}
	fi, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("session file mode %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("session folder mode %v, want 0700", di.Mode().Perm())
	}
}

// A file others can read or write is not trusted: the viewer starts signed
// out rather than load sessions anyone could have added.
func TestOwnerSessionFileLooseModeIgnored(t *testing.T) {
	file := filepath.Join(t.TempDir(), "owner", "sessions.json")
	srv := restarted(t, file, nil)
	c := pairOwner(t, srv)
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	next, _ := ownerTestServer(t)
	if err := next.owner.keepIn(file); err == nil {
		t.Error("keepIn trusted a 0644 file without a word")
	}
	if !gated(next, c) {
		t.Fatal("sessions from a 0644 file were trusted")
	}
}

// The refusal names the sign-in for the owner's browser (owner-signin.js
// opens it in place); the text for agents and the CLI stays.
func TestOwnerRefusalOffersSignIn(t *testing.T) {
	srv, _ := ownerTestServer(t)
	rec := ownerDo(srv, ownerRoutes[0].method, ownerRoutes[0].path, ownerRoutes[0].body, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("= %d, want 403", rec.Code)
	}
	if got := rec.Header().Get(ownerSignInHeader); got != "/owner" {
		t.Errorf("%s = %q, want /owner", ownerSignInHeader, got)
	}
	if !strings.Contains(rec.Body.String(), ownerRefused) {
		t.Errorf("body %q lost the refusal text", rec.Body.String())
	}
}
