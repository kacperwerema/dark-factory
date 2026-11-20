package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// cmsHelper is the cms connector's Ghost helper as the marketing template
// ships it.
var cmsHelper = filepath.Join("templates", "departments", "marketing", "cms-ghost.sh")

// The stand-in Ghost's Admin API key: <id>:<hex secret>, as Ghost gives it.
const (
	cmsKeyID     = "6750c0ffee0000000000abcd"
	cmsKeySecret = "0f1e2d3c4b5a69788796a5b4c3d2e1f00112233445566778899aabbccddeeff0"
)

// cmsMarkdown is a piece whose bytes a conversion would change: trailing
// spaces (a markdown line break), a tab, a CRLF, a non-ASCII run, HTML, a
// backslash, quotes, and no final newline.
const cmsMarkdown = "# Hello from the factory  \n\nA line with a\ttab, a \"quote\" and a \\ backslash.\r\n\n- **bold** and _emphasis_ ✓ zażółć 🚀\n- <b>raw html</b> &amp;\n\n```sh\necho '$HOME'\n```\n\nLast line, no newline"

// ghostRequest is one request the stand-in received.
type ghostRequest struct {
	Method, Path, Query string
	Body                []byte
}

// standInGhost is a Ghost Admin API on loopback: posts and pages created,
// read by id or slug, and updated with the current updated_at (409 on a
// stale one). Every request's JWT is verified against the test key, and a
// request with any query string (?newsletter= among them) fails the test.
type standInGhost struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex
	// reqs are the requests in the order they came.
	reqs []ghostRequest
	// items are posts and pages by collection, then id.
	items map[string]map[string]map[string]any
	next  int
	// raceAfterRead makes each read by id look like someone else saved
	// right after it: the item's updated_at moves on.
	raceAfterRead bool
}

// item is a copy of the stand-in's post or page, nil when it has none.
func (g *standInGhost) item(coll, id string) map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	it, ok := g.items[coll][id]
	if !ok {
		return nil
	}
	out := map[string]any{}
	for k, v := range it {
		out[k] = v
	}
	return out
}

func newStandInGhost(t *testing.T) *standInGhost {
	t.Helper()
	g := &standInGhost{t: t, items: map[string]map[string]map[string]any{"posts": {}, "pages": {}}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *standInGhost) requests() []ghostRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]ghostRequest(nil), g.reqs...)
}

func ghostAnswer(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func ghostError(w http.ResponseWriter, code int, msg string) {
	ghostAnswer(w, code, map[string]any{"errors": []map[string]any{{"message": msg}}})
}

// checkJWT verifies the request's "Authorization: Ghost <jwt>": HS256,
// kid the key's id, aud /admin/, five minutes, iat now, signed with the
// key's secret.
func checkJWT(r *http.Request) error {
	auth, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Ghost ")
	if !ok {
		return fmt.Errorf("authorization %q is not Ghost <jwt>", r.Header.Get("Authorization"))
	}
	parts := strings.Split(auth, ".")
	if len(parts) != 3 {
		return fmt.Errorf("jwt has %d parts", len(parts))
	}
	enc := base64.RawURLEncoding
	var head struct{ Alg, Typ, Kid string }
	var claims struct {
		Iat, Exp int64
		Aud      string
	}
	for i, v := range []any{&head, &claims} {
		raw, err := enc.DecodeString(parts[i])
		if err != nil {
			return fmt.Errorf("jwt part %d is not base64url without padding: %v", i, err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			return fmt.Errorf("jwt part %d %s: %v", i, raw, err)
		}
	}
	if head.Alg != "HS256" || head.Typ != "JWT" || head.Kid != cmsKeyID {
		return fmt.Errorf("jwt header %+v, want HS256 JWT kid %s", head, cmsKeyID)
	}
	if claims.Aud != "/admin/" || claims.Exp-claims.Iat != 300 {
		return fmt.Errorf("jwt claims %+v, want aud /admin/ and exp = iat+300", claims)
	}
	if d := time.Now().Unix() - claims.Iat; d < -60 || d > 60 {
		return fmt.Errorf("jwt iat %d is %d s from now", claims.Iat, d)
	}
	secret, _ := hex.DecodeString(cmsKeySecret)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if want := enc.EncodeToString(mac.Sum(nil)); !hmac.Equal([]byte(want), []byte(parts[2])) {
		return fmt.Errorf("jwt signature %s, want %s", parts[2], want)
	}
	return nil
}

func (g *standInGhost) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reqs = append(g.reqs, ghostRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body})
	if r.URL.RawQuery != "" {
		g.t.Errorf("%s %s?%s: the helper sent a query string (never ?newsletter=, nor any other)", r.Method, r.URL.Path, r.URL.RawQuery)
	}
	if err := checkJWT(r); err != nil {
		g.t.Errorf("%s %s: %v", r.Method, r.URL.Path, err)
		ghostError(w, 401, "Invalid token")
		return
	}
	if v := r.Header.Get("Accept-Version"); v != "v6.0" {
		g.t.Errorf("%s %s: Accept-Version %q, want v6.0", r.Method, r.URL.Path, v)
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/ghost/api/admin/")
	if !ok {
		ghostError(w, 404, "Resource not found")
		return
	}
	seg := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	coll := seg[0]
	items, ok := g.items[coll]
	if !ok {
		ghostError(w, 404, "Resource not found")
		return
	}
	one := func(it map[string]any) map[string]any { return map[string]any{coll: []map[string]any{it}} }
	switch {
	case r.Method == http.MethodPost && len(seg) == 1:
		var in map[string][]map[string]any
		if err := json.Unmarshal(body, &in); err != nil || len(in[coll]) != 1 {
			ghostError(w, 422, "Validation error")
			return
		}
		it := in[coll][0]
		g.next++
		id := fmt.Sprintf("%024x", g.next)
		it["id"] = id
		it["updated_at"] = fmt.Sprintf("2026-11-18T10:00:%02d.000Z", g.next)
		slug, _ := it["slug"].(string)
		if slug == "" {
			slug = strings.ToLower(strings.ReplaceAll(it["title"].(string), " ", "-"))
			it["slug"] = slug
		}
		it["url"] = g.srv.URL + "/" + slug + "/"
		items[id] = it
		ghostAnswer(w, 201, one(it))
	case r.Method == http.MethodGet && len(seg) == 3 && seg[1] == "slug":
		for _, it := range items {
			if it["slug"] == seg[2] {
				ghostAnswer(w, 200, one(it))
				return
			}
		}
		ghostError(w, 404, "Resource not found")
	case r.Method == http.MethodGet && len(seg) == 2:
		if it, ok := items[seg[1]]; ok {
			ghostAnswer(w, 200, one(it))
			if g.raceAfterRead {
				it["updated_at"] = "2026-11-18T12:00:00.000Z"
			}
			return
		}
		ghostError(w, 404, "Resource not found")
	case r.Method == http.MethodPut && len(seg) == 2:
		it, ok := items[seg[1]]
		if !ok {
			ghostError(w, 404, "Resource not found")
			return
		}
		var in map[string][]map[string]any
		if err := json.Unmarshal(body, &in); err != nil || len(in[coll]) != 1 {
			ghostError(w, 422, "Validation error")
			return
		}
		if in[coll][0]["updated_at"] != it["updated_at"] {
			ghostError(w, 409, "Saving failed! Someone else is editing this post.")
			return
		}
		for k, v := range in[coll][0] {
			it[k] = v
		}
		g.next++
		it["updated_at"] = fmt.Sprintf("2026-11-18T11:00:%02d.000Z", g.next)
		ghostAnswer(w, 200, one(it))
	default:
		ghostError(w, 404, "Resource not found")
	}
}

// cardMarkdown is the one Lexical markdown card's text in a post or page
// the helper sent.
func cardMarkdown(t *testing.T, it map[string]any) string {
	t.Helper()
	lex, _ := it["lexical"].(string)
	var doc struct {
		Root struct {
			Type     string
			Children []struct {
				Type     string
				Version  int
				Markdown string
			}
		}
	}
	if err := json.Unmarshal([]byte(lex), &doc); err != nil {
		t.Fatalf("lexical %q: %v", lex, err)
	}
	if doc.Root.Type != "root" || len(doc.Root.Children) != 1 || doc.Root.Children[0].Type != "markdown" {
		t.Fatalf("lexical %s, want one markdown card under the root", lex)
	}
	return doc.Root.Children[0].Markdown
}

// cmsRun is one helper run: its stdout, stderr and exit code.
type cmsRun struct {
	out, errOut string
	code        int
}

// cmsEnv runs the helper against the stand-in with curl, openssl and jq
// behind argv-logging stand-ins, so a test can read every command line.
type cmsEnv struct {
	t       *testing.T
	ghost   *standInGhost
	env     []string
	argvLog string
	dir     string
}

func newCmsEnv(t *testing.T) *cmsEnv {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	e := &cmsEnv{t: t, ghost: newStandInGhost(t), argvLog: filepath.Join(dir, "argv.log"), dir: dir}
	env := append(os.Environ(),
		"GHOST_URL="+e.ghost.srv.URL,
		"GHOST_ADMIN_KEY="+cmsKeyID+":"+cmsKeySecret,
		"CMS_ARGV_LOG="+e.argvLog,
	)
	for _, tool := range []string{"curl", "openssl", "jq"} {
		real, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not on PATH: the cms helper needs curl, openssl and jq (CI installs jq)", tool)
		}
		env = append(env, "CMS_REAL_"+tool+"="+real)
		standin.Write(t, filepath.Join(bin, tool), `n=${0##*/}
printf '%s %s\n' "$n" "$*" >>"$CMS_ARGV_LOG"
eval "real=\$CMS_REAL_$n"
exec "$real" "$@"
`)
	}
	e.env = append(env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return e
}

func (e *cmsEnv) file(name, body string) string {
	e.t.Helper()
	p := filepath.Join(e.dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *cmsEnv) run(args ...string) cmsRun {
	e.t.Helper()
	cmd := exec.Command("sh", append([]string{cmsHelper}, args...)...)
	cmd.Env = e.env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	r := cmsRun{out: out.String(), errOut: errOut.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatal(err)
	}
	if strings.Contains(r.out+r.errOut, cmsKeySecret) {
		e.t.Errorf("the helper printed the key's secret:\n%s%s", r.out, r.errOut)
	}
	return r
}

// line is the run's one JSON line {id, status, url}, exit 0 asked.
func (r cmsRun) line(t *testing.T) map[string]string {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", r.code, r.errOut)
	}
	if strings.Count(r.out, "\n") != 1 || !strings.HasSuffix(r.out, "\n") {
		t.Fatalf("stdout %q, want one JSON line", r.out)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(r.out), &m); err != nil {
		t.Fatalf("stdout %q: %v", r.out, err)
	}
	if len(m) != 3 || m["id"] == "" || m["status"] == "" || m["url"] == "" {
		t.Fatalf("stdout %q, want exactly id, status and url", r.out)
	}
	return m
}

// TestCmsGhostPublishSendsTheFileByteForByte: publish creates a post (or a
// page) straight as published, or scheduled with --at, with the file as
// one Lexical markdown card holding its exact bytes, the slug, excerpt and
// meta description given, and prints {id, status, url}. Every request is
// signed (the stand-in checks the JWT) and carries no query string.
func TestCmsGhostPublishSendsTheFileByteForByte(t *testing.T) {
	e := newCmsEnv(t)
	md := e.file("piece.md", cmsMarkdown)

	got := e.run("publish", "--file", md, "--title", "Hello from the factory", "--kind", "post",
		"--slug", "hello-factory", "--excerpt", "A short one", "--meta-description", "Meta words")
	m := got.line(t)
	if m["status"] != "published" || m["url"] != e.ghost.srv.URL+"/hello-factory/" {
		t.Errorf("publish printed %v, want published at /hello-factory/", m)
	}
	post := e.ghost.item("posts", m["id"])
	if post == nil {
		t.Fatalf("no post %s on the stand-in", m["id"])
	}
	if cardMarkdown(t, post) != cmsMarkdown {
		t.Errorf("the card holds %q, want the file byte for byte %q", cardMarkdown(t, post), cmsMarkdown)
	}
	for k, want := range map[string]string{"title": "Hello from the factory", "slug": "hello-factory", "custom_excerpt": "A short one", "meta_description": "Meta words", "status": "published"} {
		if post[k] != want {
			t.Errorf("post %s = %v, want %q", k, post[k], want)
		}
	}
	if _, ok := post["published_at"]; ok {
		t.Error("publish without --at sent published_at")
	}

	page := e.run("publish", "--file", md, "--title", "About", "--kind", "page").line(t)
	if page["status"] != "published" || e.ghost.item("pages", page["id"]) == nil {
		t.Errorf("page publish printed %v, want a published page", page)
	}

	at := "2026-11-20T09:00:00.000Z"
	sched := e.run("publish", "--file", md, "--title", "Later", "--kind", "post", "--at", at).line(t)
	if sched["status"] != "scheduled" || e.ghost.item("posts", sched["id"])["published_at"] != at {
		t.Errorf("scheduled publish printed %v (published_at %v), want scheduled at %s", sched, e.ghost.item("posts", sched["id"])["published_at"], at)
	}

	reqs := e.ghost.requests()
	if len(reqs) != 3 {
		t.Fatalf("requests %+v, want three POSTs and nothing else (no draft first)", reqs)
	}
	for i, path := range []string{"/ghost/api/admin/posts/", "/ghost/api/admin/pages/", "/ghost/api/admin/posts/"} {
		if reqs[i].Method != http.MethodPost || reqs[i].Path != path || reqs[i].Query != "" {
			t.Errorf("request %d = %s %s?%s, want POST %s with no query", i, reqs[i].Method, reqs[i].Path, reqs[i].Query, path)
		}
		if bytes.Contains(reqs[i].Body, []byte("newsletter")) {
			t.Errorf("request %d's body names a newsletter: %s", i, reqs[i].Body)
		}
	}
}

// TestCmsGhostUpdateReadsUpdatedAtFirst: update GETs the post for its
// updated_at, then PUTs the new file with that value; an id with no post
// is looked up as a page. A stale updated_at (Ghost's 409) is an error
// with Ghost's message and nothing on stdout.
func TestCmsGhostUpdateReadsUpdatedAtFirst(t *testing.T) {
	e := newCmsEnv(t)
	first := e.file("v1.md", "first version\n")
	second := e.file("v2.md", cmsMarkdown)

	post := e.run("publish", "--file", first, "--title", "Post", "--kind", "post").line(t)
	before := e.ghost.item("posts", post["id"])["updated_at"]
	n := len(e.ghost.requests())
	up := e.run("update", "--id", post["id"], "--file", second, "--title", "Post, refreshed").line(t)
	if up["id"] != post["id"] || up["status"] != "published" {
		t.Errorf("update printed %v, want the same id, still published", up)
	}
	reqs := e.ghost.requests()[n:]
	if len(reqs) != 2 || reqs[0].Method != http.MethodGet || reqs[0].Path != "/ghost/api/admin/posts/"+post["id"]+"/" ||
		reqs[1].Method != http.MethodPut || reqs[1].Path != "/ghost/api/admin/posts/"+post["id"]+"/" {
		t.Fatalf("update requests %+v, want GET then PUT on the post", reqs)
	}
	var put map[string][]map[string]any
	if err := json.Unmarshal(reqs[1].Body, &put); err != nil {
		t.Fatal(err)
	}
	if put["posts"][0]["updated_at"] != before {
		t.Errorf("PUT sent updated_at %v, want the one read, %v", put["posts"][0]["updated_at"], before)
	}
	if _, ok := put["posts"][0]["status"]; ok {
		t.Error("update sent a status: it changes the text and title only")
	}
	it := e.ghost.item("posts", post["id"])
	if cardMarkdown(t, it) != cmsMarkdown || it["title"] != "Post, refreshed" {
		t.Errorf("after update: title %v, card %q", it["title"], cardMarkdown(t, it))
	}

	page := e.run("publish", "--file", first, "--title", "Page", "--kind", "page").line(t)
	n = len(e.ghost.requests())
	e.run("update", "--id", page["id"], "--file", second).line(t)
	var paths []string
	for _, r := range e.ghost.requests()[n:] {
		paths = append(paths, r.Method+" "+strings.TrimPrefix(r.Path, "/ghost/api/admin"))
	}
	if want := "GET /posts/" + page["id"] + "/,GET /pages/" + page["id"] + "/,PUT /pages/" + page["id"] + "/"; strings.Join(paths, ",") != want {
		t.Errorf("page update requests %v, want %s", paths, want)
	}
	if cardMarkdown(t, e.ghost.item("pages", page["id"])) != cmsMarkdown {
		t.Error("the page's card is not the new file")
	}

	// Someone else saved in between: Ghost refuses with 409.
	e.ghost.mu.Lock()
	e.ghost.raceAfterRead = true
	e.ghost.mu.Unlock()
	got := e.run("update", "--id", post["id"], "--file", first)
	if got.code != 1 || got.out != "" || !strings.Contains(got.errOut, "HTTP 409") || !strings.Contains(got.errOut, "Someone else is editing") {
		t.Errorf("stale update: exit %d, stdout %q, stderr %q; want exit 1, nothing on stdout, Ghost's 409 message", got.code, got.out, got.errOut)
	}
}

// TestCmsGhostGetBySlug: get reads a post, else a page, by slug and
// prints {id, status, url}; no such slug is exit 1 naming the 404.
func TestCmsGhostGetBySlug(t *testing.T) {
	e := newCmsEnv(t)
	md := e.file("p.md", "text\n")
	post := e.run("publish", "--file", md, "--title", "A post", "--kind", "post", "--slug", "a-post").line(t)
	page := e.run("publish", "--file", md, "--title", "A page", "--kind", "page", "--slug", "a-page").line(t)

	if got := e.run("get", "--slug", "a-post").line(t); got["id"] != post["id"] || got["url"] != post["url"] {
		t.Errorf("get a-post = %v, want %v", got, post)
	}
	if got := e.run("get", "--slug", "a-page").line(t); got["id"] != page["id"] {
		t.Errorf("get a-page = %v, want %v", got, page)
	}
	if got := e.run("get", "--slug", "a-page", "--kind", "post"); got.code != 1 || got.out != "" || !strings.Contains(got.errOut, "404") {
		t.Errorf("get a page as --kind post: exit %d, stdout %q, stderr %q; want a 404 error", got.code, got.out, got.errOut)
	}
	if got := e.run("get", "--slug", "missing"); got.code != 1 || !strings.Contains(got.errOut, "no post or page") {
		t.Errorf("get missing: exit %d, stderr %q", got.code, got.errOut)
	}
}

// TestCmsGhostRefusesBeforeSending: a file that can't go out byte for byte
// (not UTF-8), a slug or id that would put a ? or / into the path (a
// ?newsletter= among them), a wrong call or a malformed key are refused
// before any request reaches Ghost.
func TestCmsGhostRefusesBeforeSending(t *testing.T) {
	e := newCmsEnv(t)
	bad := e.file("latin1.md", "caf\xe9\n")
	md := e.file("ok.md", "ok\n")
	cases := []struct {
		name string
		args []string
		key  string
		code int
		want string
	}{
		{"not utf-8", []string{"publish", "--file", bad, "--title", "T", "--kind", "post"}, "", 1, "byte for byte"},
		{"newsletter slug", []string{"publish", "--file", md, "--title", "T", "--kind", "post", "--slug", "x?newsletter=all"}, "", 1, "letters, digits"},
		{"path id", []string{"update", "--id", "../settings", "--file", md}, "", 1, "letters, digits"},
		{"no kind", []string{"publish", "--file", md, "--title", "T"}, "", 2, "usage:"},
		{"bad kind", []string{"publish", "--file", md, "--title", "T", "--kind", "tag"}, "", 1, "post or page"},
		{"no verb", nil, "", 2, "usage:"},
		{"unknown flag", []string{"get", "--slug", "s", "--newsletter", "all"}, "", 2, "unknown flag --newsletter"},
		{"no key", []string{"get", "--slug", "s"}, "GHOST_ADMIN_KEY=", 1, "keychain:ghost-admin-key"},
		{"key not hex", []string{"get", "--slug", "s"}, "GHOST_ADMIN_KEY=abc:zz", 1, "not hex"},
		{"no file", []string{"publish", "--file", filepath.Join(e.dir, "gone.md"), "--title", "T", "--kind", "post"}, "", 1, "no readable file"},
	}
	env := e.env
	for _, c := range cases {
		e.env = env
		if c.key != "" {
			e.env = append(append([]string(nil), env...), c.key)
		}
		got := e.run(c.args...)
		if got.code != c.code || got.out != "" || !strings.Contains(got.errOut, c.want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d, nothing on stdout, %q", c.name, got.code, got.out, got.errOut, c.code, c.want)
		}
	}
	if reqs := e.ghost.requests(); len(reqs) != 0 {
		t.Errorf("refused calls still reached Ghost: %+v", reqs)
	}
}

// TestCmsGhostKeyOnNoCommandLine: the key's secret is on no command line
// the helper starts (ps shows argv), and neither is the signed token: the
// HMAC runs over openssl's stdin and the token reaches curl in a header
// file. A wrong key is Ghost's 401, printed with exit 1.
func TestCmsGhostKeyOnNoCommandLine(t *testing.T) {
	e := newCmsEnv(t)
	md := e.file("p.md", cmsMarkdown)
	e.run("publish", "--file", md, "--title", "T", "--kind", "post").line(t)
	e.run("get", "--slug", "t").line(t)
	raw, err := os.ReadFile(e.argvLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(raw)
	for _, tool := range []string{"curl ", "openssl ", "jq "} {
		if !strings.Contains(log, tool) {
			t.Errorf("no %scommand logged: the argv stand-ins weren't used\n%s", tool, log)
		}
	}
	if strings.Contains(log, cmsKeySecret) || strings.Contains(strings.ToLower(log), "hexkey") || strings.Contains(log, "Ghost ey") {
		t.Errorf("a command line holds the key or the token:\n%s", log)
	}
}

// TestMarketingCmsExampleIsAGhostTool: department add marketing installs
// cms-ghost.sh beside role.md (the template's bytes) and writes the cms
// connector commented, so Marketing gets no connector; uncommented, cms is
// a tool running sh on that helper, its one secret the Ghost admin key in
// Keychain, a check on the site endpoint (no key needed), sends and held
// to an approved output, no per_run:, and lint says nothing about it. The
// add names the cms slot's rows, Ghost first.
func TestMarketingCmsExampleIsAGhostTool(t *testing.T) {
	asLead(t)
	proj, cfg := deptTemplateProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"add", "marketing"}); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(cmsHelper)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(proj.Root(), "departments", "marketing")
	got, err := os.ReadFile(filepath.Join(dir, "cms-ghost.sh"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("department add did not install cms-ghost.sh as the template ships it (err %v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "role.md")); err != nil {
		t.Fatalf("cms-ghost.sh is not beside role.md: %v", err)
	}
	assertContains(t, out.String(), "departments/marketing/cms-ghost.sh")
	assertContains(t, out.String(), "ghost")
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"#   - name: cms", "#     command: sh", "#     args: [departments/marketing/cms-ghost.sh]", "#       GHOST_ADMIN_KEY: keychain:ghost-admin-key"} {
		assertContains(t, string(raw), line)
	}
	if d := reloadDeptProject(t, cfg).DepartmentByKey("marketing"); d == nil || len(d.Connectors) != 0 {
		t.Fatalf("marketing installed with connectors %+v, want none (the examples are commented)", d)
	}

	uncommentConnectors(t, cfg)
	proj = reloadDeptProject(t, cfg)
	d := proj.DepartmentByKey("marketing")
	if d == nil {
		t.Fatal("no marketing department after uncommenting")
	}
	c := deptConnector(d, "cms")
	if c == nil {
		t.Fatalf("uncommented: no cms connector in %+v", d.Connectors)
	}
	if c.Kind != tracker.ConnectorTool || c.Command != "sh" || strings.Join(c.Args, " ") != "departments/marketing/cms-ghost.sh" ||
		!c.Sends || !c.NeedsApprovedOutput || c.PerRun != nil || c.URL != "" {
		t.Errorf("cms = %+v, want a sh tool on the helper, sends, needs_approved_output, no per_run", *c)
	}
	if len(c.Secrets) != 1 || c.Secrets["GHOST_ADMIN_KEY"] != "keychain:ghost-admin-key" {
		t.Errorf("cms secrets = %v, want GHOST_ADMIN_KEY: keychain:ghost-admin-key only", c.Secrets)
	}
	if !strings.Contains(c.Check, "/ghost/api/admin/site/") || strings.Contains(c.Check, "GHOST_ADMIN_KEY") {
		t.Errorf("cms check = %q, want the keyless site endpoint", c.Check)
	}
	for _, f := range []string{"brand.md", "icp.md"} {
		mustWrite(t, filepath.Join(dir, f), "# settled\n")
	}
	assertMarketingLintClean(t, proj, "uncommented")

	ctx, out = goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"partners", "--slot", "cms"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	g := strings.Index(s, "Ghost")
	for _, name := range []string{"Payload", "Strapi", "Decap CMS", "WordPress"} {
		if i := strings.Index(s, name); i < 0 || g < 0 || i < g {
			t.Errorf("department partners --slot cms: %s missing or before Ghost:\n%s", name, s)
		}
	}
}

// TestDepartmentsDocNamesTheCmsConnector: docs/departments.md names the
// cms connector and its helper under Channels as types, and the cms slot
// in the partners catalog with Marketing's partner_slots.
func TestDepartmentsDocNamesTheCmsConnector(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	section := func(head string) string {
		i := strings.Index(doc, "\n"+head+"\n")
		if i < 0 {
			t.Fatalf("docs/departments.md has no %q", head)
		}
		rest := doc[i+len(head)+2:]
		if j := strings.Index(rest, "\n### "); j >= 0 {
			rest = rest[:j]
		}
		return rest
	}
	channels := section("### Channels as types")
	for _, want := range []string{"`cms` connector", "cms-ghost.sh", "keychain:ghost-admin-key", "needs_approved_output", "?newsletter="} {
		assertContains(t, channels, want)
	}
	catalog := section("### The partners catalog")
	for _, want := range []string{"cms)", "Marketing: seo, analytics, social, cms", "Ghost is the cms slot's default"} {
		assertContains(t, catalog, want)
	}
}
