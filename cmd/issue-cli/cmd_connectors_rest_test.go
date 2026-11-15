package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/connectors"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const analyticsAddr = "marketing/social-analytics"

// postizREST stands in for Postiz's public REST API on loopback: it answers
// only while the stand-in docker has Postiz up (502 before, as nginx does),
// and records every request it gets.
type postizREST struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string // "<method> <path?query> <Authorization>"
}

func (s *postizREST) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.seen)
}

const postAnalytics = `[{"label":"Impressions","data":[{"total":"120","date":"2026-11-10"}],"percentageChange":4}]`

func newPostizREST(t *testing.T, r *postizRun) *postizREST {
	s := &postizREST{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, req.Method+" "+req.URL.RequestURI()+" "+req.Header.Get("Authorization"))
		s.mu.Unlock()
		if !slices.Contains(r.up(), "postiz") {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		if req.Header.Get("Authorization") != fakeHeaderValue {
			http.Error(w, `{"msg":"Invalid API key"}`, http.StatusUnauthorized)
			return
		}
		switch req.URL.Path {
		case "/api/public/v1/analytics/post/p1":
			io.WriteString(w, postAnalytics)
		case "/api/public/v1/analytics/post/bsky":
			io.WriteString(w, `[]`)
		case "/api/public/v1/posts":
			io.WriteString(w, `{"posts":[{"id":"p1"},{"id":"bsky"}]}`)
		case "/api/public/v1/analytics/post/echo":
			io.WriteString(w, `{"msg":"key `+fakeHeaderValue+` is fine"}`)
		case "/api/public/v1/analytics/post/moved":
			http.Redirect(w, req, "/api/public/v1/integrations/connect", http.StatusFound)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// analyticsFixture is postizFixture plus marketing/social-analytics as the
// Marketing template writes it, uncommented, its url: at the stand-in REST
// server and its key from an env: reference (a subprocess can't see a
// fake Keychain). Its per_run: is the template's, byte for byte.
func analyticsFixture(t *testing.T) (*postizRun, *postizREST) {
	t.Helper()
	r := postizFixture(t)
	srv := newPostizREST(t, r)
	item := exampleItem(t, marketingExample, "social-analytics", "          ")
	for old, repl := range map[string]string{
		"url: http://127.0.0.1:4007/api\n": "url: " + srv.URL + "/api\n",
		`"keychain:postiz"`:                `"env:ACME_SEO"`,
	} {
		if !strings.Contains(item, old) {
			t.Fatalf("the social-analytics example has no %q:\n%s", old, item)
		}
		item = strings.Replace(item, old, repl, 1)
	}
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, string(b)+item)
	return r, srv
}

// runEvents are runs.jsonl's events for addr, oldest first.
func (r *postizRun) runEvents(addr string) []string {
	b, _ := os.ReadFile(filepath.Join(r.root, "issues", ".connectors", "runs.jsonl"))
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var rec connectors.RunRecord
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Connector == addr {
			out = append(out, rec.Event)
		}
	}
	return out
}

func analyticsExec(t *testing.T, paths ...string) (string, error) {
	t.Helper()
	return runCLI(t, append([]string{"connector", "exec", analyticsAddr, "--as-owner", "--"}, paths...)...)
}

// answers are exec's JSON lines.
func answers(t *testing.T, out string) []connectors.RestAnswer {
	t.Helper()
	var as []connectors.RestAnswer
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var a connectors.RestAnswer
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			t.Fatalf("not a JSON line: %q", line)
		}
		as = append(as, a)
	}
	return as
}

// AC1, AC4: the analytics connector reads Postiz's per-post numbers through
// its REST API from a stand-in server on loopback: one start of Postiz for
// every path, GET only, the key sent and never printed, stopped after. A
// 401 and a redirect are failures that still stop Postiz, and no method
// but GET exists in the client.
func TestRestConnectorReadsOnlyGET(t *testing.T) {
	r, srv := analyticsFixture(t)
	out, err := analyticsExec(t, "/public/v1/posts?startDate=2026-11-01&endDate=2026-11-08", "/public/v1/analytics/post/p1?date=7", "/public/v1/analytics/post/bsky?date=7")
	if err != nil {
		t.Fatalf("exec: %v\n%s", err, out)
	}
	as := answers(t, out)
	if len(as) != 3 {
		t.Fatalf("got %d answers, want 3:\n%s", len(as), out)
	}
	if as[1].Path != "/public/v1/analytics/post/p1?date=7" || as[1].Status != 200 || string(as[1].Body) != postAnalytics {
		t.Errorf("per-post answer = %+v", as[1])
	}
	if string(as[2].Body) != "[]" || !as[2].OK() {
		t.Errorf("a network without analytics = %+v, want 200 []", as[2])
	}
	for _, req := range srv.requests() {
		if !strings.HasPrefix(req, "GET /api/public/v1/") || !strings.HasSuffix(req, " "+fakeHeaderValue) {
			t.Errorf("the stand-in got %q, want only GETs under /api/public/v1/ with the key", req)
		}
	}
	if n := len(srv.requests()); n != 3 {
		t.Errorf("the stand-in got %d requests, want 3: %v", n, srv.requests())
	}
	if strings.Count(r.dockerLog(), " start") != 1 {
		t.Errorf("Postiz started %d times for three paths, want once:\n%s", strings.Count(r.dockerLog(), " start"), r.dockerLog())
	}
	if up := r.up(); len(up) != 0 {
		t.Errorf("Postiz still up after the call: %v", up)
	}
	if got := r.runEvents(analyticsAddr); !slices.Equal(got, []string{connectors.RunStarted, connectors.RunStopped}) {
		t.Errorf("runs.jsonl = %v, want started, stopped", got)
	}

	t.Run("a key the answer repeats is not printed", func(t *testing.T) {
		out, err := analyticsExec(t, "/public/v1/analytics/post/echo")
		if err != nil || strings.Contains(out, fakeHeaderValue) || !strings.Contains(out, "[secret]") {
			t.Errorf("echo: %v\n%s", err, out)
		}
	})
	t.Run("a redirect is not followed", func(t *testing.T) {
		before := len(srv.requests())
		out, err := analyticsExec(t, "/public/v1/analytics/post/moved")
		if err == nil || !strings.Contains(out, `"status":302`) || !strings.Contains(out, "a redirect, not followed") {
			t.Errorf("redirect: %v\n%s", err, out)
		}
		if got := srv.requests()[before:]; len(got) != 1 {
			t.Errorf("the redirect was followed: %v", got)
		}
		if len(r.up()) != 0 {
			t.Error("Postiz still up after a failed read")
		}
	})
	t.Run("a refused key names its reference, never the value", func(t *testing.T) {
		t.Setenv("ACME_SEO", "wrong-key-value-9d2e")
		out, err := analyticsExec(t, "/public/v1/analytics/post/p1")
		if err == nil || !strings.Contains(out, "refused the key in header Authorization: env:ACME_SEO") || strings.Contains(out, "wrong-key-value-9d2e") {
			t.Errorf("401: %v\n%s", err, out)
		}
		if len(r.up()) != 0 {
			t.Error("Postiz still up after a refused key")
		}
	})
	t.Run("no method but GET is in the client", func(t *testing.T) {
		src, err := os.ReadFile(filepath.Join("..", "..", "internal", "connectors", "rest.go"))
		if err != nil {
			t.Fatal(err)
		}
		if m := regexp.MustCompile(`Method(Post|Put|Patch|Delete|Connect|Options|Trace)|"(POST|PUT|PATCH|DELETE)"`).Find(src); m != nil {
			t.Errorf("rest.go names %s: a kind: rest connector reads with GET only", m)
		}
	})
}

// AC1: a call that isn't a GET of an allowed path is refused before Postiz
// starts: a method word, a path outside get:, one leaving the base (.., //,
// an absolute URL, an encoded slash), and no path at all.
func TestRestConnectorRefusesPathsBeforeStart(t *testing.T) {
	r, srv := analyticsFixture(t)
	for _, call := range [][]string{
		{"-X", "POST", "/public/v1/posts"},
		{"POST", "/public/v1/posts"},
		{"/public/v1/analytics/post/p1", "/public/v1/integrations/connect"},
		{"/public/v1/analytics/post/../../integrations/connect"},
		{"/public/v1/analytics/post/%2e%2e/x"},
		{"/public/v1/analytics/post/a%2Fb"},
		{"//evil.example/public/v1/posts"},
		{"http://evil.example/public/v1/posts"},
		{"/public/v1/posts#x"},
		{},
	} {
		out, err := analyticsExec(t, call...)
		if err == nil {
			t.Errorf("%q passed:\n%s", call, out)
			continue
		}
		want := "connector marketing/social-analytics reads only GET /public/v1/analytics/post/*, /public/v1/analytics/*, /public/v1/posts, /public/v1/integrations"
		if len(call) == 0 {
			want = "usage: issue-cli connector exec marketing/social-analytics -- <path> [<path>...]"
		}
		if !strings.Contains(out, want) {
			t.Errorf("%q: the refusal doesn't name the allowed paths:\n%s", call, out)
		}
	}
	if got := srv.requests(); len(got) != 0 {
		t.Errorf("refused calls reached the server: %v", got)
	}
	if log := r.dockerLog(); log != "" {
		t.Errorf("refused calls started Postiz:\n%s", log)
	}
}

// AC2: the analytics connector and the social one share Postiz's lease
// (per_run: tool: postiz): while a social session holds it, an analytics
// call reads without starting it again and leaves it up; the social run,
// the last one out, stops it.
func TestRestConnectorSharesThePostizLease(t *testing.T) {
	r, srv := analyticsFixture(t)
	cmd, in := r.startExec()
	out, err := analyticsExec(t, "/public/v1/analytics/post/p1?date=7")
	if err != nil || len(answers(t, out)) != 1 || !answers(t, out)[0].OK() {
		t.Fatalf("exec while social holds Postiz: %v\n%s", err, out)
	}
	if n := strings.Count(r.dockerLog(), " start"); n != 1 {
		t.Errorf("Postiz started %d times, want once (the analytics call shares the social run's start):\n%s", n, r.dockerLog())
	}
	if !slices.Contains(r.up(), "postiz") {
		t.Fatal("the analytics call stopped Postiz while the social session still holds it")
	}
	if got := r.runEvents(analyticsAddr); len(got) != 0 {
		t.Errorf("the analytics call logged %v: it neither started nor stopped the tool", got)
	}
	in.Close()
	r.waitExit(cmd)
	r.assertDown(connectors.RunStarted, connectors.RunStopped)
	if len(srv.requests()) != 1 {
		t.Errorf("requests = %v", srv.requests())
	}
}

// issue-cli connectors lists a rest connector's paths and how to call it.
func TestConnectorsListsARestConnectorsPaths(t *testing.T) {
	analyticsFixture(t)
	out, err := runCLI(t, "connectors", "marketing")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertContains(t, out, "marketing/social-analytics (rest) · reads GET /public/v1/analytics/post/*, /public/v1/analytics/*, /public/v1/posts, /public/v1/integrations (connector exec marketing/social-analytics -- <path>...) · starts per run")
	assertContains(t, out, "  header Authorization: env:ACME_SEO set")
}

// AC3: department add marketing writes social-analytics commented; once
// uncommented it is a rest connector on Postiz's API on loopback, reading
// the analytics, posts and channels routes only, its key a keychain:
// reference, sends: false, on social's lease with social's own start:,
// ready: and stop:. The social type's measure prompt names it. Lint is
// clean.
func TestMarketingSocialAnalyticsExampleIsRestPerRun(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(cfg)
	assertContains(t, out.String(), "#   - name: social-analytics")
	assertContains(t, string(raw), "#   - name: social-analytics")

	uncommentConnectors(t, cfg)
	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("marketing")
	byName := map[string]*tracker.Connector{}
	for i := range d.Connectors {
		byName[d.Connectors[i].Name] = &d.Connectors[i]
	}
	c, social := byName["social-analytics"], byName["social"]
	if c == nil || social == nil {
		t.Fatalf("uncommented: social %v, social-analytics %v", social, c)
	}
	if c.Kind != tracker.ConnectorRest || c.URL != "http://127.0.0.1:4007/api" || c.Command != "" || c.Sends || c.Exception != postizException {
		t.Errorf("social-analytics = %+v", *c)
	}
	if len(c.Headers) != 1 || c.Headers["Authorization"] != "keychain:postiz" || len(c.Secrets) != 0 {
		t.Errorf("headers %v secrets %v, want only Authorization: keychain:postiz", c.Headers, c.Secrets)
	}
	if got := connectors.RestPatterns(c); !slices.Equal(got, []string{"/public/v1/analytics/post/*", "/public/v1/analytics/*", "/public/v1/posts", "/public/v1/integrations"}) {
		t.Errorf("get: %v", got)
	}
	for _, ok := range []string{"/public/v1/analytics/post/p1?date=7", "/public/v1/analytics/ch1?date=30"} {
		if _, err := connectors.RestPath(analyticsAddr, c, ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, write := range []string{"/public/v1/upload", "/public/v1/integrations/connect", "/public/v1/posts/p1/status", "/public/v1/credits/reset", "/public/v1/social/x"} {
		if _, err := connectors.RestPath(analyticsAddr, c, write); err == nil {
			t.Errorf("%s passes get: (a write route's path)", write)
		}
	}
	if c.PerRun == nil || social.PerRun == nil || *c.PerRun != *social.PerRun || c.PerRun.Tool != "postiz" {
		t.Errorf("per_run: %+v, want social's own %+v", c.PerRun, social.PerRun)
	}

	wf := readTemplate(t, filepath.Join("templates", "workflow", "marketing.yaml"))
	i := strings.Index(wf, "social:\n    description:")
	j := strings.Index(wf[i:], "    transitions:")
	if i < 0 || j < 0 || !strings.Contains(wf[i:i+j], "the department's `social-analytics` connector: it only reads") {
		t.Error("the social type's measure prompt doesn't name social-analytics")
	}

	for _, f := range []string{"brand.md", "icp.md"} {
		mustWrite(t, filepath.Join(proj.Root(), "departments", "marketing", f), "# settled\n")
	}
	slug, _, _ := strings.Cut(strings.TrimPrefix(postizException, "factory/"), "#")
	os.MkdirAll(filepath.Join(proj.IssueDir, "Factory"), 0755)
	mustWrite(t, filepath.Join(proj.IssueDir, "Factory", "partners.md"), "---\ntitle: \"Partners\"\nstatus: \"done\"\ncreated: \"2026-11-02\"\nslug: \""+slug+"\"\n---\n## Decisions\n"+
		"- **D-4** · 2026-11-02 · CEO chat: Postiz is accepted as the licence exception, \"Postiz can be exception for now.\"\n")
	proj = reloadDeptProject(t, cfg)
	if s := socialLint(t, proj); s != "" {
		t.Errorf("uncommented: lint findings:\n%s", s)
	}
}

func readTemplate(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// AC5: docs/departments.md describes the analytics connector where the
// social channel's measure step is, and kind: rest under Connectors.
func TestSocialAnalyticsDocumented(t *testing.T) {
	doc := readTemplate(t, filepath.Join("..", "..", "docs", "departments.md"))
	for _, want := range []string{
		"measure reads each post's numbers through the `social-analytics` connector",
		"- name: social-analytics",
		"kind: rest",
		"`get:`",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/departments.md lacks %q", want)
		}
	}
	if strings.Contains(doc, "its MCP has no analytics tool, so measure writes \"no value yet\" for reach") {
		t.Error("docs/departments.md still says measure has no analytics source")
	}
}
