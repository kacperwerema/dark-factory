package agentrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/clilog"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// standInAgent puts an agent called name first on PATH: it writes its
// arguments, one per line, to the returned file and stays up.
func standInAgent(t *testing.T, name string) string {
	t.Helper()
	bin := t.TempDir()
	record := filepath.Join(bin, "argv")
	writeStandIn(t, filepath.Join(bin, name), "for a in \"$@\"; do printf '%s\\n' \"$a\"; done > "+record+".tmp\nmv "+record+".tmp "+record+"\nexec sleep 60\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

func readArgv(t *testing.T, record string) []string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if b, err := os.ReadFile(record); err == nil {
			return strings.Split(strings.TrimSpace(string(b)), "\n")
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-in agent never recorded its arguments")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The argv a codex start really runs trusts its own folder and no other,
// and keeps the sandbox and approval settings as they are. The folder has
// dots in it, as every worktree has: codex's dotted -c keys can't name one.
func TestCodexStartTrustsExactlyItsWorktree(t *testing.T) {
	record := standInAgent(t, "codex")
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	orig := PromptDeliveryTimeout
	PromptDeliveryTimeout = 0
	t.Cleanup(func() { PromptDeliveryTimeout = orig })

	dir := filepath.Join(t.TempDir(), ".worktrees", "cli", "a.b")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	proj := &tracker.Project{Slug: "p", Terminal: "none", WorkDir: dir}
	if resp := Start(proj, Spec{Session: "agent-x", Prompt: "the brief", AgentType: "codex", Headless: true}); resp.Status != "dispatched" {
		t.Fatalf("start: %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	argv := readArgv(t, record)

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-c", `projects={"` + resolved + `"={trust_level="trusted"}}`, "-c", "check_for_update_on_startup=false", "the brief"}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv:\n got %q\nwant %q", argv, want)
	}
	for _, a := range argv {
		if strings.Contains(a, "dangerously") || strings.HasPrefix(a, "--sandbox") || strings.HasPrefix(a, "--ask-for-approval") {
			t.Fatalf("a start must not change the sandbox for this: %q", argv)
		}
	}
}

// Only codex takes them: claude has its own flags and would refuse these.
func TestClaudeStartGetsNoCodexFlags(t *testing.T) {
	record := standInAgent(t, "claude")
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	orig := PromptDeliveryTimeout
	PromptDeliveryTimeout = 0
	t.Cleanup(func() { PromptDeliveryTimeout = orig })

	proj := &tracker.Project{Slug: "p", Terminal: "none", WorkDir: t.TempDir()}
	Start(proj, Spec{Session: "agent-x", Prompt: "the brief", AgentType: "claude", Headless: true})
	if argv := readArgv(t, record); strings.Contains(strings.Join(argv, " "), "trust_level") {
		t.Fatalf("claude got codex's trust flag: %q", argv)
	}
}

func TestCodexStartFlagsEscapeThePath(t *testing.T) {
	got := CodexStartFlags(`/no/such/"odd\dir`)
	if got[1] != `projects={"/no/such/\"odd\\dir"={trust_level="trusted"}}` {
		t.Fatalf("flag = %s", got[1])
	}
}

const trustPane = `  Folder access

  /Users/me/Repo/x/.worktrees/cli/some-slug

  Trust this folder? Codex can read, edit, and run files here, subject to your permission settings.

› 1. Trust and continue
  2. Quit

  enter continue · esc quit
`

func TestStartupDialog(t *testing.T) {
	for name, c := range map[string]struct{ pane, want string }{
		"trust":          {trustPane, "Codex's trust dialog"},
		"issue's":        {"› 1. Trust and continue\n  2. Back to Agent Command Center\n", "Codex's trust dialog"},
		"moved down":     {"  1. Trust and continue\n› 2. Quit\n", "Codex's trust dialog"},
		"update":         {"  Update available · 0.163.0 → 0.166.0\n› 1. Update now (runs `sh -c ...`)\n  2. Skip\n", "Codex's update prompt"},
		"claude":         {" Quick safety check\n ❯ 1. Yes, I trust this folder\n   2. No, exit\n", "Claude's trust dialog"},
		"working":        {"• Reading the brief\n\n› 1. Trust and continue\n\n• Working (3s • esc to interrupt)\n", ""},
		"plain":          {"› Ask Codex to do anything\n", ""},
		"in a text":      {"the dialog said 1. Trust and continue, so\n", ""},
		"quoted, far up": {"› 1. Trust and continue\n" + strings.Repeat("a line\n", 30), ""},
	} {
		if got := StartupDialog(c.pane); got != c.want {
			t.Errorf("%s: StartupDialog = %q, want %q", name, got, c.want)
		}
	}
}

// A real pane showing the dialog is reported once it has been up a minute,
// not before, and a pane past the dialog is not reported at all.
func TestScanStallsReportsAPaneAtTheDialog(t *testing.T) {
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proj := &tracker.Project{Slug: "p", Terminal: "none", WorkDir: root}

	screen := filepath.Join(root, "screen.txt")
	if err := os.WriteFile(screen, []byte(trustPane), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent-stalled", "agent-fine"} {
		show := "cat " + screen + "; sleep 60"
		if name == "agent-fine" {
			show = "echo 'Ask Codex to do anything'; sleep 60"
		}
		_, cmd := CreateTmux(proj, name, root, nil, AgentCommand(show)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		pane, _ := CapturePane(Target(proj, "agent-stalled"))
		if StartupDialog(pane) != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stand-in pane never showed the dialog:\n%s", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}

	now := time.Now()
	if got := ScanStalls(proj, root, now); len(got) != 0 {
		t.Fatalf("a pane just started is not stalled yet: %+v", got)
	}
	got := ScanStalls(proj, root, now.Add(DialogStall+2*time.Second))
	if len(got) != 1 || got[0].Session != "agent-stalled" || got[0].Dialog != "Codex's trust dialog" {
		t.Fatalf("stalls = %+v", got)
	}
	if w := got[0].Words(); w != "stalled at Codex's trust dialog" {
		t.Fatalf("words = %q", w)
	}
}

// A shared-session window has no start time from tmux: its launch is the
// prompt file the launcher wrote.
func TestLaunchedAtFallsBackToThePromptFile(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, ".agent-logs", "agent-x")
	path, err := WritePrompt(logDir, "brief")
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-5 * time.Minute).Truncate(time.Second)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	if got, ok := launchedAt(Session{Name: "shared:agent-x"}, "agent-x", root); !ok || !got.Equal(when) {
		t.Fatalf("launchedAt = %v %v", got, ok)
	}
	if got, ok := launchedAt(Session{Name: "agent-y", StartTime: when.Format("2006-01-02 15:04:05")}, "agent-y", root); !ok || !got.Equal(when) {
		t.Fatalf("from the session: %v %v", got, ok)
	}
	if _, ok := launchedAt(Session{Name: "agent-z"}, "agent-z", root); ok {
		t.Fatal("no start time and no prompt file reads as known")
	}
}

// With the real codex, set up by scripts/checks on the issue's branch only
// (CHECK_REAL_CODEX=1): started by hand the folder stops at the dialog, and
// StartupDialog knows the real pane; started by Start it reaches its prompt.
// No prompt is sent, so it spends nothing.
func TestRealCodexReachesItsPrompt(t *testing.T) {
	if os.Getenv("CHECK_REAL_CODEX") == "" {
		t.Skip("set CHECK_REAL_CODEX=1 to run the real codex")
	}
	isolateTmux(t)
	orig := PromptDeliveryTimeout
	PromptDeliveryTimeout = 0
	t.Cleanup(func() { PromptDeliveryTimeout = orig })

	dir := filepath.Join(t.TempDir(), ".worktrees", "cli", "a.b")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	proj := &tracker.Project{Slug: "p", Terminal: "none", WorkDir: dir}
	waitPane := func(target, want string) string {
		t.Helper()
		var pane string
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
			pane, _ = CapturePane(target)
			if strings.Contains(pane, want) {
				return pane
			}
		}
		t.Fatalf("pane never showed %q:\n%s", want, pane)
		return ""
	}

	_, cmd := CreateTmux(proj, "agent-control", dir, nil, AgentCommand("codex -c check_for_update_on_startup=false")...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	pane := waitPane(Target(proj, "agent-control"), "Trust and continue")
	if got := StartupDialog(pane); got != "Codex's trust dialog" {
		t.Fatalf("StartupDialog on the real pane = %q:\n%s", got, pane)
	}

	if resp := Start(proj, Spec{Session: "agent-real", Prompt: "", AgentType: "codex", Headless: true}); resp.Status != "dispatched" {
		t.Fatalf("start: %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	pane = waitPane(Target(proj, "agent-real"), "Ask Codex")
	if got := StartupDialog(pane); got != "" {
		t.Fatalf("Start's codex is at %s:\n%s", got, pane)
	}
}

func panePath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "panes", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func paneText(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(panePath(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// showInPane starts session name in a private tmux server, its pane running
// show, and waits until the pane holds want.
func showInPane(t *testing.T, proj *tracker.Project, root, name, show, want string) {
	t.Helper()
	_, cmd := CreateTmux(proj, name, root, nil, AgentCommand(show)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		pane, _ := CapturePane(Target(proj, name))
		if strings.Contains(pane, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never showed %q:\n%s", name, want, pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// showFixture shows testdata pane fixture in session name, then execs then,
// and waits until the fixture's last line is in the pane in full. The pane's
// shell is interactive: a command it forks after the fixture can make it print
// "sh: child setpgid (…): Operation not permitted" under load, which would
// end the pane in place of the prompt. A plain sh -c that execs then forks
// nothing after cat, so the fixture is the pane's end, as a CLI's prompt is.
func showFixture(t *testing.T, proj *tracker.Project, root, name, fixture, then string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(paneText(t, fixture)), "\n")
	draw := "exec /bin/sh -c " + shell.Quote("cat "+shell.Quote(panePath(t, fixture))+"; exec "+then)
	showInPane(t, proj, root, name, draw, strings.TrimSpace(lines[len(lines)-1]))
}

func stallProject(t *testing.T) (*tracker.Project, string) {
	t.Helper()
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{Slug: "p", Terminal: "none", WorkDir: root}, root
}

// The panes are the real CLIs' (codex-cli 0.163.0, Claude Code 2.1.318), each
// stopped at a prompt: a worker there is reported past its launch minute, and
// it needs no clilog to be.
func TestScanStallsFindsAnApprovalPrompt(t *testing.T) {
	for pane, want := range map[string]string{
		"codex-approval":  "Codex's approval prompt",
		"claude-approval": "Claude's approval prompt",
		"claude-create":   "Claude's approval prompt",
		"claude-question": "Claude's question",
	} {
		t.Run(pane, func(t *testing.T) {
			if got := PanePrompt(paneText(t, pane)); got != want {
				t.Fatalf("PanePrompt = %q, want %q", got, want)
			}
			proj, root := stallProject(t)
			showFixture(t, proj, root, "agent-at-prompt", pane, "sleep 60")
			now := time.Now()
			if got := ScanStalls(proj, root, now); len(got) != 0 {
				t.Fatalf("a pane just started is not stalled yet: %+v", got)
			}
			got := ScanStalls(proj, root, now.Add(DialogStall+2*time.Second))
			if len(got) != 1 || got[0].Session != "agent-at-prompt" || got[0].Dialog != want {
				captured, _ := CapturePane(Target(proj, "agent-at-prompt"))
				t.Fatalf("stalls = %+v\n%s", got, captured)
			}
			if w := got[0].Words(); w != "stalled at "+want {
				t.Fatalf("words = %q", w)
			}
		})
	}
}

// A prompt is known by its last line and its first choice together: an agent
// printing one is still working or back at its own input box, and a lead's
// prompt is the owner's to answer in the chat.
func TestAQuotedPromptIsNotAStall(t *testing.T) {
	prompt := paneText(t, "codex-approval")
	for name, pane := range map[string]string{
		"plain":           "› Ask Codex to do anything\n",
		"its own box":     prompt + "\n• Printed the fixture\n\n› Ask Codex to do anything\n",
		"working":         prompt + "\n  • Working (3s • esc to interrupt)\n",
		"footer only":     "the footer reads\nPress enter to confirm or esc to cancel\n",
		"choice only":     "› 1. Yes, proceed (y)\n  2. No\n",
		"a list above":    "1. Yes\n" + strings.Repeat("a line\n", 30) + "Esc to cancel\n",
		"not a yes":       "❯ 1. Left\n  2. Right\nEsc to cancel · Tab to amend\n",
		"other CLI's bar": "❯ 1. Yes\n  2. No\nPress enter to confirm or esc to cancel\n",
	} {
		if got := PanePrompt(pane); got != "" {
			t.Errorf("%s: PanePrompt = %q", name, got)
		}
	}

	proj, root := stallProject(t)
	showFixture(t, proj, root, "lead-p", "claude-approval", "sleep 60")
	if got := ScanStalls(proj, root, time.Now().Add(FirstCallLimit+time.Minute)); len(got) != 0 {
		t.Fatalf("a lead at a prompt: %+v", got)
	}
}

// writeCall records one issue-cli call in the worker's clilog.
func writeCall(t *testing.T, root, session, project string, at time.Time) {
	t.Helper()
	dur := int64(0)
	path := clilog.AgentPath(root, session)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := clilog.Append(path, clilog.Record{TS: at.Format(time.RFC3339), Args: []string{"--project", project, "start", "s"}, DurMS: &dur}); err != nil {
		t.Fatal(err)
	}
}

// A live worker that made no issue-cli call since its launch is reported
// whatever stopped it, and the call that hides it has to be this launch's and
// this project's: a re-dispatch keeps the session name and the log, and a test
// run in the worker's shell logs its fixture project there.
func TestADispatchWithNoFirstCallIsReported(t *testing.T) {
	proj, root := stallProject(t)
	for _, name := range []string{"agent-silent", "agent-called", "agent-redispatched", "agent-fixture", "lead-quiet"} {
		showInPane(t, proj, root, name, "echo up; sleep 60", "up")
	}
	writeCall(t, root, "agent-called", "p", time.Now())
	writeCall(t, root, "agent-redispatched", "p", time.Now().Add(-time.Hour))
	writeCall(t, root, "agent-fixture", "fixture", time.Now())

	now := time.Now()
	if got := ScanStalls(proj, root, now.Add(FirstCallLimit-time.Minute)); len(got) != 0 {
		t.Fatalf("within the limit: %+v", got)
	}
	var names []string
	for _, s := range ScanStalls(proj, root, now.Add(FirstCallLimit+10*time.Second)) {
		names = append(names, s.Session)
		if s.Dialog != "" || s.Words() != "no issue-cli call in the 5 m since launch" || s.Ask() != "attach and see why, or stop it" {
			t.Errorf("%+v: %q, %q", s, s.Words(), s.Ask())
		}
	}
	slices.Sort(names)
	if want := []string{"agent-fixture", "agent-redispatched", "agent-silent"}; !slices.Equal(names, want) {
		t.Fatalf("reported %q, want %q", names, want)
	}
}

// A dialog names the cause, so it wins over the silence it causes.
func TestAPromptWinsOverNoFirstCall(t *testing.T) {
	proj, root := stallProject(t)
	showFixture(t, proj, root, "agent-at-prompt", "codex-approval", "sleep 60")
	orig := CapturePane
	t.Cleanup(func() { CapturePane = orig })
	var pane string
	CapturePane = func(target string) (string, error) { // the pane the scan read
		p, err := orig(target)
		pane = p
		return p, err
	}
	got := ScanStalls(proj, root, time.Now().Add(FirstCallLimit+time.Minute))
	if len(got) != 1 || got[0].Dialog != "Codex's approval prompt" || got[0].NoCall != 0 {
		t.Fatalf("stalls = %+v; pane ends:\n%q", got, lastLines(pane))
	}
}

// The scan reads. The pane here runs cat on its terminal, so a key sent by
// any route (send-keys, a paste) would echo in it and end up in the file.
func TestAStalledWorkerIsNeverTypedInto(t *testing.T) {
	proj, root := stallProject(t)
	origKey, origText := SendKey, SendText
	t.Cleanup(func() { SendKey, SendText = origKey, origText })
	SendKey = func(target, key string) error {
		t.Errorf("SendKey(%s, %s)", target, key)
		return nil
	}
	SendText = func(target, text string) error {
		t.Errorf("SendText(%s, %s)", target, text)
		return nil
	}
	typed := filepath.Join(root, "typed.txt")
	showFixture(t, proj, root, "agent-at-prompt", "claude-approval", "cat > "+shell.Quote(typed))
	showInPane(t, proj, root, "agent-silent", "echo up; cat > "+typed+".silent", "up")
	before := map[string]string{}
	for _, name := range []string{"agent-at-prompt", "agent-silent"} {
		before[name], _ = CapturePane(Target(proj, name))
	}

	stalls := ScanStalls(proj, root, time.Now().Add(FirstCallLimit+time.Minute))
	if len(stalls) != 2 {
		t.Fatalf("stalls = %+v", stalls)
	}
	for _, name := range []string{"agent-at-prompt", "agent-silent"} {
		if after, _ := CapturePane(Target(proj, name)); after != before[name] {
			t.Errorf("%s changed:\n%s", name, after)
		}
	}
	for _, f := range []string{typed, typed + ".silent"} {
		if b, err := os.ReadFile(f); err != nil || len(b) != 0 {
			t.Errorf("%s = %q, %v", f, b, err)
		}
	}
}
