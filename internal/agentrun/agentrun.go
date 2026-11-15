// Package agentrun launches agents (workers and leads) in tmux: it creates
// their worktree, their tmux session or shared-session window, exports the
// environment issue-cli reads, and starts the agent with its prompt. The
// viewer's dispatch handlers and `issue-cli lead spawn` both go through it.
package agentrun

import (
	"fmt"
	"github.com/kacperwerema/dark-factory/internal/tokens"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/routing"
	"github.com/kacperwerema/dark-factory/internal/shell"
	"github.com/kacperwerema/dark-factory/internal/telemetry"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Step is one line of a launch report.
type Step struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Response is the result of a launch, also the JSON body of the viewer's
// dispatch endpoints.
type Response struct {
	Status    string `json:"status"`
	Prompt    string `json:"prompt"`
	Session   string `json:"session"`
	AttachCmd string `json:"attach_cmd,omitempty"`
	// SwitchCmd reaches the agent from inside tmux; set with AttachCmd.
	SwitchCmd string `json:"switch_cmd,omitempty"`
	// Gate is the status awaiting the owner's approval when Status is
	// "waiting-on-gate" (see DispatchSpec).
	Gate  string `json:"gate,omitempty"`
	Steps []Step `json:"steps"`
}

// Spec describes one agent launch.
type Spec struct {
	// Session is the tmux session (or shared-session window) name:
	// agent-<slug> for workers, lead-<project> for leads.
	Session   string
	Prompt    string
	AgentType string
	// IssueSlug is exported as ISSUE_VIEWER_ISSUE_SLUG and names the window
	// in per-session mode. Empty for agents not bound to an issue.
	IssueSlug string
	// LeadScope is exported as ISSUE_VIEWER_LEAD_SCOPE: "project" (the CEO) or
	// department:<key> (a department lead). Empty for workers.
	LeadScope string
	// WorkDir is the project checkout the agent (or its worktree) starts
	// from; "" means ProjectWorkDir(proj).
	WorkDir string
	// Worktree is where the agent runs; zero means WorkDir itself.
	Worktree tracker.WorktreeSpec
	Workflow *tracker.WorkflowConfig
	// RepromptHint is the command quoted in hints when the prompt can be
	// re-sent (issue-cli dispatch <slug> --reprompt).
	RepromptHint string
	// Route is how AgentType was picked (routing: rules, a fallback, or
	// the owner's --agent); nil when the caller named the agent itself.
	// Start reports it as the launch's first step.
	Route *routing.Choice
	// Headless skips opening a terminal (lead_terminal for leads, terminal
	// for workers); the response carries AttachCmd and SwitchCmd.
	Headless bool
	// Decisions is the injected event the briefing's decisions block
	// writes once the agent is launched with it (nil: none). A reattach
	// or a failed launch writes nothing.
	Decisions *tracker.Injected
	// Briefing is the briefing record (size and ids, no text) the issue's
	// stats sidecar gets once the agent is launched with Prompt; nil for a
	// launch that is not an issue's worker.
	Briefing *tracker.Briefing
}

// validModelName limits --model values to characters that are safe in a
// shell command unquoted.
var validModelName = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]-]+$`)

// validSessionName limits the -n value to characters that are safe in a shell
// command unquoted; agent-<slug> and lead-<project> names always are.
var validSessionName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// LaunchCommand builds the shell command the agent's window runs. The prompt is
// passed as the agent's positional argument (both claude and codex accept
// one and start interactive with it). Claude is named after its tmux session
// (-n agent-<slug>, -n lead-<project>), so the tmux name is also its address
// for Claude Code's cross-session messaging (ListAgents / SendMessage). A
// --model flag is added only when the project enforces one
// (agent_model_source: project).
func LaunchCommand(proj *tracker.Project, agentType, session, promptPath string) string {
	return LaunchCommandWith(proj, agentType, session, promptPath, nil)
}

// LaunchCommandWith is LaunchCommand with extra flags before the prompt
// (the department's MCP connectors, ConnectorFlags), each shell-quoted.
func LaunchCommandWith(proj *tracker.Project, agentType, session, promptPath string, extra []string) string {
	nameFlag := ""
	if agentType == "claude" && validSessionName.MatchString(session) {
		nameFlag = " -n " + session
	}
	modelFlag := ""
	if m := proj.AgentModel(agentType); m != "" && validModelName.MatchString(m) {
		modelFlag = " --model " + m
	}
	extraFlags := ""
	for _, f := range extra {
		extraFlags += " " + shell.Quote(f)
	}
	// agentType and promptPath reach a login shell: an agent that isn't a
	// plain command name is single-quoted (and so fails to start), and the
	// path is single-quoted, since Go's %q still expands $() and backticks
	// inside the shell's double quotes (docs/API/security.md).
	if !ValidAgentType(agentType) {
		agentType = shell.Quote(agentType)
	}
	return fmt.Sprintf("%s%s%s%s \"$(cat %s)\"", agentType, nameFlag, modelFlag, extraFlags, shell.Quote(promptPath))
}

// validAgentType is a command name: what claude, codex or a configured
// agent is called on PATH.
var validAgentType = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// ValidAgentType reports whether agent may be launched: a plain command
// name, never shell text. Routes that take an agent from a request refuse
// anything else.
func ValidAgentType(agent string) bool {
	return validAgentType.MatchString(agent)
}

// AgentCommand wraps launch for running as a tmux window's own command: in
// the user's login shell, interactive, so the agent is found on the same PATH
// and with the same aliases as when it was typed into a fresh window. When
// the agent exits the window keeps a shell, also after a Ctrl-C: the no-op INT
// trap stops the wrapper shell from abandoning the rest of the command along
// with the agent.
func AgentCommand(launch string) []string {
	login := strings.TrimSpace(os.Getenv("SHELL"))
	if login == "" {
		login = "/bin/sh"
	}
	return []string{login, "-lic", "trap : INT; " + launch + "; exec " + shell.Quote(login) + " -l"}
}

// WritePrompt persists prompt as <sessionLogDir>/dispatch-prompt.txt and
// returns its path.
func WritePrompt(sessionLogDir, prompt string) (string, error) {
	if err := os.MkdirAll(sessionLogDir, 0755); err != nil {
		return "", err
	}
	telemetry.IgnoreAgentLogs(sessionLogDir)
	path := filepath.Join(sessionLogDir, "dispatch-prompt.txt")
	return path, os.WriteFile(path, []byte(prompt), 0644)
}

// ProjectWorkDir is the directory agents of proj start from: its configured
// workdir, else the current directory.
func ProjectWorkDir(proj *tracker.Project) string {
	if proj != nil && strings.TrimSpace(proj.WorkDir) != "" {
		return proj.WorkDir
	}
	wd, _ := os.Getwd()
	return wd
}

// Start launches the agent described by spec, or re-attaches when its
// session is already live (never starting a second one). A session that is
// only a shell (BareShell) is not live: it is removed and the agent starts.
// "dispatched" means an agent is running: a launch that dies at once comes
// back as an error with its session removed (reportDeadLaunch).
func Start(proj *tracker.Project, spec Spec) Response {
	session, prompt := spec.Session, spec.Prompt
	// No agent named means the window would run an empty command: the
	// shell says "permission denied" and an idle session is left behind.
	// The caller routes (RouteAgent) or names the agent first.
	if strings.TrimSpace(spec.AgentType) == "" {
		return Response{Status: "error", Prompt: prompt, Session: session, Steps: []Step{{
			Name: "Pick the agent", Status: "error",
			Detail: "no agent named for " + session + ": nothing was started (the caller routes with routing: or names claude / codex)"}}}
	}
	workDir := spec.WorkDir
	if workDir == "" {
		workDir = ProjectWorkDir(proj)
	}
	// Create / reuse the worktree before opening the tmux session so the agent
	// lands in an isolated working tree. Concurrent issues then cannot stomp
	// on each other's uncommitted state. On failure we abort: silently falling
	// back to the primary checkout would defeat the safety guarantee.
	// Read the base before the branch exists: afterwards it can't be told
	// from a redispatch onto the same branch.
	var base tracker.Dispatch
	base.BaseBranch, base.BaseSHA, _ = WorktreeBase(workDir, spec.Worktree)
	newDir, worktreeSteps, worktreeOK := EnsureWorktree(workDir, spec.Worktree, spec.Workflow)
	if !worktreeOK {
		return Response{Status: "error", Prompt: prompt, Session: session, Steps: worktreeSteps}
	}
	if spec.Worktree.Path != "" {
		ExcludeHandoff(newDir)
	}
	// The session's logs (clilog, prompt, messages) stay in the project
	// checkout, not the worktree, so removing a worktree loses no history.
	sessionLogDir := filepath.Join(workDir, ".agent-logs", session)
	cliLog := filepath.Join(sessionLogDir, session+".clilog")
	workDir = newDir

	steps := append([]Step{}, worktreeSteps...)

	// target is what every tmux command below addresses: the agent's own
	// session, or its window inside the project's shared session.
	target := Target(proj, session)

	response := Response{
		Status:  "dispatched",
		Prompt:  prompt,
		Session: tracker.AgentDisplayName(proj, session),
		Steps:   steps,
	}
	// Headless (spec.Headless, or terminal: none) opens no terminal; the
	// response carries the attach hints instead. No launch selects the new
	// window, so whoever is looking at the shared session (usually the lead)
	// keeps it. Leads follow lead_terminal: instead, headless when it is
	// unset.
	lead := spec.LeadScope != ""
	headless := spec.Headless || TerminalNone(proj)
	if lead {
		headless = spec.Headless || LeadHeadless(proj)
	}
	setHints := func() {
		response.AttachCmd = AttachCmd(proj, session)
		response.SwitchCmd = SwitchCmd(proj, session)
	}
	// showLead runs lead_terminal; a tab it could not add leaves the hints.
	showLead := func() bool {
		ok, shown := LeadTerminalStep(proj, session, &steps)
		if !shown {
			setHints()
		}
		return ok
	}

	// An agent that exited leaves its window as a shell. Re-attaching to it
	// would call a dead worker live, and nothing would restart it.
	if HasSession(target) && BareShell(target) {
		if err := KillTmux(proj, session); err != nil {
			steps = append(steps, Step{Name: "Remove dead session", Status: "warning", Detail: err.Error()})
		} else {
			steps = append(steps, Step{Name: "Removed dead session", Status: "ok", Detail: session + " was only a shell: its agent had exited"})
		}
	}

	if HasSession(target) {
		// A session with this name already exists — most often the same agent is
		// still running. Re-running new-session would start a second agent
		// next to it with the prompt again, so skip all setup and just
		// open a terminal attached to the existing session.
		steps = append(steps, Step{Name: "Existing session — attaching", Status: "reattached"})
		if spec.RepromptHint != "" {
			steps = append(steps, Step{Name: "Prompt not re-sent", Status: "reattached", Detail: "to re-send the current briefing use Re-send prompt or run: " + spec.RepromptHint})
		}
		response.Status = "reattached"
		switch {
		case headless:
			steps = append(steps, Step{Name: "Terminal: headless — attach manually", Status: "ok"})
			setHints()
		case lead:
			showLead()
		default:
			AttachStep(proj, session, &steps)
		}
		response.Steps = steps
		return response
	}

	// Persist the exact prompt the bot is briefed with. The agent is launched
	// with this file as its argument, and the timeline view replays it later
	// instead of reconstructing an approximation.
	promptPath, err := WritePrompt(sessionLogDir, prompt)
	if err != nil {
		steps = append(steps, Step{Name: "Write prompt file", Status: "error", Detail: err.Error()})
		response.Status = "error"
		response.Steps = steps
		return response
	}

	if spec.Route != nil {
		steps = append(steps, RouteStep(*spec.Route))
	}

	serverRoot, _ := os.Getwd()
	env := []string{
		"ISSUE_CLI_LOG=" + cliLog,
		"ISSUE_VIEWER_SERVER_PWD=" + serverRoot,
	}
	if spec.IssueSlug != "" {
		env = append(env, "ISSUE_VIEWER_ISSUE_SLUG="+spec.IssueSlug)
	}
	if spec.LeadScope != "" {
		env = append(env, "ISSUE_VIEWER_LEAD_SCOPE="+spec.LeadScope)
	}
	if proj != nil && proj.Slug != "" {
		env = append(env, "ISSUE_VIEWER_PROJECT="+proj.Slug)
	}
	// Propagate the config path the viewer was launched with so the CLI can
	// resolve `--project <slug>` against it without the bot having to know
	// the file name (e.g. projects-mfranc.yaml vs the default projects.yaml).
	if cfg := strings.TrimSpace(os.Getenv("ISSUE_VIEWER_CONFIG")); cfg != "" {
		env = append(env, "ISSUE_VIEWER_CONFIG="+cfg)
	}

	// The agent is the window's own command, started with its environment and
	// working directory already set. Nothing is typed into a shell, so keys
	// pressed in an attached client while this runs can't reach the command.
	// The prompt goes in as the agent's launch argument, never pasted: a
	// paste sent before the agent's input is ready is silently dropped. Both
	// agents start interactive, so a human stays in the loop.
	extra := ConnectorFlags(proj, spec, sessionLogDir, env, &steps)
	env = append(env, ConnectorEnv(proj, spec)...)
	if spec.AgentType == "codex" {
		extra = append(CodexStartFlags(workDir), extra...)
		sandbox := CodexSandboxFlags(codexSandbox(proj, spec.Workflow), projectIssueDir(proj), workDir)
		if len(sandbox) > 0 {
			extra = append(extra, sandbox...)
			steps = append(steps, Step{Name: "Codex sandbox (routing: codex:)", Status: "ok", Detail: strings.Join(sandbox, " ")})
		}
	}
	createName, createCmd := CreateTmux(proj, session, workDir, env,
		AgentCommand(LaunchCommandWith(proj, spec.AgentType, session, promptPath, extra))...)
	if !RunStep(&steps, fmt.Sprintf("%s, running %s (interactive, prompt as argument)", createName, spec.AgentType), createCmd) {
		response.Status = "error"
		response.Steps = steps
		return response
	}
	// Recorded before the launch is known to hold: the worker reads it at
	// its first call, and only this launch can name the base it cut.
	if spec.IssueSlug != "" {
		recordDispatch(proj, spec, base, &steps)
	}

	// In shared mode the window is already named after the session so it can be
	// matched back to the issue; renaming it to the slug would break that.
	if SharedSession(proj) == "" {
		windowName := session
		if spec.IssueSlug != "" {
			windowName = spec.IssueSlug
		}
		exec.Command("tmux", "rename-window", "-t", target, windowName).Run()
	}

	if headless {
		steps = append(steps, Step{Name: "Terminal: headless — attach manually", Status: "ok"})
		setHints()
	} else if lead && !showLead() || !lead && !ShowStep(proj, session, &steps) {
		response.Status = "error"
		response.Steps = steps
		return response
	}

	// Watched before the delivery check, which would wait out its timeout on
	// a pane that will never show the prompt, and looked at once more after.
	dead := diedAtLaunch(target, LaunchSettle)
	// Only Claude's pane rendering has been checked for the delivery marker.
	if !dead && spec.AgentType != "codex" {
		PromptDeliveryStep(&steps, target, prompt, spec.RepromptHint)
		dead = diedAtLaunch(target, 0)
	}
	if dead {
		reportDeadLaunch(proj, spec, target, &response, &steps)
		return response
	}
	if spec.Decisions != nil && proj != nil {
		tracker.WarnInjected(proj.IssueDir, *spec.Decisions, time.Now())
	}
	if spec.Briefing != nil {
		b := *spec.Briefing
		b.At = time.Now()
		b.Bytes, b.Tokens = len(prompt), tokens.Estimate(prompt)
		recordBriefing(proj, spec.IssueSlug, b, &steps)
	}

	response.Steps = steps
	return response
}

// reportDeadLaunch turns response into the error of a launch whose agent is
// gone: the step quotes the pane's last lines, which the session takes with
// it, and no session is left behind.
func reportDeadLaunch(proj *tracker.Project, spec Spec, target string, response *Response, steps *[]Step) {
	detail := spec.AgentType + " exited right after the launch; the session was removed"
	if tail := paneTail(target); tail != "" {
		detail += ". Its last lines: " + tail
	}
	if HasSession(target) {
		if err := KillTmux(proj, spec.Session); err != nil {
			detail = spec.AgentType + " exited right after the launch; removing the session failed: " + err.Error()
		}
	}
	*steps = append(*steps, Step{Name: "Agent running", Status: "error", Detail: detail})
	response.Status = "error"
	response.AttachCmd, response.SwitchCmd = "", ""
	response.Steps = *steps
}

// recordDispatch appends this launch to the issue's stats sidecar, with the
// base its work branch was cut from when this launch cut it. A failure is a
// warning step; it never fails the dispatch.
func recordDispatch(proj *tracker.Project, spec Spec, base tracker.Dispatch, steps *[]Step) {
	if proj == nil || proj.IssueDir == "" {
		return
	}
	issues, err := proj.LoadIssues()
	if err == nil {
		if issue := tracker.FindIssue(issues, spec.IssueSlug); issue != nil {
			err = tracker.RecordDispatch(issue.FilePath, tracker.Dispatch{At: time.Now(), Session: spec.Session, Agent: spec.AgentType, BaseBranch: base.BaseBranch, BaseSHA: base.BaseSHA})
		}
	}
	if err != nil {
		*steps = append(*steps, Step{Name: "Record dispatch time", Status: "warning", Detail: err.Error()})
	}
}

// recordBriefing appends b to issue slug's stats sidecar. A failure is a
// warning step; it never fails the launch.
func recordBriefing(proj *tracker.Project, slug string, b tracker.Briefing, steps *[]Step) {
	if proj == nil || proj.IssueDir == "" || slug == "" {
		return
	}
	issues, err := proj.LoadIssues()
	if err == nil {
		if issue := tracker.FindIssue(issues, slug); issue != nil {
			err = tracker.RecordBriefing(issue.FilePath, b)
		}
	}
	if err != nil {
		*steps = append(*steps, Step{Name: "Record briefing", Status: "warning", Detail: err.Error()})
	}
}

// Paste delivers text into the live session called session as one bracketed
// paste followed by Enter, and checks it arrived. The paste goes through a
// buffer named after the session (deleted after pasting) so concurrent pastes
// can't swap texts. textFile keeps a copy of what was sent and is what the
// buffer loads. Status is "no-session" when the session is not live.
func Paste(proj *tracker.Project, session, text, textFile, repromptHint string) Response {
	response := PasteSubmit(proj, session, text, textFile)
	if response.Status == "sent" {
		PromptDeliveryStep(&response.Steps, Target(proj, session), text, repromptHint)
	}
	return response
}

// PasteSubmit is Paste without the delivery check: it returns once the paste
// and Enter are in, so a caller can let go of its lock on the session before
// polling the pane (PromptDeliveryStep) for up to PromptDeliveryTimeout.
func PasteSubmit(proj *tracker.Project, session, text, textFile string) Response {
	target := Target(proj, session)
	response := Response{Status: "sent", Prompt: text, Session: tracker.AgentDisplayName(proj, session)}
	if !HasSession(target) {
		response.Status = "no-session"
		return response
	}

	var steps []Step
	textPath := textFile
	err := os.MkdirAll(filepath.Dir(textFile), 0755)
	telemetry.IgnoreAgentLogs(textFile)
	if err == nil {
		err = os.WriteFile(textFile, []byte(text), 0644)
	}
	if err != nil {
		response.Status = "error"
		response.Steps = []Step{{Name: "Write prompt file", Status: "error", Detail: err.Error()}}
		return response
	}
	if !pasteSubmit(&steps, target, "issue-viewer-"+session, textPath, response.Session) {
		response.Status = "error"
	}
	response.Steps = steps
	return response
}

// pasteSubmit loads textPath into the named buffer, pastes it into target as
// one bracketed paste (deleting the buffer) and presses Enter.
func pasteSubmit(steps *[]Step, target, buffer, textPath, name string) bool {
	ok := RunStep(steps, "Load prompt into tmux buffer "+buffer,
		exec.Command("tmux", "load-buffer", "-b", buffer, textPath)) &&
		RunStep(steps, "Paste prompt into "+name,
			exec.Command("tmux", "paste-buffer", "-d", "-p", "-b", buffer, "-t", target))
	if ok {
		time.Sleep(200 * time.Millisecond)
		ok = RunStep(steps, "Submit prompt", exec.Command("tmux", "send-keys", "-t", target, "Enter"))
	}
	return ok
}

// Nudge delivers text into the agent at the tmux target the way Paste does
// (bracketed paste, Enter, then PromptDeliveryStep's check, pressing Enter
// again while it sits in the input box) and says why when it did not land:
// the viewer's approval and send-back lines to a worker.
func Nudge(target, text string) error {
	if !HasSession(target) {
		return fmt.Errorf("session %s is not live", target)
	}
	tmp, err := os.CreateTemp("", "issue-nudge-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(text)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	buffer := "issue-viewer-" + strings.NewReplacer(":", "-", "=", "").Replace(target)
	var steps []Step
	if !pasteSubmit(&steps, target, buffer, tmp.Name(), target) {
		last := steps[len(steps)-1]
		return fmt.Errorf("%s: %s", last.Name, last.Detail)
	}
	if d := awaitDelivery(target, text); !d.blank && !d.delivered {
		return fmt.Errorf("not delivered to %s: %s", target, d.problem())
	}
	return nil
}

// FailureDetail is the last error step's detail after ": " ("" when no step
// has one), for a caller that reports a failed launch in one line.
func FailureDetail(r Response) string {
	for i := len(r.Steps) - 1; i >= 0; i-- {
		if s := r.Steps[i]; s.Status == "error" && s.Detail != "" {
			return ": " + s.Detail
		}
	}
	return ""
}
