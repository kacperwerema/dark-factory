package tracker

import (
	"fmt"
	"strings"
)

// The roles a status can carry in workflow.yaml (`role:`): what code needs
// to know about a status beyond its place in the list. Each is held by at
// most one status of a workflow. docs/Workflow/overview.md, "Status roles".
const (
	StatusRoleDesign    = "design"     // the design is written here; it stops for approval
	StatusRoleQueue     = "queue"      // ready to pick up; start advances out of it
	StatusRoleWork      = "work"       // the work itself
	StatusRoleVerify    = "verify"     // the agent's verification
	StatusRoleOwnerTest = "owner-test" // the human's own testing
)

// statusRoles is every role, in lifecycle order, with the status name that
// holds it when no status declares it, and what a workflow without it
// loses. These names are written only here (TestNoStatusNameLiterals).
var statusRoles = []struct {
	Role, OldName, Loses string
}{
	{StatusRoleDesign, "in design", "the stop-for-approval reminder in the briefing"},
	{StatusRoleQueue, "backlog", "start's hand-off, auto-start, next's backlog, the Status and company counts"},
	{StatusRoleWork, "in progress", "next's in-progress list, the agents' active span"},
	{StatusRoleVerify, "testing", "next's testing list, the guided-check hints, the YOLO owner checks' start"},
	{StatusRoleOwnerTest, "human-testing", "start's hand-off, YOLO's and approve's refusal, the playtests and checks owed to the owner"},
}

// StatusRoles lists the roles a status can carry.
func StatusRoles() []string {
	out := make([]string, len(statusRoles))
	for i, r := range statusRoles {
		out[i] = r.Role
	}
	return out
}

func oldStatusName(role string) string {
	for _, r := range statusRoles {
		if r.Role == role {
			return r.OldName
		}
	}
	return ""
}

// declaredRole is the status declaring role, "" when none does.
func (w *WorkflowConfig) declaredRole(role string) string {
	if w == nil {
		return ""
	}
	for _, s := range w.Statuses {
		if strings.TrimSpace(s.Role) == role {
			return s.Name
		}
	}
	return ""
}

// RoleStatus is the status holding role in this workflow: the one declaring
// it, else a listed status with the role's old name, else "". A nil or empty
// workflow knows the old names.
func (w *WorkflowConfig) RoleStatus(role string) string {
	if name := w.declaredRole(role); name != "" {
		return name
	}
	old := oldStatusName(role)
	if w == nil || len(w.Statuses) == 0 {
		return old
	}
	for _, s := range w.Statuses {
		if statusFold(s.Name) == statusFold(old) {
			return s.Name
		}
	}
	return ""
}

// HasRole reports whether status holds role: it declares it, or nothing
// does and status has the role's old name. Resolving per role means a file
// declaring one role keeps the rest, and a status the workflow doesn't list
// (an old issue, a nil workflow) keeps the meaning of its name.
func (w *WorkflowConfig) HasRole(status, role string) bool {
	name := w.declaredRole(role)
	if name == "" {
		name = oldStatusName(role)
	}
	return name != "" && statusFold(status) == statusFold(name)
}

// RoleOf is the role status holds, "" for none.
func (w *WorkflowConfig) RoleOf(status string) string {
	for _, r := range statusRoles {
		if w.HasRole(status, r.Role) {
			return r.Role
		}
	}
	return ""
}

// IsHandoffStatus returns true when the agent's job at this status is to hand
// off — not to work a checklist. start advances through handoff statuses and
// lands on the next work status, so the next step from here is `start`, not
// `transition`.
func (w *WorkflowConfig) IsHandoffStatus(status string) bool {
	return w.HasRole(status, StatusRoleQueue) || w.HasRole(status, StatusRoleOwnerTest)
}

// IsOwnerTestApproval reports whether name, an approval or a status, is the
// human's own Human Testing approval: any case, spaces or hyphens.
func (w *WorkflowConfig) IsOwnerTestApproval(name string) bool {
	return approvalFold(name) == approvalFold(w.ownerTestName())
}

func (w *WorkflowConfig) ownerTestName() string {
	if name := w.declaredRole(StatusRoleOwnerTest); name != "" {
		return name
	}
	return oldStatusName(StatusRoleOwnerTest)
}

func approvalFold(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
}

// BeforeQueue reports whether status comes before the queue: the design
// statuses `next --design` lists. A workflow with no queue has none.
func (w *WorkflowConfig) BeforeQueue(status string) bool {
	queue := w.RoleStatus(StatusRoleQueue)
	if w == nil || queue == "" {
		return false
	}
	i, q := w.GetStatusIndex(status), w.GetStatusIndex(queue)
	return i >= 0 && i < q
}

// RoleProblems is what a workflow's roles get wrong: a role not on the
// list, a role on two statuses or on a closing status, and a file that
// fills some roles but not all, which is how a renamed status looks in a
// file with no role: keys. Each comes with its fix. A file filling none (a
// department's own lifecycle) has nothing to say.
func (w *WorkflowConfig) RoleProblems() [][2]string {
	if w == nil {
		return nil
	}
	var out [][2]string
	seen := map[string]string{}
	for i, s := range w.Statuses {
		role := strings.TrimSpace(s.Role)
		if role == "" {
			continue
		}
		switch {
		case oldStatusName(role) == "":
			out = append(out, [2]string{fmt.Sprintf("status %q: unknown role %q", s.Name, role), "role: " + strings.Join(StatusRoles(), " | ")})
		case seen[role] != "":
			out = append(out, [2]string{fmt.Sprintf("role %s is on both %q and %q; a role is one status's", role, seen[role], s.Name), "keep it on one"})
		case i >= w.endIndex():
			out = append(out, [2]string{fmt.Sprintf("status %q closes the workflow, so role %s on it never runs", s.Name, role), "move it to an open status"})
		}
		if seen[role] == "" {
			seen[role] = s.Name
		}
	}
	var missing []int
	for i, r := range statusRoles {
		if w.RoleStatus(r.Role) == "" {
			missing = append(missing, i)
		}
	}
	if len(missing) == len(statusRoles) {
		return out
	}
	for _, i := range missing {
		r := statusRoles[i]
		out = append(out, [2]string{fmt.Sprintf("no status has role %s (it was %q): this workflow loses %s", r.Role, r.OldName, r.Loses),
			fmt.Sprintf("add `role: %s` to the status that does this; a renamed status needs it", r.Role)})
	}
	return out
}
