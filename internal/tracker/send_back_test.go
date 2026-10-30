package tracker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoWorkflow is this repo's workflow.yaml: it declares no backward edge.
func repoWorkflow(t *testing.T) *WorkflowConfig {
	t.Helper()
	wf, err := LoadWorkflow("../../workflow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestBackTargetFollowsThePath(t *testing.T) {
	wf := repoWorkflow(t)
	feature := wf.ForIssue(&Issue{Type: "feature", Status: "human-testing"})
	tweak := wf.ForIssue(&Issue{Type: "tweak", Status: "testing"})
	tests := []struct {
		name       string
		wf         *WorkflowConfig
		status, to string
		want       string
		err        error
	}{
		{"one back", feature, "human-testing", "", "testing", nil},
		{"tweak skips off-path statuses", tweak, "testing", "", "in progress", nil},
		{"asked-for earlier status", feature, "human-testing", "in progress", "in progress", nil},
		{"stay where it is", feature, "in design", "in design", "in design", nil},
		{"later status", feature, "testing", "documentation", "", ErrBadBackTarget},
		{"off-path status", tweak, "testing", "in design", "", ErrBadBackTarget},
		{"first status", feature, "idea", "", "", ErrNoBackTarget},
		{"done", feature, "done", "", "", ErrNoBackTarget},
		{"parked", feature, "parked", "", "", ErrNoBackTarget},
	}
	for _, tt := range tests {
		got, err := tt.wf.BackTarget(tt.status, tt.to)
		if got != tt.want || !errors.Is(err, tt.err) || (tt.err == nil && err != nil) {
			t.Errorf("%s: BackTarget(%q, %q) = %q, %v; want %q, %v", tt.name, tt.status, tt.to, got, err, tt.want, tt.err)
		}
	}
}

func writeIssue(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shop.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMoveBackOverridesWithoutADeclaredEdge(t *testing.T) {
	wf := repoWorkflow(t)
	path := writeIssue(t, "---\ntitle: \"Shop\"\nstatus: \"human-testing\"\nassignee: \"agent-shop\"\nhuman_approval: \"documentation\"\nqueued_approval: \"shipping\"\n---\n\nBody.\n")
	from, err := wf.MoveBack(path, "testing")
	if err != nil || from != "human-testing" {
		t.Fatalf("MoveBack = %q, %v", from, err)
	}
	data, _ := os.ReadFile(path)
	issue, err := ParseIssue("shop.md", data)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status != "testing" || issue.HumanApproval != "" || issue.QueuedApproval != "" || issue.Assignee != "agent-shop" {
		t.Fatalf("after MoveBack: status %q approval %q queued %q assignee %q", issue.Status, issue.HumanApproval, issue.QueuedApproval, issue.Assignee)
	}
	if strings.Contains(string(data), "## Testing") {
		t.Fatalf("the forward side-effect ran:\n%s", data)
	}
	store, err := LoadStats(path)
	if err != nil || len(store.Transitions) != 1 || store.Transitions[0].From != "human-testing" || store.Transitions[0].To != "testing" {
		t.Fatalf("stats = %+v, %v", store.Transitions, err)
	}
	if _, err := wf.MoveBack(path, "documentation"); !errors.Is(err, ErrBadBackTarget) {
		t.Fatalf("forward MoveBack: %v", err)
	}
}

func TestMoveBackRunsADeclaredEdge(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{{Name: "draft"}, {Name: "review"}},
		Transitions: []WorkflowTransition{
			{From: "draft", To: "review"},
			{From: "review", To: "draft", Actions: []WorkflowAction{{Type: "append_section", Title: "Rework", Body: "- [ ] Fixed"}}},
		},
	}
	path := writeIssue(t, "---\ntitle: \"Shop\"\nstatus: \"review\"\n---\n\nBody.\n")
	if _, err := wf.MoveBack(path, "draft"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "## Rework") || !strings.Contains(string(data), `status: "draft"`) && !strings.Contains(string(data), "status: draft") {
		t.Fatalf("declared edge's action did not run:\n%s", data)
	}
}

func TestRejectedComment(t *testing.T) {
	if got := RejectedComment("fix the label", "viewer", "2026-10-30", "human-testing", "testing"); got != `rejected: "fix the label" (viewer, 2026-10-30) → back to testing` {
		t.Fatalf("got %q", got)
	}
	if got := RejectedComment("redo D2", "viewer", "2026-10-30", "in design", "in design"); got != `rejected: "redo D2" (viewer, 2026-10-30) → stays in in design` {
		t.Fatalf("got %q", got)
	}
}
