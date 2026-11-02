package viewer

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// needsYouServer serves the Status fixture: ui/ready waits on a ready gate
// (in design → backlog), ui/open on a gate with its checklist open, ui/tested
// on a playtest, ui/asker on a question.
func needsYouServer(t *testing.T) (tracker.Project, string) {
	t.Helper()
	proj := setupStatusProject(t, time.Now())
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	ts := newTestServer(t, []tracker.Project{proj})
	t.Cleanup(ts.Close)
	return proj, ts.URL
}

func TestNeedsYouPageListsEveryKindWithItsAction(t *testing.T) {
	_, url := needsYouServer(t)
	resp, body := getResp(t, url+"/p/status/needs-you")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`data-testid="needs_you.count">3</span>`,
		`data-section="approve"`, `data-section="test"`, `data-section="answer"`,
		`data-kind="gate" data-slug="ui/ready" data-from="in design" data-status="backlog"`,
		`data-testid="needs_you.pick"`,
		`data-testid="needs_you.approve"`,
		`data-kind="playtest" data-slug="ui/tested"`,
		`data-ny-run="/p/status/issue/ui/tested/playtest" data-testid="needs_you.run">▶ Play</button>`,
		`data-kind="question" data-slug="ui/asker"`,
		`href="/p/status/issue/ui/asker#comments" data-testid="needs_you.open">Open</a>`,
		`data-ny-reply-form="/p/status/issue/ui/asker/comments"`,
		`Select all ready (1)`,
		`data-approve-url="/p/status/needs-you/approve"`,
		`data-testid="needs_you.confirm"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("needs-you page missing %q", want)
		}
	}
	// A gate still missing something waits on the agent: not listed.
	if strings.Contains(body, `data-slug="ui/open"`) {
		t.Error("ui/open's checklist is open: it must not be listed")
	}
	// Only gates get a checkbox.
	if n := strings.Count(body, `data-testid="needs_you.pick"`); n != 1 {
		t.Errorf("checkboxes = %d, want 1 (the ready gate)", n)
	}
}

// Proves: AC2 — every count is the list's length and links to it.
func TestNeedsYouCountsMatchEverywhere(t *testing.T) {
	_, url := needsYouServer(t)
	_, page := getResp(t, url+"/p/status/needs-you")
	if !strings.Contains(page, `data-testid="needs_you.count">3<`) {
		t.Fatal("list count is not 3")
	}
	checks := map[string][]string{
		"/p/status/?classic=1":        {`href="/p/status/needs-you" class="chip chip-amber nav-count nav-split-count"`, `data-testid="nav.tab.status.count">3</a>`},
		"/p/status/status?classic=1":  {`data-testid="status.waiting.count">3</span>`, `href="/p/status/needs-you" data-testid="status.waiting.all"`},
		"/p/status/status":            {`href="/p/status/needs-you" data-testid="map.waiting"><span class="wb-you-mark">!</span>3 waiting on you`, `data-testid="report.action.needs_you"`},
		"/p/status/company?classic=1": {`href="/p/status/needs-you" data-testid="map.waiting"><span class="wb-you-mark">!</span>3 waiting on you`},
		"/p/status/company":           {`href="/p/status/needs-you" data-testid="map.waiting"><span class="wb-fl-you-mark">!</span>3 waiting on you`, `Waiting on you · 3`},
		"/p/status/hall":              {`href="/p/status/needs-you" data-testid="map.waiting"><span class="wb-you-mark">!</span>3 waiting on you`},
		"/":                           {`href="/needs-you" data-testid="map.waiting"><span class="wb-you-mark">!</span>3 waiting on you`},
	}
	for path, wants := range checks {
		_, body := getResp(t, url+path)
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing %q", path, want)
			}
		}
	}
}

func TestNeedsYouJSONMatchesPage(t *testing.T) {
	_, url := needsYouServer(t)
	resp, body := getResp(t, url+"/p/status/needs-you.json")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var out NeedsYouJSON
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Count != 3 || len(out.Rows) != 3 || out.URL != "/p/status/needs-you" {
		t.Fatalf("json = %+v", out)
	}
	var gate *WbQueueCard
	for i := range out.Rows {
		if out.Rows[i].Kind == "gate" {
			gate = &out.Rows[i]
		}
	}
	if gate == nil || gate.Slug != "ui/ready" || !gate.Ready || gate.From != "in design" || gate.Target != "backlog" {
		t.Errorf("gate row = %+v", gate)
	}
	if !strings.Contains(body, `"after":[]`) {
		t.Errorf("after should be an empty list: %s", body)
	}
}

func postNeedsYou(t *testing.T, url, body string, header map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, b.String()
}

func TestNeedsYouApproveStampsReadyGatesOnly(t *testing.T) {
	proj, url := needsYouServer(t)
	nudged := false
	withMockTmuxSendKeys(t, func(string, []string) error { nudged = true; return nil })
	withMockTmuxSessions(t, []AgentSession{{Name: "agent-ui-ready"}})

	code, body := postNeedsYou(t, url+"/p/status/needs-you/approve",
		`{"items":[{"slug":"ui/ready","status":"backlog"},{"slug":"ui/open","status":"backlog"},{"slug":"ui/nope","status":"backlog"}],"words":"ship them"}`, nil)
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var out struct{ Results []NeedsYouApproval }
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 3 {
		t.Fatalf("results = %+v", out.Results)
	}
	if r := out.Results[0]; !r.OK || r.Error != "" {
		t.Errorf("ui/ready = %+v, want ok", r)
	}
	if r := out.Results[1]; r.OK || !strings.Contains(r.Error, "no longer waiting on an approval") && !strings.Contains(r.Error, "still open") {
		t.Errorf("ui/open = %+v, want a not-ready error", r)
	}
	if r := out.Results[2]; r.OK || r.Error != "issue not found" {
		t.Errorf("ui/nope = %+v", r)
	}

	// The station's record: frontmatter, approval time, the words; the
	// status does not move and no agent is messaged.
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	var ready, open *tracker.Issue
	for _, is := range issues {
		switch is.Slug {
		case "ui/ready":
			ready = is
		case "ui/open":
			open = is
		}
	}
	if ready.HumanApproval != "backlog" || ready.Status != "in design" {
		t.Errorf("ui/ready = approval %q status %q, want backlog / in design", ready.HumanApproval, ready.Status)
	}
	if open.HumanApproval != "" {
		t.Errorf("ui/open must not be stamped, got %q", open.HumanApproval)
	}
	if st, _ := tracker.LoadStats(ready.FilePath); st.LastApproval == nil || st.LastApproval.Status != "backlog" {
		t.Errorf("approval time not recorded: %+v", st.LastApproval)
	}
	raw, _ := os.ReadFile(ready.FilePath)
	if !regexp.MustCompile(`approval: .*ship them`).Match(raw) {
		t.Errorf("approval: comment with the words missing:\n%s", raw)
	}
	if nudged {
		t.Error("bulk approve must not message the agent")
	}

	// Again: already approved, not toggled off.
	_, body = postNeedsYou(t, url+"/p/status/needs-you/approve", `{"items":[{"slug":"ui/ready","status":"backlog"}]}`, nil)
	if !strings.Contains(body, "already approved for backlog") {
		t.Errorf("second approve = %s", body)
	}
	issues, _ = tracker.LoadIssues(proj.IssueDir)
	for _, is := range issues {
		if is.Slug == "ui/ready" && is.HumanApproval != "backlog" {
			t.Errorf("second approve cleared the stamp: %q", is.HumanApproval)
		}
	}
}

func TestNeedsYouApproveRefusesBadInputAndCrossOrigin(t *testing.T) {
	_, url := needsYouServer(t)
	if code, _ := postNeedsYou(t, url+"/p/status/needs-you/approve", `{"items":[]}`, nil); code != http.StatusBadRequest {
		t.Errorf("empty items: %d, want 400", code)
	}
	code, _ := postNeedsYou(t, url+"/p/status/needs-you/approve", `{"items":[{"slug":"ui/ready","status":"backlog"}]}`, map[string]string{"Origin": "http://evil.example"})
	if code != http.StatusForbidden {
		t.Errorf("cross-origin: %d, want 403", code)
	}
}

func TestNeedsYouGateThatMovedOn(t *testing.T) {
	proj, url := needsYouServer(t)
	// The gate is for backlog; asking for another status is refused.
	_, body := postNeedsYou(t, url+"/p/status/needs-you/approve", `{"items":[{"slug":"ui/ready","status":"in progress"}]}`, nil)
	if !strings.Contains(body, "now waits on approval for backlog, not in progress") {
		t.Errorf("wrong status = %s", body)
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	for _, is := range issues {
		if is.Slug == "ui/ready" && is.HumanApproval != "" {
			t.Errorf("stamped the wrong status: %q", is.HumanApproval)
		}
	}
}

func TestNeedsYouPlaytestsOffDropsRows(t *testing.T) {
	proj, url := needsYouServer(t)
	wf, _ := os.ReadFile(proj.WorkflowFile)
	if err := os.WriteFile(proj.WorkflowFile, append([]byte("playtest: agents\n"), wf...), 0644); err != nil {
		t.Fatal(err)
	}
	_, body := getResp(t, url+"/p/status/needs-you")
	if strings.Contains(body, `data-kind="playtest"`) {
		t.Error("playtest rows must go when playtest: agents")
	}
	if !strings.Contains(body, `data-testid="needs_you.count">2</span>`) {
		t.Error("count should drop to 2")
	}
}

func TestNeedsYouAllProjectsAndBugReportsOnce(t *testing.T) {
	a := setupStatusProject(t, time.Now())
	b := setupStatusProject(t, time.Now())
	b.Name, b.Slug = "Other", "other"
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 2 }
	t.Cleanup(func() { unsentBugReports = prev })
	ts := newTestServer(t, []tracker.Project{a, b})
	defer ts.Close()

	_, body := getResp(t, ts.URL+"/needs-you")
	for _, want := range []string{
		`data-project="status" data-approve-url="/p/status/needs-you/approve"`,
		`data-project="other" data-approve-url="/p/other/needs-you/approve"`,
		// 3 each, plus the bug reports once.
		`data-testid="needs_you.count">7</span>`,
		`Select all ready (2)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/needs-you missing %q", want)
		}
	}
	if n := strings.Count(body, `data-kind="bugs"`); n != 1 {
		t.Errorf("bug report rows = %d, want 1", n)
	}
	// Approving across projects: each project's endpoint stamps its own.
	for _, p := range []string{"status", "other"} {
		_, res := postNeedsYou(t, ts.URL+"/p/"+p+"/needs-you/approve", `{"items":[{"slug":"ui/ready","status":"backlog"}]}`, nil)
		if !strings.Contains(res, `"ok":true`) {
			t.Errorf("%s approve = %s", p, res)
		}
	}
}

func TestNeedsYouEmpty(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(root+"/issues", 0755)
	os.WriteFile(root+"/workflow.yaml", []byte("statuses:\n  - name: \"idea\"\n  - name: \"done\"\n"), 0644)
	proj := tracker.Project{Name: "Quiet", Slug: "quiet", IssueDir: root + "/issues", WorkDir: root, WorkflowFile: root + "/workflow.yaml"}
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, body := getResp(t, ts.URL+"/p/quiet/needs-you")
	if !strings.Contains(body, `data-testid="needs_you.empty"`) || strings.Contains(body, `data-testid="needs_you.bar"`) {
		t.Errorf("empty list should say so with no toolbar:\n%s", body)
	}
}

// Gates YOLO passed show below the list to review, and are not counted.
func TestNeedsYouYOLOAfterTheFact(t *testing.T) {
	proj := setupYOLOProject(t)
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, body := getResp(t, ts.URL+"/p/yp/needs-you")
	if !strings.Contains(body, `data-testid="needs_you.after"`) || !strings.Contains(body, `data-slug="shop" data-testid="needs_you.after.row"><a class="wb-ny-after-link" href="/p/yp/issue/shop#yolo">`) || !strings.Contains(body, `data-testid="needs_you.after.agree">Agree</button>`) || !strings.Contains(body, `data-yolo-url="/p/yp/yolo/review"`) {
		t.Fatalf("after-the-fact section missing:\n%s", body)
	}
	_, js := getResp(t, ts.URL+"/p/yp/needs-you.json")
	var out NeedsYouJSON
	if err := json.Unmarshal([]byte(js), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.After) != 1 || out.After[0].Slug != "shop" || out.Count != len(out.Rows) {
		t.Errorf("json = %+v", out)
	}
	for _, r := range out.Rows {
		if r.Slug == "shop" {
			t.Errorf("a gate YOLO passed must not be counted: %+v", r)
		}
	}
}

// TestNeedsYouPagesShareComponents: the page is made of the wb/ny-*
// components (none of their markup inline), and /needs-you and a project's
// own list render the same rows; only the page across projects names the
// project above its group.
func TestNeedsYouPagesShareComponents(t *testing.T) {
	src, err := templateFS.ReadFile("templates/needs_you.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"wb/ny-bar", "wb/ny-group", "wb/ny-empty", "wb/ny-confirm"} {
		if !strings.Contains(string(src), `{{template "`+c+`"`) {
			t.Errorf("needs_you.html does not use %s", c)
		}
	}
	for _, inline := range []string{`data-testid="needs_you.row"`, `data-testid="needs_you.section"`, `<dialog`} {
		if strings.Contains(string(src), inline) {
			t.Errorf("needs_you.html still carries %s inline", inline)
		}
	}

	a := setupStatusProject(t, time.Now())
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()

	_, all := getResp(t, ts.URL+"/needs-you")
	_, own := getResp(t, ts.URL+"/p/status/needs-you")
	if !strings.Contains(all, `<h2 class="wb-ny-proj">`) || strings.Contains(own, `<h2 class="wb-ny-proj">`) {
		t.Error("only /needs-you should name the project above its group")
	}
	rows := regexp.MustCompile(`(?s)<div class="wb-ny-row.*?data-testid="needs_you.row.msg"`)
	if a, o := rows.FindAllString(all, -1), rows.FindAllString(own, -1); len(a) == 0 || strings.Join(a, "") != strings.Join(o, "") {
		t.Errorf("the two pages should render the same rows: %d vs %d", len(a), len(o))
	}
}
