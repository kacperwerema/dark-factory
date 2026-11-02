package viewer

// The owner credential on loopback (owner.go, docs/API/security.md "Owner
// sign-in"): every O route refuses a local request without the owner's
// session cookie, forged browser headers or not; the owner's browser gets the
// cookie only by typing a one-time code the viewer showed outside HTTP.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ownerRoutes is every O row of docs/API/security.md's route inventory, as a
// request a stranger would send. TestOwnerRoutesListedInSecurityDoc keeps the
// two in step.
var ownerRoutes = []struct{ method, path, body string }{
	{"POST", "/p/test-project/issue/some-issue/approve", `{"status":"in progress"}`},
	{"POST", "/p/test-project/issue/System/some-issue/approve", `{"status":"in progress"}`},
	{"POST", "/p/test-project/needs-you/approve", `{"slugs":["some-issue"]}`},
	{"POST", "/p/test-project/issue/some-issue/send-back", `{"words":"x"}`},
	{"POST", "/p/test-project/issue/some-issue/yolo-review", `{"status":"done","verdict":"agree","words":"ok"}`},
	{"POST", "/p/test-project/issue/some-issue/clarify", `{"answers":[{"id":"Q1","option":1}]}`},
	{"POST", "/p/test-project/yolo/on", `{}`},
	{"POST", "/p/test-project/yolo/extend", `{}`},
	{"POST", "/p/test-project/yolo/off", `{}`},
	{"POST", "/p/test-project/yolo/review", `{"slug":"some-issue","verdict":"agree"}`},
	{"POST", "/p/test-project/autostart/pause", `{}`},
	{"POST", "/p/test-project/autostart/resume", `{}`},
	{"POST", "/p/test-project/autostart/off", `{}`},
	{"POST", "/p/test-project/owner-checks/tick", `{}`},
	{"POST", "/p/test-project/departments/add", `template=marketing`},
	{"POST", "/p/test-project/shelf/review", `{"id":"x","verdict":"approve"}`},
	{"POST", "/p/test-project/shelf/comment", `{"id":"x","text":"hi"}`},
	{"POST", "/p/test-project/shelf/comment/resolve", `{"id":"x","thread":"t"}`},
	{"POST", "/p/test-project/decisions/some-issue/D-1/confirm", `{}`},
	{"POST", "/p/test-project/decisions/some-issue/D-1/renew", `{"expires":"2099-01-01"}`},
	{"POST", "/p/test-project/decisions/some-issue/D-1/let-go", `{}`},
	{"POST", "/p/test-project/decisions/challenge/resolve", `{"ref":"some-issue#D-1"}`},
	{"POST", "/p/test-project/hall/station/backlog/edit/preview", `{}`},
	{"POST", "/p/test-project/hall/station/backlog/edit/save", `{}`},
	{"POST", "/p/test-project/health/recommendation/r1/file", `{}`},
	{"POST", "/p/test-project/health/recommendation/r1/dismiss", `{}`},
	{"POST", "/p/test-project/memory/edit", `{}`},
	{"POST", "/p/test-project/memory/delete", `{}`},
	{"POST", "/p/test-project/memory/pin", `{}`},
	{"POST", "/p/test-project/memory/unpin", `{}`},
	{"POST", "/p/test-project/postmortem/some-issue/decide", `{"answers":[]}`},
	{"POST", "/p/test-project/lead/file", `{}`},
	{"GET", "/p/test-project/lead/file/0123456789abcdef0123456789abcdef/notes.md", ``},
	{"POST", "/p/test-project/crew/lead", `{}`},
	{"POST", "/p/test-project/crew/stop", `{"session":"agent-x"}`},
	{"POST", "/p/test-project/crew/nudge", `{"session":"agent-x","text":"hi"}`},
	{"GET", "/p/test-project/crew/tail", ``},
	{"POST", "/p/test-project/queue/pause", `{}`},
	{"POST", "/p/test-project/queue/resume", `{}`},
	// Phone notifications: global O routes (ownerOnlyGlobal).
	{"POST", "/push/subscribe", `{}`},
	{"POST", "/push/change", `{}`},
	{"POST", "/push/unsubscribe", `{}`},
	{"POST", "/push/forget", ``},
	{"POST", "/push/settings", ``},
	{"POST", "/push/rebind", ``},
	{"POST", "/push/test", `{}`},
}

func ownerTestServer(t *testing.T) (*Server, tracker.Project) {
	t.Helper()
	proj, _ := setupTestProject(t)
	srv, err := NewServer([]tracker.Project{proj})
	if err != nil {
		t.Fatal(err)
	}
	return srv, proj
}

func ownerDo(srv *Server, method, path, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	ct := "application/json"
	if strings.HasPrefix(body, "template=") || strings.HasPrefix(body, "code=") || body == "" {
		ct = "application/x-www-form-urlencoded"
	}
	req.Header.Set("Content-Type", ct)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

// Every O route refuses a local request without the credential: no headers
// (curl), forged browser headers, and a cookie the viewer never issued.
func TestOwnerRoutesRefuseWithoutCredential(t *testing.T) {
	srv, _ := ownerTestServer(t)
	forged := map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Origin": "http://127.0.0.1:8080", "Referer": "http://127.0.0.1:8080/p/test-project/hall"}
	for _, rt := range ownerRoutes {
		for name, try := range map[string]struct {
			headers map[string]string
			cookies []*http.Cookie
		}{
			"curl":           {},
			"forged browser": {headers: forged},
			"made-up cookie": {headers: forged, cookies: []*http.Cookie{{Name: ownerCookie, Value: strings.Repeat("ab", 32)}}},
		} {
			t.Run(rt.path+"/"+name, func(t *testing.T) {
				rec := ownerDo(srv, rt.method, rt.path, rt.body, try.headers, try.cookies...)
				if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "/owner") {
					t.Errorf("%s %s = %d %q, want 403 naming /owner", rt.method, rt.path, rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// pairOwner runs the browser flow against srv and returns the session cookie.
func pairOwner(t *testing.T, srv *Server) *http.Cookie {
	t.Helper()
	var mu sync.Mutex
	var shown string
	srv.owner.deliver = func(code string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		shown = code
		return "test", nil
	}
	rec := ownerDo(srv, "POST", "/owner/code", "", map[string]string{"Origin": "http://127.0.0.1:8080"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /owner/code = %d %s", rec.Code, rec.Body)
	}
	mu.Lock()
	code := shown
	mu.Unlock()
	if code == "" {
		t.Fatal("no code was delivered")
	}
	if strings.Contains(rec.Body.String(), code) || strings.Contains(rec.Header().Get("Location"), code) {
		t.Fatal("the code came back in the HTTP answer")
	}
	rec = ownerDo(srv, "POST", "/owner/pair", "code="+url.QueryEscape(code), map[string]string{"Origin": "http://127.0.0.1:8080"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /owner/pair = %d %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == ownerCookie {
			return c
		}
	}
	t.Fatal("no owner cookie set")
	return nil
}

func TestOwnerPairSetsStrictHttpOnlyCookie(t *testing.T) {
	srv, _ := ownerTestServer(t)
	c := pairOwner(t, srv)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Errorf("cookie %+v, want HttpOnly, SameSite=Strict, Path=/", c)
	}
	if len(c.Value) < 64 {
		t.Errorf("cookie value %q too short", c.Value)
	}
	for _, rt := range ownerRoutes {
		rec := ownerDo(srv, rt.method, rt.path, rt.body, nil, c)
		if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), ownerRefused) {
			t.Errorf("%s %s with the owner cookie = gate 403", rt.method, rt.path)
		}
	}
	// Signing out ends the session.
	rec := ownerDo(srv, "POST", "/owner/signout", "", map[string]string{"Origin": "http://127.0.0.1:8080"}, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("signout = %d", rec.Code)
	}
	rec = ownerDo(srv, "POST", ownerRoutes[0].path, ownerRoutes[0].body, nil, c)
	if rec.Code != http.StatusForbidden {
		t.Errorf("after sign-out = %d, want 403", rec.Code)
	}
}

// The code is one use, few tries, short lived; a new one replaces the old.
func TestOwnerCodeLimits(t *testing.T) {
	srv, _ := ownerTestServer(t)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	srv.owner.now = func() time.Time { return now }
	var code string
	srv.owner.deliver = func(c string) (string, error) { code = c; return "test", nil }
	origin := map[string]string{"Origin": "http://127.0.0.1:8080"}
	pair := func(c string) int {
		return ownerDo(srv, "POST", "/owner/pair", "code="+c, origin).Code
	}

	ownerDo(srv, "POST", "/owner/code", "", origin)
	for i := 0; i < ownerCodeTries; i++ {
		if got := pair("00000000x"); got != http.StatusForbidden {
			t.Fatalf("wrong code = %d, want 403", got)
		}
	}
	if got := pair(code); got != http.StatusForbidden {
		t.Errorf("right code after %d wrong tries = %d, want 403 (code burnt)", ownerCodeTries, got)
	}

	now = now.Add(ownerCodeEvery)
	ownerDo(srv, "POST", "/owner/code", "", origin)
	now = now.Add(ownerCodeTTL + time.Second)
	if got := pair(code); got != http.StatusForbidden {
		t.Errorf("expired code = %d, want 403", got)
	}

	now = now.Add(ownerCodeEvery)
	ownerDo(srv, "POST", "/owner/code", "", origin)
	if got := pair(code); got != http.StatusSeeOther {
		t.Errorf("fresh code = %d, want 303", got)
	}
	if got := pair(code); got != http.StatusForbidden {
		t.Errorf("code used twice = %d, want 403", got)
	}
}

// Asking for codes in a loop (an agent's curl) doesn't show the owner a new
// dialog each time.
func TestOwnerCodeRateLimited(t *testing.T) {
	srv, _ := ownerTestServer(t)
	n := 0
	srv.owner.deliver = func(string) (string, error) { n++; return "test", nil }
	for i := 0; i < 5; i++ {
		ownerDo(srv, "POST", "/owner/code", "", map[string]string{"Origin": "http://127.0.0.1:8080"})
	}
	if n != 1 {
		t.Errorf("delivered %d codes in a burst, want 1", n)
	}
}

// The owner pages refuse cross-site posts like every write.
func TestOwnerPairRefusesCrossSite(t *testing.T) {
	srv, _ := ownerTestServer(t)
	rec := ownerDo(srv, "POST", "/owner/code", "", map[string]string{"Sec-Fetch-Site": "cross-site"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site code request = %d, want 403", rec.Code)
	}
}

// A verified owner on a public name is the owner: no second sign-in.
func TestOwnerVerifiedIdentityCounts(t *testing.T) {
	srv, _ := ownerTestServer(t)
	req := httptest.NewRequest("POST", "/p/test-project/yolo/off", strings.NewReader("{}"))
	if srv.fromOwner(req) {
		t.Fatal("a bare request is the owner")
	}
	req = req.WithContext(context.WithValue(req.Context(), identityKey{}, "owner@example.com"))
	if !srv.fromOwner(req) {
		t.Error("a verified identity is not the owner")
	}
}

// W, R and X routes stay open to issue-cli and agents.
func TestOwnerGateLeavesAgentRoutesOpen(t *testing.T) {
	srv, _ := ownerTestServer(t)
	for _, rt := range []struct{ method, path, body string }{
		{"GET", "/p/test-project/issues.json", ""},
		{"GET", "/p/test-project/issue/some-issue/approvals.json", ""},
		{"POST", "/p/test-project/issue/some-issue/comments", `{"text":"progress: x"}`},
		{"POST", "/p/test-project/issues/create", `{"title":"t"}`},
		{"POST", "/p/test-project/issue/some-issue/check-script", `{}`},
	} {
		rec := ownerDo(srv, rt.method, rt.path, rt.body, nil)
		if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), ownerRefused) {
			t.Errorf("%s %s hit the owner gate", rt.method, rt.path)
		}
	}
}

func TestOwnerPageShowsState(t *testing.T) {
	srv, _ := ownerTestServer(t)
	rec := ownerDo(srv, "GET", "/owner", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-testid="owner.code"`) {
		t.Fatalf("GET /owner = %d %s", rec.Code, rec.Body)
	}
	c := pairOwner(t, srv)
	rec = ownerDo(srv, "GET", "/owner", "", nil, c)
	if !strings.Contains(rec.Body.String(), `data-testid="owner.signed_in"`) {
		t.Errorf("signed-in page lacks the signed-in state: %s", rec.Body)
	}
}

// Every O row of docs/API/security.md's route inventory has a request in
// ownerRoutes, and every request in ownerRoutes is an O row.
func TestOwnerRoutesListedInSecurityDoc(t *testing.T) {
	doc, err := os.ReadFile(repoPath("docs", "API", "security.md"))
	if err != nil {
		t.Fatal(err)
	}
	tick := regexp.MustCompile("`([^`]+)`")
	var patterns []*regexp.Regexp
	var names []string
	for _, line := range strings.Split(string(doc), "\n") {
		cells := strings.Split(strings.ReplaceAll(line, `\|`, "\x00"), "|")
		if len(cells) < 5 || strings.TrimSpace(cells[3]) != "O" {
			continue
		}
		for _, m := range tick.FindAllStringSubmatch(cells[1], -1) {
			tok := strings.ReplaceAll(m[1], "\x00", "|")
			// a/b/on|extend|off → a/b/on, a/b/extend, a/b/off
			alts := strings.Split(tok, "|")
			base := alts[0][:strings.LastIndex(alts[0], "/")+1]
			for i, a := range alts {
				if i > 0 {
					a = base + a
				}
				re := regexp.QuoteMeta(a)
				re = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(re, `[^/]+`)
				re = strings.Replace(re, "…/", `issue/.+/`, 1)
				patterns = append(patterns, regexp.MustCompile("^"+re+"$"))
				names = append(names, a)
			}
		}
	}
	if len(patterns) < 10 {
		t.Fatalf("found %d O routes in docs/API/security.md, want the whole inventory", len(patterns))
	}
	covered := make([]bool, len(patterns))
	for _, rt := range ownerRoutes {
		rest, inProject := strings.CutPrefix(rt.path, "/p/test-project/")
		if inProject && !ownerOnlyRoute(rt.method, rest) {
			t.Errorf("%s is in ownerRoutes but ownerOnlyRoute doesn't gate it", rest)
		}
		if !inProject && !ownerOnlyGlobal(rt.method, rest) {
			t.Errorf("%s is in ownerRoutes but ownerOnlyGlobal doesn't gate it", rest)
		}
		hit := false
		for i, p := range patterns {
			if p.MatchString(rest) {
				covered[i], hit = true, true
			}
		}
		if !hit {
			t.Errorf("%s is gated but not an O row of docs/API/security.md", rest)
		}
	}
	for i, ok := range covered {
		if !ok {
			t.Errorf("O route %q of docs/API/security.md has no request in ownerRoutes", names[i])
		}
	}
}
