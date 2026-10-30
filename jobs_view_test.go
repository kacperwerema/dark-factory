package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/jobs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// setupJobsProject is the department-lead fixture with a daily Marketing
// job (due every day from 00:00, so on the real clock) on the content
// workflow, and a dispatch stand-in that records what it started.
func setupJobsProject(t *testing.T) (tracker.Project, *[]string) {
	t.Helper()
	proj := setupDeptLeadProject(t)
	root := proj.Root()
	content, err := os.ReadFile("cmd/issue-cli/templates/workflow/content.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"workflows/marketing.yaml":       string(content),
		"departments/marketing/leads.md": "# Lead search\n\nSearch for new leads for {{period}}.",
	} {
		path := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	proj.Departments[0].Workflow = "workflows/marketing.yaml"
	proj.Departments[0].Jobs = []tracker.Job{{ID: "lead-search", Name: "Daily lead search", Every: "day", Brief: "departments/marketing/leads.md"}}
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prevBugs := unsentBugReports
	unsentBugReports = func() int { return 0 }
	origHas := agentrun.HasSession
	agentrun.HasSession = func(string) bool { return false }
	var started []string
	orig := startDispatch
	startDispatch = func(_ *tracker.Project, spec agentrun.Spec) agentrun.Response {
		started = append(started, spec.IssueSlug)
		return agentrun.Response{Status: "dispatched", Session: spec.Session}
	}
	t.Cleanup(func() { startDispatch, unsentBugReports, agentrun.HasSession = orig, prevBugs, origHas })
	return proj, &started
}

func TestJobsView_DueJobInNeedsYouAndStartRun(t *testing.T) {
	proj, started := setupJobsProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	today := time.Now().Format("2006-01-02")

	_, body := getResp(t, ts.URL+"/p/agents/needs-you")
	for _, want := range []string{
		`data-section="jobs"`,
		`data-kind="job" data-slug="marketing/lead-search"`,
		`Daily lead search · ` + today + ` — Marketing: Lead search`,
		`data-ny-run="/p/agents/jobs/marketing/lead-search/run" data-testid="needs_you.run">▶ Start run</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("needs-you misses %q", want)
		}
	}
	_, html := getBody(t, ts.URL+"/p/agents/company")
	for _, want := range []string{`data-testid="company.room.jobs"`, `<b>Daily lead search</b>`, `daily 00:00`, `due · waits for you`, `auto off`} {
		if !strings.Contains(html, want) {
			t.Errorf("company room misses %q", want)
		}
	}

	if _, md := getResp(t, ts.URL+"/p/agents/status.md"); !strings.Contains(md, "Jobs: 1 job, 1 due waiting for you\n") {
		t.Errorf("status.md misses the Jobs line:\n%s", md)
	}

	code, out := postNeedsYou(t, ts.URL+"/p/agents/jobs/marketing/lead-search/run", "", nil)
	if code != http.StatusOK || len(*started) != 1 || (*started)[0] != "seo/lead-search-"+today {
		t.Fatalf("start run: %d %s, started %v", code, out, *started)
	}
	var resp agentrun.Response
	if err := json.Unmarshal([]byte(out), &resp); err != nil || resp.Status != "dispatched" {
		t.Fatalf("response = %s", out)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	run := tracker.FindIssue(issues, "seo/lead-search-"+today)
	comments, _ := tracker.LoadComments(run.FilePath)
	if len(comments) != 1 || !strings.Contains(comments[0].Text, `dispatch: "▶ Start run on Needs you" (Needs you; recurring job marketing/lead-search, run `+today) {
		t.Fatalf("comments = %+v", comments)
	}
	if log := jobs.ReadLog(&proj); len(log) != 1 || log[0].How != jobs.HowOwner {
		t.Fatalf("log = %+v", log)
	}

	// Started: off the list, and a second click is refused.
	_, body = getResp(t, ts.URL+"/p/agents/needs-you")
	if strings.Contains(body, `data-kind="job"`) {
		t.Error("the started job is still in Needs you")
	}
	if code, out := postNeedsYou(t, ts.URL+"/p/agents/jobs/marketing/lead-search/run", "", nil); code != http.StatusConflict || !strings.Contains(out, "is not due") || len(*started) != 1 {
		t.Errorf("second click: %d %s", code, out)
	}
	if code, _ := postNeedsYou(t, ts.URL+"/p/agents/jobs/marketing/nope/run", "", nil); code != http.StatusNotFound {
		t.Errorf("unknown job: %d", code)
	}
	// A cross-origin POST never starts one.
	if code, _ := postNeedsYou(t, ts.URL+"/p/agents/jobs/marketing/lead-search/run", "", map[string]string{"Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Errorf("cross-origin: %d", code)
	}
}

func TestJobsView_StandingStartLeavesItToTheLead(t *testing.T) {
	proj, _ := setupJobsProject(t)
	if err := jobs.TurnOn(ciRootFor(&proj), proj.Slug, jobs.AllJobs, jobs.Auto{Words: "for now all", By: "lead-agents", Since: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, body := getResp(t, ts.URL+"/p/agents/needs-you")
	if strings.Contains(body, `data-kind="job"`) {
		t.Error("a job with a standing start is in Needs you")
	}
	_, html := getBody(t, ts.URL+"/p/agents/company")
	if !strings.Contains(html, `due · lead starts it`) || !strings.Contains(html, `auto on`) {
		t.Error("room doesn't say the lead starts it")
	}
}

func TestJobsStatusLine(t *testing.T) {
	if got := jobsStatusLine(nil); got != "" {
		t.Errorf("no jobs: %q", got)
	}
	got := jobsStatusLine([]jobs.Status{{Verdict: jobs.VerdictOwner}, {Verdict: jobs.VerdictHold}, {}, {Verdict: jobs.VerdictError}})
	if got != "Jobs: 4 jobs, 1 due waiting for you, 1 held, 1 broken (issue-cli health lint --check departments)" {
		t.Errorf("line = %q", got)
	}
}

// The viewer starts a run only from the owner's click: StartRun is called
// from handleJobRun and nowhere else in the server (no watcher, no
// goroutine).
func TestJobsView_OnlyTheClickStartsARun(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		n := strings.Count(src, "agentrun.StartRun(")
		switch {
		case f == "jobs_view.go" && n != 1:
			t.Errorf("jobs_view.go calls StartRun %d times, want once (handleJobRun)", n)
		case f != "jobs_view.go" && n > 0:
			t.Errorf("%s starts a recurring job's run; only ▶ Start run may", f)
		}
	}
	src, _ := os.ReadFile("jobs_view.go")
	fn := string(src)[strings.Index(string(src), "func (s *Server) handleJobRun"):]
	if !strings.Contains(fn, "agentrun.StartRun(") || strings.Contains(string(src), "s.bg.Go") || strings.Contains(string(src), "time.NewTicker") {
		t.Error("StartRun must be called from handleJobRun only, with no background goroutine or ticker")
	}
}
