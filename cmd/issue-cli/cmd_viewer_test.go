package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tmuxguard"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// standInViewer is the test binary run as issue-viewer (TestMain, under
// ISSUE_CLI_TEST_AS_VIEWER=1): args are its state dir and port. It records
// itself in <state>/ai-workflow-engine/viewer.json with the version in
// <state>/version, as the viewer does, and answers every request.
func standInViewer(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "stand-in viewer: want <state dir> <port>")
		os.Exit(2)
	}
	state, port := args[0], args[1]
	os.Setenv("XDG_STATE_HOME", state)
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stand-in viewer:", err)
		os.Exit(1)
	}
	version, _ := os.ReadFile(filepath.Join(state, "version"))
	url := "http://127.0.0.1:" + port
	if err := viewerstate.Write(viewerstate.State{Config: filepath.Join(state, "projects.yaml"), URL: url, PID: os.Getpid(), Started: time.Now(), Version: strings.TrimSpace(string(version))}); err != nil {
		fmt.Fprintln(os.Stderr, "stand-in viewer:", err)
		os.Exit(1)
	}
	fmt.Printf("Issue Viewer running at %s\n", url)
	http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") }))
	os.Exit(0)
}

// viewerServer is a private tmux server: plain tmux calls in this process
// reach it (TMUX_TMPDIR), the test's own go through -S sock.
type viewerServer struct {
	sock, state, bin string
}

func newViewerServer(t *testing.T) *viewerServer {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sockDir, err := os.MkdirTemp("/tmp", "vrs-")
	if err != nil {
		t.Fatal(err)
	}
	s := &viewerServer{sock: filepath.Join(sockDir, "tmux-"+strconv.Itoa(os.Getuid()), "default"), state: realTempDir(t), bin: t.TempDir()}
	if err := os.MkdirAll(filepath.Dir(s.sock), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		tmuxguard.KillServer(s.sock, envWithout("TMUX"))
		_ = os.RemoveAll(sockDir)
	})
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", sockDir)
	t.Setenv("XDG_STATE_HOME", s.state)
	return s
}

func (s *viewerServer) tmux(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("tmux", append([]string{"-S", s.sock}, args...)...)
	cmd.Env = envWithout("TMUX")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v %s", args, err, out)
	}
	return string(out)
}

// startViewer runs the stand-in as the start command of session
// issue-viewer, the way the owner runs the real one, and waits until it
// answers. It returns the stand-in's pid.
func (s *viewerServer) startViewer(t *testing.T, version string) int {
	t.Helper()
	s.setVersion(t, version)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	standin.Write(t, filepath.Join(s.bin, "issue-viewer"), "bin=$1; shift; ISSUE_CLI_TEST_AS_VIEWER=1 exec \"$bin\" \"$@\"\n")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	s.tmux(t, "new-session", "-d", "-s", "issue-viewer", filepath.Join(s.bin, "issue-viewer")+" "+exe+" "+s.state+" "+port)
	var rec viewerstate.State
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if r, err := viewerstate.Read(); err == nil && r.PID > 0 && viewerstate.Alive(r.PID) && httpAnswers(r.URL) {
			rec = r
			break
		}
		if time.Now().After(deadline) {
			out, _ := exec.Command("tmux", "-S", s.sock, "capture-pane", "-p", "-t", "issue-viewer").CombinedOutput()
			t.Fatalf("stand-in viewer never answered; pane:\n%s", out)
		}
	}
	return rec.PID
}

func (s *viewerServer) setVersion(t *testing.T, v string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.state, "version"), []byte(v+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// AC1: viewer restart respawns the viewer's own pane: the session lives, a
// new process answers, and the line names both versions and pids.
func TestViewerRestartRespawnsThePaneAndWaitsForTheNewViewer(t *testing.T) {
	s := newViewerServer(t)
	oldPID := s.startViewer(t, "v1.0.0-old")
	s.setVersion(t, "v1.0.1-new")

	ctx, out, _ := newTestContext(nil, false)
	if err := runViewer(ctx, []string{"restart", "--timeout", "30s"}); err != nil {
		t.Fatalf("viewer restart: %v\n%s", err, out)
	}
	rec, err := viewerstate.Read()
	if err != nil {
		t.Fatal(err)
	}
	if rec.PID == oldPID || !viewerstate.Alive(rec.PID) {
		t.Fatalf("viewer.json pid %d (old %d): want a new live process", rec.PID, oldPID)
	}
	if !httpAnswers(rec.URL) {
		t.Errorf("the new viewer at %s doesn't answer", rec.URL)
	}
	if viewerstate.Alive(oldPID) {
		t.Errorf("the old viewer (pid %d) still runs", oldPID)
	}
	s.tmux(t, "has-session", "-t", "issue-viewer") // fails the test when the session closed
	want := fmt.Sprintf("viewer restart: v1.0.0-old (pid %d) → v1.0.1-new (pid %d), answering at %s", oldPID, rec.PID, rec.URL)
	if got := out.String(); !strings.Contains(got, want) || strings.Count(strings.TrimSpace(got), "\n") != 0 {
		t.Errorf("output:\n%s\nwant one line with %q", got, want)
	}
}

// AC2: no pane runs the viewer: exit non-zero with one line naming what it
// looked for, with a tmux server up and with none at all.
func TestViewerRestartWithoutAViewerPaneSaysWhatItLookedFor(t *testing.T) {
	s := newViewerServer(t)
	ctx, out, _ := newTestContext(nil, false)
	check := func(name string) {
		t.Helper()
		err := runViewer(ctx, []string{"restart"})
		if err == nil {
			t.Fatalf("%s: restarted with no viewer pane:\n%s", name, out)
		}
		msg := err.Error()
		if strings.Contains(msg, "\n") || !strings.Contains(msg, "no tmux pane runs the viewer") || !strings.Contains(msg, "a pane whose start command runs issue-viewer") {
			t.Errorf("%s: want one line naming what it looked for, got:\n%s", name, msg)
		}
	}
	check("no tmux server")
	s.tmux(t, "new-session", "-d", "-s", "other", "exec sleep 600")
	// A stale viewer.json names a pid no pane holds.
	if err := viewerstate.Write(viewerstate.State{URL: "http://127.0.0.1:1", PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	check("a server without the viewer")
	if msg := noViewerPaneLine(os.Getpid(), nil); !strings.Contains(msg, "the pane holding pid "+strconv.Itoa(os.Getpid())+" from "+viewerstate.Path()) {
		t.Errorf("the line doesn't name viewer.json's pid: %s", msg)
	}
	s.tmux(t, "has-session", "-t", "other")
}

// findViewerPane: the recorded pid's pane (or its ancestor's) first, else
// the one pane whose start command runs issue-viewer; several refuse.
func TestFindViewerPane(t *testing.T) {
	orig := viewerParentPID
	viewerParentPID = func(pid int) int { return map[int]int{300: 200, 200: 1}[pid] }
	t.Cleanup(func() { viewerParentPID = orig })
	panes := []viewerPane{
		{ID: "%1", PID: 100, Session: "lead", Start: `"claude -n agent-issue-viewer-fix"`},
		{ID: "%2", PID: 200, Session: "issue-viewer", Start: `"/bin/zsh -lc 'issue-viewer -config x'"`},
		{ID: "%3", PID: 400, Session: "iv", Start: `"/home/someone/.local/bin/issue-viewer -config /c/projects.yaml -port 8090"`},
	}
	if p, err := findViewerPane(panes, 300); err != nil || p == nil || p.ID != "%2" {
		t.Errorf("pid 300 under the pane's shell 200: got %+v, %v; want %%2", p, err)
	}
	if p, err := findViewerPane(panes, 999); err != nil || p == nil || p.ID != "%3" {
		t.Errorf("no pane holds the pid: got %+v, %v; want %%3 by its start command", p, err)
	}
	two := append(panes, viewerPane{ID: "%4", PID: 500, Session: "sandbox", Start: "exec /tmp/x/issue-viewer -config /tmp/x/p.yaml"})
	if _, err := findViewerPane(two, 0); err == nil || !strings.Contains(err.Error(), "%3 (session iv), %4 (session sandbox)") {
		t.Errorf("two viewer panes: want a refusal naming both, got %v", err)
	}
	dead := []viewerPane{{ID: "%5", PID: 600, Dead: true, Start: "issue-viewer"}}
	if p, err := findViewerPane(dead, 0); p != nil || err != nil {
		t.Errorf("a dead pane: got %+v, %v; want none", p, err)
	}
}

// AC4: a dispatched worker is refused before anything is looked up.
func TestViewerRestartRefusesAWorker(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	asWorker(t)
	ctx, out, _ := newTestContext(proj, false)
	err := runViewer(ctx, []string{"restart"})
	if err == nil || !strings.Contains(err.Error(), "workers never restart the viewer: it is the lead's or the owner's call") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("worker: want a one-line refusal, got %v\n%s", err, out)
	}
}

// AC3: the lead's brief and the after-install hints name the command.
func TestAfterInstallNamesViewerRestart(t *testing.T) {
	if a := engineWorkflow(t).Leads.ProjectAddendum; !strings.Contains(a, "make install && issue-cli viewer restart") {
		t.Errorf("lead addendum doesn't name viewer restart after the install:\n%s", a)
	}
	if !strings.HasSuffix(installCommand("/r"), "make install && issue-cli viewer restart") {
		t.Errorf("the tick's install command: %s", installCommand("/r"))
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI", "overview.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "make install && issue-cli viewer restart") {
		t.Errorf("docs/CLI/overview.md doesn't name viewer restart for the after-install step")
	}
}
