package agentrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// The attach command names a private tmux server when the board runs on one,
// so a sandbox's or playtest's command reaches its sessions, not the real
// server's.
func TestAttachCmdNamesPrivateServer(t *testing.T) {
	proj := &tracker.Project{Slug: "p"}
	t.Setenv("TMUX_TMPDIR", "")
	if got := AttachCmd(proj, "lead-p"); got != "tmux attach -t lead-p" {
		t.Errorf("default server: %q", got)
	}
	t.Setenv("TMUX_TMPDIR", "/tmp/lsbx-tmux-1000")
	if got := AttachCmd(proj, "lead-p"); got != "TMUX_TMPDIR=/tmp/lsbx-tmux-1000 tmux attach -t lead-p" {
		t.Errorf("private server: %q", got)
	}
	shared := &tracker.Project{Slug: "p", TmuxSession: "work"}
	if got := AttachCmd(shared, "agent-x"); got != `TMUX_TMPDIR=/tmp/lsbx-tmux-1000 tmux attach -t work \; select-window -t agent-x` {
		t.Errorf("private server, shared session: %q", got)
	}
	t.Setenv("TMUX_TMPDIR", "/tmp/my dir")
	if got := AttachCmd(proj, "lead-p"); got != "TMUX_TMPDIR='/tmp/my dir' tmux attach -t lead-p" {
		t.Errorf("quoted dir: %q", got)
	}
}

// The switch command is for a human already inside tmux, where $TMUX names
// the server, so it never carries TMUX_TMPDIR. A shared-session target is
// quoted: zsh expands a leading "=" to a command path.
func TestSwitchCmd(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", "/tmp/lsbx-tmux-1000")
	proj := &tracker.Project{Slug: "p"}
	if got := SwitchCmd(proj, "agent-x"); got != "tmux switch-client -t agent-x" {
		t.Errorf("own session: %q", got)
	}
	shared := &tracker.Project{Slug: "p", TmuxSession: "work"}
	if got := SwitchCmd(shared, "agent-x"); got != "tmux switch-client -t '=work:=agent-x'" {
		t.Errorf("shared session: %q", got)
	}
	want := "Inside tmux:  tmux switch-client -t '=work:=agent-x'\nFrom a shell: TMUX_TMPDIR=/tmp/lsbx-tmux-1000 tmux attach -t work \\; select-window -t agent-x"
	if got := AttachHint(shared, "agent-x"); got != want {
		t.Errorf("hint:\n%s\nwant:\n%s", got, want)
	}
}

// withI3 stands in for a machine with or without i3-msg on PATH.
func withI3(t *testing.T, has bool) {
	t.Helper()
	old := lookPath
	lookPath = func(name string) (string, error) {
		if has && name == "i3-msg" {
			return "/usr/bin/i3-msg", nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPath = old })
}

func TestTerminalNone(t *testing.T) {
	withI3(t, true)
	for _, c := range []struct {
		terminal string
		want     bool
	}{{"none", true}, {" none ", true}, {"", false}, {"alacritty -e tmux attach -t {{session}}", false}} {
		if got := TerminalNone(&tracker.Project{Terminal: c.terminal}); got != c.want {
			t.Errorf("TerminalNone(%q) = %v", c.terminal, got)
		}
	}
	if TerminalNone(nil) {
		t.Error("TerminalNone(nil) = true")
	}
}

// A project without terminal: on a machine without i3 (a Mac, most Linux
// desktops) is headless: the i3 + alacritty default could only fail there,
// and a newcomer's first dispatch did ("✗ Open alacritty", exit 1).
func TestTerminalUnsetWithoutI3IsHeadless(t *testing.T) {
	withI3(t, false)
	if !TerminalNone(&tracker.Project{}) {
		t.Error("unset terminal without i3-msg should be headless")
	}
	if TerminalNone(&tracker.Project{I3Workspace: "3"}) {
		t.Error("i3_workspace asks for i3: not headless")
	}
	if TerminalNone(&tracker.Project{Terminal: "open -a Terminal"}) {
		t.Error("a terminal command is not headless")
	}
	var steps []Step
	if !OpenTerminalStep(&tracker.Project{}, "agent-x", &steps) {
		t.Fatalf("steps = %+v", steps)
	}
	if len(steps) != 1 || steps[0].Status != "ok" || !strings.Contains(steps[0].Detail, "terminal:") {
		t.Errorf("headless step should say how to get a window: %+v", steps)
	}
	withI3(t, true)
	if TerminalNone(&tracker.Project{}) {
		t.Error("with i3-msg on PATH the default stays i3 + alacritty")
	}
}

// Every project's agents share one tmux server and the agent-<slug> naming,
// so listings are scoped: a shared-session project keeps its own session's
// windows, a per-agent-session project keeps standalone sessions started under
// its root.
func TestForProject(t *testing.T) {
	sessions := []Session{
		{Name: "work:agent-a"},
		{Name: "work:lead-p"},
		{Name: "other:agent-a"},
		{Name: "agent-b", Path: "/proj/.worktrees/b"},
		{Name: "agent-c", Path: "/proj"},
		{Name: "agent-d", Path: "/projector"},
		{Name: "agent-e", Path: "/elsewhere"},
		{Name: "agent-f"},
	}
	names := func(ss []Session) []string {
		var out []string
		for _, s := range ss {
			out = append(out, s.Name)
		}
		return out
	}

	shared := &tracker.Project{Slug: "p", TmuxSession: "work"}
	if got := names(ForProject(shared, "/proj", sessions)); strings.Join(got, ",") != "work:agent-a,work:lead-p" {
		t.Errorf("shared session: %v", got)
	}
	perAgent := &tracker.Project{Slug: "p"}
	if got := names(ForProject(perAgent, "/proj", sessions)); strings.Join(got, ",") != "agent-b,agent-c,agent-f" {
		t.Errorf("per-agent sessions: %v", got)
	}
}

func TestParseSessions(t *testing.T) {
	got := ParseSessions("issue-viewer\t1792610000\t/home/u/Work\nbad\n\nagent-x\t1792610100\t/p/.worktrees/x\n")
	if len(got) != 2 || got[0].Name != "issue-viewer" || got[0].Path != "/home/u/Work" || got[1].Path != "/p/.worktrees/x" || got[0].StartTime == "" {
		t.Errorf("ParseSessions = %+v", got)
	}
}

// The agent is created detached as the window's own command, with its env and
// working directory passed to tmux, never typed into a shell.
func TestCreateTmuxRunsCommandDetached(t *testing.T) {
	original := HasSession
	t.Cleanup(func() { HasSession = original })
	HasSession = func(string) bool { return true }
	env := []string{"ISSUE_CLI_LOG=/l/a.clilog", "ISSUE_VIEWER_PROJECT=p"}
	cases := []struct {
		proj *tracker.Project
		want string
	}{
		{&tracker.Project{}, "tmux new-session -d -s agent-a -c /w -e ISSUE_CLI_LOG=/l/a.clilog -e ISSUE_VIEWER_PROJECT=p sh -lic run"},
		{&tracker.Project{TmuxSession: "work"}, "tmux new-window -d -t =work: -n agent-a -c /w -e ISSUE_CLI_LOG=/l/a.clilog -e ISSUE_VIEWER_PROJECT=p sh -lic run"},
	}
	for _, c := range cases {
		_, cmd := CreateTmux(c.proj, "agent-a", "/w", env, "sh", "-lic", "run")
		if got := strings.Join(cmd.Args, " "); got != c.want {
			t.Errorf("CreateTmux = %q\n want %q", got, c.want)
		}
	}
	HasSession = func(string) bool { return false }
	_, cmd := CreateTmux(&tracker.Project{TmuxSession: "work"}, "agent-a", "/w", nil)
	if got, want := strings.Join(cmd.Args, " "), "tmux new-session -d -s work -n agent-a -c /w"; got != want {
		t.Errorf("CreateTmux without a shared session = %q, want %q", got, want)
	}
}

func TestAgentCommandRunsInLoginShellAndKeepsAShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	got := AgentCommand(`claude -n agent-a "$(cat "/p")"`)
	want := []string{"/bin/zsh", "-lic", `trap : INT; claude -n agent-a "$(cat "/p")"; exec /bin/zsh -l`}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("AgentCommand = %q, want %q", got, want)
	}
	t.Setenv("SHELL", "")
	if got := AgentCommand("x"); got[0] != "/bin/sh" {
		t.Fatalf("without $SHELL the shell is %q, want /bin/sh", got[0])
	}
}

// The agent comes from the pane's start command, which AgentCommand wraps;
// anything else (a shell started by hand) is unknown.
func TestAgentOfStartCommand(t *testing.T) {
	for cmd, want := range map[string]string{
		`/bin/zsh -lic "trap : INT; claude -n agent-x \"$(cat \"/p/prompt.txt\")\"; exec /bin/zsh -l"`: "claude",
		`/bin/sh -lic "trap : INT; codex --model o3 \"$(cat \"/p/prompt.txt\")\"; exec /bin/sh -l"`:    "codex",
		strings.Join(AgentCommand(LaunchCommand(&tracker.Project{}, "codex", "agent-x", "/p")), " "):   "codex",
		strings.Join(AgentCommand(LaunchCommand(&tracker.Project{}, "claude", "agent-x", "/p")), " "):  "claude",
		`/bin/zsh`:                        "",
		`/bin/zsh -lic "trap : INT; vim"`: "",
	} {
		if got := agentOfStartCommand(cmd); got != want {
			t.Errorf("agentOfStartCommand(%q) = %q, want %q", cmd, got, want)
		}
	}
}

// TestForProjectRootBehindSymlink: tmux reports a session's path resolved
// (macOS /var is /private/var) while the project root is configured through
// the link; the session still belongs to the project.
func TestForProjectRootBehindSymlink(t *testing.T) {
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "proj", ".worktrees", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	sessions := []Session{
		{Name: "agent-a", Path: filepath.Join(resolved, "proj")},
		{Name: "agent-b", Path: filepath.Join(resolved, "proj", ".worktrees", "b")},
		{Name: "agent-c", Path: filepath.Join(link, "proj")},
		{Name: "agent-e", Path: resolved},
	}
	var got []string
	for _, s := range ForProject(&tracker.Project{Slug: "p"}, filepath.Join(link, "proj"), sessions) {
		got = append(got, s.Name)
	}
	if strings.Join(got, ",") != "agent-a,agent-b,agent-c" {
		t.Errorf("sessions under a linked root: %v", got)
	}
}

func TestUnderDirResolvesLinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	resolved, _ := filepath.EvalSymlinks(real)
	// tmux reports the resolved path; the project root may be the link.
	if !underDir(filepath.Join(resolved, "sub"), link) {
		t.Error("a pane under the link's target should be under the link")
	}
	if underDir("/elsewhere", link) {
		t.Error("an unrelated path is not under the link")
	}
}

func TestUnderDirRelativeRoot(t *testing.T) {
	// projects.yaml may say workdir: "." (the README) or "demo" (make demo);
	// tmux reports the absolute path.
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir("demo", 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if !underDir(filepath.Join(cwd, "demo"), "demo") {
		t.Error("a session in <cwd>/demo should be under workdir demo")
	}
	if !underDir(filepath.Join(cwd, "demo", ".worktrees", "x"), ".") {
		t.Error("a session under the cwd should be under workdir .")
	}
	if underDir("/elsewhere", "demo") {
		t.Error("an unrelated path is not under a relative workdir")
	}
}
