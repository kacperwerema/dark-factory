package agentrun

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Worker setup (docs/agent-dispatch.md "Worker setup"): an issue's Claude
// Code worker starts with only what workflow.yaml workers: names plus its
// department's connectors, instead of every MCP server, plugin and skill of
// the owner's Claude Code. Leads have their own lean setup (lead_setup.go);
// codex and workers: setup: full launch as before. Nothing here writes the owner's own Claude Code files: the
// session gets its own mcp.json and settings.json in its log dir.

// claudeAIConnectorsOff is the session env that keeps claude.ai connectors
// (Claude Docs and the like) out of a lean worker: --strict-mcp-config does
// not cover them (code.claude.com/docs/en/mcp, "Disable claude.ai
// connectors").
const claudeAIConnectorsOff = "ENABLE_CLAUDEAI_MCP_SERVERS=false"

// LeanWorker reports whether spec launches an issue's Claude Code worker
// with the lean setup: claude, bound to an issue, not a lead, and its
// workflow not workers: setup: full.
func LeanWorker(spec Spec) bool {
	return spec.AgentType == "claude" && spec.IssueSlug != "" && spec.LeadScope == "" && !spec.Workflow.WorkerSetupFull()
}

// WorkerSetup is a lean worker's launch: its flags, its session env and
// the step reporting what it loads.
type WorkerSetup struct {
	Flags []string
	Env   []string
}

// WorkerSetupFlags builds a lean worker's launch: --strict-mcp-config with
// --mcp-config=<sessionLogDir>/mcp.json holding the department's connectors
// and the workers: mcp: servers its issue matches (copied from the owner's
// Claude Code config), --settings=<sessionLogDir>/settings.json turning off
// every plugin and personal or project skill workers: does not keep, the
// connectors' --disallowedTools, and the env turning claude.ai connectors
// off. env is the session's environment, passed on to the connectors as
// ConnectorFlags does. A server workers: names that the owner's config
// does not define is a warning step; the launch goes on without it.
func WorkerSetupFlags(proj *tracker.Project, spec Spec, workDir, sessionLogDir string, env []string, steps *[]Step) WorkerSetup {
	servers := map[string]any{}
	var loaded []string

	dept, conns := connectorServers(proj, spec, env)
	for _, s := range conns {
		servers[s.Name] = s
	}
	if len(conns) > 0 {
		loaded = append(loaded, fmt.Sprintf("%s connectors: %s", dept, strings.Join(connectorNames(conns), ", ")))
	}

	root := ProjectWorkDir(proj)
	issue := specIssue(proj, spec)
	var missing []string
	if issue != nil {
		owner := connectors.OwnerMCPServers(root, workDir)
		for _, name := range spec.Workflow.WorkerMCPFor(issue, issueTypeOf(spec.Workflow, issue)) {
			def, ok := owner[name]
			if !ok {
				missing = append(missing, name)
				continue
			}
			if _, taken := servers[name]; !taken {
				servers[name] = def
				loaded = append(loaded, name)
			}
		}
	}

	mcpPath := filepath.Join(sessionLogDir, "mcp.json")
	b, _ := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	if err := os.WriteFile(mcpPath, append(b, '\n'), 0600); err != nil {
		// No file, no strict config: a worker with too much beats one
		// without its connectors.
		*steps = append(*steps, Step{Name: "Write worker MCP config", Status: "warning", Detail: err.Error() + "; the worker starts with the owner's full setup"})
		return WorkerSetup{Flags: ConnectorFlags(proj, spec, sessionLogDir, env, steps), Env: ConnectorEnv(proj, spec)}
	}
	// Each flag that takes a list is written --flag=value, so none swallows
	// the prompt that follows (LaunchCommandWith).
	flags := []string{"--strict-mcp-config", "--mcp-config=" + mcpPath}
	dropped := spec.Workflow.WorkerToolsDropped()
	if off := workerDisallowed(connectors.ClaudeDisallowed(conns), dropped); off != "" {
		flags = append(flags, "--disallowedTools="+off)
	}

	kept := workersKeep{skills: spec.Workflow.WorkerSkillsFor(issue, issueTypeOfOrEmpty(spec.Workflow, issue))}
	if spec.Workflow != nil && spec.Workflow.Workers != nil {
		kept.plugins = spec.Workflow.Workers.Plugins
	}
	settings, off := WorkerSettings(root, workDir, filepath.Dir(sessionLogDir), kept)
	settings["hooks"] = ReadNudgeHooks()
	setPath := filepath.Join(sessionLogDir, "settings.json")
	sb, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(setPath, append(sb, '\n'), 0600); err != nil {
		*steps = append(*steps, Step{Name: "Write worker settings", Status: "warning", Detail: err.Error() + "; plugins and skills stay on"})
		off = workerOff{}
	} else {
		flags = append(flags, "--settings="+setPath)
		off.nudge = true
	}
	envOut := []string{claudeAIConnectorsOff}
	if slices.Contains(dropped, tracker.WorkerToolArtifact) {
		envOut = append(envOut, artifactToolOff)
	}

	what := "no MCP server"
	if len(loaded) > 0 {
		what = strings.Join(loaded, "; ")
	}
	*steps = append(*steps, Step{
		Name:   fmt.Sprintf("Worker setup (workers:): %s; %s", what, off.summary(dropped)),
		Status: "ok",
		Detail: mcpPath + " (workers: skills: and tools: keep one, setup: full in workflow.yaml the owner's whole setup; docs/agent-dispatch.md#worker-setup)",
	})
	if len(missing) > 0 {
		*steps = append(*steps, Step{
			Name:   "Worker MCP server not found: " + strings.Join(missing, ", "),
			Status: "warning",
			Detail: "workers: mcp: names it, but neither ~/.claude.json nor the project's .mcp.json defines it (claude mcp list); the worker starts without it",
		})
	}
	return WorkerSetup{Flags: flags, Env: append(envOut, connectors.ClaudeStartEnv(conns)...)}
}

// ReadNudgeHooks are a lean worker's settings hooks: after each tool call
// Claude Code runs issue-cli hook read-nudge (spend.ReadNudge), which adds
// a one-line nudge to the worker's context when its last two turns were
// each one read-only call (docs/agent-dispatch.md#worker-setup). --settings
// merges hooks with the owner's own, which still run.
func ReadNudgeHooks() map[string]any {
	return map[string]any{
		"PostToolUse": []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": ReadNudgeCommand, "timeout": 5}},
		}},
	}
}

// ReadNudgeCommand is the hook's command line.
const ReadNudgeCommand = "issue-cli hook read-nudge"

// artifactToolOff is the session env that keeps Claude Code's Artifact tool
// (its schema is the largest single part of a worker's first turn,
// measured 2026-11-16) out of a lean worker unless workers: tools: keeps it.
const artifactToolOff = "CLAUDE_CODE_DISABLE_ARTIFACT=1"

// workerDisallowed is a lean worker's one --disallowedTools list: the
// connectors' denied tools (connectorOff, comma-separated) and the built-in
// tools it starts without, but Artifact, which goes off by env
// (artifactToolOff). A bare tool name in --disallowedTools takes the tool
// out of the model's context, schema and all. "" when there is none.
func workerDisallowed(connectorOff string, dropped []string) string {
	var list []string
	if connectorOff != "" {
		list = append(list, connectorOff)
	}
	for _, t := range dropped {
		if t != tracker.WorkerToolArtifact {
			list = append(list, t)
		}
	}
	return strings.Join(list, ",")
}

// workersKeep is what WorkerSettings leaves on: plugins and skills by
// name, or every plugin (allPlugins: a lead's settings name none).
type workersKeep struct {
	plugins, skills []string
	allPlugins      bool
}

// workerOff counts what WorkerSettings turned off.
type workerOff struct {
	plugins, skills, listed int
	bundled                 bool
	// nudge: the settings carry the read-nudge hook (ReadNudgeHooks).
	nudge bool
}

func (o workerOff) summary(toolsOff []string) string {
	s := fmt.Sprintf("plugins off %d, %s, claude.ai connectors off", o.plugins, o.skillsAndTools(toolsOff))
	if o.nudge {
		s += ", read nudge on"
	}
	return s
}

// skillsAndTools is the skills and tools part of summary.
func (o workerOff) skillsAndTools(toolsOff []string) string {
	s := fmt.Sprintf("skills off %d", o.skills)
	if o.listed > 0 {
		s += fmt.Sprintf(" (%d listed by Claude Code)", o.listed)
	}
	if o.bundled {
		s += ", bundled skills off"
	}
	if len(toolsOff) > 0 {
		s += ", " + strings.Join(toolsOff, ", ") + " off"
	}
	return s
}

// WorkerSettings is the --settings document turning off every plugin the
// owner has on (user and project settings' enabledPlugins, plugins synced
// from claude.ai as <name>@synced), every personal or project skill, every
// skill Claude Code listed in this project's newest transcripts (bundled
// ones and anthropic-skills:<name> synced from claude.ai) or an earlier
// lean worker under logsRoot turned off, except those kept. The earlier
// workers' names keep the set from shrinking once every new transcript is
// a lean worker's, which lists only what it kept. Each is named: --settings merges enabledPlugins and skillOverrides
// key by key, so one left out keeps its file's value. disableBundledSkills
// covers the bundled ones no transcript listed yet, unless a kept skill is
// neither personal nor project (it may be bundled). Never nil.
func WorkerSettings(root, workDir, logsRoot string, keep workersKeep) (map[string]any, workerOff) {
	var off workerOff
	plugins := map[string]bool{}
	for _, id := range connectors.OwnerPlugins(root, workDir) {
		if !keep.allPlugins && !slices.Contains(keep.plugins, id) {
			plugins[id] = false
			off.plugins++
		}
	}
	skills := map[string]string{}
	onDisk := connectors.OwnerSkills(root, workDir)
	for _, name := range onDisk {
		if !slices.Contains(keep.skills, name) {
			skills[name] = "off"
			off.skills++
		}
	}
	for _, name := range append(connectors.ListedSkills(root), priorOffSkills(logsRoot)...) {
		if _, done := skills[name]; done || slices.Contains(keep.skills, name) || slices.Contains(onDisk, name) {
			continue
		}
		skills[name] = "off"
		off.skills++
		off.listed++
	}
	doc := map[string]any{}
	if len(plugins) > 0 {
		doc["enabledPlugins"] = plugins
	}
	if len(skills) > 0 {
		doc["skillOverrides"] = skills
	}
	if !slices.ContainsFunc(keep.skills, func(n string) bool { return !slices.Contains(onDisk, n) }) {
		doc["disableBundledSkills"] = true
		off.bundled = true
	}
	return doc, off
}

// priorOffSkills are the skill names the settings.json of the newest
// listedLogs session log dirs under logsRoot turned off.
func priorOffSkills(logsRoot string) []string {
	if logsRoot == "" {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(logsRoot, "*", "settings.json"))
	type file struct {
		path string
		mod  int64
	}
	var files []file
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			files = append(files, file{p, fi.ModTime().UnixNano()})
		}
	}
	slices.SortFunc(files, func(a, b file) int { return cmp.Compare(b.mod, a.mod) })
	if len(files) > listedLogs {
		files = files[:listedLogs]
	}
	var out []string
	for _, f := range files {
		var doc struct {
			SkillOverrides map[string]string `json:"skillOverrides"`
		}
		if b, err := os.ReadFile(f.path); err == nil && json.Unmarshal(b, &doc) == nil {
			for name, v := range doc.SkillOverrides {
				if v == "off" {
					out = append(out, name)
				}
			}
		}
	}
	return out
}

// listedLogs is how many earlier workers' settings priorOffSkills reads.
const listedLogs = 20

// issueTypeOfOrEmpty is issueTypeOf for an issue that may be nil.
func issueTypeOfOrEmpty(wf *tracker.WorkflowConfig, issue *tracker.Issue) string {
	if issue == nil {
		return ""
	}
	return issueTypeOf(wf, issue)
}

// specIssue is spec's issue, nil when it has none or it cannot be read.
func specIssue(proj *tracker.Project, spec Spec) *tracker.Issue {
	if proj == nil || spec.IssueSlug == "" || proj.IssueDir == "" {
		return nil
	}
	issues, err := proj.LoadIssues()
	if err != nil {
		return nil
	}
	return tracker.FindIssue(issues, spec.IssueSlug)
}

// issueTypeOf is issue's type: type:, else the workflow's default_type.
func issueTypeOf(wf *tracker.WorkflowConfig, issue *tracker.Issue) string {
	if issue.Type != "" {
		return issue.Type
	}
	if wf != nil {
		return wf.DefaultType
	}
	return ""
}
