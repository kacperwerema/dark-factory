package connectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// The owner's Claude Code setup, read (never written) so a lean worker's
// launch can name what it keeps (internal/agentrun/worker_setup.go,
// docs/agent-dispatch.md "Worker setup") and lint can name a workers:
// server the owner does not have.

// claudeConfigDir is Claude Code's config folder: CLAUDE_CONFIG_DIR, else
// ~/.claude.
func claudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// claudeJSON is Claude Code's state file holding user and local MCP
// servers: $CLAUDE_CONFIG_DIR/.claude.json, else ~/.claude.json.
func claudeJSON() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

// OwnerMCPServers are the MCP server definitions the owner's Claude Code
// would load for a session in workDir, by name: the project's .mcp.json,
// then ~/.claude.json's entry for the project (root or workDir), then its
// user mcpServers, the later one winning as Claude Code's own scopes do
// (local over project over user). Definitions are copied as written.
func OwnerMCPServers(root, workDir string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	add := func(m map[string]json.RawMessage) {
		for k, v := range m {
			out[k] = v
		}
	}
	type serversFile struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	var state struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
		Projects   map[string]serversFile     `json:"projects"`
	}
	if b, err := os.ReadFile(claudeJSON()); err == nil {
		json.Unmarshal(b, &state)
	}
	add(state.MCPServers)
	for _, dir := range uniqueDirs(root, workDir) {
		var f serversFile
		if b, err := os.ReadFile(filepath.Join(dir, ".mcp.json")); err == nil && json.Unmarshal(b, &f) == nil {
			add(f.MCPServers)
		}
	}
	for _, dir := range uniqueDirs(root, workDir) {
		add(state.Projects[dir].MCPServers)
	}
	return out
}

// OwnerPlugins are the plugin ids the owner has on, sorted: enabledPlugins
// true in the user's settings.json and the project's .claude/settings.json
// and settings.local.json, and each plugin synced from claude.ai
// (plugins/synced/*/manifest.json) as <name>@synced, the id claude plugin
// list shows.
func OwnerPlugins(root, workDir string) []string {
	on := map[string]bool{}
	read := func(path string) {
		var s struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &s) == nil {
			for id, v := range s.EnabledPlugins {
				if v {
					on[id] = true
				} else {
					delete(on, id)
				}
			}
		}
	}
	read(filepath.Join(claudeConfigDir(), "settings.json"))
	for _, dir := range uniqueDirs(root, workDir) {
		read(filepath.Join(dir, ".claude", "settings.json"))
		read(filepath.Join(dir, ".claude", "settings.local.json"))
	}
	manifests, _ := filepath.Glob(filepath.Join(claudeConfigDir(), "plugins", "synced", "*", "manifest.json"))
	for _, m := range manifests {
		var f struct {
			Plugins []struct {
				Name string `json:"name"`
			} `json:"plugins"`
		}
		if b, err := os.ReadFile(m); err == nil && json.Unmarshal(b, &f) == nil {
			for _, p := range f.Plugins {
				if p.Name != "" {
					on[p.Name+"@synced"] = true
				}
			}
		}
	}
	ids := make([]string, 0, len(on))
	for id := range on {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// OwnerSkills are the personal (<config dir>/skills) and project
// (.claude/skills) skill names, sorted: each folder holding a SKILL.md.
func OwnerSkills(root, workDir string) []string {
	seen := map[string]bool{}
	dirs := []string{filepath.Join(claudeConfigDir(), "skills")}
	for _, d := range uniqueDirs(root, workDir) {
		dirs = append(dirs, filepath.Join(d, ".claude", "skills"))
	}
	for _, d := range dirs {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if _, err := os.Stat(filepath.Join(d, e.Name(), "SKILL.md")); err == nil {
				seen[e.Name()] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func uniqueDirs(dirs ...string) []string {
	var out []string
	for _, d := range dirs {
		if d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}
