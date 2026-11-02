package tracker

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DispatchPrompt is the briefing an agent dispatch would send for an issue,
// plus where the agent would run. It is the shape of both
// GET /p/<project>/issue/<slug>/dispatch-prompt?format=json and
// `issue-cli dispatch-prompt --json`.
type DispatchPrompt struct {
	Prompt   string `json:"prompt"`
	Slug     string `json:"slug"`
	Status   string `json:"status"`
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Session  string `json:"session"`
	// Lead is the session the agent reports to.
	Lead string `json:"lead,omitempty"`
	// DecisionRefs is every decision the briefing's block carries (shown
	// or folded), for the injected event a real dispatch writes.
	DecisionRefs []string `json:"decision_refs,omitempty"`
}

// BuildDispatchPrompt composes the prompt a dispatch would send right now for
// issue, without creating the worktree or tmux session. workDir is the
// project's working directory the worktree path is resolved against. live
// reports whether a lead session is running (nil: none is); the prompt says
// whether the project lead is live. The server and issue-cli both call this so their
// output is byte-identical.
func BuildDispatchPrompt(proj *Project, issue *Issue, wf *WorkflowConfig, workDir string, live func(name string) bool) DispatchPrompt {
	var issues []*Issue
	if proj != nil && proj.IssueDir != "" {
		issues, _ = LoadIssues(proj.IssueDir)
	}
	wt := IssueWorktree(workDir, issue, wf)
	route := LeadFor(proj, issue, issues, live)
	yoloOn := YOLOOn != nil && proj != nil && YOLOOn(workDir, proj.Slug)
	prompt := buildAgentPrompt(proj, issue, wf, wt.Path, wt.Branch, yoloOn)
	prompt += InjectProject(proj, DepartmentPromptSection(proj, issue))
	prompt += InjectProject(proj, LeadPromptSection(issue, route))
	decisions := IssueDecisionBlock(proj, issues, issue, issue.Status, wf, decisionsNow())
	prompt += InjectProject(proj, DecisionsPromptSection(decisions, issue.Slug))
	prompt += InjectProject(proj, MemoryPromptSection(proj, issue))
	prompt += ShellPromptSection(DetectShell())
	prompt += BuildPromptSection(proj, workDir, wf)
	prompt += InjectProject(proj, CIPromptSection(workDir, wf))
	return DispatchPrompt{
		Prompt:       prompt,
		Slug:         issue.Slug,
		Status:       issue.Status,
		Worktree:     wt.Path,
		Branch:       wt.Branch,
		Session:      AgentDisplayName(proj, AgentSessionName(issue.Slug)),
		Lead:         route.Lead,
		DecisionRefs: decisions.Refs(),
	}
}

// CIPromptSection is the "## Local CI" part of a worker's briefing: where
// the integration branch stands now, and that CI runs after the merge
// without the agent. Empty without a ci: block.
func CIPromptSection(workDir string, wf *WorkflowConfig) string {
	if CIStatusLine == nil {
		return ""
	}
	line := CIStatusLine(workDir, wf)
	if line == "" {
		return ""
	}
	return "\n\n## Local CI\n\n" + line + "\n" +
		"- When `make validate` or a test fails on your branch, compare with this line (`issue-cli ci status`): a failure main already has is not yours to fix.\n" +
		"- After you fast-forward main, local CI tests it by itself. Never wait for it: close your issue and stop. If it goes red on your merge, the lead may re-open your issue.\n"
}

// LeadPromptSection is the "## Your lead" part of a worker's briefing. It
// names the project lead the worker reports to and how that lead sees its
// work.
func LeadPromptSection(issue *Issue, route LeadRoute) string {
	state := "not running"
	if route.Live {
		state = "live"
	}
	var b strings.Builder
	b.WriteString("\n\n## Your lead\n\n")
	switch {
	case route.Covering && route.GroupLead != "" && route.Lead == route.GroupLead:
		fmt.Fprintf(&b, "You report to the %s lead %s (%s), covering for the %s lead %s, which is not running (the human starts it: issue-cli lead spawn --department %s). %s reports to the CEO %s.", route.Group, route.Lead, state, route.Department, route.DepartmentLead, DepartmentKey(route.Department), route.Group, route.CEO)
	case route.Covering:
		fmt.Fprintf(&b, "You report to the CEO %s (%s), covering for the %s lead %s, which is not running (the human starts it: issue-cli lead spawn --department %s).", route.Lead, state, route.Department, route.DepartmentLead, DepartmentKey(route.Department))
	case route.GroupLead != "":
		fmt.Fprintf(&b, "You report to the %s lead %s (%s), who reports to the %s lead %s, under the CEO %s.", route.Department, route.Lead, state, route.Group, route.GroupLead, route.CEO)
	case route.Department != "":
		fmt.Fprintf(&b, "You report to the %s lead %s (%s), who reports to the CEO %s.", route.Department, route.Lead, state, route.CEO)
	default:
		fmt.Fprintf(&b, "You report to the project lead %s (%s).", route.Lead, state)
	}
	if route.Epic != "" {
		fmt.Fprintf(&b, " Your issue is part of epic %s.", route.Epic)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "- Your lead follows your issue trail: ask with `issue-cli comment %s \"question: ...\"` and report with `progress:` comments. It relays questions and approvals to the human.\n", issue.Slug)
	fmt.Fprintf(&b, "- Don't message %s or any other agent (SendMessage) unless the human, in your chat, gives you permission or asks you to. Ask the human in your chat, or post a question: comment. Your address is %s, in case the lead messages you.\n", route.Lead, AgentSessionName(issue.Slug))
	b.WriteString("- To agree a format with a sibling issue's worker (one you build on, or one your brief names): `issue-cli ask <sibling> \"...\"`. It shows on that worker's next issue-cli call, and its `issue-cli answer` shows on yours; don't wait for it. " + NotAnApproval + "\n")
	b.WriteString("- Only the human decides; a worker never ticks a gate.\n")
	b.WriteString("- `issue-cli lead whoami` shows your lead and whether it is live right now.\n")
	return b.String()
}

// AgentSessionName is the tmux session (or shared-session window) name a
// dispatched agent for slug runs in.
func AgentSessionName(slug string) string {
	// Only [a-z0-9_-] survive: the name is a tmux target and is typed into
	// shells, so ":", "*", "$" or quotes from a crafted system folder must
	// not reach either (docs/API/security.md).
	return "agent-" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return '-'
	}, slug)
}

// AgentDisplayName is the human-facing name of the agent's tmux location,
// e.g. "agent-foo" or "work:agent-foo" when the project uses a shared session.
func AgentDisplayName(proj *Project, name string) string {
	if proj != nil {
		if shared := strings.TrimSpace(proj.TmuxSession); shared != "" {
			return shared + ":" + name
		}
	}
	return name
}

// ResolveWorktree returns the worktree path, branch name, and whether the
// dispatch should create one. Pure: no side effects, safe to call before
// deciding whether to actually run git.
func ResolveWorktree(workdir, slug string, wf *WorkflowConfig) (path, branch string, enabled bool) {
	if wf == nil || !wf.WorktreeEnabled() || strings.TrimSpace(slug) == "" || strings.TrimSpace(workdir) == "" {
		return "", "", false
	}
	return filepath.Join(workdir, ".worktrees", slug), "work/" + slug, true
}

// BuildAgentPrompt fills AgentDispatchPromptTemplate's placeholders for issue, injects
// --project into every issue-cli command, and appends the worktree section
// when the agent runs in one.
func BuildAgentPrompt(proj *Project, issue *Issue, wf *WorkflowConfig, worktreePath, worktreeBranch string) string {
	return buildAgentPrompt(proj, issue, wf, worktreePath, worktreeBranch, false)
}

// buildAgentPrompt is BuildAgentPrompt; with yoloOn the stop lines, the
// status guidance and the reminder carry the YOLO rule instead.
func buildAgentPrompt(proj *Project, issue *Issue, wf *WorkflowConfig, worktreePath, worktreeBranch string, yoloOn bool) string {
	currentPrompt := "Use issue-cli to inspect the current workflow requirements for this status before making changes."
	if wf != nil {
		if prompt := wf.StatusPrompt(issue.Status); strings.TrimSpace(prompt) != "" {
			currentPrompt = prompt
		}
	}

	statusReminder := ""
	switch issue.Status {
	case "in design":
		statusReminder = "When the design is complete, tell the human in chat which approval it needs in the issue viewer (see the status guidance), then stop and end your turn. Continue when the human messages you that it is approved."
	case "backlog":
		statusReminder = "Tell the human in chat that this needs `in progress` approval in the issue viewer, then stop and end your turn. Continue when the human messages you that it is approved."
	}
	if yoloOn {
		currentPrompt, _ = YOLOGuidance(currentPrompt)
		currentPrompt = strings.TrimSpace(currentPrompt + "\n" + YOLORule(issue.Slug))
		if statusReminder != "" {
			statusReminder = "YOLO passes the approval this status waits for: when the rest is done, run the transition (from backlog, issue-cli start); don't stop."
		}
	}

	// Typed issues get a Type line under Status in the metadata block, so the
	// agent knows its path before reading any workflow output.
	statusField := issue.Status
	if wf != nil && wf.ActiveType != "" {
		statusField += fmt.Sprintf("\n  Type: %s (%s)", wf.ActiveType, wf.PathLine())
	}

	prompt := renderPromptTemplate(AgentDispatchPromptTemplate, agentPromptFields(issue, currentPrompt, statusReminder, statusField, yoloOn))
	prompt += "\n\n" + RedRoundsPromptSection(issue.Slug, wf)

	// Inject --project so dispatched bots in a multi-project projects.yaml
	// setup don't silently run against the default project. The web app
	// already knows the project; the bot would otherwise have to discover or
	// guess it. Replace bare `issue-cli ` (trailing space) so we don't break
	// adjacent tokens, and skip when the project has no slug (bootstrap mode).
	prompt = InjectProject(proj, prompt)

	if worktreePath != "" {
		prompt += fmt.Sprintf(`

## Worktree

You are already in an isolated git worktree at %s on branch %s.
- Do your code work and make commits here on %s. Do not switch this checkout to another branch.
- Issue management is NOT code work. The issues/ tree is excluded from this worktree on purpose — it lives on the primary checkout (master). Run every issue-cli command exactly as written: --project already points it at the primary checkout, so it manages the issue on master regardless of your worktree branch. Do not cd into the primary checkout, and do not try to create or edit issue files inside this worktree.
- Leave the worktree in place after the issue ships: do not remove it, and nobody has to clean it up.
- A sandbox viewer you start by hand gets its own state dir: XDG_STATE_HOME=<sandbox>/state issue-viewer -config <sandbox>/projects.yaml. The shared viewer.json (~/.local/state/ai-workflow-engine/) belongs to the owner's viewer: every issue-cli on the machine follows it. Never pass -record-viewer.
- The worker guard refuses issue-cli dispatch, department add and lead spawn from your shell, even with --config on a sandbox projects.yaml: it knows you by your environment, tmux name and worktree, not by the config. Walk those steps in a Go test with a clean env and a private tmux server, as TestNewcomerWalkthrough (newcomer_test.go) does. Never strip the guard's environment to get past it.
`, worktreePath, worktreeBranch, worktreeBranch)
	}
	return prompt
}

// RedRoundsPromptSection is the briefing's rule for the red-round cap
// (internal/redrounds): how many rounds a check may stay red, and what to
// do when one can't be fixed.
func RedRoundsPromptSection(slug string, wf *WorkflowConfig) string {
	n := wf.RedRoundCap()
	return fmt.Sprintf(`## Red checks

A check that is still red after %d fix rounds (a gate's command, the proof check, a local CI command; one round per commit) becomes a question for the owner: transition posts it and refuses without re-running the check until the owner resolves it. Stop working on that check then; don't loop on it.
  issue-cli rounds %s                                         # each red check: red N of %d
  issue-cli rounds %s --cannot-fix "<check>" --why "..."      # can't fix it: ask now, recommendation first`, n, slug, n, slug)
}

// agentPromptFields is the value of each {{name}} placeholder in
// AgentDispatchPromptTemplate, with the stop lines for yoloOn.
func agentPromptFields(issue *Issue, statusGuidance, statusReminder, statusField string, yoloOn bool) map[string]string {
	fields := map[string]string{
		"slug":            issue.Slug,
		"system":          issue.System,
		"status_guidance": statusGuidance,
		"status_reminder": statusReminder,
		"title":           issue.Title,
		"status":          statusField,
		"priority":        issue.Priority,
		"body":            issue.BodyRaw,
	}
	for name, value := range dispatchGateLines(yoloOn) {
		fields[name] = value
	}
	return fields
}

// renderPromptTemplate replaces each {{name}} in tmpl with fields[name]. The
// replacement is one pass over tmpl, so a value that itself contains {{...}}
// (an issue body, a status prompt) is left as written.
func renderPromptTemplate(tmpl string, fields map[string]string) string {
	pairs := make([]string, 0, 2*len(fields))
	for name, value := range fields {
		pairs = append(pairs, "{{"+name+"}}", value)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// InjectProject adds --project <slug> to every issue-cli command in text, so
// agents in a multi-project projects.yaml setup don't silently run against
// the default project. Bare `issue-cli ` (trailing space) is replaced so
// adjacent tokens aren't broken; a project without a slug (bootstrap mode) is
// left alone.
func InjectProject(proj *Project, text string) string {
	if proj == nil || proj.Slug == "" {
		return text
	}
	return strings.ReplaceAll(text, "issue-cli ", "issue-cli --project "+proj.Slug+" ")
}

// PromptDeliveryMarker is the text whose appearance in the agent's pane shows
// the prompt reached it: the start of the prompt's last non-empty line. The
// last line is used because Claude redraws the screen and a long prompt's
// first lines scroll out of capture-pane's reach, while the tail stays visible
// right after the prompt renders. Truncated so terminal wrapping can't split
// it. Returns "" for a blank prompt.
func PromptDeliveryMarker(prompt string) string {
	lines := strings.Split(prompt, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > 40 {
			line = strings.TrimSpace(string(r[:40]))
		}
		return line
	}
	return ""
}
