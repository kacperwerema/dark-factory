package viewer

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/push"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/webpush"
)

// standIn is a stand-in push service: it checks each push's VAPID JWT and
// Topic the way a real one does, and decrypts it with the device's key.
type standIn struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	got    []push.Payload
	raw    [][]byte
	status int
	key    string // the VAPID public key every push must carry
	device map[string]*ecdh.PrivateKey
	auth   map[string][]byte
}

func newStandIn(t *testing.T) *standIn {
	t.Helper()
	si := &standIn{t: t, status: http.StatusCreated, device: map[string]*ecdh.PrivateKey{}, auth: map[string][]byte{}}
	si.srv = httptest.NewTLSServer(http.HandlerFunc(si.serve))
	t.Cleanup(si.srv.Close)
	old := webpush.AllowedEndpoint
	webpush.AllowedEndpoint = func(u string) bool { return strings.HasPrefix(u, si.srv.URL+"/") }
	t.Cleanup(func() { webpush.AllowedEndpoint = old })
	return si
}

func (si *standIn) serve(w http.ResponseWriter, r *http.Request) {
	claims, k, err := webpush.VerifyVAPID(r.Header.Get("Authorization"))
	if err != nil || (si.key != "" && k != si.key) {
		http.Error(w, "BadJwtToken", http.StatusForbidden)
		return
	}
	if claims.Aud != si.srv.URL || claims.Sub == "" || claims.Exp < time.Now().Unix() {
		http.Error(w, "BadJwtToken", http.StatusForbidden)
		return
	}
	if len(r.Header.Get("Topic")) > 32 || r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("TTL") == "" {
		http.Error(w, "bad headers", http.StatusBadRequest)
		return
	}
	si.mu.Lock()
	defer si.mu.Unlock()
	priv := si.device[r.URL.Path]
	body, _ := io.ReadAll(r.Body)
	plain, err := webpush.Decrypt(priv, si.auth[r.URL.Path], body)
	if err != nil {
		http.Error(w, "undecryptable", http.StatusBadRequest)
		return
	}
	var p push.Payload
	if err := json.Unmarshal(plain, &p); err != nil {
		http.Error(w, "not JSON", http.StatusBadRequest)
		return
	}
	si.got = append(si.got, p)
	si.raw = append(si.raw, plain)
	w.WriteHeader(si.status)
}

// newDevice makes a browser's keys and its subscription JSON.
func (si *standIn) newDevice(name string) pushSubscribeRequest {
	priv, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	path := "/push/" + name
	si.mu.Lock()
	si.device[path], si.auth[path] = priv, auth
	si.mu.Unlock()
	var req pushSubscribeRequest
	req.Endpoint = si.srv.URL + path
	req.Keys.P256dh = base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
	req.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	return req
}

func (si *standIn) take() []push.Payload {
	si.mu.Lock()
	defer si.mu.Unlock()
	out := si.got
	si.got = nil
	return out
}

// pushTestServer is a viewer of proj with its own state dir, the sender
// for its config, talking to a stand-in push service.
func pushTestServer(t *testing.T, projects ...tracker.Project) (*Server, *standIn, string) {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	cfg := filepath.Join(t.TempDir(), "projects.yaml")
	os.WriteFile(cfg, []byte("projects: []\n"), 0o644)
	srv, err := NewServer(projects)
	if err != nil {
		t.Fatal(err)
	}
	si := newStandIn(t)
	srv.push = newPushService(push.Dir(), cfg, true, "")
	srv.push.client = si.srv.Client()
	srv.push.client.CheckRedirect = webpush.Client().CheckRedirect
	k, err := srv.push.Keys()
	if err != nil {
		t.Fatal(err)
	}
	si.key = k.PublicKey()
	// Settings: a contact, so pushes go.
	srv.push.store.Update(func(st *push.State) error { st.Settings.Subject = "mailto:owner@example.com"; return nil })
	stubPushSources(t, nil, nil, nil)
	return srv, si, cfg
}

// stubPushSources fixes the scan's sources; rows nil means the real list.
func stubPushSources(t *testing.T, rows func(p *tracker.Project) []WbQueueCard, stuck, limits []push.Seen) {
	t.Helper()
	pr, ps, pl := pushRows, pushStuck, pushLimits
	if rows != nil {
		pushRows = func(p *tracker.Project, _ []*tracker.Issue, _ time.Time) []WbQueueCard { return rows(p) }
	}
	pushStuck = func(*tracker.Project, []*tracker.Issue, time.Time) []push.Seen { return stuck }
	pushLimits = func(string, time.Time) []push.Seen { return limits }
	t.Cleanup(func() { pushRows, pushStuck, pushLimits = pr, ps, pl })
}

func subscribe(t *testing.T, srv *Server, cookie *http.Cookie, req pushSubscribeRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(req)
	return ownerDo(srv, "POST", "/push/subscribe", string(body), map[string]string{"Origin": "http://127.0.0.1:8080", "User-Agent": "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1"}, cookie)
}

// everyKindRows is one Needs you row of each kind for project p.
func everyKindRows(p *tracker.Project) []WbQueueCard {
	home, _ := os.UserHomeDir()
	pre := "/p/" + p.Slug
	return []WbQueueCard{
		{Kind: "ci", Slug: "main", Note: "go test fails in " + home + "/secret", URL: pre + "/status"},
		{Kind: "gate", Slug: "ui/ready", Title: "Ready thing", Target: "backlog", URL: pre + "/issue/ui/ready"},
		{Kind: "playtest", Slug: "ui/tested", Title: "Tested thing", URL: pre + "/issue/ui/tested"},
		{Kind: "checks", Slug: "ui/checked", Title: "Checked thing", URL: pre + "/issue/ui/checked"},
		{Kind: "question", Slug: "ui/asker", Title: "Asked in " + home + "/repo", Note: "question: the secret is hunter2", URL: pre + "/issue/ui/asker#comments"},
		{Kind: "clarify", Slug: "ui/clar", Title: "Clarify thing", URL: pre + "/issue/ui/clar"},
		{Kind: "outputs", Slug: "Marketing", URL: pre + "/shelf"},
		{Kind: "job", Slug: "weekly", URL: pre + "/company"},
		{Kind: "postmortem", Slug: "ui/pm", URL: pre + "/postmortem/ui/pm"},
		{Kind: "connectors", Slug: "gsc", URL: "https://evil.example/x"},
		{Kind: "bugs", Slug: "issue-cli report-bug", URL: pre + "/status"},
	}
}

// Proves: AC1 — each event kind produces one encrypted push (RFC 8291,
// VAPID JWT checked by the stand-in); AC6 — the payload is title, project,
// kind, relative link and tag only.
func TestPushEachKindOncePerDevice(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, si, _ := pushTestServer(t, proj)
	cookie := pairOwner(t, srv)
	if rec := subscribe(t, srv, cookie, si.newDevice("phone")); rec.Code != 200 {
		t.Fatalf("subscribe = %d %s", rec.Code, rec.Body)
	}
	stubPushSources(t, everyKindRows,
		[]push.Seen{{Key: proj.Slug + "|stuck|agent-x|", Project: proj.Slug, ProjectName: proj.Name, Kind: "stuck", Title: pushTitle("stuck", "X"), URL: "/p/" + proj.Slug + "/agents?agent=agent-x", Cooldown: time.Hour}},
		[]push.Seen{{Key: "|limit|claude|", Kind: "limit", Title: pushTitle("limit", "claude rests until 18:00"), URL: "/p/" + proj.Slug + "/agents"}})
	if n := srv.pushScan(context.Background()); n != 12 {
		t.Fatalf("first scan sent %d, want 12 (10 row kinds without bugs, stuck, limit)", n)
	}
	got := si.take()
	kinds := map[string]int{}
	home, _ := os.UserHomeDir()
	for i, p := range got {
		kinds[p.Kind]++
		raw := string(si.raw[i])
		var fields map[string]any
		json.Unmarshal(si.raw[i], &fields)
		for k := range fields {
			if k != "title" && k != "project" && k != "kind" && k != "url" && k != "tag" {
				t.Errorf("payload has field %q", k)
			}
		}
		if strings.Contains(raw, home) || strings.Contains(raw, "hunter2") || strings.Contains(raw, "secret") {
			t.Errorf("payload leaks a path or note: %s", raw)
		}
		if !strings.HasPrefix(p.URL, "/p/") && p.URL != "/needs-you" {
			t.Errorf("payload link %q is not a relative viewer path", p.URL)
		}
		if p.Kind != "limit" && p.Project != proj.Name {
			t.Errorf("payload project %q", p.Project)
		}
	}
	for _, k := range []string{"ci", "gate", "playtest", "checks", "question", "clarify", "outputs", "job", "postmortem", "connectors", "stuck", "limit"} {
		if kinds[k] != 1 {
			t.Errorf("kind %s: %d pushes, want 1", k, kinds[k])
		}
	}
	if kinds["bugs"] != 0 {
		t.Error("unsent bug reports were pushed")
	}
	// A repeat scan sends nothing.
	if n := srv.pushScan(context.Background()); n != 0 || len(si.take()) != 0 {
		t.Fatalf("repeat scan sent %d", n)
	}
}

// Proves: AC1 — the real Needs you rows (a ready gate, a playtest, a
// question) each push once, titled and linked.
func TestPushFromRealNeedsYouRows(t *testing.T) {
	proj := setupStatusProject(t, time.Now())
	withMockTmuxSessions(t, nil)
	stubAgentsGit(t)
	prev := unsentBugReports
	unsentBugReports = func() int { return 0 }
	t.Cleanup(func() { unsentBugReports = prev })
	srv, si, _ := pushTestServer(t, proj)
	cookie := pairOwner(t, srv)
	// Subscribing seeds what waits now: nothing is sent for it.
	subscribe(t, srv, cookie, si.newDevice("phone"))
	if n := srv.pushScan(context.Background()); n != 0 {
		t.Fatalf("subscribing flooded %d pushes", n)
	}
	// A second device subscribed before anything waited gets them all.
	srv.push.store.Update(func(st *push.State) error {
		for _, e := range st.Events {
			e.Closed = time.Now().Add(-time.Hour)
		}
		return nil
	})
	if n := srv.pushScan(context.Background()); n != 3 {
		t.Fatalf("reopened waits sent %d, want 3", n)
	}
	titles := map[string]string{}
	for _, p := range si.take() {
		titles[p.Kind] = p.Title + " " + p.URL
	}
	for kind, want := range map[string]string{
		"gate":     "Gate ready: ",
		"playtest": "Playtest to run: ",
		"question": "Question: ",
	} {
		if !strings.HasPrefix(titles[kind], want) || !strings.Contains(titles[kind], "/p/status/issue/") {
			t.Errorf("%s push = %q", kind, titles[kind])
		}
	}
}

// Proves: AC2 — mute and quiet hours suppress; a repeat sends nothing.
func TestPushMuteQuietAndRepeat(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, si, _ := pushTestServer(t, proj)
	cookie := pairOwner(t, srv)
	subscribe(t, srv, cookie, si.newDevice("phone"))
	rows := []WbQueueCard{{Kind: "gate", Slug: "a", Target: "backlog", URL: "/p/" + proj.Slug + "/issue/a"}}
	stubPushSources(t, func(*tracker.Project) []WbQueueCard { return rows }, nil, nil)

	form := "mute_" + proj.Slug + "=1&quiet_from=&quiet_to="
	if rec := ownerDo(srv, "POST", "/push/settings", form, map[string]string{"Origin": "http://127.0.0.1:8080", "Content-Type": "application/x-www-form-urlencoded"}, cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("settings = %d %s", rec.Code, rec.Body)
	}
	if n := srv.pushScan(context.Background()); n != 0 {
		t.Fatalf("muted project sent %d", n)
	}
	// Unmuted, quiet all day: held.
	now := time.Now()
	from := now.Add(-time.Hour).Format("15:04")
	to := now.Add(2 * time.Hour).Format("15:04")
	ownerDo(srv, "POST", "/push/settings", "quiet_from="+from+"&quiet_to="+to, map[string]string{"Origin": "http://127.0.0.1:8080", "Content-Type": "application/x-www-form-urlencoded"}, cookie)
	rows = append(rows, WbQueueCard{Kind: "gate", Slug: "b", Target: "backlog", URL: "/p/" + proj.Slug + "/issue/b"})
	if n := srv.pushScan(context.Background()); n != 0 {
		t.Fatalf("quiet hours sent %d", n)
	}
	// Quiet hours over: one digest for b (a was muted when it came).
	srv.push.now = func() time.Time { return now.Add(3 * time.Hour) }
	if n := srv.pushScan(context.Background()); n != 1 {
		t.Fatalf("after quiet hours sent %d, want one digest", n)
	}
	if got := si.take(); len(got) != 1 || got[0].Kind != "digest" || got[0].Title != "1 new thing waits on you" {
		t.Fatalf("digest %+v", got)
	}
	if n := srv.pushScan(context.Background()); n != 0 {
		t.Fatalf("repeat sent %d", n)
	}
	// Bad quiet hours are refused.
	rec := ownerDo(srv, "POST", "/push/settings", "quiet_from=25:00&quiet_to=07:00", map[string]string{"Origin": "http://127.0.0.1:8080", "Content-Type": "application/x-www-form-urlencoded"}, cookie)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "bad=quiet") {
		t.Fatalf("bad quiet hours: %d %s", rec.Code, loc)
	}
}

// Proves: AC3 — subscribe and unsubscribe are owner-only: 403 on a public
// Host without a token, 403 on loopback without the owner's sign-in.
func TestPushRoutesOwnerOnly(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, si, _ := pushTestServer(t, proj)
	srv.allowedHosts = []string{"factory.example.com"}
	dev := si.newDevice("phone")
	body, _ := json.Marshal(dev)
	for _, path := range []string{"/push/subscribe", "/push/unsubscribe"} {
		// Public Host, no Access token: the identity gate.
		req := httptest.NewRequest("POST", path, bytes.NewReader(body))
		req.Host = "factory.example.com"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("public %s without a token = %d", path, rec.Code)
		}
		// Loopback without the owner's cookie.
		rec = ownerDo(srv, "POST", path, string(body), map[string]string{"Origin": "http://127.0.0.1:8080"})
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "/owner") {
			t.Errorf("loopback %s without sign-in = %d %s", path, rec.Code, rec.Body)
		}
	}
	st, _ := srv.push.store.Read()
	if len(st.Devices) != 0 {
		t.Fatal("a refused subscribe was stored")
	}
	cookie := pairOwner(t, srv)
	if rec := subscribe(t, srv, cookie, dev); rec.Code != 200 {
		t.Fatalf("owner subscribe = %d %s", rec.Code, rec.Body)
	}
	if rec := ownerDo(srv, "POST", "/push/unsubscribe", string(body), map[string]string{"Origin": "http://127.0.0.1:8080"}, cookie); rec.Code != 200 {
		t.Fatalf("owner unsubscribe = %d %s", rec.Code, rec.Body)
	}
	// A subscription naming anything but a push service is refused.
	srv2, _, _ := pushTestServer(t, proj)
	c2 := pairOwner(t, srv2)
	bad := dev
	bad.Endpoint = "https://127.0.0.1:9/hook"
	if rec := subscribe(t, srv2, c2, bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("a non-push endpoint was accepted: %d %s", rec.Code, rec.Body)
	}
	// Public GETs of the page and the app shell need the token too (D-3).
	for _, path := range []string{"/push", "/push/key", "/manifest.webmanifest", "/sw.js", "/apple-touch-icon.png"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "factory.example.com"
		rec := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("public GET %s without a token = %d", path, rec.Code)
		}
	}
}

// Proves: AC7 — no endpoint, p256dh, auth or private key on /push, in
// /push/key, status.md or the log.
func TestPushSecretsStayInTheStateDir(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, si, _ := pushTestServer(t, proj)
	cookie := pairOwner(t, srv)
	dev := si.newDevice("phone")
	subscribe(t, srv, cookie, dev)
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	si.status = http.StatusGone
	stubPushSources(t, func(p *tracker.Project) []WbQueueCard {
		return []WbQueueCard{{Kind: "gate", Slug: "a", Target: "x", URL: "/p/" + p.Slug + "/issue/a"}}
	}, nil, nil)
	srv.pushScan(context.Background())

	vapid, _ := os.ReadFile(webpush.KeysFile(srv.push.store.Dir))
	var kf struct{ Private string }
	json.Unmarshal(vapid, &kf)
	secrets := []string{dev.Endpoint, strings.TrimPrefix(dev.Endpoint, "https://"), dev.Keys.P256dh, dev.Keys.Auth, kf.Private}

	deskBody, _ := json.Marshal(si.newDevice("desk"))
	ownerDo(srv, "POST", "/push/subscribe", string(deskBody), map[string]string{"Origin": "http://127.0.0.1:8080", "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) Firefox/130.0"}, cookie)
	page := ownerDo(srv, "GET", "/push", "", nil, cookie).Body.String()
	key := ownerDo(srv, "GET", "/push/key", "", nil, cookie).Body.String()
	statusMD := ownerDo(srv, "GET", "/p/"+proj.Slug+"/status.md", "", nil).Body.String()
	for name, text := range map[string]string{"/push": page, "/push/key": key, "status.md": statusMD, "log": logBuf.String(), "startup": srv.pushStartupLine()} {
		for _, s := range secrets {
			if s != "" && strings.Contains(text, s) {
				t.Errorf("%s shows a secret (%q…)", name, s[:min(12, len(s))])
			}
		}
	}
	if !strings.Contains(page, "removed by the push service") || !strings.Contains(page, `data-testid="push.device"`) {
		t.Errorf("/push doesn't list devices by label or the removed one:\n%s", page)
	}
	if !strings.Contains(statusMD, "Push: 1 device") || !strings.Contains(statusMD, "removed: iPhone · Safari on") {
		t.Errorf("status.md has no Push: line with the removed device:\n%s", statusMD)
	}
	if fi, err := os.Stat(filepath.Join(srv.push.store.Dir, "subscriptions.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("subscriptions.json %v %v", fi, err)
	}
}

// Proves: AC8 — only the recorded viewer sends: a second viewer on the same
// state dir with other projects sends nothing and closes nothing.
func TestPushOnlyTheBoundViewerSends(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, si, _ := pushTestServer(t, proj)
	cookie := pairOwner(t, srv)
	subscribe(t, srv, cookie, si.newDevice("phone"))
	rows := func(p *tracker.Project) []WbQueueCard {
		if p.Slug != proj.Slug {
			return []WbQueueCard{{Kind: "gate", Slug: "scenario", Target: "x", URL: "/p/" + p.Slug + "/issue/scenario"}}
		}
		return []WbQueueCard{{Kind: "gate", Slug: "a", Target: "x", URL: "/p/" + p.Slug + "/issue/a"}}
	}
	stubPushSources(t, rows, nil, nil)
	if n := srv.pushScan(context.Background()); n != 1 {
		t.Fatalf("bound viewer sent %d", n)
	}
	si.take()

	// A sandbox viewer with another board on the same state dir: one with
	// another config (bound elsewhere), one Claim refused.
	other, _ := setupTestProject(t)
	other.Slug, other.Name = "scenario", "Scenario"
	for name, ps := range map[string]*pushService{
		"other config":  newPushService(srv.push.store.Dir, filepath.Join(t.TempDir(), "projects.yaml"), true, ""),
		"claim refused": newPushService(srv.push.store.Dir, srv.push.config, false, "config is in a temp folder"),
	} {
		sandbox, err := NewServer([]tracker.Project{other})
		if err != nil {
			t.Fatal(err)
		}
		ps.client = srv.push.client
		sandbox.push = ps
		for i := 0; i < 3; i++ {
			if n := sandbox.pushScan(context.Background()); n != 0 {
				t.Fatalf("%s: sandbox sent %d", name, n)
			}
		}
		c := pairOwner(t, sandbox)
		if rec := subscribe(t, sandbox, c, si.newDevice("sandbox")); rec.Code != http.StatusConflict {
			t.Fatalf("%s: sandbox subscribe = %d %s", name, rec.Code, rec.Body)
		}
	}
	if got := si.take(); len(got) != 0 {
		t.Fatalf("the stand-in got %v from a sandbox", got)
	}
	// The bound viewer's event stayed open: nothing is sent again.
	if n := srv.pushScan(context.Background()); n != 0 {
		t.Fatalf("a sandbox closed the real event: resent %d", n)
	}
}

func TestPushRebindMovesTheSender(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, si, cfg := pushTestServer(t, proj)
	cookie := pairOwner(t, srv)
	subscribe(t, srv, cookie, si.newDevice("phone"))
	// The owner moved projects.yaml: the new viewer runs from another path.
	moved := newPushService(srv.push.store.Dir, filepath.Join(filepath.Dir(cfg), "moved.yaml"), true, "")
	srv.push = moved
	st, _ := moved.store.Read()
	if ok, why := moved.active(st); ok || !strings.Contains(why, "projects.yaml") {
		t.Fatalf("active = %v %q", ok, why)
	}
	if rec := ownerDo(srv, "POST", "/push/rebind", "", map[string]string{"Origin": "http://127.0.0.1:8080"}, cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("rebind = %d %s", rec.Code, rec.Body)
	}
	st, _ = moved.store.Read()
	if ok, _ := moved.active(st); !ok {
		t.Fatal("rebind left the viewer inactive")
	}
}

func TestPushTestSendsToThisDevice(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, si, _ := pushTestServer(t, proj)
	cookie := pairOwner(t, srv)
	dev := si.newDevice("phone")
	subscribe(t, srv, cookie, dev)
	body, _ := json.Marshal(dev)
	rec := ownerDo(srv, "POST", "/push/test", string(body), map[string]string{"Origin": "http://127.0.0.1:8080"}, cookie)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"outcome":"sent"`) {
		t.Fatalf("test push = %d %s", rec.Code, rec.Body)
	}
	if got := si.take(); len(got) != 1 || got[0].Kind != "test" {
		t.Fatalf("stand-in got %+v", got)
	}
}

func TestPushNeedsAContact(t *testing.T) {
	if got := pushSubject(push.Settings{}, []string{"127.0.0.1", "factory.example.com"}); got != "https://factory.example.com" {
		t.Fatalf("subject from allowed_hosts = %q", got)
	}
	if got := pushSubject(push.Settings{}, nil); got != "" {
		t.Fatalf("subject with no public name = %q, want none", got)
	}
	for s, want := range map[string]bool{"o@example.com": true, "mailto:o@example.com": true, "https://f.example.com": true, "o@localhost": false, "nope": false, "https://": false} {
		if validSubject(s) != want {
			t.Errorf("validSubject(%q) = %v", s, !want)
		}
	}
}

// The app shell: the manifest opens Needs you, the worker is served from
// the root, every Workshop page links the manifest with credentials.
func TestPushAppShell(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, _, _ := pushTestServer(t, proj)
	rec := ownerDo(srv, "GET", "/manifest.webmanifest", "", nil)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("manifest %d %s: %v", rec.Code, rec.Header().Get("Content-Type"), err)
	}
	if m["start_url"] != "/needs-you" || m["display"] != "standalone" || m["scope"] != "/" {
		t.Fatalf("manifest %v", m)
	}
	for _, path := range []string{"/static/icons/icon-192.png", "/static/icons/icon-512.png", "/apple-touch-icon.png"} {
		if rec := ownerDo(srv, "GET", path, "", nil); rec.Code != 200 || !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")) {
			t.Errorf("%s = %d", path, rec.Code)
		}
	}
	rec = ownerDo(srv, "GET", "/sw.js", "", nil)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") || !strings.Contains(rec.Body.String(), "notificationclick") {
		t.Fatalf("sw.js %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("sw.js CSP %q", csp)
	}
	page := ownerDo(srv, "GET", "/needs-you", "", nil).Body.String()
	for _, want := range []string{`<link rel="manifest" href="/manifest.webmanifest" crossorigin="use-credentials">`, `<link rel="apple-touch-icon" href="/apple-touch-icon.png">`, `href="/push"`} {
		if !strings.Contains(page, want) {
			t.Errorf("/needs-you missing %s", want)
		}
	}
}

func TestPushTitleScrubsAndClips(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := pushTitle("gate", "fix "+home+"/x\n"+strings.Repeat("y", 300))
	if strings.Contains(got, home) || len([]rune(got)) > pushTitleMax || strings.Contains(got, "\n") {
		t.Fatalf("title %q", got)
	}
	if deviceLabel("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit Version/18.0 Mobile Safari/604.1") != "iPhone · Safari" {
		t.Fatal("deviceLabel")
	}
}
