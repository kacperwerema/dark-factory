package tracker_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/tracker/statustest"
)

func parseWorkflow(t *testing.T, raw string) *tracker.WorkflowConfig {
	t.Helper()
	var wf tracker.WorkflowConfig
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatal(err)
	}
	return &wf
}

func TestStatusRoleParsedAndKeptByOverlays(t *testing.T) {
	wf := parseWorkflow(t, `
statuses:
  - name: "ready"
    role: "queue"
  - name: "build"
  - name: "backlog"
  - name: "done"
default_type: feature
types:
  feature:
    path: [ready, build, done]
systems:
  Core:
    statuses:
      - name: "build"
        role: "work"
`)
	if got := wf.RoleStatus(tracker.StatusRoleQueue); got != "ready" {
		t.Fatalf("RoleStatus(queue) = %q", got)
	}
	if !wf.HasRole("Ready", tracker.StatusRoleQueue) || wf.RoleOf("ready") != tracker.StatusRoleQueue {
		t.Fatal("ready doesn't hold queue, in any case")
	}
	if wf.HasRole("backlog", tracker.StatusRoleQueue) {
		t.Fatal("a declared role leaves the old name with nothing, and backlog kept it")
	}
	if got := wf.ForType("feature").RoleStatus(tracker.StatusRoleQueue); got != "ready" {
		t.Fatalf("a type's path dropped the role: %q", got)
	}
	if got := wf.ForSystem("Core").RoleStatus(tracker.StatusRoleWork); got != "build" {
		t.Fatalf("an overlay's role: %q", got)
	}
	if got := wf.RoleStatus(tracker.StatusRoleWork); got != "" {
		t.Fatalf("the overlay's role leaked into the base: %q", got)
	}
	if got := tracker.StatusRoles(); !slices.Equal(got, []string{"design", "queue", "work", "verify", "owner-test"}) {
		t.Fatalf("roles = %v", got)
	}
}

func TestRolesFallBackToOldNames(t *testing.T) {
	old := map[string]string{
		"in design": tracker.StatusRoleDesign, "backlog": tracker.StatusRoleQueue, "in progress": tracker.StatusRoleWork,
		"testing": tracker.StatusRoleVerify, "human-testing": tracker.StatusRoleOwnerTest,
	}
	noKeys := tracker.DefaultWorkflow()
	for i := range noKeys.Statuses {
		noKeys.Statuses[i].Role = ""
	}
	var none *tracker.WorkflowConfig
	for _, wf := range []*tracker.WorkflowConfig{noKeys, none, {}} {
		for name, role := range old {
			if !wf.HasRole(name, role) || wf.RoleStatus(role) != name || wf.RoleOf(name) != role {
				t.Errorf("%s doesn't hold %s with no role: key (wf %v)", name, role, wf != nil)
			}
		}
		if !wf.IsHandoffStatus("backlog") || !wf.IsHandoffStatus("human-testing") || wf.IsHandoffStatus("in progress") {
			t.Error("handoff statuses changed with no role: key")
		}
		if !wf.IsOwnerTestApproval("Human Testing") {
			t.Error("the owner's approval changed with no role: key")
		}
	}
	// An old issue's status the workflow doesn't list keeps its name's meaning.
	short := parseWorkflow(t, "statuses:\n  - name: \"a\"\n  - name: \"done\"\n")
	if !short.HasRole("backlog", tracker.StatusRoleQueue) || short.RoleStatus(tracker.StatusRoleQueue) != "" {
		t.Fatal("an unlisted status lost its meaning, or RoleStatus named a status the file lacks")
	}
	// A file declaring one role keeps the others: YOLO's refusal above all.
	partial := parseWorkflow(t, "statuses:\n  - name: \"ready\"\n    role: \"queue\"\n  - name: \"human-testing\"\n  - name: \"done\"\n")
	if !partial.IsOwnerTestApproval("human-testing") {
		t.Fatal("declaring queue lost owner-test")
	}
}

func TestRoleProblems(t *testing.T) {
	has := func(wf *tracker.WorkflowConfig, want string) bool {
		for _, p := range wf.RoleProblems() {
			if strings.Contains(p[0], want) {
				return p[1] != ""
			}
		}
		return false
	}
	bad := parseWorkflow(t, `
statuses:
  - name: "a"
    role: "qeue"
  - name: "b"
    role: "work"
  - name: "c"
    role: "work"
  - name: "done"
    role: "verify"
`)
	for _, want := range []string{`unknown role "qeue"`, `role work is on both "b" and "c"`, `status "done" closes the workflow`} {
		if !has(bad, want) {
			t.Errorf("no problem %q in %v", want, bad.RoleProblems())
		}
	}

	// A rename in a file with no role: keys: the one way it shows.
	renamed := tracker.DefaultWorkflow()
	for i := range renamed.Statuses {
		renamed.Statuses[i].Role = ""
		if renamed.Statuses[i].Name == "human-testing" {
			renamed.Statuses[i].Name = "owner-check"
		}
	}
	if !has(renamed, "no status has role owner-test") || len(renamed.RoleProblems()) != 1 {
		t.Fatalf("rename = %v", renamed.RoleProblems())
	}
	if got := tracker.DefaultWorkflow().RoleProblems(); len(got) != 0 {
		t.Fatalf("the bundled template: %v", got)
	}
	if got := statustest.Renamed().RoleProblems(); len(got) != 0 {
		t.Fatalf("renamed with roles kept: %v", got)
	}
}

func TestDevelopmentWorkflowsDeclareEveryRole(t *testing.T) {
	repo, err := tracker.LoadWorkflow(filepath.Join("..", "..", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, wf := range []*tracker.WorkflowConfig{tracker.DefaultWorkflow(), repo} {
		for _, role := range tracker.StatusRoles() {
			declared := slices.ContainsFunc(wf.Statuses, func(s tracker.WorkflowStatus) bool { return s.Role == role })
			if !declared {
				t.Errorf("role %s isn't declared", role)
			}
		}
	}
	if _, err := os.Stat(filepath.Join("..", "..", "cmd", "issue-cli", "templates", "workflow-features", "status-roles.yaml")); err != nil {
		t.Fatal(err)
	}
}

// TestRenamedStatusesKeepTrackerBehaviour is the part of the Design table
// that lives in this package.
func TestRenamedStatusesKeepTrackerBehaviour(t *testing.T) {
	wf := statustest.Renamed()
	queue, work := statustest.Name(tracker.StatusRoleQueue), statustest.Name(tracker.StatusRoleWork)
	owner, design := statustest.Name(tracker.StatusRoleOwnerTest), statustest.Name(tracker.StatusRoleDesign)
	if !wf.IsHandoffStatus(queue) || !wf.IsHandoffStatus(owner) || wf.IsHandoffStatus(work) {
		t.Fatal("hand-offs didn't follow the rename")
	}
	if wf.IsHandoffStatus("backlog") || wf.IsHandoffStatus("human-testing") {
		t.Fatal("the old names are still hand-offs")
	}
	if got := wf.StartTarget(queue); got != work {
		t.Fatalf("StartTarget(queue) = %q", got)
	}
	if !wf.IsOwnerTestApproval(owner) || wf.IsOwnerTestApproval("human-testing") {
		t.Fatal("the owner's approval didn't follow the rename")
	}
	if !wf.BeforeQueue(design) || !wf.BeforeQueue("idea") || wf.BeforeQueue(queue) || wf.BeforeQueue(work) {
		t.Fatal("BeforeQueue")
	}
	if parseWorkflow(t, "statuses:\n  - name: \"a\"\n  - name: \"done\"\n").BeforeQueue("a") {
		t.Fatal("a workflow with no queue has statuses before it")
	}

	prompt := func(status string) string {
		return tracker.BuildAgentPrompt(&tracker.Project{Slug: "p", Name: "P"}, &tracker.Issue{Slug: "s", Title: "S", Status: status}, wf, "", "")
	}
	if !strings.Contains(prompt(queue), "needs `in progress` approval") && !strings.Contains(prompt(queue), "then stop and end your turn") {
		t.Error("the queue's reminder didn't follow the rename")
	}
	if !strings.Contains(prompt(design), "When the design is complete") {
		t.Error("the design reminder didn't follow the rename")
	}
}

func TestApprovalErrorWordingFollowsTheQueue(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.md")
	queue, work := statustest.Name(tracker.StatusRoleQueue), statustest.Name(tracker.StatusRoleWork)
	if err := os.WriteFile(file, []byte("---\ntitle: \"S\"\nstatus: \""+queue+"\"\n---\n\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := statustest.Renamed().StartIssueOnce(file, "s", "me")
	var missing *tracker.ApprovalMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v", err)
	}
	if want := "cannot start s: human approval for \"" + work + "\" is missing"; !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %q, want it to start %q", err, want)
	}
}
