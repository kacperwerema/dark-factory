package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// withMarketingWorkflow gives the project a Marketing department (system
// UI) on the bundled content template, or on content when it isn't "".
func withMarketingWorkflow(t *testing.T, a *tracker.Project, content string) {
	t.Helper()
	if content == "" {
		data, err := workflowupgrade.ReadTemplate("content")
		if err != nil {
			t.Fatal(err)
		}
		content = string(data)
	}
	path := filepath.Join(a.TelemetryRoot(), "workflows", "marketing.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	a.Departments = []tracker.Department{{Name: "Marketing", Systems: []string{"UI"}, Workflow: "workflows/marketing.yaml"}}
}

func TestDepartmentRoomShowsItsOwnLine(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	withMarketingWorkflow(t, &a, "")
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company := getBody(t, ts.URL+"/p/agents/company?classic=1")
	assertContains(t, company, `data-testid="company.room.dept.line"`, `owner review`, `station from marketing.yaml`)
	if strings.Contains(company, `company.room.dept.workflow_error`) {
		t.Error("a good file shows no workflow error")
	}
}

func TestDepartmentRoomShowsABrokenFile(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	withMarketingWorkflow(t, &a, "statuses: [: broken\n")
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, company := getBody(t, ts.URL+"/p/agents/company?classic=1")
	assertContains(t, company, `data-testid="company.room.dept.workflow_error"`, `department Marketing`, `workflow error`)
	// Product's room still draws its line.
	assertContains(t, company, `data-testid="company.room.product"`)
}

func TestWorkboardSwitchesWorkflowByDepartment(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{a})
	_, before := getBody(t, ts.URL+"/p/agents/workboard?classic=1&all=1")
	ts.Close()
	if strings.Contains(before, `workboard.department.`) {
		t.Fatal("no department switch without department workflow files")
	}

	withMarketingWorkflow(t, &a, "")
	ts = newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, product := getBody(t, ts.URL+"/p/agents/workboard?classic=1&all=1")
	assertContains(t, product, `data-testid="workboard.department.product"`, `data-testid="workboard.department.marketing"`)
	if strings.Contains(product, `data-testid="workboard.column.owner_review"`) {
		t.Error("Product's board drew a Marketing column")
	}
	_, mk := getBody(t, ts.URL+"/p/agents/workboard?classic=1&all=1&department=Marketing")
	assertContains(t, mk, `data-testid="workboard.column.brief"`, `data-testid="workboard.column.owner_review"`)
}
