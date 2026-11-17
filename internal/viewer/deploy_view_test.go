package viewer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/deploy"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// TestReportDeployCard: the Report's Last deploy card is absent without
// deploy:, an honest empty state naming the dry run before the first run,
// and the newest record after one, its red step named with the command to
// go on; status.md carries the same in one line.
func TestReportDeployCard(t *testing.T) {
	proj := setupStatusProject(t, time.Now())
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, body := getResp(t, ts.URL+"/p/status/status")
	if strings.Contains(body, `data-testid="report.deploy"`) {
		t.Fatal("a project with no deploy: shows the Last deploy card")
	}

	wf, err := os.ReadFile(proj.WorkflowFile)
	if err != nil {
		t.Fatal(err)
	}
	steps := "\ndeploy:\n  - name: backup\n    run: \"true\"\n  - name: app\n    run: ./install.sh\n    needs: owner-ok\n  - name: landing\n    run: \"true\"\n"
	if err := os.WriteFile(proj.WorkflowFile, append(wf, []byte(steps)...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, body = getResp(t, ts.URL+"/p/status/status")
	assertContains(t, body, `data-testid="report.deploy"`, `data-testid="report.deploy.empty"`, `No deploy recorded yet.`, `issue-cli --project status deploy --dry-run`)
	_, md := getResp(t, ts.URL+"/p/status/status.md")
	assertContains(t, md, "Last deploy: none yet")

	start := time.Now().Add(-2 * time.Hour)
	exit := 1
	rec := deploy.Record{
		ID: start.Format("20060102-150405"), Started: start, Finished: start.Add(41 * time.Second), By: "lead-status",
		State: deploy.Red, Failed: "app", Log: "x.log",
		Steps: []deploy.StepResult{
			{Name: "backup", State: deploy.Green, Seconds: 12},
			{Name: "app", State: deploy.Red, Why: "run exit 1", Exit: &exit, Seconds: 29},
			{Name: "landing", State: deploy.NotRun},
		},
	}
	writeDeployRecord(t, proj.IssueDir, rec)
	_, body = getResp(t, ts.URL+"/p/status/status")
	assertContains(t, body,
		`data-testid="report.deploy.state">red</span>`,
		`data-testid="report.deploy.when"`, `by lead-status · 41s`, `2h ago`,
		`data-testid="report.deploy.step.backup.state">green</span>`,
		`data-testid="report.deploy.step.app.state">red</span>`,
		`data-testid="report.deploy.why">run exit 1</span>`,
		`data-testid="report.deploy.step.landing.state">not run</span>`,
		`data-testid="report.deploy.resume"`, `issue-cli --project status deploy --from app`,
	)
	if strings.Contains(body, `data-testid="report.deploy.empty"`) {
		t.Error("the empty state shows beside a record")
	}
	_, md = getResp(t, ts.URL+"/p/status/status.md")
	assertContains(t, md, "Last deploy: "+start.Format("2006-01-02 15:04")+" red by lead-status at app")
}

func writeDeployRecord(t *testing.T, issueDir string, rec deploy.Record) {
	t.Helper()
	dir := deploy.Dir(issueDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rec.ID+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
