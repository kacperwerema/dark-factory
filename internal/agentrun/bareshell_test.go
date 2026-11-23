package agentrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// An empty agent never becomes a tmux window: the shell would run an empty
// command ("zsh: permission denied") and leave an idle session.
func TestStartRefusesAnEmptyAgent(t *testing.T) {
	isolateTmux(t)
	proj := &tracker.Project{Slug: "p", Terminal: "none", WorkDir: t.TempDir()}
	resp := Start(proj, Spec{Session: "postmortem-x", Prompt: "prompt", AgentType: " ", Headless: true})
	if resp.Status != "error" || !strings.Contains(stepNames(resp.Steps), "Pick the agent") {
		t.Fatalf("empty agent: %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	if HasSession(Target(proj, "postmortem-x")) {
		t.Fatal("a session was created")
	}
}

// BareShell tells a pane whose agent exited (the wrapper exec'd its login
// shell) from one whose agent still runs.
func TestBareShell(t *testing.T) {
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	orig := PromptDeliveryTimeout
	PromptDeliveryTimeout = 0
	t.Cleanup(func() { PromptDeliveryTimeout = orig })
	proj := &tracker.Project{Slug: "p", Terminal: "none", WorkDir: t.TempDir()}

	// The agent "sleep" gets the prompt as its argument: it runs for a minute.
	if resp := Start(proj, Spec{Session: "running", Prompt: "60", AgentType: "sleep", Headless: true}); resp.Status != "dispatched" {
		t.Fatalf("running: %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	// The agent "true" exits at once: the pane is left a bare shell. Made
	// by hand, since Start removes such a session.
	createExited(t, proj, "exited")
	deadline := time.Now().Add(10 * time.Second)
	for !BareShell(Target(proj, "exited")) {
		if time.Now().After(deadline) {
			t.Fatal("the pane of an agent that exited is not a bare shell")
		}
		time.Sleep(100 * time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		if BareShell(Target(proj, "running")) {
			t.Fatal("a running agent's pane counts as a bare shell")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if BareShell(Target(proj, "no-such-session")) {
		t.Fatal("a missing session counts as a bare shell")
	}
}

// createExited makes the session an agent that exited leaves behind.
func createExited(t *testing.T, proj *tracker.Project, session string) {
	t.Helper()
	_, cmd := CreateTmux(proj, session, t.TempDir(), nil, AgentCommand("true")...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

// launchProject is a headless project with no real delivery wait: a stand-in
// agent never shows Claude's conversation.
func launchProject(t *testing.T) *tracker.Project {
	t.Helper()
	isolateTmux(t)
	t.Setenv("SHELL", "/bin/sh")
	orig := PromptDeliveryTimeout
	PromptDeliveryTimeout = 0
	t.Cleanup(func() { PromptDeliveryTimeout = orig })
	return &tracker.Project{Slug: "p", Terminal: "none", WorkDir: t.TempDir(), IssueDir: t.TempDir()}
}

func injected() *tracker.Injected {
	return &tracker.Injected{Briefing: tracker.BriefingDispatch, Issue: "x", Refs: []string{"a/b#D-1"}}
}

// A launch whose agent exits at once is not "dispatched": the error names
// what happened, no session is left to reach, and the briefing's decisions
// were not delivered to anyone.
func TestStartReportsALaunchThatDies(t *testing.T) {
	proj := launchProject(t)
	resp := Start(proj, Spec{Session: "dies", Prompt: "prompt", AgentType: "true", Headless: true, Decisions: injected()})
	if resp.Status != "error" {
		t.Fatalf("status %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	last := resp.Steps[len(resp.Steps)-1]
	if last.Name != "Agent running" || last.Status != "error" || !strings.Contains(last.Detail, "true exited right after the launch") {
		t.Fatalf("last step %+v", last)
	}
	if HasSession(Target(proj, "dies")) {
		t.Fatal("the dead session was left")
	}
	if resp.AttachCmd != "" || resp.SwitchCmd != "" {
		t.Fatalf("hints to a removed session: %q %q", resp.AttachCmd, resp.SwitchCmd)
	}
	if _, err := os.Stat(tracker.DecisionLogPath(proj.IssueDir)); !os.IsNotExist(err) {
		t.Fatalf("a dead launch logged its decisions as delivered: %v", err)
	}
	if got := FailureDetail(resp); !strings.HasPrefix(got, ": true exited") {
		t.Fatalf("FailureDetail = %q", got)
	}
}

// An agent that never started leaves the shell's own words, and the session
// that held them goes: the report quotes them.
func TestStartQuotesWhyTheAgentNeverStarted(t *testing.T) {
	proj := launchProject(t)
	resp := Start(proj, Spec{Session: "missing", Prompt: "prompt", AgentType: "no-such-agent-x", Headless: true})
	if resp.Status != "error" {
		t.Fatalf("status %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	if d := resp.Steps[len(resp.Steps)-1].Detail; !strings.Contains(d, "Its last lines: ") || !strings.Contains(d, "not found") {
		t.Fatalf("detail %q", d)
	}
}

// An agent that survives the first watch and dies before the delivery check
// ends is caught by the look after it.
func TestStartLooksAgainAfterTheDeliveryCheck(t *testing.T) {
	proj := launchProject(t)
	origSettle, origBare := LaunchSettle, BareShell
	t.Cleanup(func() { LaunchSettle, BareShell = origSettle, origBare })
	LaunchSettle = 0
	looks := 0
	BareShell = func(string) bool { looks++; return looks > 1 }
	resp := Start(proj, Spec{Session: "late", Prompt: "60", AgentType: "sleep", Headless: true})
	if resp.Status != "error" || looks != 2 {
		t.Fatalf("status %q, %d looks\n%s", resp.Status, looks, stepNames(resp.Steps))
	}
}

// A session that is only a shell is no live agent: Start replaces it instead
// of re-attaching, and a running agent is still left alone.
func TestStartReplacesADeadSession(t *testing.T) {
	proj := launchProject(t)
	createExited(t, proj, "w")
	target := Target(proj, "w")
	deadline := time.Now().Add(10 * time.Second)
	for !BareShell(target) {
		if time.Now().After(deadline) {
			t.Fatal("no bare shell")
		}
		time.Sleep(50 * time.Millisecond)
	}
	spec := Spec{Session: "w", Prompt: "60", AgentType: "sleep", Headless: true}
	resp := Start(proj, spec)
	if resp.Status != "dispatched" || !strings.Contains(stepNames(resp.Steps), "Removed dead session") {
		t.Fatalf("over a dead session: %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	if !HasSession(target) || BareShell(target) {
		t.Fatal("no agent running after the start")
	}
	if resp := Start(proj, spec); resp.Status != "reattached" {
		t.Fatalf("over a running agent: %q\n%s", resp.Status, stepNames(resp.Steps))
	}
}

// paneTail keeps the last lines, on one line, and caps a long one.
func TestPaneTail(t *testing.T) {
	orig := CapturePane
	t.Cleanup(func() { CapturePane = orig })
	CapturePane = func(string) (string, error) { return "a\n\nb\n  c  \nd\n\n", nil }
	if got := paneTail("t"); got != "b | c | d" {
		t.Fatalf("tail %q", got)
	}
	CapturePane = func(string) (string, error) { return strings.Repeat("x", 5000), nil }
	if got := paneTail("t"); len(got) != paneTailChars {
		t.Fatalf("len %d", len(got))
	}
}

// waitBare waits for target's pane to turn into a bare shell.
func waitBare(t *testing.T, target string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !BareShell(target) {
		if time.Now().After(deadline) {
			t.Fatal("no bare shell")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A worker that died long after its launch leaves a session that is only a
// shell: Reprompt treats that as no worker and pastes nothing, since the
// briefing would run as commands.
func TestRepromptRefusesABareShell(t *testing.T) {
	proj := launchProject(t)
	issue := &tracker.Issue{Slug: "x", Title: "X"}
	session := tracker.AgentSessionName(issue.Slug)
	createExited(t, proj, session)
	target := Target(proj, session)
	waitBare(t, target)
	assertRepromptRefused(t, proj, issue, target)
}

// assertRepromptRefused reprompts issue over target's bare shell and checks
// the refusal: its step, the session kept, and nothing typed into the shell.
func assertRepromptRefused(t *testing.T, proj *tracker.Project, issue *tracker.Issue, target string) {
	t.Helper()
	before := settledPrompt(t, target)
	resp := Reprompt(proj, issue, proj.WorkDir)
	if resp.Status != "no-session" || len(resp.Steps) != 1 {
		t.Fatalf("status %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	if d := resp.Steps[0].Detail; resp.Steps[0].Status != "error" || !strings.Contains(d, "only a shell") || !strings.Contains(d, "dispatch "+issue.Slug+" --quote") || strings.Contains(d, "--reprompt") {
		t.Fatalf("step %+v", resp.Steps[0])
	}
	if !HasSession(target) {
		t.Fatal("the refusal removed the session; a dispatch does that")
	}
	if typed := shellInput(before, settledPane(t, target)); typed != "" {
		t.Fatalf("something reached the shell: %q", typed)
	}
}

// paneSettleGap is how far apart settledPane's two equal captures are: past
// the few milliseconds a login shell takes from counting as bare (its exec)
// to drawing its prompt, even on a loaded machine.
const paneSettleGap = 250 * time.Millisecond

// settledPane is target's pane once two captures paneSettleGap apart agree,
// so text still on its way (a prompt being drawn, an echo) is in it.
func settledPane(t *testing.T, target string) string {
	t.Helper()
	return settle(t, target, func(string) bool { return true })
}

// settledPrompt is settledPane once the pane shows something: a bare shell
// counts as bare from its exec on, before it has drawn its prompt, and a
// blank pane is as still as a finished one.
func settledPrompt(t *testing.T, target string) string {
	t.Helper()
	return settle(t, target, func(pane string) bool { return len(paneInputLines(pane)) > 0 })
}

func settle(t *testing.T, target string, ready func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	prev, err := CapturePane(target)
	for {
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(paneSettleGap)
		var cur string
		cur, err = CapturePane(target)
		if err == nil && cur == prev && ready(cur) {
			return cur
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane did not settle in 10s; last capture %q", cur)
		}
		prev = cur
	}
}

// paneInputLines is a pane's text as its shell sees it: its non-blank lines
// with trailing spaces cut. Blank rows and padding are the screen's, not
// input.
func paneInputLines(pane string) []string {
	var lines []string
	for _, l := range strings.Split(pane, "\n") {
		if l = strings.TrimRight(l, " \t"); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// shellInput is what reached a shell between two settled captures of its
// pane: the text after was given beyond before (keys on the prompt line, an
// Enter's new line), or all of after when its earlier text changed too. ""
// when nothing did.
func shellInput(before, after string) string {
	b := strings.Join(paneInputLines(before), "\n")
	a := strings.Join(paneInputLines(after), "\n")
	if a == b {
		return ""
	}
	if strings.HasPrefix(a, b) {
		return a[len(b):]
	}
	return a
}

// slowPromptShell is a stand-in login shell ($SHELL) that counts as bare for
// a second before it draws its prompt, as /bin/sh does for a moment under
// load: after AgentCommand's exec it is a shell with no child and no
// wrapper mark, sitting in a builtin read. Then it prompts for each line,
// running none.
func slowPromptShell(t *testing.T) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	path := filepath.Join(t.TempDir(), "sh")
	script := "#!" + bash + `
if [ "$1" = -lic ]; then eval "$2"; exit; fi
read -t 1 -r _
while :; do printf 'stand-in$ '; IFS= read -r _ || exit 0; done
`
	standin.Write(t, path, script)
	return path
}

// The flake TestRepromptRefusesABareShell had under load, made certain: a
// pane counts as a bare shell before its prompt is drawn, so a capture taken
// at once is blank and a later one is not, with nothing typed. The settled
// capture waits for the prompt, and the refusal still holds.
func TestRepromptRefusesAShellStillDrawingItsPrompt(t *testing.T) {
	proj := launchProject(t)
	t.Setenv("SHELL", slowPromptShell(t))
	issue := &tracker.Issue{Slug: "y", Title: "Y"}
	session := tracker.AgentSessionName(issue.Slug)
	createExited(t, proj, session)
	target := Target(proj, session)
	waitBare(t, target)
	if early, err := CapturePane(target); err != nil || len(paneInputLines(early)) != 0 {
		t.Fatalf("the stand-in drew its prompt before it counted as bare (%v): %q", err, early)
	}
	assertRepromptRefused(t, proj, issue, target)
}

// shellInput sees what a reprompt would leave in a shell: keys on the prompt
// line, an Enter's new prompt line, a paste over several lines. Typed into
// the stand-in shell by hand, each is caught.
func TestShellInputSeesWhatWasTyped(t *testing.T) {
	proj := launchProject(t)
	t.Setenv("SHELL", slowPromptShell(t))
	session := tracker.AgentSessionName("z")
	createExited(t, proj, session)
	target := Target(proj, session)
	waitBare(t, target)
	for _, keys := range [][]string{{"-l", "issue-cli show"}, {"Enter"}, {"-l", "one\ntwo"}} {
		before := settledPrompt(t, target)
		if out, err := exec.Command("tmux", append([]string{"send-keys", "-t", target}, keys...)...).CombinedOutput(); err != nil {
			t.Fatalf("send-keys %q: %v %s", keys, err, out)
		}
		if typed := shellInput(before, settledPane(t, target)); typed == "" {
			t.Errorf("send-keys %q: nothing seen\n%q", keys, before)
		}
	}
	if got := shellInput("$ \n\n", "$   \n\n\n"); got != "" {
		t.Errorf("blank rows and padding counted as input: %q", got)
	}
	if got := shellInput("$ ", "$ ls\n$ "); got != " ls\n$" {
		t.Errorf("typed line = %q", got)
	}
}

// Snapshot.BareShell agrees with BareShell on real panes, read once for a
// page over many agents.
func TestSnapshotBareShell(t *testing.T) {
	proj := launchProject(t)
	if resp := Start(proj, Spec{Session: "running", Prompt: "60", AgentType: "sleep", Headless: true}); resp.Status != "dispatched" {
		t.Fatalf("running: %q\n%s", resp.Status, stepNames(resp.Steps))
	}
	createExited(t, proj, "exited")
	waitBare(t, Target(proj, "exited"))
	s := TakeSnapshot()
	if !s.BareShell(proj, "exited") {
		t.Error("the exited agent's session is not a bare shell")
	}
	if s.BareShell(proj, "running") {
		t.Error("a running agent counts as a bare shell")
	}
	if s.BareShell(proj, "no-such-session") {
		t.Error("a missing session counts as a bare shell")
	}
	var none *Snapshot
	if none.BareShell(proj, "exited") {
		t.Error("a nil snapshot knows nothing")
	}
}

// Each thing that keeps a pane from counting, without tmux. One look at ps
// per pane the rest leaves standing, none for the others.
func TestSnapshotBareShellRule(t *testing.T) {
	origPanes, origProcs, origArgs := ListPanes, ProcessTable, PaneArgs
	t.Cleanup(func() { ListPanes, ProcessTable, PaneArgs = origPanes, origProcs, origArgs })
	ListPanes = func() []Pane {
		return []Pane{
			{Session: "dead", Window: "w", PID: 10, Command: "-zsh"},
			{Session: "wrapper", Window: "w", PID: 20, Command: "zsh"},
			{Session: "child", Window: "w", PID: 30, Command: "zsh"},
			{Session: "agent", Window: "w", PID: 40, Command: "claude.exe"},
			{Session: "stopped", Window: "w", PID: 50, Command: "zsh"},
			{Session: "split", Window: "a", PID: 10, Command: "zsh"},
			{Session: "split", Window: "a", PID: 40, Command: "claude.exe"},
			{Session: "shared", Window: "agent-x", PID: 10, Command: "zsh"},
		}
	}
	ProcessTable = func() []Proc {
		return []Proc{{PID: 10, PPID: 1, Stat: "S"}, {PID: 31, PPID: 30, Stat: "S"}, {PID: 50, PPID: 1, Stat: "T"}}
	}
	looks := 0
	PaneArgs = func(pid int) (string, error) {
		looks++
		if pid == 20 {
			return "/bin/sh -c " + agentWrapperMark + "claude", nil
		}
		return "-zsh", nil
	}
	proj := &tracker.Project{}
	s := TakeSnapshot()
	for name, want := range map[string]bool{"dead": true, "wrapper": false, "child": false, "agent": false, "stopped": false, "split": false} {
		if got := s.BareShell(proj, name); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	if looks != 3 { // dead, wrapper, split's first pane
		t.Errorf("%d ps looks, want 3", looks)
	}
	if !s.BareShell(&tracker.Project{TmuxSession: "shared"}, "agent-x") {
		t.Error("a window of the shared session is not found")
	}
	ProcessTable = func() []Proc { return nil }
	if TakeSnapshot().BareShell(proj, "dead") {
		t.Error("with no process table a pane must not read as dead")
	}
}
