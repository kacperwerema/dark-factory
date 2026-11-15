package viewer

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/recent"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/workflowupgrade"
)

// updatesWorkflow is a v0.36.0 development workflow with a features:
// record and local CI, like batch-runner's.
const updatesWorkflow = `template: development@v0.36.0
features:
  installed:
    - status-prompts@v0.36.0
  declined:
    - scoring
statuses:
  - name: "idea"
    prompt: "Clarify it."
  - name: "done"
transitions:
  - from: "idea"
    to: "done"
ci:
  run:
    - cmd: make check
`

func updatesProject(t *testing.T) tracker.Project {
	t.Helper()
	proj, dir := setupTestProject(t)
	proj.WorkflowFile = filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(proj.WorkflowFile, []byte(updatesWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj
}

// postForm posts without following the redirect and returns its Location.
func postForm(t *testing.T, u string, form url.Values) string {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST %s: status %d", u, resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func TestUpdatesTabListsProposals(t *testing.T) {
	proj := updatesProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, body := getBody(t, ts.URL+"/p/test-project/updates?classic=1")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{
		`data-testid="updates.page"`,
		"development@v0.36.0",
		`data-testid="updates.state">behind`,
		`data-testid="updates.card.ci-test-results"`,
		`data-testid="updates.card.proof-links"`,
		`data-testid="updates.card.lead-prompt"`,
		`data-testid="nav.tab.updates.count"`,
		`data-testid="updates.declined.scoring"`,
		"Which CI commands write test results",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, not := range []string{`data-testid="updates.card.local-ci"`, `data-testid="updates.card.scoring"`} {
		if strings.Contains(body, not) {
			t.Errorf("should not list %q (installed or declined)", not)
		}
	}
	// Newest first: a v0.54.0 proposal comes before the v0.36.0 opt-ins.
	if strings.Index(body, "updates.card.ci-test-results") > strings.Index(body, "updates.card.worktree") {
		t.Error("v0.54.0 proposals should come before v0.36.0 ones")
	}

	_, filtered := getBody(t, ts.URL+"/p/test-project/updates?category=workbench&classic=1")
	if !strings.Contains(filtered, "updates.card.health-tuning") || strings.Contains(filtered, "updates.card.proof-links") {
		t.Error("category filter should keep workbench cards only")
	}
}

func TestUpdatesDeclineAndReopenWriteTheRecord(t *testing.T) {
	proj := updatesProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	loc := postForm(t, ts.URL+"/p/test-project/updates/decline", url.Values{"id": {"health-tuning"}})
	if loc != "/p/test-project/updates?declined=health-tuning" {
		t.Fatalf("redirect = %q", loc)
	}
	raw, _ := os.ReadFile(proj.WorkflowFile)
	if !strings.Contains(string(raw), "  declined:\n    - health-tuning\n    - scoring\n") && !strings.Contains(string(raw), "    - health-tuning") {
		t.Fatalf("health-tuning not declined:\n%s", raw)
	}
	if !strings.Contains(string(raw), "ci:\n  run:\n    - cmd: make check\n") {
		t.Fatalf("decline changed more than the record:\n%s", raw)
	}
	_, body := getBody(t, ts.URL+loc+"&classic=1")
	for _, want := range []string{`data-testid="updates.flash"`, "health-tuning declined", `data-testid="updates.undo"`, `updates-declined-row just`} {
		if !strings.Contains(body, want) {
			t.Errorf("after decline: missing %q", want)
		}
	}
	if strings.Contains(body, `data-testid="updates.card.health-tuning"`) {
		t.Error("declined card still listed")
	}

	loc = postForm(t, ts.URL+"/p/test-project/updates/reopen", url.Values{"id": {"health-tuning"}, "classic": {"1"}})
	if !strings.HasSuffix(loc, "&classic=1") {
		t.Errorf("a classic form should go back to the classic tab: %q", loc)
	}
	_, body = getBody(t, ts.URL+loc)
	if !strings.Contains(body, `data-testid="updates.card.health-tuning"`) || !strings.Contains(body, "re-opened") {
		t.Error("re-open should list the card again")
	}

	loc = postForm(t, ts.URL+"/p/test-project/updates/decline", url.Values{"id": {"no-such-feature"}})
	_, body = getBody(t, ts.URL+loc+"&classic=1")
	if !strings.Contains(body, "unknown feature id") {
		t.Errorf("unknown id: no error flash in %s", loc)
	}
}

func TestUpdatesInstallStartsAnAgentWithTheOperatorsWords(t *testing.T) {
	proj := updatesProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	var gotSession, gotPrompt string
	orig := dispatchAgentSession
	dispatchAgentSession = func(_ *tracker.Project, session, prompt, _ string, _ string, _ *tracker.WorkflowConfig) DispatchResponse {
		gotSession, gotPrompt = session, prompt
		return DispatchResponse{Status: "dispatched", Session: session}
	}
	t.Cleanup(func() { dispatchAgentSession = orig })

	loc := postForm(t, ts.URL+"/p/test-project/updates/install", url.Values{"id": {"ci-test-results"}, "words": {"make check writes trx"}})
	if loc != "/p/test-project/updates?installing=ci-test-results" {
		t.Fatalf("redirect = %q", loc)
	}
	if gotSession != "agent-test-project-install-ci-test-results" {
		t.Errorf("session = %q", gotSession)
	}
	for _, want := range []string{"Install workflow feature ci-test-results@v0.54.0", `"make check writes trx"`, "workflow scan --installed ci-test-results", "Commit only when they say so", "issue-cli --project test-project"} {
		if !strings.Contains(gotPrompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, gotPrompt)
		}
	}

	gotPrompt = ""
	loc = postForm(t, ts.URL+"/p/test-project/updates/install", url.Values{"id": {"scoring"}})
	if gotPrompt != "" || !strings.Contains(loc, "error=") {
		t.Errorf("a declined feature should not start an agent (redirect %q)", loc)
	}
}

func TestUpdatesShowsARunningInstall(t *testing.T) {
	proj := updatesProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	withMockTmuxSessions(t, []AgentSession{{Name: "agent-other"}, {Name: "agent-test-project-install-lead-prompt"}})

	_, body := getBody(t, ts.URL+"/p/test-project/updates?classic=1")
	if !strings.Contains(body, `data-testid="updates.card.lead-prompt.installing"`) || strings.Contains(body, `data-testid="updates.card.lead-prompt.decline"`) {
		t.Error("a running install shows its chip and hides Decline")
	}
}

func TestUpdatesHiddenWithoutATemplateWorkflow(t *testing.T) {
	t.Chdir(t.TempDir()) // no ./workflow.yaml to fall back to
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	code, _ := getBody(t, ts.URL+"/p/test-project/updates")
	if code != http.StatusNotFound {
		t.Errorf("built-in workflow: status %d, want 404", code)
	}
	_, list := getBody(t, ts.URL+"/p/test-project/")
	if strings.Contains(list, `data-testid="nav.tab.updates"`) {
		t.Error("built-in workflow should have no Updates tab")
	}
}

func TestUpdatesOverviewComparesProjects(t *testing.T) {
	t.Chdir(t.TempDir())
	a := updatesProject(t)
	b, _ := setupTestProject(t)
	b.Name, b.Slug = "Plain", "plain"
	ts := newTestServer(t, []tracker.Project{a, b})
	defer ts.Close()

	_, body := getBody(t, ts.URL+"/updates?classic=1")
	for _, want := range []string{`data-testid="updates.overview"`, `href="/p/test-project/updates"`, "development@v0.36.0", ">behind<", "built-in or custom", "1 installed · 1 declined"} {
		if !strings.Contains(body, want) {
			t.Errorf("classic overview missing %q", want)
		}
	}
	if strings.Index(body, "Test Project") > strings.Index(body, "Plain") {
		t.Error("a behind project should sort before a built-in one")
	}

	_, home := getBody(t, ts.URL+"/")
	if !strings.Contains(home, `href="/updates" data-testid="map.updates_link">Workflow updates (`) {
		t.Error("the map should link /updates with the count of updates to decide")
	}
}

func TestUpdateCardSummaryIsTheFirstSentence(t *testing.T) {
	c := UpdateCard{}
	c.What = "`systems:` overrides prompts, e.g. for\nCombat. The rest `here`."
	if got := string(c.SummaryHTML()); got != "<code>systems:</code> overrides prompts, e.g. for Combat." {
		t.Errorf("summary = %q", got)
	}
	if got := string(c.WhatHTML()); !strings.Contains(got, "The rest <code>here</code>.") {
		t.Errorf("what = %q", got)
	}
}

// A current stamp with nothing to propose but a feature the record misses
// is not up to date: the tab and the overview say "not recorded".
func TestUpdatesStaleRecordIsNotUpToDate(t *testing.T) {
	t.Chdir(t.TempDir())
	data, err := workflowupgrade.ReadTemplate("development")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := workflowupgrade.BuildPlan("workflow.yaml", data, "", false)
	if err != nil {
		t.Fatal(err)
	}
	declined := []string{"ci-test-results"}
	for _, id := range fresh.Undecided {
		if id != "local-ci" {
			declined = append(declined, id)
		}
	}
	wf := strings.Replace(string(data), "features:\n", "features:\n  declined: ["+strings.Join(declined, ", ")+"]\n", 1) + "\nci:\n  run:\n    - cmd: make check\n"
	proj, dir := setupTestProject(t)
	proj.WorkflowFile = filepath.Join(dir, "workflow.yaml")
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, body := getBody(t, ts.URL+"/p/test-project/updates?classic=1")
	if !strings.Contains(body, `data-testid="updates.state">not recorded`) {
		t.Errorf("state should be not recorded:\n%s", body)
	}
	_, overview := getBody(t, ts.URL+"/updates")
	if !strings.Contains(overview, `data-testid="updates_overview.card.test-project.state">not recorded<`) {
		t.Error("overview should say not recorded")
	}
}

// One Updates page asks tmux which install agents run with one listing, not
// a has-session per proposal. With the viewer's machine readings on (as
// viewer.Run turns them on), the page and the classic page each make at most
// 2 tmux calls (list-sessions and list-windows) however many cards they show;
// read afresh every time, they still make no has-session.
func TestUpdatesAsksTmuxOncePerRequest(t *testing.T) {
	proj := updatesProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "tmux.log")
	standin.Write(t, filepath.Join(bin, "tmux"), "printf '%s\\n' \"$*\" >> \"$UPDATES_TMUX_LOG\"\n[ \"$1\" = has-session ] && exit 1\nexit 0\n")
	t.Setenv("UPDATES_TMUX_LOG", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	calls := func(page string) []string {
		os.Remove(log)
		_, body := getBody(t, ts.URL+page)
		if cards := strings.Count(body, `.decline"`); cards < 3 {
			t.Fatalf("%s: %d cards, want several to count against", page, cards)
		}
		data, _ := os.ReadFile(log)
		if s := strings.TrimSpace(string(data)); s != "" {
			return strings.Split(s, "\n")
		}
		return nil
	}
	pages := []string{"/p/test-project/updates?classic=1", "/p/test-project/updates"}

	for _, page := range pages {
		for _, call := range calls(page) {
			if strings.HasPrefix(call, "has-session") {
				t.Errorf("%s asks tmux per name: %s", page, call)
			}
		}
	}

	recent.Keep(time.Minute)
	t.Cleanup(func() { recent.Keep(0) })
	for i, page := range pages {
		got := calls(page)
		if len(got) > 2 || i == 0 && len(got) == 0 {
			t.Errorf("%s: %d tmux calls, want 1 to 2:\n%s", page, len(got), strings.Join(got, "\n"))
		}
	}
}

// liveSessionNames answers like has-session: a session of its own, or in
// shared mode a window of the project's shared session only.
func TestLiveSessionNamesSharedMode(t *testing.T) {
	withMockTmuxSessions(t, []AgentSession{{Name: "agent-solo"}, {Name: "work:agent-a"}, {Name: "other:agent-b"}})
	own := liveSessionNames(&tracker.Project{Slug: "p"})
	if !own("agent-solo") || own("agent-a") {
		t.Error("own-session mode: only agent-solo is live")
	}
	shared := liveSessionNames(&tracker.Project{Slug: "p", TmuxSession: "work"})
	if !shared("agent-a") || shared("agent-b") || shared("agent-solo") {
		t.Error("shared mode: only work:agent-a is live")
	}
}
