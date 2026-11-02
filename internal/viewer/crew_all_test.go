package viewer

// The Crew page across projects (crew_all.go) against a private tmux server:
// never the shared one. crewTmux starts nothing on the developer's server and
// kills only its own socket's server, with TMUX unset.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// crewTmux points every tmux call at a private server under /tmp (a short
// path: the socket's limit is ~104 bytes) and returns a function that runs
// tmux on exactly that socket. Cleanup kills that server only: TMUX is
// removed from the command's environment and the socket is named with -S.
func crewTmux(t *testing.T) func(args ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "crw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	os.Unsetenv("TMUX")
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("SHELL", "/bin/sh")
	sock := filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") {
			env = append(env, kv)
		}
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command("tmux", append([]string{"-S", sock}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", sock, "kill-server")
		kill.Env = env
		_ = kill.Run()
		_ = os.RemoveAll(dir)
	})
	return run
}

// crewQuiet pins the machine readings and the log's folder.
func crewQuiet(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	mem, load := autostart.ReadMemory, autostart.ReadLoad
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 41, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 4.2, Cores: 14} }
	t.Cleanup(func() { autostart.ReadMemory, autostart.ReadLoad = mem, load })
	stubAgentsGit(t)
	return state
}

func realDir(t *testing.T, dir string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// crewProjects is two sandbox projects: Agents (setupAgentsProject: ui/child
// at the testing gate, ui/asker with a question) and Beta (beta/work in
// progress, nobody's clilog: working; its slug is beta/beta-work).
func crewProjects(t *testing.T) (tracker.Project, tracker.Project) {
	t.Helper()
	a := setupAgentsProject(t)
	a.WorkDir = realDir(t, a.WorkDir)
	a.IssueDir = filepath.Join(a.WorkDir, "issues")
	a.WorkflowFile = filepath.Join(a.WorkDir, "workflow.yaml")
	a.Terminal = "none"

	root := realDir(t, t.TempDir())
	os.MkdirAll(filepath.Join(root, "issues", "Beta"), 0o755)
	os.WriteFile(filepath.Join(root, "issues", "Beta", "work.md"), []byte("---\ntitle: \"Beta work\"\nstatus: \"in progress\"\n---\n\nBody.\n"), 0o644)
	os.WriteFile(filepath.Join(root, "workflow.yaml"), []byte("statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\n"), 0o644)
	b := tracker.Project{Name: "Beta", Slug: "beta", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml"), Terminal: "none"}
	return a, b
}

func crewSession(t *testing.T, tmux func(...string) (string, error), name, dir string) {
	t.Helper()
	if out, err := tmux("new-session", "-d", "-s", name, "-c", dir, "sleep 600"); err != nil {
		t.Fatalf("new-session %s: %v %s", name, err, out)
	}
}

func crewServer(t *testing.T, projects ...tracker.Project) *Server {
	t.Helper()
	srv, err := NewServer(projects)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// AC1: the page lists both projects' sessions with the right states, and a
// session of one project never shows under the other.
func TestCrewAllListsTwoProjects(t *testing.T) {
	tmux := crewTmux(t)
	crewQuiet(t)
	a, b := crewProjects(t)
	crewSession(t, tmux, "agent-ui-child", a.WorkDir)
	crewSession(t, tmux, "agent-ui-asker", a.WorkDir)
	crewSession(t, tmux, "lead-agents", a.WorkDir)
	crewSession(t, tmux, "agent-beta-beta-work", b.WorkDir)
	srv := crewServer(t, a, b)

	rec := ownerDo(srv, "GET", "/crew", "", nil)
	if rec.Code != 200 {
		t.Fatalf("GET /crew = %d %s", rec.Code, rec.Body)
	}
	html := rec.Body.String()
	assertContains(t, html,
		`data-testid="crew_all.page"`,
		`memory 41% · can start`, `load 4.2 on 14 cores`,
		`data-testid="crew_all.project.agents"`, `data-testid="crew_all.project.beta"`,
		`data-testid="crew_all.row.agent-ui-child"`, `waiting on you at the testing gate`,
		`data-testid="crew_all.row.agent-ui-asker"`, `asked a question`,
		`data-testid="crew_all.row.agent-beta-beta-work"`,
		`data-testid="crew_all.row.lead-agents"`,
		// Beta's CEO is not running.
		`data-testid="crew_all.row.lead-beta"`, `not running`,
		// Signed out: read only, a way to sign in, no buttons.
		`data-testid="crew_all.signin"`,
	)
	if strings.Contains(html, `crew_all.row.agent-ui-child.stop`) {
		t.Error("a signed-out page offers Stop")
	}
	agents := html[strings.Index(html, `crew_all.project.agents"`):strings.Index(html, `crew_all.project.beta"`)]
	beta := html[strings.Index(html, `crew_all.project.beta"`):]
	if strings.Contains(agents, "agent-beta-beta-work") || strings.Contains(beta, "agent-ui-child") {
		t.Error("a session shows under the other project")
	}
	if !strings.Contains(beta, `crew_all.row.agent-beta-beta-work.state"><span class="wb-chip wb-chip--green">`) {
		t.Errorf("Beta's worker is not working:\n%s", beta)
	}

	// Signed in: the actions show.
	c := pairOwner(t, srv)
	html = ownerDo(srv, "GET", "/crew", "", nil, c).Body.String()
	assertContains(t, html, `data-testid="crew_all.row.agent-ui-child.stop"`, `data-testid="crew_all.row.agent-ui-child.nudge"`,
		`data-testid="crew_all.row.agent-ui-child.tail"`, `data-testid="crew_all.row.lead-beta.start"`, `data-crew-start="/p/beta/crew/lead"`)
	if strings.Contains(html, `data-testid="crew_all.signin"`) {
		t.Error("the signed-in page still says read only")
	}
}

// AC2: every crew action needs the owner. A public Host without a token is
// refused by the identity gate; loopback without the sign-in by the owner
// gate; the signed-in browser gets through.
func TestCrewAllActionsNeedTheOwner(t *testing.T) {
	crewTmux(t)
	crewQuiet(t)
	a, _ := crewProjects(t)
	srv := crewServer(t, a)
	srv.allowedHosts = allowedHostsFor(defaultHost, []string{"factory.example.com"})
	routes := []struct{ method, path, body string }{
		{"POST", "/p/agents/crew/lead", `{}`},
		{"POST", "/p/agents/crew/stop", `{"session":"agent-ui-child"}`},
		{"POST", "/p/agents/crew/nudge", `{"session":"agent-ui-child","text":"hi"}`},
		{"GET", "/p/agents/crew/tail?session=agent-ui-child", ``},
		{"POST", "/p/agents/queue/pause", `{}`},
		{"POST", "/p/agents/autostart/pause", `{}`},
	}
	c := pairOwner(t, srv)
	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
		req.Host = "factory.example.com"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("public %s %s without a token = %d, want 403", rt.method, rt.path, rec.Code)
		}
		rec = ownerDo(srv, rt.method, rt.path, rt.body, nil)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), ownerRefused) {
			t.Errorf("loopback %s %s without the sign-in = %d %q, want 403 %q", rt.method, rt.path, rec.Code, rec.Body, ownerRefused)
		}
		rec = ownerDo(srv, rt.method, rt.path, rt.body, nil, c)
		if rec.Code == http.StatusForbidden {
			t.Errorf("signed-in %s %s = 403 %s", rt.method, rt.path, rec.Body)
		}
	}
}

// AC3: Stop ends exactly the named session (not the one whose name it
// prefixes), keeps the worktree, logs who and when, and leaves a comment.
func TestCrewAllStopEndsOneSession(t *testing.T) {
	tmux := crewTmux(t)
	state := crewQuiet(t)
	a, _ := crewProjects(t)
	worktree := filepath.Join(a.WorkDir, ".worktrees", "ui", "child")
	os.MkdirAll(worktree, 0o755)
	os.WriteFile(filepath.Join(worktree, "work.txt"), []byte("kept"), 0o644)
	crewSession(t, tmux, "agent-ui-child", a.WorkDir)
	crewSession(t, tmux, "agent-ui-child-2", a.WorkDir)
	crewSession(t, tmux, "agent-ui-asker", a.WorkDir)
	srv := crewServer(t, a)
	c := pairOwner(t, srv)

	rec := ownerDo(srv, "POST", "/p/agents/crew/stop", `{"session":"agent-ui-child"}`, nil, c)
	if rec.Code != 200 {
		t.Fatalf("stop = %d %s", rec.Code, rec.Body)
	}
	out, _ := tmux("list-sessions", "-F", "#{session_name}")
	got := strings.Fields(out)
	if strings.Join(got, ",") != "agent-ui-asker,agent-ui-child-2" {
		t.Fatalf("sessions after stop = %v, want agent-ui-asker and agent-ui-child-2", got)
	}
	if b, err := os.ReadFile(filepath.Join(worktree, "work.txt")); err != nil || string(b) != "kept" {
		t.Errorf("the worktree was touched: %v %q", err, b)
	}
	logged, _ := os.ReadFile(filepath.Join(state, "ai-workflow-engine", "crew", "actions.jsonl"))
	var act CrewAction
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(logged))), &act); err != nil || act.Action != "stopped" || act.Target != "agent-ui-child" || act.Who != "owner (signed-in browser)" || act.At.IsZero() || !act.OK {
		t.Errorf("log = %q (%v)", logged, err)
	}
	issue := srv.findIssueBySlug(&a, "ui/child")
	comments, _ := tracker.LoadComments(issue.FilePath)
	if len(comments) == 0 || !strings.HasPrefix(comments[len(comments)-1].Text, "stopped: the owner stopped agent-ui-child from the Crew page") {
		t.Errorf("no stopped: comment on the issue: %+v", comments)
	}

	// A name that is not a live worker of this project: 404, nothing ends.
	for _, name := range []string{"agent-ui-child", "lead-agents", "agent-elsewhere", "mem-guard"} {
		rec = ownerDo(srv, "POST", "/p/agents/crew/stop", `{"session":"`+name+`"}`, nil, c)
		if rec.Code != http.StatusNotFound {
			t.Errorf("stop %s = %d, want 404", name, rec.Code)
		}
	}
	out, _ = tmux("list-sessions", "-F", "#{session_name}")
	if len(strings.Fields(out)) != 2 {
		t.Errorf("a refused stop ended a session: %s", out)
	}
}

// AC4: a worker with a process in state T (what mem-guard's SIGSTOP leaves)
// shows as paused, has no Stop or Nudge, and the routes refuse it.
func TestCrewAllPausedWorkerHasNoStop(t *testing.T) {
	tmux := crewTmux(t)
	crewQuiet(t)
	a, _ := crewProjects(t)
	// The agent CLI runs under the pane's shell, and mem-guard stops it
	// there. (tmux sends SIGCONT to a pane's own process when it stops, so
	// a stop on the pane pid itself would not hold.)
	if out, err := tmux("new-session", "-d", "-s", "agent-ui-child", "-c", a.WorkDir, "sh -c 'sleep 600; true'"); err != nil {
		t.Fatal(err, out)
	}
	crewSession(t, tmux, "agent-ui-asker", a.WorkDir)
	out, err := tmux("list-panes", "-t", "=agent-ui-child:", "-F", "#{pane_pid}")
	if err != nil {
		t.Fatal(err, out)
	}
	panePID, _ := strconv.Atoi(strings.TrimSpace(out))
	pid := 0
	for deadline := time.Now().Add(5 * time.Second); pid == 0; {
		for _, p := range agentrun.ProcessTable() {
			if p.PPID == panePID {
				pid = p.PID
			}
		}
		if pid == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("no child under pane %d", panePID)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })
	// ps shows T once the signal lands.
	deadline := time.Now().Add(5 * time.Second)
	for !agentrun.TakeSnapshot().Paused(&a, "agent-ui-child") {
		if time.Now().After(deadline) {
			t.Fatal("the stopped pane never showed as paused")
		}
		time.Sleep(50 * time.Millisecond)
	}
	srv := crewServer(t, a)
	c := pairOwner(t, srv)
	html := ownerDo(srv, "GET", "/crew", "", nil, c).Body.String()
	row := html[strings.Index(html, `data-testid="crew_all.row.agent-ui-child"`):]
	// Paused workers sort last, so the row ends at its project's card.
	row = row[:strings.Index(row, `</section>`)]
	if !strings.Contains(row, "paused by mem-guard") {
		t.Errorf("the paused worker's row doesn't say paused:\n%s", row)
	}
	if strings.Contains(row, "crew_all.row.agent-ui-child.stop") || strings.Contains(row, "crew_all.row.agent-ui-child.nudge") {
		t.Errorf("the paused worker offers Stop or Nudge:\n%s", row)
	}
	assertContains(t, html, `mem-guard paused 1`, `data-testid="crew_all.row.agent-ui-asker.stop"`)
	for _, act := range []string{"stop", "nudge"} {
		rec := ownerDo(srv, "POST", "/p/agents/crew/"+act, `{"session":"agent-ui-child","text":"hi"}`, nil, c)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "paused by mem-guard") {
			t.Errorf("%s a paused worker = %d %s, want 409", act, rec.Code, rec.Body)
		}
	}
	if _, err := tmux("has-session", "-t", "=agent-ui-child:"); err != nil {
		t.Error("the paused worker's session was ended")
	}
}

// Nudge: the text arrives with the owner's prefix; too long or empty is
// refused before anything reaches tmux.
func TestCrewAllNudge(t *testing.T) {
	tmux := crewTmux(t)
	crewQuiet(t)
	a, _ := crewProjects(t)
	crewSession(t, tmux, "agent-ui-child", a.WorkDir)
	var sent []string
	orig := crewNudgeSend
	crewNudgeSend = func(target, text string) error { sent = append(sent, target+"|"+text); return nil }
	t.Cleanup(func() { crewNudgeSend = orig })
	srv := crewServer(t, a)
	c := pairOwner(t, srv)

	rec := ownerDo(srv, "POST", "/p/agents/crew/nudge", `{"session":"agent-ui-child","text":"  Where are\nyou?  "}`, nil, c)
	if rec.Code != 200 || len(sent) != 1 || sent[0] != "=agent-ui-child:|Message from the owner (Crew page): Where are you?" {
		t.Fatalf("nudge = %d %s, sent %q", rec.Code, rec.Body, sent)
	}
	for _, text := range []string{"", "   ", strings.Repeat("x", 301)} {
		rec = ownerDo(srv, "POST", "/p/agents/crew/nudge", `{"session":"agent-ui-child","text":"`+text+`"}`, nil, c)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("nudge %d chars = %d, want 400", len(text), rec.Code)
		}
	}
	if len(sent) != 1 {
		t.Errorf("a refused nudge reached tmux: %q", sent)
	}
}

// Tail: the pane's last lines, for a live agent of this project only.
func TestCrewAllTail(t *testing.T) {
	tmux := crewTmux(t)
	crewQuiet(t)
	a, _ := crewProjects(t)
	if out, err := tmux("new-session", "-d", "-s", "agent-ui-child", "-c", a.WorkDir, "printf 'line one\\nline two\\n'; sleep 600"); err != nil {
		t.Fatal(err, out)
	}
	srv := crewServer(t, a)
	c := pairOwner(t, srv)
	var body struct {
		Lines []string `json:"lines"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := ownerDo(srv, "GET", "/p/agents/crew/tail?session=agent-ui-child", "", nil, c)
		if rec.Code != 200 {
			t.Fatalf("tail = %d %s", rec.Code, rec.Body)
		}
		json.Unmarshal(rec.Body.Bytes(), &body)
		if strings.Contains(strings.Join(body.Lines, "\n"), "line two") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tail never showed the pane: %q", body.Lines)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if rec := ownerDo(srv, "GET", "/p/agents/crew/tail?session=agent-nope", "", nil, c); rec.Code != http.StatusNotFound {
		t.Errorf("tail of an unknown session = %d, want 404", rec.Code)
	}
}

// The queue pause: set from the viewer, it holds queue next before anything
// else, and resume lifts it.
func TestCrewAllQueuePause(t *testing.T) {
	crewTmux(t)
	crewQuiet(t)
	a, _ := crewProjects(t)
	srv := crewServer(t, a)
	c := pairOwner(t, srv)
	if rec := ownerDo(srv, "POST", "/p/agents/queue/pause", `{}`, nil, c); rec.Code != http.StatusNotFound {
		t.Errorf("pause without a queue = %d, want 404", rec.Code)
	}
	if err := queue.Update(a.IssueDir, func(q *queue.Queue) error {
		_, err := q.Add(queue.Item{Slug: "loner", Quote: "go"}, queue.Where{}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if rec := ownerDo(srv, "POST", "/p/agents/queue/pause", `{}`, nil, c); rec.Code != 200 {
		t.Fatalf("pause = %d %s", rec.Code, rec.Body)
	}
	q, _ := queue.Load(a.IssueDir)
	v := agentrun.QueueEvaluate(agentrun.QueueInput{Proj: &a, Queue: q, Now: time.Now()})
	if v.Start || !strings.Contains(v.Reason, "the queue is paused by the owner (Crew page)") {
		t.Errorf("a paused queue's verdict = %+v", v.Verdict)
	}
	html := ownerDo(srv, "GET", "/crew", "", nil, c).Body.String()
	assertContains(t, html, `data-testid="crew_all.switch.queue.resume"`)
	if rec := ownerDo(srv, "POST", "/p/agents/queue/resume", `{}`, nil, c); rec.Code != 200 {
		t.Fatalf("resume = %d %s", rec.Code, rec.Body)
	}
	q, _ = queue.Load(a.IssueDir)
	if q.Paused || q.PauseLine() != "" {
		t.Errorf("resume left the queue paused: %+v", q)
	}
}

func TestParsePanesAndProcs(t *testing.T) {
	panes := agentrun.ParsePanes("agent-a\tagent-a\t101\nwork\tagent-b\t202\nbad line\n")
	if len(panes) != 2 || panes[1].Window != "agent-b" || panes[1].PID != 202 {
		t.Fatalf("panes = %+v", panes)
	}
	procs := agentrun.ParseProcs("  101     1 Ss\n  150   101 S+\n  151   150 T\n  202     1 Ss\n")
	if len(procs) != 4 || procs[2].Stat != "T" {
		t.Fatalf("procs = %+v", procs)
	}
	lp, pt := agentrun.ListPanes, agentrun.ProcessTable
	agentrun.ListPanes = func() []agentrun.Pane { return panes }
	agentrun.ProcessTable = func() []agentrun.Proc { return procs }
	t.Cleanup(func() { agentrun.ListPanes, agentrun.ProcessTable = lp, pt })
	s := agentrun.TakeSnapshot()
	if !s.Paused(&tracker.Project{}, "agent-a") {
		t.Error("a grandchild in state T should pause agent-a")
	}
	if s.Paused(&tracker.Project{TmuxSession: "work"}, "agent-b") {
		t.Error("agent-b has no stopped process")
	}
	if s.Paused(&tracker.Project{}, "agent-b") {
		t.Error("agent-b is a window of the shared session, not a session of its own")
	}
}
