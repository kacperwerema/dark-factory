package agentrun

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/connectors"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// SpecDepartment is the department an agent works for: a department lead's
// own, else its issue's; "" for Product, the CEO and agents with no issue.
func SpecDepartment(proj *tracker.Project, spec Spec) string {
	if proj == nil || len(proj.Departments) == 0 {
		return ""
	}
	if key, ok := strings.CutPrefix(spec.LeadScope, tracker.LeadScopeDepartmentPrefix); ok {
		if d := proj.DepartmentByKey(key); d != nil {
			return d.Name
		}
		return ""
	}
	if spec.LeadScope != "" || spec.IssueSlug == "" || proj.IssueDir == "" {
		return ""
	}
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return ""
	}
	if issue := tracker.FindIssue(issues, spec.IssueSlug); issue != nil {
		return proj.DepartmentOf(issue)
	}
	return ""
}

// ConnectorFlags are the launch flags giving a department's agent its MCP
// connectors (factory/department-connectors#D-4): claude's --mcp-config
// <sessionLogDir>/mcp.json, codex's -c mcp_servers.… overrides. Each server
// is issue-cli connector exec, so neither holds a secret. env is the
// session's environment; its ISSUE_VIEWER_* and ISSUE_CLI_LOG entries are
// passed on to the servers. nil for Product, the CEO, or no connectors.
func ConnectorFlags(proj *tracker.Project, spec Spec, sessionLogDir string, env []string, steps *[]Step) []string {
	dept := SpecDepartment(proj, spec)
	if dept == "" {
		return nil
	}
	pass := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "ISSUE_VIEWER_") || k == "ISSUE_CLI_LOG" {
			pass[k] = v
		}
	}
	servers := connectors.Servers(proj, dept, pass)
	if len(servers) == 0 {
		return nil
	}
	var names []string
	for _, s := range servers {
		names = append(names, s.Name)
	}
	switch spec.AgentType {
	case "claude":
		path := filepath.Join(sessionLogDir, "mcp.json")
		if err := os.WriteFile(path, connectors.MCPConfig(servers), 0600); err != nil {
			*steps = append(*steps, Step{Name: "Write MCP connectors", Status: "warning", Detail: err.Error()})
			return nil
		}
		*steps = append(*steps, Step{Name: fmt.Sprintf("%s connectors: %s", dept, strings.Join(names, ", ")), Status: "ok", Detail: path})
		return []string{"--mcp-config", path}
	case "codex":
		*steps = append(*steps, Step{Name: fmt.Sprintf("%s connectors: %s", dept, strings.Join(names, ", ")), Status: "ok"})
		return connectors.CodexFlags(servers)
	}
	return nil
}
