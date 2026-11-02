package viewer

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The remote home: on the public name, / is Needs you across every project
// for a request the identity gate verified; loopback keeps the map.

const remoteHost = "factory.example.com"

// remoteSandbox is two projects (Acme with a clarify round, a question and
// a ready gate; Beta with nothing waiting) behind a viewer whose identity
// gate trusts the stand-in Access team f. No owner sign-in cookie: a
// public request is the owner only by its token.
func remoteSandbox(t *testing.T) (http.Handler, *accessFixture, string) {
	t.Helper()
	f := newAccessFixture(t)
	_, acme := deptSandbox(t)
	dir := filepath.Join(acme.Root(), "issues")
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("q.md", "---\ntitle: \"Questions\"\nslug: \"q\"\nstatus: \"in design\"\n---\n\n"+clarifyViewBody)
	write("sync.md", "---\ntitle: \"Sync\"\nslug: \"sync\"\nstatus: \"in progress\"\n---\n\nSync places.\n"+
		"<!-- issue-viewer-comments\n"+
		`{"id":1,"block":0,"date":"2026-10-30","text":"question: should sync include deleted places?","status":"open","source":"cli"}`+"\n-->\n")
	beta := tracker.Project{Name: "Beta", Slug: "beta", IssueDir: t.TempDir()}
	srv, err := NewServer([]tracker.Project{acme, beta})
	if err != nil {
		t.Fatal(err)
	}
	srv.identity = f.checker
	srv.allowedHosts = allowedHostsFor(defaultHost, []string{remoteHost})
	return srv.Routes(), f, dir
}

func remoteGet(t *testing.T, h http.Handler, path, host string, hdr map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// AC1: a verified owner token on the public name opens Needs you across
// projects at /, with the project switcher on top.
func TestRemoteHomeIsNeedsYouAcrossProjects(t *testing.T) {
	h, f, _ := remoteSandbox(t)
	code, body := remoteGet(t, h, "/", remoteHost, map[string]string{cfAccessHeader: f.token(t, nil)})
	if code != http.StatusOK {
		t.Fatalf("GET / with an owner token = %d %s", code, body)
	}
	for _, want := range []string{
		`data-testid="needs_you.page"`,
		`data-testid="needs_you.switch"`,
		`href="/p/acme/needs-you" data-testid="needs_you.switch.project"`,
		`href="/p/beta/needs-you" data-testid="needs_you.switch.project"`,
		`href="/?map=1" data-testid="needs_you.switch.map"`,
		`tap to act`,
		`data-kind="clarify" data-slug="q"`,
		`data-kind="question" data-slug="sync"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("remote home lacks %q", want)
		}
	}
	if strings.Contains(body, `data-testid="map.leads_link"`) {
		t.Error("remote home rendered the map")
	}
	// Beta has nothing waiting: in the switcher with 0, no group of its own.
	if !strings.Contains(body, `Beta <b>0</b>`) || strings.Contains(body, `data-project="beta"`) {
		t.Error("Beta: want a 0 chip and no group")
	}
}

// AC3: the same request without a token, with a broken one or with a
// non-owner's, is refused by the identity gate before the home is built.
func TestRemoteHomeRefusedWithoutAVerifiedOwner(t *testing.T) {
	h, f, _ := remoteSandbox(t)
	for name, hdr := range map[string]map[string]string{
		"no token":     nil,
		"garbage":      {cfAccessHeader: "a.b.c"},
		"not an owner": {cfAccessHeader: f.token(t, func(_, c map[string]any) { c["email"] = "someone@example.com" })},
		"expired":      {cfAccessHeader: f.token(t, func(_, c map[string]any) { c["exp"] = f.now.Add(-time.Hour).Unix() })},
		// A tunnel that rewrites Host to localhost still sends Cf-* headers.
		"tunnel to localhost": {"Cf-Ray": "abc"},
	} {
		host := remoteHost
		if name == "tunnel to localhost" {
			host = "localhost:8080"
		}
		for _, path := range []string{"/", "/?map=1", "/needs-you"} {
			code, body := remoteGet(t, h, path, host, hdr)
			if code != http.StatusForbidden || strings.Contains(body, "needs_you.page") {
				t.Errorf("%s: GET %s = %d, want 403", name, path, code)
			}
		}
	}
}

// AC5: loopback keeps the map at /, and ?map=1 opens the map on the public
// name.
func TestRemoteHomeLoopbackAndMapKeepTheMap(t *testing.T) {
	h, f, _ := remoteSandbox(t)
	for name, c := range map[string]struct {
		path, host string
		hdr        map[string]string
	}{
		"loopback /":      {"/", "localhost:8080", nil},
		"loopback 127":    {"/", "127.0.0.1:8080", nil},
		"public ?map=1":   {"/?map=1", remoteHost, map[string]string{cfAccessHeader: f.token(t, nil)}},
		"loopback ?map=1": {"/?map=1", "localhost:8080", nil},
	} {
		code, body := remoteGet(t, h, c.path, c.host, c.hdr)
		if code != http.StatusOK || !strings.Contains(body, `data-testid="map.leads_link"`) || strings.Contains(body, `data-testid="needs_you.page"`) {
			t.Errorf("%s: = %d, want the map", name, code)
		}
	}
}

// AC2 (model): each row kind carries its one-tap action: clarify questions
// with their options (recommended first), the open question's text and
// where a reply posts, the chat on every issue row.
func TestNeedsYouRowsCarryTheirOneTapActions(t *testing.T) {
	h, f, _ := remoteSandbox(t)
	_, body := remoteGet(t, h, "/p/acme/needs-you", remoteHost, map[string]string{cfAccessHeader: f.token(t, nil)})
	for _, want := range []string{
		`data-clarify-url="/p/acme/issue/q/clarify"`,
		`data-qid="Q1" data-qtext="Where does the owner answer?"`,
		`data-qid="Q2"`,
		`data-ny-opt="1" data-label="On the Station"`,
		`<span class="wb-ny-opt-c">Cost: one card</span>`,
		`data-testid="needs_you.question">question: should sync include deleted places?</blockquote>`,
		`data-ny-reply-form="/p/acme/issue/sync/comments"`,
		`data-testid="needs_you.reply">Reply</button>`,
		`href="/p/acme/lead" data-testid="needs_you.chat"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Needs you lacks %q", want)
		}
	}
	// /needs-you on the public name: its Map chip and crumb open the map,
	// not / (which is this list again).
	_, all := remoteGet(t, h, "/needs-you", remoteHost, map[string]string{cfAccessHeader: f.token(t, nil)})
	if !strings.Contains(all, `href="/?map=1" data-testid="needs_you.switch.map"`) || !strings.Contains(all, `href="/?map=1">Map</a>`) {
		t.Error("/needs-you on the public name: Map chip or crumb doesn't open the map")
	}
	// The project's own page has no switcher.
	if strings.Contains(body, `data-testid="needs_you.switch"`) {
		t.Error("a project's Needs you shows the switcher")
	}
}

func TestNYClarifyRecommendedFirstAndFallsBackToTheStation(t *testing.T) {
	q := func(id string, opts ...tracker.ClarifyOption) tracker.ClarifyQuestion {
		return tracker.ClarifyQuestion{ID: id, Text: "text " + id, Options: opts}
	}
	a := tracker.ClarifyOption{Num: 1, Label: "A"}
	b := tracker.ClarifyOption{Num: 2, Label: "B", Recommended: true}
	got := nyClarify([]tracker.ClarifyQuestion{q("Q1", a, b)})
	if len(got) != 1 || got[0].Options[0].Label != "B" || got[0].Options[1].Label != "A" {
		t.Fatalf("recommended first: %+v", got)
	}
	if nyClarify([]tracker.ClarifyQuestion{q("Q1", a), q("Q2", a), q("Q3", a), q("Q4", a)}) != nil {
		t.Error("4 questions answered in the row; want the Station")
	}
	if nyClarify([]tracker.ClarifyQuestion{q("Q1", a), q("Q2")}) != nil {
		t.Error("a question without options answered in the row; want the Station")
	}
	if nyClarify(nil) != nil {
		t.Error("no questions: want nil")
	}
}

// A clarify answer tapped on a Needs you row goes through the Station's
// route on the public name, quoting the option, and says where it came
// from; the same POST without a token is refused.
func TestNeedsYouClarifyAnswerOnThePublicName(t *testing.T) {
	h, f, dir := remoteSandbox(t)
	path := filepath.Join(dir, "q.md")
	answer := `{"from":"needs-you","answers":[{"id":"Q1","option":1,"words":"On the Station (tapped on Needs you)","text":"Where does the owner answer?"}]}`
	post := func(hdr map[string]string) int {
		req := httptest.NewRequest(http.MethodPost, "/p/acme/issue/q/clarify", strings.NewReader(answer))
		req.Host = remoteHost
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://"+remoteHost)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	before := string(mustRead(t, path))
	if code := post(nil); code != http.StatusForbidden {
		t.Fatalf("no token: %d, want 403", code)
	}
	if string(mustRead(t, path)) != before {
		t.Fatal("a refused answer changed the issue")
	}
	if code := post(map[string]string{cfAccessHeader: f.token(t, nil)}); code != http.StatusOK {
		t.Fatalf("owner token: %d", code)
	}
	comments, _ := tracker.LoadComments(path)
	if len(comments) != 1 || comments[0].Text != "clarify: the owner answered Q1 (D-1) on Needs you" {
		t.Errorf("comments = %+v", comments)
	}
	if body := string(mustRead(t, path)); !strings.Contains(body, `owner: "On the Station (tapped on Needs you)" (D-1)`) {
		t.Errorf("answer line doesn't quote the tapped option:\n%s", body)
	}
}
