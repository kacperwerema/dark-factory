package viewer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/socials"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Connect socials page against a stand-in Postiz that answers only
// while the stand-in docker has it "up", a fake Keychain, and the
// template's own social connector entry: every action must leave the tool
// stopped, and the sentinel key must land in Keychain only.

const (
	sentinelKey     = "SENTINEL-postiz-key-7f3a9c"
	socialsAppValue = "SENTINEL-app-secret-51be"
	socialsAppPass  = "SENTINEL-bsky-apppass-90d2"
)

type socialsRig struct {
	t       *testing.T
	root    string
	config  string
	compose string
	srv     *Server
	cookie  *http.Cookie
	postiz  *httptest.Server
	upFile  string
	logBuf  *bytes.Buffer

	mu       sync.Mutex
	keychain map[string]string
	channels []map[string]any
	calls    []string
	connects []string
}

func (g *socialsRig) up() bool { _, err := os.Stat(g.upFile); return err == nil }

func newSocialsRig(t *testing.T, withConnector bool) *socialsRig {
	t.Helper()
	withMockTmuxSessions(t, nil)
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	g := &socialsRig{t: t, root: root, keychain: map[string]string{}, upFile: filepath.Join(root, "docker", "up")}
	for _, d := range []string{"home", "state", "config", "bin", "docker", "acme/issues"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))

	// The fake Keychain.
	oldW, oldV, oldP, oldK := tracker.SecretWrite, tracker.SecretValue, tracker.SecretPresent, keychainHere
	t.Cleanup(func() {
		tracker.SecretWrite, tracker.SecretValue, tracker.SecretPresent, keychainHere = oldW, oldV, oldP, oldK
	})
	tracker.SecretWrite = func(ref tracker.SecretRef, v string) error {
		if err := tracker.CheckSecretValue(v); err != nil {
			return err
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		g.keychain[ref.String()] = v
		return nil
	}
	tracker.SecretValue = func(ref tracker.SecretRef) (string, bool) {
		g.mu.Lock()
		defer g.mu.Unlock()
		v, ok := g.keychain[ref.String()]
		return v, ok
	}
	tracker.SecretPresent = func(ref tracker.SecretRef) bool { _, ok := tracker.SecretValue(ref); return ok }
	keychainHere = func() bool { return true }

	// The stand-in Postiz: 502 while down (nginx in front of a stopped
	// backend), 401 without the key, then its public API.
	g.postiz = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.postiz.Close)

	// Stand-ins on PATH: docker (compose up -d / stop, label stop),
	// mem-guard, and sleep: the stand-in Postiz answers at once, so the
	// ready step's six probes 2 s apart need no waits. Each body is the same
	// in every test (docker's folder comes from the environment), so macOS's
	// first-exec check is paid once per script (internal/standin).
	bin := filepath.Join(root, "bin")
	t.Setenv("SOCIALS_DOCKER_DIR", filepath.Join(root, "docker"))
	standin.Write(t, filepath.Join(bin, "docker"), "D=\"$SOCIALS_DOCKER_DIR\"\necho \"$*\" >> \"$D/docker.log\"\n"+
		"case \"$*\" in\n*' up -d'*) touch \"$D/up\" ;;\n*' start'*) touch \"$D/up\" ;;\n*' stop'*) rm -f \"$D/up\" ;;\nps*) ;;\nstop*) rm -f \"$D/up\" ;;\nesac\nexit 0\n")
	standin.Write(t, filepath.Join(bin, "mem-guard"), "echo 'ok: memory used 20% (< 75%)'\n")
	standin.Write(t, filepath.Join(bin, "sleep"), "exit 0\n")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	g.compose = filepath.Join(root, "postiz", "compose.yaml")
	os.MkdirAll(filepath.Dir(g.compose), 0o755)
	os.WriteFile(g.compose, []byte("services: {}\n"), 0o644)

	g.config = filepath.Join(root, "projects.yaml")
	body := "projects:\n  # the sandbox\n  - name: Acme\n    issues: " + filepath.Join(root, "acme", "issues") + "\n    workdir: " + filepath.Join(root, "acme") + "\n    departments:\n      - name: Marketing\n        role: SEO and socials # mine\n"
	os.WriteFile(g.config, []byte(body), 0o644)
	if withConnector {
		item, err := socialEntry(g.postiz.URL, g.compose, socials.OverridePath(socials.PostizTool))
		if err != nil {
			t.Fatal(err)
		}
		// One probe instead of six over 12 s: the stand-in has no nginx.
		item = strings.Replace(item, "for i in 1 2 3 4 5 6; do", "for i in 1; do", 1)
		item = strings.Replace(item, "sleep 2; done", "done", 1)
		b, _ := os.ReadFile(g.config)
		os.WriteFile(g.config, append(b, []byte("        connectors:\n"+indent(item, 10))...), 0o644)
		g.keychain["keychain:postiz"] = sentinelKey
	}
	projects, err := tracker.LoadProjects(g.config)
	if err != nil {
		t.Fatal(err)
	}
	projects[0].WorkflowFile = repoWorkflow(t)
	g.srv, err = NewServer(projects)
	if err != nil {
		t.Fatal(err)
	}
	g.cookie = pairOwner(t, g.srv)
	old := connectHold
	t.Cleanup(func() {
		connectHold = old
		for k := range connectSessions.m {
			endConnect(k, "test over")
		}
	})
	g.logBuf = &bytes.Buffer{}
	log.SetOutput(g.logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return g
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	var b strings.Builder
	for _, l := range strings.SplitAfter(s, "\n") {
		if l != "" {
			b.WriteString(pad + l)
		}
	}
	return b.String()
}

func (g *socialsRig) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.calls = append(g.calls, r.Method+" "+r.URL.Path)
	g.mu.Unlock()
	if !g.up() {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	if r.URL.Path == "/api/mcp" {
		http.Error(w, `{"msg":"No API Key found"}`, http.StatusUnauthorized)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/integrations/social-connect/") {
		var body struct{ State, Code, Timezone string }
		json.NewDecoder(r.Body).Decode(&body)
		raw, _ := base64.StdEncoding.DecodeString(body.Code)
		var f map[string]string
		json.Unmarshal(raw, &f)
		if body.State != "st8" || f["password"] != socialsAppPass {
			http.Error(w, `{"msg":"bad"}`, http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		g.connects = append(g.connects, "bluesky")
		g.channels = append(g.channels, map[string]any{"id": "ch-bsky", "providerIdentifier": "bluesky"})
		g.mu.Unlock()
		w.Write([]byte(`{}`))
		return
	}
	if r.Header.Get("Authorization") != sentinelKey {
		http.Error(w, `{"msg":"Invalid API key"}`, http.StatusUnauthorized)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case r.URL.Path == "/api/public/v1/is-connected":
		w.Write([]byte(`{"connected":true}`))
	case r.URL.Path == "/api/public/v1/debug/channels":
		json.NewEncoder(w).Encode(g.channels)
	case r.URL.Path == "/api/public/v1/social/linkedin":
		w.Write([]byte(`{"url":"https://www.linkedin.com/oauth/v2/authorization?state=abc"}`))
	case r.URL.Path == "/api/public/v1/social/bluesky":
		w.Write([]byte(`{"url":"st8"}`))
	case strings.HasPrefix(r.URL.Path, "/api/public/v1/integrations/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(r.URL.Path, "/api/public/v1/integrations/")
		var keep []map[string]any
		for _, c := range g.channels {
			if c["id"] != id {
				keep = append(keep, c)
			}
		}
		g.channels = keep
		w.Write([]byte(`{}`))
	default:
		http.NotFound(w, r)
	}
}

func (g *socialsRig) do(method, path string, form url.Values) *httptest.ResponseRecorder {
	g.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(g.cookie)
	rec := httptest.NewRecorder()
	g.srv.Routes().ServeHTTP(rec, req)
	return rec
}

// mustStopped fails when the stand-in Postiz is still up.
func (g *socialsRig) mustStopped(after string) {
	g.t.Helper()
	if g.up() {
		g.t.Fatalf("Postiz is still up after %s", after)
	}
}

// noSentinelAnywhere scans every file under the sandbox (projects.yaml,
// the issues dir and its logs, the state dir with the clilog and the
// override, HOME), the log output and the answers for each sentinel.
func (g *socialsRig) noSentinelAnywhere(bodies ...string) {
	g.t.Helper()
	sentinels := []string{sentinelKey, socialsAppValue, socialsAppPass}
	filepath.WalkDir(g.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, s := range sentinels {
			if bytes.Contains(b, []byte(s)) {
				g.t.Errorf("%s holds a typed secret", p)
			}
		}
		return nil
	})
	for _, s := range sentinels {
		if strings.Contains(g.logBuf.String(), s) {
			g.t.Errorf("the log holds a typed secret:\n%s", g.logBuf)
		}
		for _, b := range bodies {
			if strings.Contains(b, s) {
				g.t.Errorf("an answer holds a typed secret")
			}
		}
	}
}

func TestSocialsSetupWritesTheConnectorAndKeepsTheKeyInKeychainOnly(t *testing.T) {
	g := newSocialsRig(t, false)
	before, _ := os.ReadFile(g.config)

	page := g.do("GET", "/p/acme/socials/marketing", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `data-testid="socials.setup.form"`) {
		t.Fatalf("page = %d\n%s", page.Code, page.Body)
	}
	if strings.Contains(page.Body.String(), `data-testid="socials.networks"`) {
		t.Error("networks shown before the connector exists")
	}

	prev := g.do("POST", "/p/acme/socials/marketing/setup", url.Values{"url": {g.postiz.URL}, "compose": {g.compose}, "op": {"preview"}})
	if prev.Code != http.StatusOK {
		t.Fatalf("preview = %d\n%s", prev.Code, prev.Body)
	}
	body := prev.Body.String()
	for _, want := range []string{`data-testid="socials.diff"`, "+ ", "- name: social", "Bearer keychain:postiz", " up -d", `name="key"`} {
		if !strings.Contains(body, want) {
			t.Errorf("preview misses %q", want)
		}
	}
	if now, _ := os.ReadFile(g.config); !bytes.Equal(now, before) {
		t.Fatal("the preview wrote projects.yaml")
	}
	sum := soBetween(body, `name="sum" value="`, `"`)
	if sum == "" {
		t.Fatal("no sum in the preview")
	}

	save := g.do("POST", "/p/acme/socials/marketing/setup", url.Values{"url": {g.postiz.URL}, "compose": {g.compose}, "op": {"save"}, "sum": {sum}, "key": {sentinelKey}})
	if save.Code != http.StatusSeeOther || !strings.Contains(save.Header().Get("Location"), "ok=setup") {
		t.Fatalf("save = %d %s\n%s", save.Code, save.Header().Get("Location"), save.Body)
	}
	if g.keychain["keychain:postiz"] != sentinelKey {
		t.Errorf("Keychain = %v", g.keychain)
	}
	after, _ := os.ReadFile(g.config)
	if !strings.Contains(string(after), "role: SEO and socials # mine") || !strings.Contains(string(after), "- name: social") {
		t.Errorf("projects.yaml:\n%s", after)
	}
	g.mustStopped("the setup's key check")
	page = g.do("GET", "/p/acme/socials/marketing", nil)
	for _, want := range []string{`data-testid="socials.setup.exists"`, `data-testid="socials.key.set"`, `data-testid="socials.network.linkedin"`, `data-testid="socials.network.bluesky"`, "checked "} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("page after setup misses %q", want)
		}
	}
	g.noSentinelAnywhere(prev.Body.String(), save.Body.String(), save.Header().Get("Location"), page.Body.String())
}

func TestSocialsSetupRefusesAProjectsYamlChangedSinceItsDiff(t *testing.T) {
	g := newSocialsRig(t, false)
	prev := g.do("POST", "/p/acme/socials/marketing/setup", url.Values{"url": {g.postiz.URL}, "compose": {g.compose}, "op": {"preview"}})
	sum := soBetween(prev.Body.String(), `name="sum" value="`, `"`)
	b, _ := os.ReadFile(g.config)
	os.WriteFile(g.config, append(b, []byte("# hand edit\n")...), 0o644)
	save := g.do("POST", "/p/acme/socials/marketing/setup", url.Values{"url": {g.postiz.URL}, "compose": {g.compose}, "op": {"save"}, "sum": {sum}, "key": {sentinelKey}})
	if save.Code != http.StatusConflict || !strings.Contains(save.Body.String(), "changed since the diff") {
		t.Fatalf("save = %d\n%s", save.Code, save.Body)
	}
	if len(g.keychain) != 0 {
		t.Errorf("Keychain written on a refused save: %v", g.keychain)
	}
	g.noSentinelAnywhere(save.Body.String())
}

func TestSocialsNetworkValuesGoToKeychainAndTheOverrideHoldsReferencesOnly(t *testing.T) {
	g := newSocialsRig(t, true)
	page := g.do("GET", "/p/acme/socials/marketing", nil).Body.String()
	row := soBetween(page, `data-testid="socials.network.linkedin"`, `</details>`)
	for _, want := range []string{"LINKEDIN_CLIENT_ID", "LINKEDIN_CLIENT_SECRET", "/integrations/social/linkedin", "Saving also adds", "keychain:postiz-env/LINKEDIN_CLIENT_ID", "disabled"} {
		if !strings.Contains(row, want) {
			t.Errorf("LinkedIn row misses %q", want)
		}
	}
	sum := soBetween(row, `name="sum" value="`, `"`)
	rec := g.do("POST", "/p/acme/socials/marketing/fields", url.Values{"network": {"linkedin"}, "sum": {sum}, "LINKEDIN_CLIENT_ID": {"client-id-1"}, "LINKEDIN_CLIENT_SECRET": {socialsAppValue}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("fields = %d\n%s", rec.Code, rec.Body)
	}
	if g.keychain["keychain:postiz-env/LINKEDIN_CLIENT_SECRET"] != socialsAppValue {
		t.Errorf("Keychain = %v", g.keychain)
	}
	cfg, _ := os.ReadFile(g.config)
	if !strings.Contains(string(cfg), "LINKEDIN_CLIENT_SECRET: keychain:postiz-env/LINKEDIN_CLIENT_SECRET") {
		t.Errorf("projects.yaml has no reference:\n%s", cfg)
	}
	ov, err := os.ReadFile(socials.OverridePath(socials.PostizTool))
	if err != nil || !strings.Contains(string(ov), "LINKEDIN_CLIENT_SECRET: ${LINKEDIN_CLIENT_SECRET:-}") {
		t.Errorf("override = %s, %v", ov, err)
	}
	if g.up() || len(g.calls) != 0 {
		t.Errorf("saving values started Postiz (calls %v)", g.calls)
	}
	g.noSentinelAnywhere(rec.Body.String(), rec.Header().Get("Location"))
}

func TestSocialsConnectHoldsPostizForTheSignInThenStopsIt(t *testing.T) {
	g := newSocialsRig(t, true)
	g.keychain["keychain:postiz-env/LINKEDIN_CLIENT_ID"] = "x"
	g.keychain["keychain:postiz-env/LINKEDIN_CLIENT_SECRET"] = "y"
	// The hold outlasts the page read on any machine; the test then ends
	// it the way its timer would, so no window races a loaded machine.
	connectHold = time.Minute
	rec := g.do("POST", "/p/acme/socials/marketing/connect", url.Values{"network": {"linkedin"}})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "https://www.linkedin.com/oauth/") {
		t.Fatalf("connect = %d %q\n%s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if !g.up() {
		t.Fatal("Postiz is not up for the sign-in")
	}
	page := g.do("GET", "/p/acme/socials/marketing", nil).Body.String()
	if !strings.Contains(page, `data-testid="socials.hold"`) {
		t.Error("the page doesn't say Postiz is up for the sign-in")
	}
	cs := heldConnect(t, "marketing")
	if left := time.Until(cs.until); left < 50*time.Second || left > time.Minute {
		t.Errorf("the hold ends in %s, want connectHold (1m)", left)
	}
	cs.expire()
	g.mustStopped("the sign-in hold ran out")
	if !strings.Contains(g.logBuf.String(), "ended (time is up)") {
		t.Errorf("the log doesn't say the time was up:\n%s", g.logBuf)
	}
	// The timer is what calls expire: a short hold stops on its own. Only
	// the stop is waited for, so a slow machine just waits longer.
	connectHold = 10 * time.Millisecond
	g.do("POST", "/p/acme/socials/marketing/connect", url.Values{"network": {"linkedin"}})
	deadline := time.Now().Add(10 * time.Second)
	for g.up() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	g.mustStopped("a short hold's timer")

	// A sign-in that lands: the check sees the new channel and ends the hold.
	connectHold = time.Minute
	g.do("POST", "/p/acme/socials/marketing/connect", url.Values{"network": {"linkedin"}})
	g.mu.Lock()
	g.channels = append(g.channels, map[string]any{"id": "ch-li", "providerIdentifier": "linkedin"})
	g.mu.Unlock()
	if rec := g.do("POST", "/p/acme/socials/marketing/check", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("check = %d\n%s", rec.Code, rec.Body)
	}
	g.mustStopped("a check saw the new channel")

	// Done stops it too.
	g.do("POST", "/p/acme/socials/marketing/connect", url.Values{"network": {"linkedin"}})
	g.do("POST", "/p/acme/socials/marketing/connect-done", nil)
	g.mustStopped("Done")
	g.noSentinelAnywhere(rec.Body.String())
}

// heldConnect is the one sign-in hold on dept's tool.
func heldConnect(t *testing.T, dept string) *connectSession {
	t.Helper()
	key := "acme/" + tracker.DepartmentKey(dept) + "/" + socialConnector
	connectSessions.Lock()
	cs := connectSessions.m[key]
	connectSessions.Unlock()
	if cs == nil {
		t.Fatalf("no sign-in hold on %s", key)
	}
	return cs
}

func TestSocialsConnectRefusesANetworkWithoutItsAppValues(t *testing.T) {
	g := newSocialsRig(t, true)
	rec := g.do("POST", "/p/acme/socials/marketing/connect", url.Values{"network": {"x"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "needs its API key first") {
		t.Fatalf("connect = %d\n%s", rec.Code, rec.Body)
	}
	if len(g.calls) != 0 {
		t.Errorf("Postiz was called: %v", g.calls)
	}
}

func TestSocialsStatusReadsConnectedExpiredAndStopsAfter(t *testing.T) {
	g := newSocialsRig(t, true)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	g.channels = []map[string]any{
		{"id": "ch-li", "providerIdentifier": "linkedin"},
		{"id": "ch-x", "providerIdentifier": "x", "tokenExpiration": past},
		{"id": "ch-yt", "providerIdentifier": "youtube", "refreshNeeded": true},
		{"id": "ch-gone", "providerIdentifier": "reddit", "deletedAt": past},
	}
	rec := g.do("POST", "/p/acme/socials/marketing/check", nil)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=checked") {
		t.Fatalf("check = %d\n%s", rec.Code, rec.Body)
	}
	g.mustStopped("Check now")
	page := g.do("GET", "/p/acme/socials/marketing", nil).Body.String()
	for id, want := range map[string]string{"linkedin": ">connected<", "x": ">expired<", "youtube": ">expired<", "reddit": ">not connected<", "pinterest": ">not connected<"} {
		chip := soBetween(page, `data-testid="socials.network.`+id+`.state">`, `</span>`)
		if ">"+chip+"<" != want {
			t.Errorf("%s = %q, want %s", id, chip, want)
		}
	}
	if strings.Contains(page, "ch-gone") {
		t.Error("a deleted channel shows")
	}
	// The room links the page and names the expired networks; Needs you has a row.
	company := g.do("GET", "/p/acme/company", nil).Body.String()
	if !strings.Contains(company, `data-testid="company.room.connectors.socials"`) || !strings.Contains(company, "expired: x, youtube") {
		t.Error("the room has no Connect socials link or expired chip")
	}
	ny := g.do("GET", "/p/acme/needs-you", nil).Body.String()
	if !strings.Contains(ny, "sign in again to x, youtube") {
		t.Error("Needs you has no row for the expired networks")
	}
	// The log holds ids, providers and states only.
	logb, _ := os.ReadFile(filepath.Join(g.root, "acme", "issues", ".connectors", "socials.jsonl"))
	if !strings.Contains(string(logb), `"provider":"youtube","state":"expired"`) {
		t.Errorf("socials.jsonl = %s", logb)
	}
}

func TestSocialsBlueskyConnectsWithAnAppPasswordKeptNowhere(t *testing.T) {
	g := newSocialsRig(t, true)
	rec := g.do("POST", "/p/acme/socials/marketing/connect", url.Values{"network": {"bluesky"}, "f_service": {"https://bsky.social"}, "f_identifier": {"me.bsky.social"}, "f_password": {socialsAppPass}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=connected") {
		t.Fatalf("connect = %d %s\n%s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if len(g.connects) != 1 {
		t.Fatalf("Postiz connects = %v", g.connects)
	}
	g.mustStopped("the Bluesky connect")
	page := g.do("GET", "/p/acme/socials/marketing", nil).Body.String()
	if chip := soBetween(page, `data-testid="socials.network.bluesky.state">`, `</span>`); chip != "connected" {
		t.Errorf("bluesky = %q", chip)
	}
	if _, ok := g.keychain["keychain:postiz-env/password"]; ok || len(g.keychain) != 1 {
		t.Errorf("the app password was kept: %v", g.keychain)
	}
	g.noSentinelAnywhere(rec.Body.String(), rec.Header().Get("Location"), page)
}

func TestSocialsDisconnectNeedsTheConfirmAndStopsAfter(t *testing.T) {
	g := newSocialsRig(t, true)
	g.channels = []map[string]any{{"id": "ch-li", "providerIdentifier": "linkedin"}}
	g.do("POST", "/p/acme/socials/marketing/check", nil)
	if rec := g.do("POST", "/p/acme/socials/marketing/disconnect", url.Values{"channel": {"ch-li"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("without confirm = %d", rec.Code)
	}
	rec := g.do("POST", "/p/acme/socials/marketing/disconnect", url.Values{"channel": {"ch-li"}, "confirm": {"yes"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("disconnect = %d\n%s", rec.Code, rec.Body)
	}
	if len(g.channels) != 0 {
		t.Errorf("channels left: %v", g.channels)
	}
	g.mustStopped("Disconnect")
	if rec := g.do("POST", "/p/acme/socials/marketing/disconnect", url.Values{"channel": {"ch-other"}, "confirm": {"yes"}}); rec.Code != http.StatusConflict {
		t.Errorf("an unknown channel = %d", rec.Code)
	}
}

func TestSocialsAWrongKeySaysSoWithoutTheKey(t *testing.T) {
	g := newSocialsRig(t, true)
	g.keychain["keychain:postiz"] = "SENTINEL-wrong-key-0000"
	rec := g.do("POST", "/p/acme/socials/marketing/check", nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "refused the key") || strings.Contains(rec.Body.String(), "SENTINEL-wrong-key") {
		t.Fatalf("check = %d\n%s", rec.Code, rec.Body)
	}
	g.mustStopped("a refused check")
}

func TestSocialsRefusesStrangersAndCrossOriginWrites(t *testing.T) {
	g := newSocialsRig(t, true)
	for _, path := range []string{"/p/acme/socials/marketing", "/p/acme/socials/marketing/check"} {
		method := "POST"
		if !strings.HasSuffix(path, "/check") {
			method = "GET"
		}
		req := httptest.NewRequest(method, path, nil)
		req.Host = "127.0.0.1:8080"
		rec := httptest.NewRecorder()
		g.srv.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without the owner = %d", method, path, rec.Code)
		}
	}
	req := httptest.NewRequest("POST", "/p/acme/socials/marketing/key", strings.NewReader(url.Values{"key": {sentinelKey}}.Encode()))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(g.cookie)
	rec := httptest.NewRecorder()
	g.srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin key write = %d", rec.Code)
	}
	if g.keychain["keychain:postiz"] != sentinelKey {
		t.Error("a cross-origin write changed the key")
	}
	if g.up() || len(g.calls) != 0 {
		t.Errorf("a refused request reached Postiz: %v", g.calls)
	}
}

func TestSocialsEntryIsTheTemplatesExampleWithUpAndTheOverride(t *testing.T) {
	item, err := socialEntry("http://127.0.0.1:4007", "~/postiz/compose.local.yaml", "/state/socials/postiz/compose.override.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"url: http://127.0.0.1:4007/api/mcp", "-f ~/postiz/compose.local.yaml -f /state/socials/postiz/compose.override.yaml up -d'", "-f ~/postiz/compose.local.yaml stop", "tool: postiz", "sends: true"} {
		if !strings.Contains(item, want) {
			t.Errorf("entry misses %q:\n%s", want, item)
		}
	}
	for _, bad := range [][2]string{{"http://10.0.0.5:4007", "~/c.yaml"}, {"http://127.0.0.1:4007/x", "~/c.yaml"}, {"http://127.0.0.1:4007", "~/my compose.yaml"}, {"http://127.0.0.1:4007", "~/c.yaml'; rm -rf ~"}} {
		if _, err := socialEntry(bad[0], bad[1], "/s/o.yaml"); err == nil {
			t.Errorf("socialEntry(%q, %q) accepted", bad[0], bad[1])
		}
	}
}

// soBetween is what s holds between a and b ("" when either is missing).
func soBetween(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// docs/departments.md tells the owner how to connect socials from the factory.
func TestDocsShowConnectSocials(t *testing.T) {
	b, err := os.ReadFile(repoPath("docs", "departments.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, want := range []string{
		"### Connecting socials from the factory",
		"/p/<project>/socials/<dept>",
		"Keychain item `postiz`",
		"keychain:postiz-env/<VAR>",
		"compose.override.yaml",
		"redirect URI",
		"app password",
		"<issues>/.connectors/socials.jsonl",
		"stops after (epic D-5)",
		"`security -i` on its stdin",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/departments.md misses %q", want)
		}
	}
}
