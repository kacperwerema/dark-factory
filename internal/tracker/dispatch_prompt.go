package tracker

import (
	"fmt"
	"path/filepath"
	"regexp"
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
	// MemoryIDs are the pinned memories the briefing's ## Memory shows,
	// for the briefing record a real dispatch writes.
	MemoryIDs []string `json:"memory_ids,omitempty"`
	// Stage is the stage briefing's size against its budget; nil unless
	// the issue's status is session: fresh (internal/briefing/stage.go).
	Stage *StageBriefing `json:"stage,omitempty"`
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
		"- When `make validate` or a test fails on your branch, compare with this line (`issue-cli ci status`): a failure main already has is not yours to fix. If it has no issue, file it (`issue-cli create`); if it has one, name it in your `tests:` comment.\n" +
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
	b.WriteString("- " + WorkerMessageRule(route.Lead, AgentSessionName(issue.Slug)) + "\n")
	b.WriteString("- To agree a format with a sibling issue's worker (one you build on, or one your brief names): `issue-cli ask <sibling> \"...\"`. It shows on that worker's next issue-cli call, and its `issue-cli answer` shows on yours; don't wait for it. " + NotAnApproval + "\n")
	b.WriteString("- Only the human decides; a worker never ticks a gate.\n")
	b.WriteString("- `issue-cli lead whoami` shows your lead and whether it is live right now.\n")
	return b.String()
}

// WorkerMessageRule is the brief's line on messaging: a worker doesn't start
// a conversation with an agent on its own. docs/agent-dispatch.md quotes it
// (TestDocsQuoteTheCode).
func WorkerMessageRule(lead, address string) string {
	return "Don't message " + lead + " or any other agent (SendMessage) unless the human, in your chat, gives you permission or asks you to. Ask the human in your chat, or post a question: comment. Your address is " + address + ", in case the lead messages you."
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
	return BuildAgentPromptYOLO(proj, issue, wf, worktreePath, worktreeBranch, false)
}

// BuildAgentPromptYOLO is BuildAgentPrompt; with yoloOn the stop lines, the
// status guidance and the reminder carry the YOLO rule instead.
func BuildAgentPromptYOLO(proj *Project, issue *Issue, wf *WorkflowConfig, worktreePath, worktreeBranch string, yoloOn bool) string {
	currentPrompt := "Use issue-cli to inspect the current workflow requirements for this status before making changes."
	if wf != nil {
		if prompt := wf.StatusPrompt(issue.Status); strings.TrimSpace(prompt) != "" {
			currentPrompt = prompt
		}
	}

	statusReminder := ""
	switch {
	case wf.HasRole(issue.Status, StatusRoleDesign):
		statusReminder = "When the design is complete, tell the human in chat which approval it needs in the issue viewer (see the status guidance), then stop and end your turn. Continue when the human messages you that it is approved."
	case wf.HasRole(issue.Status, StatusRoleQueue):
		statusReminder = "Tell the human in chat that this needs `in progress` approval in the issue viewer, then stop and end your turn. Continue when the human messages you that it is approved."
	}
	if yoloOn {
		currentPrompt, _ = YOLOGuidance(currentPrompt)
		currentPrompt = strings.TrimSpace(currentPrompt + "\n" + YOLORule(wf, issue.Slug))
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

	fields := agentPromptFields(issue, wf, currentPrompt, statusReminder, statusField, yoloOn)
	if worktreePath != "" {
		// The worktree leaves the issues tree out: a path under it in the
		// brief only resolves from the main checkout.
		fields["body"] = AbsIssuePaths(proj, fields["body"])
	}

	// Inject --project so dispatched bots in a multi-project projects.yaml
	// setup don't silently run against the default project. The web app
	// already knows the project; the bot would otherwise have to discover or
	// guess it. The template and the fields go in one by one: the title is
	// the issue's words, left as written, and the render stays one pass.
	for name, value := range fields {
		switch name {
		case "title":
		case "body":
			fields[name] = injectProjectBody(proj, value)
		default:
			fields[name] = InjectProject(proj, value)
		}
	}
	prompt := renderPromptTemplate(InjectProject(proj, AgentDispatchPromptTemplate), fields)
	prompt += "\n\n" + InjectProject(proj, RedRoundsPromptSection(issue.Slug, wf))

	prompt += WorktreePromptSection(proj, issue, worktreePath, worktreeBranch)
	return prompt
}

// WorktreePromptSection is the "## Worktree" part of a worker's briefing:
// where it works and what it leaves alone; "" when it runs in no worktree.
func WorktreePromptSection(proj *Project, issue *Issue, worktreePath, worktreeBranch string) string {
	if worktreePath == "" {
		return ""
	}
	return fmt.Sprintf(`

## Worktree

You are already in an isolated git worktree at %s on branch %s.
- Do your code work and make commits here on %s. Do not switch this checkout to another branch.
- Issue management is NOT code work. The issues/ tree is excluded from this worktree on purpose — it lives on the primary checkout (master). Run every issue-cli command exactly as written: --project already points it at the primary checkout, so it manages the issue on master regardless of your worktree branch. Do not cd into the primary checkout, and do not try to create or edit issue files inside this worktree.
%s- Leave the worktree in place after the issue ships: do not remove it, and nobody has to clean it up.
- %s
- %s
`, worktreePath, worktreeBranch, worktreeBranch, WorktreePathLines(proj, issue), SandboxViewerRule, WorkerGuardRule)
}

// The Worktree section's two rules for a worker trying things out; the docs
// quote them (TestDocsQuoteTheCode).
const (
	SandboxViewerRule = "A sandbox viewer you start by hand gets its own state dir: XDG_STATE_HOME=<sandbox>/state issue-viewer -config <sandbox>/projects.yaml. The shared viewer.json (~/.local/state/ai-workflow-engine/) belongs to the owner's viewer: every issue-cli on the machine follows it. Never pass -record-viewer."
	WorkerGuardRule   = "The worker guard refuses issue-cli dispatch, department add and lead spawn from your shell, even with --config on a sandbox projects.yaml: it knows you by your environment, tmux name and worktree, not by the config. Walk those steps in a Go test with a clean env and a private tmux server, as TestNewcomerWalkthrough (newcomer_test.go) does. Never strip the guard's environment to get past it."
)

// WorktreePathLines are the Worktree section's lines naming, as absolute
// paths, the folders a worktree leaves out: the issues tree (read only,
// through issue-cli) and, for a department's issue, its departments/<key>/
// folder, where department files such as brand.md and icp.md are written
// (file_exists gates check them under the project root, not the worktree).
// "" without a project.
func WorktreePathLines(proj *Project, issue *Issue) string {
	if proj == nil || proj.IssueDir == "" {
		return ""
	}
	issuesDir := proj.IssueDir
	if abs, err := filepath.Abs(issuesDir); err == nil {
		issuesDir = abs
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- The issues tree is at %s (read it for files a brief names, such as designs; change issues only through issue-cli).\n", issuesDir)
	if issue != nil {
		if dept := proj.DepartmentOf(issue); dept != "" {
			fmt.Fprintf(&b, "- Your department's files are in %s (role.md, brand.md, icp.md): it is not in this worktree, so write department files there, where the gates check them.\n", filepath.Join(proj.Root(), "departments", DepartmentKey(dept))+string(filepath.Separator))
		}
	}
	return b.String()
}

// issuePathRe finds a relative path under an issues folder named name: the
// folder at a word start (not after a letter, digit, -, _, . or /), an
// optional ./, then at least one character of the path.
func issuePathRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^\w./-])(?:\./)?` + regexp.QuoteMeta(name) + `/([^\s/])`)
}

// AbsIssuePaths rewrites each relative path under the project's issues
// folder in text (issues/Factory/designs/x.md) to its absolute path, for a
// worker whose worktree leaves that folder out. Text without a project is
// returned as is.
func AbsIssuePaths(proj *Project, text string) string {
	if proj == nil || proj.IssueDir == "" {
		return text
	}
	abs, err := filepath.Abs(proj.IssueDir)
	if err != nil {
		return text
	}
	name := filepath.Base(abs)
	return issuePathRe(name).ReplaceAllString(text, "${1}"+strings.ReplaceAll(abs, "$", "$$")+"/${2}")
}

// RedRoundsPromptSection is the briefing's rule for the red-round cap
// (internal/redrounds): how many rounds a check may stay red, and what to
// do when one can't be fixed.
func RedRoundsPromptSection(slug string, wf *WorkflowConfig) string {
	n := wf.RedRoundCap()
	return fmt.Sprintf(`## Red checks

A check that is still red after %d fix rounds (`+RedRoundsWhatCounts+`) becomes a question for the owner: transition posts it and refuses without re-running the check until the owner resolves it. Stop working on that check then; don't loop on it.
  issue-cli rounds %s                                         # each red check: red N of %d
  issue-cli rounds %s --cannot-fix "<check>" --why "..."      # can't fix it: ask now, recommendation first`, n, slug, n, slug)
}

// agentPromptFields is the value of each {{name}} placeholder in
// AgentDispatchPromptTemplate, with the stop lines for yoloOn.
func agentPromptFields(issue *Issue, wf *WorkflowConfig, statusGuidance, statusReminder, statusField string, yoloOn bool) map[string]string {
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
	// The stop lines name the issue as <slug>; the brief is for one issue,
	// so they carry its slug.
	for name, value := range DispatchGateLines(wf, yoloOn) {
		fields[name] = strings.ReplaceAll(value, "<slug>", issue.Slug)
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
// the default project. Bare `issue-cli ` (trailing space) is matched so
// adjacent tokens aren't broken; a project without a slug (bootstrap mode) is
// left alone. A command that names its project already stays as it is, and
// so does one inside a quoted string (quotedAt): that is text for a program
// to parse or the words of whoever wrote it, not a command to run.
func InjectProject(proj *Project, text string) string {
	if proj == nil || proj.Slug == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = injectLine(proj.Slug, line)
	}
	return strings.Join(lines, "\n")
}

// injectProjectBody is InjectProject for an issue body: a box a side-effect
// wrote still tells the worker what to run, but a blockquote or a fenced
// block is quoted as a whole and stays as written.
func injectProjectBody(proj *Project, body string) string {
	if proj == nil || proj.Slug == "" {
		return body
	}
	lines := strings.Split(body, "\n")
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		switch mark := fenceMark(trimmed); {
		case fence == "" && mark != "":
			fence = mark
		case fence != "":
			if mark == fence {
				fence = ""
			}
		case !strings.HasPrefix(trimmed, ">"):
			lines[i] = injectLine(proj.Slug, line)
		}
	}
	return strings.Join(lines, "\n")
}

// fenceMark is the fence a line opens or closes, "" when it is neither.
func fenceMark(trimmed string) string {
	for _, mark := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, mark) {
			return mark
		}
	}
	return ""
}

// injectLine adds --project to the commands of one line, skipping quoted
// strings. A quote never runs over its line.
func injectLine(slug, line string) string {
	const cli = "issue-cli "
	if !strings.Contains(line, cli) {
		return line
	}
	var b strings.Builder
	inCmd := false
	for i := 0; i < len(line); {
		if end := quotedAt(line, i, inCmd); end > i {
			b.WriteString(line[i:end])
			i = end
			continue
		}
		if !strings.HasPrefix(line[i:], cli) {
			b.WriteByte(line[i])
			i++
			continue
		}
		i += len(cli)
		inCmd = true
		b.WriteString(cli)
		if !strings.HasPrefix(line[i:], "--project") {
			b.WriteString("--project " + slug + " ")
		}
	}
	return b.String()
}

// quotedAt returns the index just past the quoted string opening at line[i],
// or i when none does. A double quote opens one anywhere it has a closing
// quote. A single quote opens one only at the start of a word that is an
// argument: a flag's value, or any word after an issue-cli command on the
// line (inCmd), the way shell.Quote writes a value. Elsewhere it is an
// apostrophe, or prose naming a command for the reader to run.
func quotedAt(line string, i int, inCmd bool) int {
	switch q := line[i]; {
	case q == '"':
		for j := i + 1; j < len(line); j++ {
			switch line[j] {
			case '\\':
				j++
			case q:
				return j + 1
			}
		}
		return i
	case q == '\'' && (flagValueAt(line, i) || inCmd && wordStartAt(line, i)):
		return singleQuotedEnd(line, i)
	}
	return i
}

// singleQuotedEnd returns the index just past the single-quoted word opening
// at line[i], with shell.Quote's '\” escape inside it, or i when it does not
// close on the line. Nothing escapes inside single quotes.
func singleQuotedEnd(line string, i int) int {
	end := i
	for {
		j := strings.IndexByte(line[end+1:], '\'')
		if j < 0 {
			return i
		}
		end += 1 + j + 1
		if !strings.HasPrefix(line[end:], `\''`) {
			return end
		}
		end += 2
	}
}

// wordStartAt says whether line[i] starts a word: the line's start, or after a
// space or a tab.
func wordStartAt(line string, i int) bool {
	return i == 0 || line[i-1] == ' ' || line[i-1] == '\t'
}

// flagValueAt says whether line[i] starts a flag's value: `--flag <i>` or
// `--flag=<i>`.
func flagValueAt(line string, i int) bool {
	if i == 0 || (line[i-1] != ' ' && line[i-1] != '=') {
		return false
	}
	words := strings.Fields(line[:i])
	return len(words) > 0 && strings.HasPrefix(words[len(words)-1], "--")
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
