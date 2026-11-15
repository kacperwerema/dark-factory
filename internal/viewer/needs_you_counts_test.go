package viewer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/bench"
	"github.com/kacperwerema/dark-factory/internal/ci"
	"github.com/kacperwerema/dark-factory/internal/postmortem"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// needsYouEveryKind are the kinds of Needs you row, each of which the fixture
// below holds at least once. A new kind goes here and in the fixture.
var needsYouEveryKind = []string{
	"ci", "gate", "partner", "playtest", "checks", "question", "clarify",
	"challenge", "decision", "outputs", "job", "connectors", "nightly",
	"postmortem", "bugs",
}

// everyKindProject is the Status fixture with one thing of every kind
// waiting on the owner, and a done issue whose newest comment is a question
// (closed: it waits on nobody).
func everyKindProject(t *testing.T) tracker.Project {
	t.Helper()
	now := time.Now()
	proj := setupStatusProject(t, now)
	root := proj.Root()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// CI red.
	wf, err := os.ReadFile(proj.WorkflowFile)
	if err != nil {
		t.Fatal(err)
	}
	write("workflow.yaml", string(wf)+ciBlockOn)
	if err := ci.WriteState(ciRootFor(&proj), redState()); err != nil {
		t.Fatal(err)
	}
	// A check script to run, an output to review, a job due, a connector
	// missing its secret, a partner stage awaiting the owner.
	write("scripts/checks/judged.sh", "#!/usr/bin/env bash\n# issue: ui/judged\n")
	write("issues/.outputs/outputs.jsonl", `{"op":"add","id":"o-post","date":"2026-10-28T10:00:00+01:00","department":"marketing","title":"Launch post","kind":"post","url":"https://example.test/p"}`+"\n")
	write("departments/marketing/leads.md", "# Lead search\n\nSearch.\n")
	t.Setenv("EVERY_KIND_MISSING", "")
	cfg := omPartner(t)
	proj.Departments = []tracker.Department{
		{
			Name: "Marketing", Systems: []string{"Marketing"},
			Jobs:       []tracker.Job{{ID: "lead-search", Name: "Daily lead search", Every: "day", Brief: "departments/marketing/leads.md"}},
			Connectors: []tracker.Connector{{Name: "qb", Kind: "tool", Command: "qb", Secrets: map[string]string{"QB_TOKEN": "env:EVERY_KIND_MISSING"}}},
		},
		{Name: "Video", Systems: []string{"Video"}, Partner: &cfg},
	}
	write("issues/Video/cut.md", "---\ntitle: \"Cut\"\nstatus: \"in progress\"\nsystem: \"Video\"\npartner_project: \"waiting\"\n---\n\nBody.\n")
	// A clarify round, two hard decisions (one expired, one challenged), a
	// post-mortem, a closed question.
	write("issues/UI/clarified.md", "---\ntitle: \"Clarified\"\nstatus: \"in progress\"\n---\n\n"+clarifyViewBody+"\n")
	expired := now.AddDate(0, 0, -1).Format("2006-01-02")
	write("issues/UI/rules.md", "---\ntitle: \"Rules\"\nstatus: \"backlog\"\n---\n\n## Decisions\n\n"+
		"- **D-1** · 2026-10-01 · chat: Freeze the map, \"freeze it\" {scope: project; hard; expires: "+expired+"}\n"+
		"- **D-2** · 2026-10-02 · chat: Two lanes only, \"two lanes\" {scope: project; hard}\n")
	openTestChallenge(t, proj, "ui/rules#D-2", "ui/high", "three lanes fit")
	if _, err := postmortem.Save(filepath.Join(root, "retros"), postmortem.Meta{Issue: "ui/shipped", Title: "Shipped", Status: "done", System: "UI"}, pmRetroBody, now); err != nil {
		t.Fatal(err)
	}
	write("issues/UI/closed.md", "---\ntitle: \"Closed\"\nstatus: \"done\"\n---\n\nBody.\n"+
		"<!-- issue-viewer-comments\n{\"id\":1,\"block\":0,\"date\":\"2026-10-18\",\"text\":\"question: too late?\",\"status\":\"open\",\"source\":\"cli\"}\n-->\n")
	// A nightly pass rate under last week's median.
	t.Setenv("ISSUE_VIEWER_BENCH_DIR", t.TempDir())
	b := bench.New("factory-v1")
	b.Tasks = []bench.Task{{Project: proj.Slug, Slug: "a"}}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	d1 := now.AddDate(0, 0, -5)
	for i, passed := range []int{3, 3, 2, 4, 2} {
		if err := bench.AppendNight(nightAt(d1.AddDate(0, 0, i), passed, 10, 12)); err != nil {
			t.Fatal(err)
		}
	}
	return proj
}

// wantCount fails unless body holds marker with n in it.
func wantCount(t *testing.T, path, body, marker string, n int) {
	t.Helper()
	if want := fmt.Sprintf(marker, n); !strings.Contains(body, want) {
		got := regexp.MustCompile(regexp.QuoteMeta(strings.SplitN(marker, "%d", 2)[0]) + `\d+`).FindString(body)
		t.Errorf("%s: want %q, page has %q", path, want, got)
	}
}

const (
	headerCount = `<span class="wb-you-mark">!</span>%d<span class="wb-you-words"> waiting on you</span>`
	floorCount  = `<span class="wb-fl-you-mark">!</span>%d<span class="wb-you-words"> waiting on you</span>`
)

// Proves: AC2 — one row of every kind, one number on every page that says
// how much waits on the owner: the project's pages say the length of
// needsYouRows, the pages across projects say the sum with unsent bug
// reports once. A copy with its own count fails here on the kind it skips.
func TestNeedsYouEveryKindCountsEverywhere(t *testing.T) {
	proj := everyKindProject(t)
	other := setupStatusProject(t, time.Now())
	other.Name, other.Slug = "Other", "other"
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	stubLeadPane(t, "")
	stubLeadChat(t, true)
	prev := unsentBugReports
	unsentBugReports = func() int { return 2 }
	t.Cleanup(func() { unsentBugReports = prev })
	ts := newTestServer(t, []tracker.Project{proj, other})
	defer ts.Close()

	var ny NeedsYouJSON
	_, body := getResp(t, ts.URL+"/p/status/needs-you.json")
	if err := json.Unmarshal([]byte(body), &ny); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, r := range ny.Rows {
		kinds[r.Kind] = true
	}
	for _, k := range needsYouEveryKind {
		if !kinds[k] {
			t.Errorf("the fixture has no %q row: %+v", k, kinds)
		}
	}
	n := ny.Count
	if n != len(ny.Rows) {
		t.Fatalf("count %d, %d rows", n, len(ny.Rows))
	}

	get := func(path string) string {
		t.Helper()
		code, page := getBody(t, ts.URL+path)
		if code != 200 {
			t.Fatalf("GET %s: %d\n%s", path, code, page)
		}
		return page
	}
	for _, c := range []struct{ path, marker string }{
		{"/p/status/needs-you", `data-testid="needs_you.count">%d<`},
		{"/p/status/?classic=1", `data-testid="nav.tab.status.count">%d<`},
		{"/p/status/status", headerCount},
		{"/p/status/status", `<div class="wb-ccard-num">%d</div>`},
		{"/p/status/status?classic=1", `data-testid="status.waiting.count">%d<`},
		{"/p/status/status.md", "## Waiting on you (%d)"},
		{"/p/status/company", floorCount},
		{"/p/status/company?classic=1", headerCount},
		{"/p/status/hall", headerCount},
		{"/p/status/lead", `data-testid="lead_chat.waiting.count">Waiting on you: %d<`},
		{"/p/status/lead", headerCount},
		{"/p/status/lead?part=live", `data-testid="lead_chat.waiting.count">Waiting on you: %d<`},
		{"/p/status/updates", headerCount},
	} {
		wantCount(t, c.path, get(c.path), c.marker, n)
	}

	// What each list shows adds up to what it says.
	md := get("/p/status/status.md")
	section := md[strings.Index(md, "## Waiting on you"):]
	section = section[:strings.Index(section, "\n## Working on")]
	if got := strings.Count(section, "\n- "); got != n {
		t.Errorf("status.md lists %d lines under a count of %d:\n%s", got, n, section)
	}
	_, lead := getBody(t, ts.URL+"/p/status/lead.json?waiting=1")
	var chat LeadChatModel
	if err := json.Unmarshal([]byte(lead), &chat); err != nil {
		t.Fatal(err)
	}
	if len(chat.Waiting) != n {
		t.Errorf("lead.json has %d rows, the list %d", len(chat.Waiting), n)
	}
	classic := get("/p/status/status?classic=1")
	for _, k := range []string{"partner", "clarify", "challenge", "decision", "outputs", "job", "connectors", "nightly", "postmortem"} {
		if !strings.Contains(classic, `data-testid="status.waiting.other.`+k+`.`) {
			t.Errorf("classic Status does not list the %s row", k)
		}
	}
	if s := get("/p/status/status"); strings.Contains(s, "Nothing waits on you") {
		t.Error("Report says nothing waits")
	}

	// Across projects: the other project's rows, its bug reports row once.
	var overRows NeedsYouJSON
	_, body = getResp(t, ts.URL+"/p/other/needs-you.json")
	if err := json.Unmarshal([]byte(body), &overRows); err != nil {
		t.Fatal(err)
	}
	total := n + overRows.Count - 1
	for _, path := range []string{"/", "/needs-you", "/updates", "/crew", "/leads"} {
		wantCount(t, path, get(path), headerCount, total)
	}
}

// A done issue's open question waits on nobody: Status and the list agree.
func TestStatusNeedsYouLeavesClosedIssuesOut(t *testing.T) {
	proj := setupStatusProject(t, time.Now())
	stubAgentsGit(t)
	path := filepath.Join(proj.IssueDir, "UI", "closed.md")
	if err := os.WriteFile(path, []byte("---\ntitle: \"Closed\"\nstatus: \"done\"\n---\n\nBody.\n"+
		"<!-- issue-viewer-comments\n{\"id\":1,\"block\":0,\"date\":\"2026-10-18\",\"text\":\"question: too late?\",\"status\":\"open\",\"source\":\"cli\"}\n-->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	m := buildStatus(&proj, issues, nil, time.Now())
	if want := len(needsYouRows(&proj, "", "", openOnly(&proj, issues), time.Now())); m.WaitingCount() != want {
		t.Errorf("WaitingCount = %d, list = %d", m.WaitingCount(), want)
	}
}

// The chat's poll reuses its rows for liveWaitingTTL; the page reads fresh.
func TestLeadWaitingLiveReusesRowsForAWhile(t *testing.T) {
	proj := setupStatusProject(t, time.Now())
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	srv, err := NewServer([]tracker.Project{proj})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first := len(srv.leadWaitingLive(&proj, "/p/status", now))
	body := "---\ntitle: \"Later\"\nstatus: \"in progress\"\n---\n\nBody.\n" +
		"<!-- issue-viewer-comments\n{\"id\":1,\"block\":0,\"date\":\"2026-10-18\",\"text\":\"question: and this?\",\"status\":\"open\",\"source\":\"cli\"}\n-->\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "UI", "later.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := len(srv.leadWaitingLive(&proj, "/p/status", now.Add(liveWaitingTTL-time.Second))); got != first {
		t.Errorf("inside the window: %d rows, want the kept %d", got, first)
	}
	if got := len(srv.leadWaitingLive(&proj, "/p/status", now.Add(liveWaitingTTL))); got != first+1 {
		t.Errorf("after the window: %d rows, want %d", got, first+1)
	}
	if got := len(leadWaiting(&proj, "/p/status", now)); got != first+1 {
		t.Errorf("the page's read: %d rows, want %d", got, first+1)
	}
}

// waitingCountCopy is what a page must not write to get a waiting number:
// the issue rows alone leave CI, the shelf, jobs and the rest out.
var waitingCountCopy = regexp.MustCompile(`len\(\s*waitingOnOwner\(`)

// Proves: AC2 — the count stays one count: no file but needs_you.go takes
// the length of waitingOnOwner, and WaitingCount stays the list's length.
func TestNoPageCountsWaitingOnOwnerItself(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "needs_you.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			if waitingCountCopy.MatchString(line) {
				t.Errorf("%s counts waitingOnOwner itself: use needsYouRows (or needsYouIssueRows for one system's slice): %s", f, strings.TrimSpace(line))
			}
		}
	}
	m := &StatusModel{NeedsYou: make([]WbQueueCard, 7), Waiting: make([]WaitingItem, 2), UnsentBugReports: 3}
	if m.WaitingCount() != 7 {
		t.Errorf("WaitingCount = %d: it is the length of the rows, nothing on top", m.WaitingCount())
	}
}
