package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// sessionLive reports whether proj's agent or lead session called name is
// running. A var so tests never consult the real tmux server.
var sessionLive = func(proj *tracker.Project) func(string) bool {
	return func(name string) bool { return agentrun.IsLive(proj, name) }
}

// liveSessionNames lists the bare names (agent-x, lead-y) of proj's live tmux
// sessions and shared-session windows; other projects' agents on the same
// tmux server are left out. A var for tests.
var liveSessionNames = func(proj *tracker.Project) map[string]bool {
	names := map[string]bool{}
	for _, s := range agentrun.ForProject(proj, projectRoot(proj), agentrun.ListSessions()) {
		names[agentrun.BareName(s.Name)] = true
	}
	return names
}

// startAgent launches an agent through the shared launcher. A var so tests
// can capture the spec instead of starting tmux.
var startAgent = agentrun.Start

// pasteIntoSession delivers text into a live session. A var for tests.
var pasteIntoSession = agentrun.Paste

// sessionAgent names the agent a live session runs (claude, codex, or ""). A
// var for tests.
var sessionAgent = agentrun.SessionAgent

// currentTmuxNames returns the tmux session and window this process runs in,
// or "", "" outside tmux. A var for tests.
var currentTmuxNames = func() (session, window string) {
	if os.Getenv("TMUX") == "" {
		return "", ""
	}
	out, err := exec.Command("tmux", "display-message", "-p", "#S\t#W").Output()
	if err != nil {
		return "", ""
	}
	session, window, _ = strings.Cut(strings.TrimSpace(string(out)), "\t")
	return session, window
}

// workingDir is os.Getwd, a var so tests don't depend on where they run.
var workingDir = os.Getwd

// Identity is who the current session is in the lead hierarchy.
type Identity struct {
	Role tracker.LeadRole
	// Name is the session name (lead-x, agent-y); "" when unmanaged or
	// unknown.
	Name string
	// Scope is "project" for the CEO, department:<key> for a department
	// lead.
	Scope string
	// Department is a department lead's department ("" otherwise).
	Department string
	// Slug is the worker's issue ("" when unknown).
	Slug string
	// Signal says how the role was detected.
	Signal string
}

// whoAmI detects the current session's role, in order: the lead scope the
// spawned project lead exports, the issue slug a dispatched worker exports,
// the tmux session or window name, and a cwd under .worktrees/. Anything else
// is unmanaged — a human, or a lead started by hand. Only the project lead's
// scope and name count as a lead.
func whoAmI(proj *tracker.Project, issues []*tracker.Issue) Identity {
	scope := strings.TrimSpace(os.Getenv("ISSUE_VIEWER_LEAD_SCOPE"))
	if scope == tracker.LeadScopeProject {
		return projectLeadIdentity(proj, "ISSUE_VIEWER_LEAD_SCOPE")
	}
	if key, ok := strings.CutPrefix(scope, tracker.LeadScopeDepartmentPrefix); ok {
		return departmentLeadIdentity(proj, key, "ISSUE_VIEWER_LEAD_SCOPE")
	}
	if slug := strings.TrimSpace(os.Getenv("ISSUE_VIEWER_ISSUE_SLUG")); slug != "" {
		return Identity{Role: tracker.RoleWorker, Name: tracker.AgentSessionName(slug), Slug: slug, Signal: "ISSUE_VIEWER_ISSUE_SLUG"}
	}
	session, window := currentTmuxNames()
	for _, name := range []string{session, window} {
		switch {
		case name != "" && name == tracker.LeadSessionName(proj):
			return projectLeadIdentity(proj, "tmux name "+name)
		case name != "" && proj.DepartmentOfLead(name) != nil:
			return departmentLeadIdentity(proj, proj.DepartmentOfLead(name).Name, "tmux name "+name)
		case strings.HasPrefix(name, "agent-"):
			id := Identity{Role: tracker.RoleWorker, Name: name, Signal: "tmux name " + name}
			for _, issue := range issues {
				if tracker.AgentSessionName(issue.Slug) == name {
					id.Slug = issue.Slug
				}
			}
			return id
		}
	}
	if wd, err := workingDir(); err == nil && strings.Contains(filepath.ToSlash(wd), "/.worktrees/") {
		return Identity{Role: tracker.RoleWorker, Signal: "working directory under .worktrees/"}
	}
	return Identity{Role: tracker.RoleUnmanaged, Signal: "no lead or worker signal"}
}

// commentAuthor is who signs a comment this session writes on issue: the
// session's name, else the issue's assignee when a worker writes it, else ""
// (a shell with no lead or worker signal).
func commentAuthor(proj *tracker.Project, issue *tracker.Issue) string {
	me := whoAmI(proj, nil)
	if me.Name != "" {
		return me.Name
	}
	if me.Role == tracker.RoleWorker && issue != nil {
		return strings.TrimSpace(issue.Assignee)
	}
	return ""
}

func projectLeadIdentity(proj *tracker.Project, signal string) Identity {
	return Identity{Role: tracker.RoleProjectLead, Name: tracker.LeadSessionName(proj), Scope: tracker.LeadScopeProject, Signal: signal}
}

// departmentLeadIdentity is the lead of the department named or keyed key.
// A department projects.yaml no longer has keeps the role and the key, and
// its scope checks refuse every issue.
func departmentLeadIdentity(proj *tracker.Project, key, signal string) Identity {
	name := key
	if d := proj.DepartmentByKey(key); d != nil {
		name = d.Name
	}
	return Identity{Role: tracker.RoleDepartmentLead, Name: tracker.DepartmentLeadName(proj, name), Scope: tracker.LeadScopeDepartment(name), Department: name, Signal: signal}
}

// outOfScope is the refusal a department lead gets for an issue outside its
// department, or outside its group for a group's lead ("" when the issue is
// in reach, or me is not a department lead). verb names what was refused.
func outOfScope(proj *tracker.Project, me Identity, issue *tracker.Issue, verb string) string {
	if me.Role != tracker.RoleDepartmentLead || issue == nil {
		return ""
	}
	owner := proj.DepartmentOf(issue)
	if proj.InScope(me.Department, owner) {
		return ""
	}
	ask := "the CEO " + tracker.LeadSessionName(proj)
	if g := proj.GroupOf(owner); g != nil && !strings.EqualFold(g.Name, me.Department) {
		ask = "the " + g.Name + " lead " + tracker.DepartmentLeadName(proj, g.Name) + " or " + ask
	}
	if owner == "" {
		owner = tracker.ProductName
	}
	if proj.IsGroup(me.Department) {
		return fmt.Sprintf("%s is %s's, not in %s: a group lead %s only its group's and its departments' issues. Ask %s", issue.Slug, owner, me.Department, verb, ask)
	}
	return fmt.Sprintf("%s is %s's, not %s's: a department lead %s only its own department's issues. Ask %s", issue.Slug, owner, me.Department, verb, ask)
}

// sender is how a message from this identity is signed.
func (id Identity) sender() string {
	if id.Name != "" {
		return id.Name
	}
	return "human"
}
