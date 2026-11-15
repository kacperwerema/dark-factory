package viewer

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestAgentLaunchCommand_CodexUsesPromptFile(t *testing.T) {
	got := agentLaunchCommand(nil, "codex", "agent-x", "/tmp/agent-prompt-123.txt")
	if !strings.Contains(got, `codex "$(cat `) {
		t.Fatalf("agentLaunchCommand(codex) = %q, want codex to read from a temp prompt file", got)
	}
	if !strings.Contains(got, `/tmp/agent-prompt-123.txt`) {
		t.Fatalf("agentLaunchCommand(codex) = %q, missing prompt path", got)
	}
	if strings.Contains(got, " -n ") {
		t.Fatalf("agentLaunchCommand(codex) = %q, codex has no -n session name", got)
	}
}

// Claude is named after its tmux session so ListAgents / SendMessage address
// it by the same string; a name unsafe to type into the shell is dropped.
func TestAgentLaunchCommand_ClaudeSessionName(t *testing.T) {
	cases := map[string]string{
		"agent-ui-child":    `claude -n agent-ui-child "$(cat /p)"`,
		"lead-issue-viewer": `claude -n lead-issue-viewer "$(cat /p)"`,
		"agent-x; rm -rf /": `claude "$(cat /p)"`,
		"":                  `claude "$(cat /p)"`,
	}
	for session, want := range cases {
		if got := agentLaunchCommand(nil, "claude", session, "/p"); got != want {
			t.Errorf("session %q: got %q, want %q", session, got, want)
		}
	}
}

func TestAgentLaunchCommand_ModelSource(t *testing.T) {
	models := map[string]string{"claude": "claude-opus-5-5", "codex": "gpt-5"}
	cases := []struct {
		name  string
		proj  *tracker.Project
		agent string
		want  string
	}{
		{"nil project", nil, "claude", `claude "$(cat /p)"`},
		{"default source is global", &tracker.Project{AgentModels: models}, "claude", `claude "$(cat /p)"`},
		{"explicit global", &tracker.Project{AgentModels: models, AgentModelSource: "global"}, "claude", `claude "$(cat /p)"`},
		{"project enforces claude", &tracker.Project{AgentModels: models, AgentModelSource: "project"}, "claude", `claude --model claude-opus-5-5 "$(cat /p)"`},
		{"project enforces codex", &tracker.Project{AgentModels: models, AgentModelSource: "project"}, "codex", `codex --model gpt-5 "$(cat /p)"`},
		{"project without model for agent", &tracker.Project{AgentModels: map[string]string{"codex": "gpt-5"}, AgentModelSource: "project"}, "claude", `claude "$(cat /p)"`},
		{"unsafe model ignored", &tracker.Project{AgentModels: map[string]string{"claude": "x; rm -rf /"}, AgentModelSource: "project"}, "claude", `claude "$(cat /p)"`},
	}
	for _, c := range cases {
		if got := agentLaunchCommand(c.proj, c.agent, "", "/p"); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStartAgentSession_ReattachWhenSessionExists(t *testing.T) {
	// Headless terminal so openTerminalStep doesn't shell out.
	proj := &tracker.Project{Slug: "test", Terminal: "none"}
	session := tmuxSessionName("bug-in-login")

	withMockTmuxHasSession(t, func(name string) bool {
		if name != "="+session+":" {
			t.Errorf("agentrun.HasSession called with %q, want %q", name, session)
		}
		return true
	})

	resp := startAgentSession(proj, session, "do the work", "bug-in-login", "claude", nil)

	if resp.Status != "reattached" {
		t.Fatalf("Status = %q, want reattached", resp.Status)
	}
	if resp.Session != session {
		t.Fatalf("Session = %q, want %q", resp.Session, session)
	}
	if resp.AttachCmd == "" || !strings.Contains(resp.AttachCmd, session) {
		t.Fatalf("AttachCmd = %q, want it to mention %q", resp.AttachCmd, session)
	}

	var sawAttaching, sawHeadless bool
	for _, step := range resp.Steps {
		if step.Name == "Existing session — attaching" {
			sawAttaching = true
			if step.Status != "reattached" {
				t.Errorf("attaching step status = %q, want reattached", step.Status)
			}
		}
		if strings.HasPrefix(step.Name, "Terminal: headless") {
			sawHeadless = true
		}
		if strings.HasPrefix(step.Name, "Create tmux session") {
			t.Errorf("unexpected Create tmux session step on reattach: %+v", step)
		}
		if strings.HasPrefix(step.Name, "Log to ") || strings.HasPrefix(step.Name, "CLI log to ") {
			t.Errorf("unexpected logging setup step on reattach: %+v", step)
		}
	}
	if !sawAttaching {
		t.Errorf("missing 'Existing session — attaching' step; got %+v", resp.Steps)
	}
	if !sawHeadless {
		t.Errorf("missing headless terminal step; got %+v", resp.Steps)
	}
}

func TestStartAgentSession_DoesNotReattachWhenSessionMissing(t *testing.T) {
	// Regression guard: when agentrun.HasSession returns false, the reattach branch
	// must NOT fire — the response status must not be "reattached" regardless
	// of whether the downstream new-session call succeeds in the test env.
	//
	// startAgentSession's downstream new-session step actually shells out to
	// tmux, so run it on a private server with a stand-in agent: launching
	// claude here would start a real session with the test prompt.
	isolateTmux(t)
	proj := &tracker.Project{Slug: "test", Terminal: "none", WorkDir: t.TempDir()}
	session := "issue-viewer-test-missing-session-xyz"

	withMockTmuxHasSession(t, func(string) bool { return false })

	resp := startAgentSession(proj, session, "prompt", "slug", "true", nil)
	if resp.Status == "reattached" {
		t.Fatalf("Status = reattached when session does not exist; steps=%+v", resp.Steps)
	}
	for _, step := range resp.Steps {
		if step.Name == "Existing session — attaching" {
			t.Fatalf("unexpected attaching step when session missing: %+v", step)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

func TestBuildAgentPrompt_AppendsWorktreeBlockWhenSet(t *testing.T) {
	issue := &tracker.Issue{Slug: "fix-foo", Title: "T", Status: "in progress", BodyRaw: "body"}
	prompt := buildAgentPrompt(nil, issue, &tracker.WorkflowConfig{}, "/work/proj/.worktrees/fix-foo", "work/fix-foo")
	for _, want := range []string{
		"## Worktree",
		"/work/proj/.worktrees/fix-foo",
		"work/fix-foo",
		"Leave the worktree in place",
		"primary checkout (master)",
		"refuses issue-cli dispatch, department add and lead spawn from your shell, even with --config on a sandbox",
		"TestNewcomerWalkthrough (newcomer_test.go)",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q\n%s", want, prompt)
		}
	}
}

func TestBuildAgentPrompt_OmitsWorktreeBlockWhenEmpty(t *testing.T) {
	issue := &tracker.Issue{Slug: "fix-foo", Title: "T", Status: "in progress", BodyRaw: "body"}
	prompt := buildAgentPrompt(nil, issue, &tracker.WorkflowConfig{}, "", "")
	if strings.Contains(prompt, "## Worktree") {
		t.Fatalf("expected no worktree block when path is empty")
	}
}

func TestBuildAgentPrompt_TeachesCheckByID(t *testing.T) {
	issue := &tracker.Issue{Slug: "fix-foo", Title: "T", Status: "in progress", BodyRaw: "body"}
	prompt := buildAgentPrompt(nil, issue, &tracker.WorkflowConfig{}, "", "")
	for _, want := range []string{
		"issue-cli check fix-foo <id> [<id>...]\n",
		"issue-cli check fix-foo <id> [<id>...]   # mark checkboxes done by id",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "%!") {
		t.Fatalf("prompt has a format verb mismatch:\n%s", prompt)
	}
}

type errStub struct{}

func (errStub) Error() string { return "exit status 1" }

func TestRenderActionPrompt_SubstitutesIssueFields(t *testing.T) {
	issue := &tracker.Issue{
		Slug:     "fix-login",
		Title:    "Fix login",
		Status:   "in progress",
		System:   "Auth",
		Priority: "high",
		Number:   42,
	}
	prompt := renderActionPrompt(
		"slug={{slug}} title={{title}} status={{status}} system={{system}} priority={{priority}} number={{number}} unknown={{nope}}",
		issue,
	)
	want := "slug=fix-login title=Fix login status=in progress system=Auth priority=high number=42 unknown={{nope}}"
	if prompt != want {
		t.Fatalf("renderActionPrompt =\n  %q\nwant\n  %q", prompt, want)
	}
}

// TestHandleCustomAction_DispatchesConfiguredPrompt drives the full HTTP path:
// a project workflow defines a custom action, the POST resolves it, templates
// the prompt, and dispatches to a per-action tmux session with the action's
// configured agent.
func TestHandleCustomAction_DispatchesConfiguredPrompt(t *testing.T) {
	proj, tmpDir := setupTestProject(t)
	wfPath := filepath.Join(tmpDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(`statuses:
  - name: "in progress"
    description: "Actively being implemented"
actions:
  - id: "defer-to-team"
    label: "Defer to team"
    agent: "codex"
    prompt: |
      Defer {{slug}} ({{title}}) to the team.
`), 0644); err != nil {
		t.Fatal(err)
	}
	proj.WorkflowFile = wfPath

	var gotPrompt, gotSession, gotIssueSlug, gotAgent string
	origDispatch := dispatchAgentSession
	dispatchAgentSession = func(_ *tracker.Project, session, prompt, issueSlug, agentType string, wf *tracker.WorkflowConfig) DispatchResponse {
		gotPrompt, gotSession, gotIssueSlug, gotAgent = prompt, session, issueSlug, agentType
		return DispatchResponse{Status: "dispatched", Prompt: prompt, Session: session}
	}
	t.Cleanup(func() { dispatchAgentSession = origDispatch })

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/p/test-project/issue/bug-in-login/action/defer-to-team", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if !strings.Contains(gotPrompt, "Defer bug-in-login (Bug in login) to the team.") {
		t.Fatalf("prompt not templated from action: %q", gotPrompt)
	}
	if gotAgent != "codex" {
		t.Fatalf("expected codex agent from action config, got %q", gotAgent)
	}
	if gotIssueSlug != "bug-in-login" {
		t.Fatalf("expected issue slug bug-in-login, got %q", gotIssueSlug)
	}
	if !strings.Contains(gotSession, "bug-in-login-defer-to-team") {
		t.Fatalf("expected per-action session name, got %q", gotSession)
	}
}

func TestHandleCustomAction_UnknownActionReturns404(t *testing.T) {
	proj, tmpDir := setupTestProject(t)
	wfPath := filepath.Join(tmpDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte("statuses:\n  - name: \"in progress\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	proj.WorkflowFile = wfPath

	called := false
	origDispatch := dispatchAgentSession
	dispatchAgentSession = func(_ *tracker.Project, session, prompt, issueSlug, agentType string, _ *tracker.WorkflowConfig) DispatchResponse {
		called = true
		return DispatchResponse{}
	}
	t.Cleanup(func() { dispatchAgentSession = origDispatch })

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/p/test-project/issue/bug-in-login/action/nope", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown action, got %d", resp.StatusCode)
	}
	if called {
		t.Fatalf("dispatch must not run for an unknown action")
	}
}

// TestHandleProjectAction_DispatchesConfiguredPrompt drives the full HTTP path
// for a project-level action: the POST resolves it from project_actions,
// templates the prompt with project context, and dispatches to a per-action
// tmux session with no issue slug.
func TestHandleProjectAction_DispatchesConfiguredPrompt(t *testing.T) {
	proj, tmpDir := setupTestProject(t)
	wfPath := filepath.Join(tmpDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(`statuses:
  - name: "in progress"
    description: "Actively being implemented"
project_actions:
  - id: "triage-backlog"
    label: "Triage backlog"
    agent: "codex"
    prompt: |
      Triage the backlog for {{project}}.
`), 0644); err != nil {
		t.Fatal(err)
	}
	proj.WorkflowFile = wfPath

	var gotPrompt, gotSession, gotIssueSlug, gotAgent string
	origDispatch := dispatchAgentSession
	dispatchAgentSession = func(_ *tracker.Project, session, prompt, issueSlug, agentType string, wf *tracker.WorkflowConfig) DispatchResponse {
		gotPrompt, gotSession, gotIssueSlug, gotAgent = prompt, session, issueSlug, agentType
		return DispatchResponse{Status: "dispatched", Prompt: prompt, Session: session}
	}
	t.Cleanup(func() { dispatchAgentSession = origDispatch })

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/p/test-project/action/triage-backlog", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if !strings.Contains(gotPrompt, "Triage the backlog for Test Project.") {
		t.Fatalf("prompt not templated from project action: %q", gotPrompt)
	}
	if gotAgent != "codex" {
		t.Fatalf("expected codex agent from action config, got %q", gotAgent)
	}
	if gotIssueSlug != "" {
		t.Fatalf("expected empty issue slug for project action, got %q", gotIssueSlug)
	}
	if !strings.Contains(gotSession, "test-project-triage-backlog") {
		t.Fatalf("expected per-action session name, got %q", gotSession)
	}
}

func TestHandleProjectAction_UnknownActionReturns404(t *testing.T) {
	proj, tmpDir := setupTestProject(t)
	wfPath := filepath.Join(tmpDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte("statuses:\n  - name: \"in progress\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	proj.WorkflowFile = wfPath

	called := false
	origDispatch := dispatchAgentSession
	dispatchAgentSession = func(_ *tracker.Project, session, prompt, issueSlug, agentType string, _ *tracker.WorkflowConfig) DispatchResponse {
		called = true
		return DispatchResponse{}
	}
	t.Cleanup(func() { dispatchAgentSession = origDispatch })

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/p/test-project/action/nope", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown project action, got %d", resp.StatusCode)
	}
	if called {
		t.Fatalf("dispatch must not run for an unknown project action")
	}
}

func TestStartAgentSession_SharedSessionAddsWindows(t *testing.T) {
	isolateTmux(t)
	standInAgentRuns(t)
	proj := &tracker.Project{Slug: "test", Terminal: "none", TmuxSession: "work", WorkDir: t.TempDir()}

	first := startAgentSession(proj, "agent-a", "prompt", "a", "true", nil)
	if first.Status != "dispatched" {
		t.Fatalf("first dispatch Status = %q; steps=%+v", first.Status, first.Steps)
	}
	if first.Session != "work:agent-a" {
		t.Fatalf("Session = %q, want work:agent-a", first.Session)
	}
	if !strings.Contains(first.AttachCmd, "tmux attach -t work") {
		t.Fatalf("AttachCmd = %q, want it to attach to the shared session", first.AttachCmd)
	}

	second := startAgentSession(proj, "agent-b", "prompt", "b", "true", nil)
	if second.Status != "dispatched" {
		t.Fatalf("second dispatch Status = %q; steps=%+v", second.Status, second.Steps)
	}
	var sawNewWindow bool
	for _, step := range second.Steps {
		if strings.HasPrefix(step.Name, "Create tmux window agent-b in session work") && step.Status == "ok" {
			sawNewWindow = true
		}
	}
	if !sawNewWindow {
		t.Fatalf("second dispatch should add a window to the existing session; steps=%+v", second.Steps)
	}

	out, err := testTmux(t, "list-sessions", "-F", "#{session_name}").Output()
	if err != nil || strings.TrimSpace(string(out)) != "work" {
		t.Fatalf("sessions = %q (err %v), want only the shared session", out, err)
	}
	windows := listSharedAgentWindows()
	if len(windows) != 2 || windows[0].Name != "work:agent-a" || windows[1].Name != "work:agent-b" {
		t.Fatalf("shared agent windows = %+v", windows)
	}

	again := startAgentSession(proj, "agent-a", "prompt", "a", "true", nil)
	if again.Status != "reattached" {
		t.Fatalf("re-dispatch Status = %q, want reattached; steps=%+v", again.Status, again.Steps)
	}
}

// activeWindow is the selected window of a session on the test's tmux server.
func activeWindow(t *testing.T, session string) string {
	t.Helper()
	out, err := testTmux(t, "display-message", "-p", "-t", "="+session+":", "#{window_name}").Output()
	if err != nil {
		t.Fatalf("active window of %s: %v", session, err)
	}
	return strings.TrimSpace(string(out))
}

// A dispatch never changes what the shared session shows: the lead's window
// stays selected while workers' windows are added and re-dispatched, with
// terminal: none (D-4) and with a terminal configured.
func TestStartAgentSession_SharedHeadlessKeepsLeadFocused(t *testing.T) {
	isolateTmux(t)
	standInAgentRuns(t)
	proj := &tracker.Project{Slug: "test", Terminal: "none", TmuxSession: "work", WorkDir: t.TempDir()}
	if out, err := testTmux(t, "new-session", "-d", "-s", "work", "-n", "lead-test").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v %s", err, out)
	}

	for _, name := range []string{"agent-a", "agent-b", "agent-a"} {
		resp := startAgentSession(proj, name, "prompt", strings.TrimPrefix(name, "agent-"), "true", nil)
		if resp.Status == "error" {
			t.Fatalf("dispatch %s: steps=%+v", name, resp.Steps)
		}
		if got := activeWindow(t, "work"); got != "lead-test" {
			t.Fatalf("after dispatching %s (%s) the active window is %q, want lead-test; steps=%+v", name, resp.Status, got, resp.Steps)
		}
		for _, step := range resp.Steps {
			if strings.HasPrefix(step.Name, "Select window") {
				t.Errorf("headless dispatch selected a window: %+v", step)
			}
		}
		if resp.SwitchCmd != "tmux switch-client -t '=work:="+name+"'" || !strings.Contains(resp.AttachCmd, "tmux attach -t work") {
			t.Errorf("hints: switch %q, attach %q", resp.SwitchCmd, resp.AttachCmd)
		}
	}

	withTerminal := &tracker.Project{Slug: "test", Terminal: "true {{session}}", TmuxSession: "work", WorkDir: proj.WorkDir}
	original := agentrun.SessionAttached
	agentrun.SessionAttached = func(string) bool { return true }
	t.Cleanup(func() { agentrun.SessionAttached = original })
	if resp := startAgentSession(withTerminal, "agent-c", "prompt", "c", "true", nil); resp.Status == "error" || resp.SwitchCmd != "" {
		t.Fatalf("dispatch with a terminal: status %q, switch %q; steps=%+v", resp.Status, resp.SwitchCmd, resp.Steps)
	}
	if got := activeWindow(t, "work"); got != "lead-test" {
		t.Fatalf("with a terminal the lead's window stays selected too; active = %q", got)
	}
}

func TestAttachAgentStep_SharedSessionAlreadyAttachedOpensNoTerminal(t *testing.T) {
	isolateTmux(t)
	// A terminal command that fails loudly if it were run.
	proj := &tracker.Project{Slug: "test", Terminal: "exit 1", TmuxSession: "work"}
	original := agentrun.SessionAttached
	agentrun.SessionAttached = func(string) bool { return true }
	t.Cleanup(func() { agentrun.SessionAttached = original })

	var steps []DispatchStep
	if !attachAgentStep(proj, "agent-a", &steps) {
		t.Fatalf("attachAgentStep failed; steps=%+v", steps)
	}
	for _, step := range steps {
		if step.Name == "Open terminal" {
			t.Fatalf("opened a terminal although the shared session is attached; steps=%+v", steps)
		}
	}
}

func TestAttachAgentStep_SharedSessionDetachedOpensTerminal(t *testing.T) {
	isolateTmux(t)
	proj := &tracker.Project{Slug: "test", Terminal: "true {{session}}", TmuxSession: "work"}
	original := agentrun.SessionAttached
	agentrun.SessionAttached = func(string) bool { return false }
	t.Cleanup(func() { agentrun.SessionAttached = original })

	var steps []DispatchStep
	attachAgentStep(proj, "agent-a", &steps)
	var opened bool
	for _, step := range steps {
		if step.Name == "Open terminal" && step.Status == "ok" {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("expected a terminal to be opened on the detached shared session; steps=%+v", steps)
	}
}

// An issue whose next gate only waits on the owner's approval is refused by
// ▶ (409, the gate named, nothing started); force dispatches anyway. The
// refusal is agentrun.DispatchSpec's, shared with `issue-cli dispatch`.
func TestHandleDispatchAgent_RefusesAtAReadyGateUnlessForced(t *testing.T) {
	proj, tmpDir := setupTestProject(t)
	wfPath := filepath.Join(tmpDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(`statuses:
  - name: "in progress"
  - name: "testing"
transitions:
  - from: "in progress"
    to: "testing"
    actions:
      - type: "require_human_approval"
        status: "testing"
`), 0644); err != nil {
		t.Fatal(err)
	}
	proj.WorkflowFile = wfPath
	origHas := agentrun.HasSession
	agentrun.HasSession = func(string) bool { return false }
	t.Cleanup(func() { agentrun.HasSession = origHas })

	var started []agentrun.Spec
	origStart := startDispatch
	startDispatch = func(_ *tracker.Project, spec agentrun.Spec) DispatchResponse {
		started = append(started, spec)
		return DispatchResponse{Status: "dispatched", Session: spec.Session}
	}
	t.Cleanup(func() { startDispatch = origStart })

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	post := func(body string) (*http.Response, DispatchResponse) {
		t.Helper()
		resp, err := http.Post(ts.URL+"/p/test-project/issue/bug-in-login/dispatch", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var dr DispatchResponse
		json.NewDecoder(resp.Body).Decode(&dr)
		return resp, dr
	}

	resp, dr := post(`{"agent":"claude"}`)
	if resp.StatusCode != http.StatusConflict || dr.Status != agentrun.StatusWaitingOnGate || dr.Gate != "testing" {
		t.Fatalf("status %d, response %+v; want 409 waiting-on-gate for testing", resp.StatusCode, dr)
	}
	if len(dr.Steps) != 1 || !strings.Contains(dr.Steps[0].Detail, "issue-cli --project test-project dispatch bug-in-login --force") {
		t.Fatalf("steps = %+v, want the --force hint", dr.Steps)
	}
	if len(started) != 0 {
		t.Fatalf("a refused dispatch started %+v", started)
	}

	resp, dr = post(`{"agent":"codex","force":true}`)
	if resp.StatusCode != http.StatusOK || dr.Status != "dispatched" {
		t.Fatalf("forced: status %d, response %+v", resp.StatusCode, dr)
	}
	if len(started) != 1 || started[0].AgentType != "codex" || started[0].Session != "agent-bug-in-login" || started[0].RepromptHint != "issue-cli --project test-project dispatch bug-in-login --reprompt --quote \"<the human's words>\"" {
		t.Fatalf("started = %+v", started)
	}
}
