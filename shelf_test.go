package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// shelfProject is the agents project with Marketing owning system UI and
// three outputs: a page from ui/child in review, an image Marketing made
// by job, approved, and a Product report in review.
func shelfProject(t *testing.T) (tracker.Project, []*outputs.Output) {
	t.Helper()
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	a.Departments = []tracker.Department{{Name: "Marketing", Color: "#E2559B", Systems: []string{"UI"}}}
	now := time.Now()
	img := filepath.Join(t.TempDir(), "hero.svg")
	if err := os.WriteFile(img, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), 0o644); err != nil {
		t.Fatal(err)
	}
	var made []*outputs.Output
	for i, in := range []outputs.AddInput{
		{Department: "marketing", Title: "Acme FAQ", Kind: "page", URL: "https://acme.example/faq", Issue: "ui/child", By: "agent-ui-child"},
		{Department: "marketing", Title: "Hero banner", Kind: "design", File: img, Job: "weekly-pass"},
		{Department: "product", Title: "Weekly report", Kind: "report", URL: "https://example.com/report"},
	} {
		o, err := outputs.Add(&a, in, now.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, o)
	}
	if _, err := outputs.Review(&a, made[1].ID, outputs.StateApproved, "ok", "", "owner", now); err != nil {
		t.Fatal(err)
	}
	return a, made
}

func TestCompanyShelf(t *testing.T) {
	a, _ := shelfProject(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/company?classic=1")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		// Marketing's room: 1 in review, 2 made this week, to its shelf.
		`<a class="wb-shelf-link" href="/p/agents/shelf?dept=marketing" data-testid="company.room.outputs"><span class="wb-chip wb-chip--you">📦 1 to review</span><span class="wb-shelf-week">2 made this week</span>`,
		// Product's line.
		`href="/p/agents/shelf?dept=product" data-testid="company.room.outputs"><span class="wb-chip wb-chip--you">📦 1 to review</span><span class="wb-shelf-week">1 made this week</span>`,
		// HQ: the total and the newest first.
		`data-testid="company.hq.outputs"`, `<b>Outputs</b><span class="wb-chip wb-chip--you">2 to review</span>`,
		// Waiting on you: a row per department with outputs in review.
		`href="/p/agents/shelf?dept=marketing&amp;state=review" data-testid="company.queue.outputs"`, `1 Marketing output</span>`,
		`1 Product output</span>`,
	)
	if i, j := strings.Index(html, "Weekly report"), strings.Index(html, "Acme FAQ"); i < 0 || j < 0 || i > j {
		t.Errorf("HQ lists the newest first: report at %d, FAQ at %d", i, j)
	}
}

func TestCompanyWithoutOutputs(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	a.Departments = []tracker.Department{{Name: "Marketing", Systems: []string{"UI"}}}
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/company?classic=1")
	for _, id := range []string{"company.room.outputs", "company.hq.outputs", "company.queue.outputs"} {
		if strings.Contains(html, id) {
			t.Errorf("no outputs, but the page has %s", id)
		}
	}
	_, status := getBody(t, ts.URL+"/p/agents/status")
	if strings.Contains(status, "outputs to review") {
		t.Error("no outputs, but the Status report lists outputs to review")
	}
}

func TestShelfPage(t *testing.T) {
	a, made := shelfProject(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/shelf")
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`data-testid="shelf.page"`, `Outputs shelf`,
		// Tabs with counts.
		`Marketing <b>2</b>`, `Product <b>1</b>`, `All <b>3</b>`, `review <b>2</b>`, `approved <b>1</b>`,
		// A URL output links out; its issue goes to the station.
		`<a class="wb-shelf-title" href="https://acme.example/faq" rel="noopener noreferrer" data-testid="shelf.title">Acme FAQ</a>`,
		`<a href="/p/agents/issue/ui/child" data-testid="shelf.issue">ui/child</a>`, `by agent-ui-child`,
		// A file output links to the shelf file and shows its thumbnail.
		`href="/p/agents/shelf/file/marketing/`+made[1].ID+`-hero.svg"`, `data-testid="shelf.thumb"`, `job weekly-pass`,
		// Rows in review carry Approve / Reject.
		`data-testid="shelf.approve"`, `data-testid="shelf.reject"`,
	)
	if n := strings.Count(html, `data-testid="shelf.approve"`); n != 2 {
		t.Errorf("approve buttons = %d, want 2 (the approved output has none)", n)
	}

	// Filters.
	_, html = getBody(t, ts.URL+"/p/agents/shelf?dept=marketing&state=review")
	if strings.Contains(html, "Weekly report") || strings.Contains(html, "Hero banner") || !strings.Contains(html, "Acme FAQ") {
		t.Errorf("marketing + review shows the wrong rows")
	}
	_, html = getBody(t, ts.URL+"/p/agents/shelf?state=published")
	assertContains(t, html, `data-testid="shelf.empty">No outputs match these filters.`)
}

func TestShelfEmpty(t *testing.T) {
	a := setupAgentsProject(t)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/shelf")
	assertContains(t, html, `Nothing on the shelf yet. An agent records what an issue or a job made with<code>issue-cli output add --issue &lt;slug&gt;`)
}

func TestShelfReview(t *testing.T) {
	a, made := shelfProject(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	loc := postForm(t, ts.URL+"/p/agents/shelf/review", url.Values{"id": {made[0].ID}, "verdict": {"approve"}, "dept": {"marketing"}, "state": {"review"}})
	if loc != "/p/agents/shelf?dept=marketing&state=review" {
		t.Errorf("redirect = %q", loc)
	}
	postForm(t, ts.URL+"/p/agents/shelf/review", url.Values{"id": {made[2].ID}, "verdict": {"reject"}})
	list, _ := outputs.Load(&a)
	got := map[string]*outputs.Output{}
	for _, o := range list {
		got[o.ID] = o
	}
	if o := got[made[0].ID]; o.State != outputs.StateApproved || o.ReviewedBy != "owner" || o.Quote != "approved on the shelf" {
		t.Errorf("approved = %+v", o)
	}
	if o := got[made[2].ID]; o.State != outputs.StateRejected || o.Quote != "rejected on the shelf" {
		t.Errorf("rejected = %+v", o)
	}
	// Nothing in review now: no chip, no waiting row.
	_, html := getBody(t, ts.URL+"/p/agents/company?classic=1")
	if strings.Contains(html, "to review</span>") || strings.Contains(html, "company.queue.outputs") {
		t.Error("the company page still shows outputs to review")
	}

	for _, form := range []url.Values{
		{"id": {made[0].ID}, "verdict": {"maybe"}},
		{"id": {"nope"}, "verdict": {"approve"}},
	} {
		resp, err := http.PostForm(ts.URL+"/p/agents/shelf/review", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%v: status %d, want 400", form, resp.StatusCode)
		}
	}
	// A cross-site POST is refused before it reaches the shelf.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/agents/shelf/review", strings.NewReader(url.Values{"id": {made[0].ID}, "verdict": {"reject"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin review: status %d, want 403", resp.StatusCode)
	}
}

func TestShelfFile(t *testing.T) {
	a, made := shelfProject(t)
	secret := filepath.Join(a.IssueDir, ".outputs", "outputs.jsonl")
	if _, err := os.Stat(secret); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/p/agents/shelf/file/marketing/" + made[1].ID + "-hero.svg")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("file: %d csp=%q", resp.StatusCode, resp.Header.Get("Content-Security-Policy"))
	}
	for _, path := range []string{"../outputs.jsonl", "..%2Foutputs.jsonl", "marketing/../../outputs.jsonl", "marketing", "nope.png"} {
		resp, err := http.Get(ts.URL + "/p/agents/shelf/file/" + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestStationAndStatusShowOutputs(t *testing.T) {
	a, _ := shelfProject(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/issue/ui/child")
	assertContains(t, html, `data-testid="station.outputs"`, `Outputs · 1`, `1 to review`, `Acme FAQ`)
	_, html = getBody(t, ts.URL+"/p/agents/issue/loner")
	if strings.Contains(html, `data-testid="station.outputs"`) {
		t.Error("an issue that made nothing shows an Outputs panel")
	}

	_, html = getBody(t, ts.URL+"/p/agents/status")
	assertContains(t, html, `<a href="/p/agents/shelf?dept=marketing&amp;state=review" data-testid="report.waiting.link">Marketing output to review</a>`, `Product output to review`)
	_, md := getBody(t, ts.URL+"/p/agents/status.md")
	assertContains(t, md, "- 1 Marketing output to review on the shelf (issue-cli outputs marketing --state review)")
}

// F-15 (docs/API/security.md): the check was lexical and os.Stat follows
// links, so a symlink under .outputs/files reached any file on the machine.
// A link that stays inside the shelf still serves.
func TestShelfFileRefusesSymlinkOut(t *testing.T) {
	a, made := shelfProject(t)
	dir := filepath.Join(a.IssueDir, ".outputs", "files")
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "out.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "marketing", made[1].ID+"-hero.svg"), filepath.Join(dir, "in.svg")); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	code, body := getBody(t, ts.URL+"/p/agents/shelf/file/out.txt")
	if code != http.StatusNotFound || strings.Contains(body, "SECRET") {
		t.Errorf("symlink out of the shelf: %d %q, want 404", code, body)
	}
	if code, _ := getBody(t, ts.URL+"/p/agents/shelf/file/in.svg"); code != http.StatusOK {
		t.Errorf("symlink inside the shelf: %d, want 200", code)
	}
}
