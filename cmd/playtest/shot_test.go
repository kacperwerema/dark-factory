package main

import (
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckShotURL(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:8080/p/x/hall", "https://example.localhost/"} {
		if err := checkShotURL(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "127.0.0.1:8080/p/x", "file:///etc/passwd", "http://"} {
		if err := checkShotURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestOneShotDefaults(t *testing.T) {
	s := oneShot{URL: "http://127.0.0.1/"}.withDefaults("/repo")
	if s.W != shotWidth || s.H != shotHeight || s.Out != filepath.Join("/repo", ".playtest", "shot.png") {
		t.Errorf("defaults = %+v", s)
	}
	s = oneShot{W: 390, H: 844, Out: "a.png"}.withDefaults("/repo")
	if s.W != 390 || s.H != 844 || s.Out != "a.png" {
		t.Errorf("given values replaced: %+v", s)
	}
}

func TestNoMatchNamesTheSessionsIssue(t *testing.T) {
	all := []*Session{{Name: "hall", Issue: "ui/hall"}}
	msg := noMatch(all, opts{issue: "ui/other", session: "hall", sessions: "s"})
	for _, want := range []string{`issue="ui/other"`, `session "hall" is for issue "ui/hall"`, "ISSUE=ui/hall"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	msg = noMatch(all, opts{issue: "ui/other", session: "nope", sessions: "s"})
	if !strings.Contains(msg, `issue="ui/other" session="nope"`) || strings.Contains(msg, "is for issue") {
		t.Errorf("unknown session: %q", msg)
	}
}

// floorPage is a Workshop-like page: one screen tall, the floor scrolls
// inside, and the last room sits far below the fold.
const floorPage = `<!doctype html><html><head><style>
html, body { margin: 0; height: 100%; overflow: hidden; }
.shell { display: flex; height: 100vh; }
.floor { flex: 1; overflow-y: auto; }
.room { height: 600px; border-bottom: 2px solid #000; }
</style></head><body><div class="shell"><main class="floor" id="floor">
<div class="room"></div><div class="room"></div><div class="room"></div><div class="room" id="last">last</div>
</main></div></body></html>`

// TestShotUnrollsTheScrollingFloor: FULL=1 on a page that only scrolls
// inside shoots the whole floor, as does SCROLL=<selector>; a selector that
// matches nothing fails.
func TestShotUnrollsTheScrollingFloor(t *testing.T) {
	if _, err := ChromePath(); err != nil {
		t.Skip(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, floorPage)
	}))
	defer srv.Close()
	b, err := NewBrowser(false, 800, 600)
	if err != nil {
		t.Skip(err)
	}
	defer b.Close()
	dir := t.TempDir()
	height := func(name string, s oneShot) int {
		t.Helper()
		s.URL, s.W, s.H, s.Out = srv.URL, 800, 600, filepath.Join(dir, name)
		if err := takeOneShot(b, s); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		f, err := os.Open(s.Out)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		cfg, err := png.DecodeConfig(f)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Height
	}
	if h := height("viewport.png", oneShot{}); h != 600 {
		t.Errorf("viewport shot is %d px tall, want 600", h)
	}
	for name, s := range map[string]oneShot{"full.png": {Full: true}, "scroll.png": {Full: true, Scroll: "#floor"}} {
		if h := height(name, s); h < 4*600 {
			t.Errorf("%s is %d px tall, want the whole floor (≥ 2400)", name, h)
		}
	}
	err = takeOneShot(b, oneShot{URL: srv.URL, W: 800, H: 600, Full: true, Scroll: "#nope", Out: filepath.Join(dir, "nope.png")})
	if err == nil || !strings.Contains(err.Error(), "matches no element") {
		t.Errorf("unknown selector: %v", err)
	}
}

// TestRunsRootForANamedSession: only a session asked for by name that is
// another issue's goes to the root's runs and keeps its shots.
func TestRunsRootForANamedSession(t *testing.T) {
	r := &Runner{RunsRoot: "/wt/.playtest/runs", EvidenceRuns: "/root/.playtest/runs", OwnIssue: "ui/mine", Named: "hall"}
	other := &Session{Name: "hall", Issue: "ui/hall"}
	sweep := &Session{Name: "board", Issue: "ui/board"}
	mine := &Session{Name: "hall", Issue: "ui/mine"}
	if got := r.runsRoot(other); got != r.EvidenceRuns || !r.keepsShots(other) {
		t.Errorf("named other session: root %s, keeps %v", got, r.keepsShots(other))
	}
	if got := r.runsRoot(sweep); got != r.RunsRoot || r.keepsShots(sweep) {
		t.Errorf("unnamed other session: root %s, keeps %v", got, r.keepsShots(sweep))
	}
	if got := r.runsRoot(mine); got != r.RunsRoot || !r.keepsShots(mine) {
		t.Errorf("own session: root %s, keeps %v", got, r.keepsShots(mine))
	}
	r.EvidenceRuns = ""
	if got := r.runsRoot(other); got != r.RunsRoot {
		t.Errorf("no root runs (not a worktree): %s", got)
	}
}

// TestEvidenceRunsDir: a linked worktree's root runs are the main
// checkout's; the main checkout itself has none other than its own.
func TestEvidenceRunsDir(t *testing.T) {
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(root, "init", "-q")
	git(root, "commit", "-q", "--allow-empty", "--no-verify", "-m", "x")
	wt := filepath.Join(root, ".worktrees", "ui", "x")
	git(root, "worktree", "add", "-q", "-b", "work/ui/x", wt)
	if got, want := EvidenceRunsDir(wt), filepath.Join(root, ".playtest", "runs"); got != want {
		t.Errorf("worktree: %q, want %q", got, want)
	}
	if got := EvidenceRunsDir(root); got != "" {
		t.Errorf("main checkout: %q, want \"\"", got)
	}
}
