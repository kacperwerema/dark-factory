package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The stand-in Ghost's admin key for the integration it makes: Ghost's
// secret field already carries "<id>:<hex>".
const (
	cmsUpKeyID  = "6750c0ffee0000000000beef"
	cmsUpKeyHex = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
)

// setupGhost is Ghost's setup surface on loopback: /site/, the setup
// state and call, a session (cookie), integrations with api_keys, and
// settings. Every call but /site/ and setup wants the Origin header, and
// those after setup want the session cookie, as Ghost's own do.
type setupGhost struct {
	t   *testing.T
	srv *httptest.Server
	mu  sync.Mutex

	setUp                 bool
	email, password       string
	integrations          []string
	linkTagging           bool
	setupCalls, keyPosts  int
	sessions, notReadyFor int
	reqs                  []string
}

func newSetupGhost(t *testing.T) *setupGhost {
	t.Helper()
	g := &setupGhost{t: t, linkTagging: true}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *setupGhost) port() int {
	_, p, _ := net.SplitHostPort(g.srv.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	return n
}

func (g *setupGhost) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	path := strings.TrimPrefix(r.URL.Path, "/ghost/api/admin")
	g.reqs = append(g.reqs, r.Method+" "+path+"?"+r.URL.RawQuery+" "+string(body))
	if path == "/site/" {
		if g.notReadyFor > 0 {
			g.notReadyFor--
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		ghostAnswer(w, 200, map[string]any{"site": map[string]any{"title": "x"}})
		return
	}
	if r.Header.Get("Origin") != "http://"+g.srv.Listener.Addr().String() {
		ghostError(w, 403, "origin "+r.Header.Get("Origin"))
		return
	}
	switch {
	case path == "/authentication/setup/" && r.Method == "GET":
		ghostAnswer(w, 200, map[string]any{"setup": []map[string]any{{"status": g.setUp}}})
		return
	case path == "/authentication/setup/" && r.Method == "POST":
		if g.setUp {
			ghostError(w, 403, "Setup has already been completed.")
			return
		}
		var in struct {
			Setup []struct{ Name, Email, Password, BlogTitle string } `json:"setup"`
		}
		if json.Unmarshal(body, &in) != nil || len(in.Setup) != 1 || len(in.Setup[0].Password) < 10 || in.Setup[0].BlogTitle == "" {
			ghostError(w, 422, "bad setup")
			return
		}
		g.setUp, g.email, g.password = true, in.Setup[0].Email, in.Setup[0].Password
		g.setupCalls++
		ghostAnswer(w, 201, map[string]any{"users": []map[string]any{{"email": g.email}}})
		return
	case path == "/session/" && r.Method == "POST":
		var in struct{ Username, Password string }
		if json.Unmarshal(body, &in) != nil || !g.setUp || in.Username != g.email || in.Password != g.password {
			ghostError(w, 422, "Your password is incorrect.")
			return
		}
		g.sessions++
		http.SetCookie(w, &http.Cookie{Name: "ghost-admin-api-session", Value: "s" + strconv.Itoa(g.sessions), Path: "/ghost"})
		w.WriteHeader(201)
		return
	}
	if c, err := r.Cookie("ghost-admin-api-session"); err != nil || c.Value == "" {
		ghostError(w, 403, "Authorization failed")
		return
	}
	switch {
	case path == "/integrations/" && r.Method == "GET":
		var list []map[string]any
		for _, n := range g.integrations {
			list = append(list, ghostIntegrationJSON(n))
		}
		ghostAnswer(w, 200, map[string]any{"integrations": list})
	case path == "/integrations/" && r.Method == "POST":
		var in struct {
			Integrations []struct{ Name string } `json:"integrations"`
		}
		if json.Unmarshal(body, &in) != nil || len(in.Integrations) != 1 {
			ghostError(w, 422, "bad integration")
			return
		}
		g.integrations = append(g.integrations, in.Integrations[0].Name)
		g.keyPosts++
		ghostAnswer(w, 201, map[string]any{"integrations": []map[string]any{ghostIntegrationJSON(in.Integrations[0].Name)}})
	case path == "/settings/" && r.Method == "PUT":
		var in struct {
			Settings []struct {
				Key   string
				Value any
			} `json:"settings"`
		}
		if json.Unmarshal(body, &in) != nil {
			ghostError(w, 422, "bad settings")
			return
		}
		for _, s := range in.Settings {
			if s.Key == "outbound_link_tagging" {
				g.linkTagging = s.Value != false
			}
		}
		ghostAnswer(w, 200, map[string]any{"settings": []map[string]any{{"key": "outbound_link_tagging", "value": g.linkTagging}}})
	default:
		ghostError(w, 404, "no route "+r.Method+" "+path)
	}
}

func ghostIntegrationJSON(name string) map[string]any {
	return map[string]any{"id": "int1", "name": name, "api_keys": []map[string]any{
		{"id": "content1", "type": "content", "secret": "c0ffee"},
		{"id": cmsUpKeyID, "type": "admin", "secret": cmsUpKeyID + ":" + cmsUpKeyHex},
	}}
}

// cmsUpRig is one machine for cms up: stand-in docker, security and
// mem-guard on PATH, each logging its argv, a Keychain folder, and a data
// home outside any repository.
type cmsUpRig struct {
	t                    *testing.T
	proj                 *tracker.Project
	ghost                *setupGhost
	data, keychain, argv string
}

// cmsStandIns: docker and mem-guard log their argv and answer from the
// environment; security keeps items as files in $CMS_KEYCHAIN, reads
// security -i's add-generic-password line from stdin (the value only
// there), and answers find-generic-password with or without -w.
const cmsStandInSecurity = `printf '%s\n' "security $*" >> "$CMS_ARGV_LOG"
if [ "$1" = "-i" ]; then
  while IFS= read -r line; do
    case "$line" in
      quit) exit 0 ;;
      add-generic-password*)
        svc=$(printf '%s' "$line" | sed -n 's/.* -s \([^ ]*\).*/\1/p')
        acct=$(printf '%s' "$line" | sed -n 's/.* -a \([^ ]*\).*/\1/p')
        val=$(printf '%s' "$line" | sed -n 's/.* -w "\(.*\)"$/\1/p')
        printf '%s' "$val" > "$CMS_KEYCHAIN/$svc.$acct" ;;
    esac
  done
  exit 0
fi
svc=; acct=; w=
while [ $# -gt 0 ]; do
  case "$1" in -s) svc=$2; shift ;; -a) acct=$2; shift ;; -w) w=1 ;; esac
  shift
done
f="$CMS_KEYCHAIN/$svc.$acct"
[ -f "$f" ] || exit 44
[ -n "$w" ] && cat "$f" && echo
exit 0
`

const cmsStandInLogExit = `printf '%s\n' "${0##*/} $*" >> "$CMS_ARGV_LOG"
case "${0##*/}" in
  mem-guard) [ -n "$CMS_MEMGUARD" ] && printf '%s\n' "$CMS_MEMGUARD"; exit "${CMS_MEMGUARD_EXIT:-0}" ;;
  docker) exit "${CMS_DOCKER_EXIT:-0}" ;;
esac
`

func newCmsUpRig(t *testing.T) *cmsUpRig {
	t.Helper()
	r := &cmsUpRig{t: t, proj: newTestProject(t), ghost: newSetupGhost(t)}
	r.data, r.keychain = realTempDir(t), realTempDir(t)
	bin := realTempDir(t)
	r.argv = filepath.Join(realTempDir(t), "argv.log")
	standin.Write(t, filepath.Join(bin, "security"), cmsStandInSecurity)
	standin.Write(t, filepath.Join(bin, "docker"), cmsStandInLogExit)
	standin.Write(t, filepath.Join(bin, "mem-guard"), cmsStandInLogExit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_DATA_HOME", r.data)
	t.Setenv("CMS_KEYCHAIN", r.keychain)
	t.Setenv("CMS_ARGV_LOG", r.argv)
	t.Setenv("CMS_MEMGUARD", "ok: memory used 30% (start<75)")
	t.Setenv("CMS_MEMGUARD_EXIT", "0")
	t.Setenv("CMS_DOCKER_EXIT", "0")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	oldOS, oldTimeout, oldPoll := tracker.KeychainOS, cmsReadyTimeout, cmsPollEvery
	tracker.KeychainOS, cmsReadyTimeout, cmsPollEvery = "darwin", 10*time.Second, 10*time.Millisecond
	t.Cleanup(func() { tracker.KeychainOS, cmsReadyTimeout, cmsPollEvery = oldOS, oldTimeout, oldPoll })
	return r
}

func (r *cmsUpRig) dir() string { return filepath.Join(r.data, "ai-workflow-engine", "cms", "acme") }

func (r *cmsUpRig) run(args ...string) (string, error) {
	ctx, out, errOut := newTestContext(r.proj, false)
	err := runCms(ctx, append([]string{"up", "--port", strconv.Itoa(r.ghost.port()), "--email", "owner@example.com"}, args...))
	return out.String() + errOut.String(), err
}

func (r *cmsUpRig) item(name string) string {
	b, err := os.ReadFile(filepath.Join(r.keychain, name+".acme"))
	if err != nil {
		return ""
	}
	return string(b)
}

func (r *cmsUpRig) argvLog() string {
	b, _ := os.ReadFile(r.argv)
	return string(b)
}

// TestCmsUpStandsGhostUp: one run writes the compose, starts it, sets
// Ghost up with a random password that went to Keychain first, puts the
// integration's admin key in Keychain, and turns link tagging off.
func TestCmsUpStandsGhostUp(t *testing.T) {
	r := newCmsUpRig(t)
	r.ghost.notReadyFor = 3
	out, err := r.run()
	if err != nil {
		t.Fatalf("cms up: %v\n%s", err, out)
	}
	g := r.ghost
	if !g.setUp || g.setupCalls != 1 || g.email != "owner@example.com" {
		t.Fatalf("setup: done %v, calls %d, email %q", g.setUp, g.setupCalls, g.email)
	}
	if pw := r.item("ghost-owner"); pw == "" || pw != g.password || len(pw) < 30 {
		t.Errorf("Keychain ghost-owner holds %d chars, Ghost got %d: want the same random password", len(pw), len(g.password))
	}
	if k := r.item("ghost-admin-key"); k != cmsUpKeyID+":"+cmsUpKeyHex {
		t.Errorf("Keychain ghost-admin-key = %q, want the integration's <id>:<hex>", k)
	}
	if g.linkTagging {
		t.Error("outbound_link_tagging is still on")
	}
	if g.keyPosts != 1 || len(g.integrations) != 1 || g.integrations[0] != "Dark Factory publish" {
		t.Errorf("integrations %v (posts %d)", g.integrations, g.keyPosts)
	}
	log := r.argvLog()
	for _, want := range []string{
		"mem-guard check",
		"docker compose -p ghost-acme -f " + filepath.Join(r.dir(), "compose.yaml") + " up --detach",
		"security -i",
	} {
		assertContains(t, log, want)
	}
	if strings.Index(log, "mem-guard check") > strings.Index(log, "docker compose") {
		t.Errorf("docker ran before mem-guard check:\n%s", log)
	}
	for _, want := range []string{"✓ compose: written", "✓ ghost: running", "✓ owner account: set up", "✓ admin key: integration made", "✓ link tagging: off",
		"/ghost as owner@example.com", "security find-generic-password -s ghost-owner -a acme -w", "uncomment the cms entry", "docs/stand-up-the-cms.md"} {
		assertContains(t, out, want)
	}
	st, ok := readCmsState(r.dir())
	if !ok || st.Email != "owner@example.com" || st.Port != g.port() || st.Title != "acme" {
		t.Errorf("ghost.json = %+v (ok %v)", st, ok)
	}
}

// TestCmsUpComposeIsLoopbackOnly: the compose listens on 127.0.0.1 only,
// restarts unless stopped, has the pings off and mail pointed nowhere, and
// sits outside every repository.
func TestCmsUpComposeIsLoopbackOnly(t *testing.T) {
	r := newCmsUpRig(t)
	if out, err := r.run(); err != nil {
		t.Fatalf("cms up: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(r.dir(), "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c := string(b)
	p := strconv.Itoa(r.ghost.port())
	for _, want := range []string{
		`ports: ["127.0.0.1:` + p + `:2368"]`, "restart: unless-stopped", "url: http://127.0.0.1:" + p,
		`privacy__useUpdateCheck: "false"`, `privacy__useGravatar: "false"`, `privacy__useRpcPing: "false"`,
		"mail__transport: SMTP", "mail__options__host: 127.0.0.1", `security__staffDeviceVerification: "false"`,
	} {
		assertContains(t, c, want)
	}
	for _, l := range strings.Split(c, "\n") {
		if strings.Contains(l, "ports:") && strings.Count(l, ":") != 3 {
			t.Errorf("a port without its host: %q", l)
		}
	}
	if strings.Contains(c, "0.0.0.0") {
		t.Error("the compose listens on every interface")
	}
	if repo := gitWorkTreeAbove(r.dir()); repo != "" {
		t.Errorf("the compose is inside the git work tree %s", repo)
	}
}

// TestCmsUpSecretsNeverPrintedOrInArgv: neither the owner password nor the
// admin key is in the output, any stand-in's argv, or a file of the folder.
func TestCmsUpSecretsNeverPrintedOrInArgv(t *testing.T) {
	r := newCmsUpRig(t)
	out, err := r.run()
	if err != nil {
		t.Fatalf("cms up: %v\n%s", err, out)
	}
	ctx, jout, jerr := newTestContext(r.proj, true)
	if err := runCms(ctx, []string{"up", "--port", strconv.Itoa(r.ghost.port())}); err != nil {
		t.Fatalf("cms up --json: %v", err)
	}
	pw, key := r.item("ghost-owner"), r.item("ghost-admin-key")
	if pw == "" || key == "" {
		t.Fatal("Keychain lacks an item")
	}
	files := map[string]string{"output": out + jout.String() + jerr.String(), "argv": r.argvLog()}
	_ = filepath.WalkDir(r.dir(), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			files[p] = string(b)
		}
		return nil
	})
	for where, s := range files {
		for name, secret := range map[string]string{"password": pw, "admin key": key, "key hex": cmsUpKeyHex} {
			if strings.Contains(s, secret) {
				t.Errorf("the %s is in %s", name, where)
			}
		}
	}
}

// TestCmsUpTwiceIsSafe: a second run sets nothing up again and makes no
// second integration; with the admin key gone from Keychain it reuses the
// integration Ghost has. The compose is kept, an edited one too.
func TestCmsUpTwiceIsSafe(t *testing.T) {
	r := newCmsUpRig(t)
	if out, err := r.run(); err != nil {
		t.Fatalf("first: %v\n%s", err, out)
	}
	pw := r.item("ghost-owner")
	out, err := r.run()
	if err != nil {
		t.Fatalf("second: %v\n%s", err, out)
	}
	for _, want := range []string{"✓ compose: already there", "✓ owner account: already set up", "✓ admin key: already in Keychain", "✓ link tagging: off"} {
		assertContains(t, out, want)
	}
	if r.ghost.setupCalls != 1 || r.ghost.keyPosts != 1 || r.item("ghost-owner") != pw {
		t.Fatalf("setup calls %d, integration posts %d, password kept %v", r.ghost.setupCalls, r.ghost.keyPosts, r.item("ghost-owner") == pw)
	}

	if err := os.Remove(filepath.Join(r.keychain, "ghost-admin-key.acme")); err != nil {
		t.Fatal(err)
	}
	compose := filepath.Join(r.dir(), "compose.yaml")
	edited := strings.Replace(cmsCompose(r.ghost.port()), "url: http://127.0.0.1", "url: https://orchard.example #", 1)
	if err := os.WriteFile(compose, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	r.ghost.linkTagging = true
	out, err = r.run()
	if err != nil {
		t.Fatalf("third: %v\n%s", err, out)
	}
	assertContains(t, out, "✓ admin key: integration reused")
	assertContains(t, out, "✓ compose: kept")
	if r.ghost.keyPosts != 1 || len(r.ghost.integrations) != 1 || r.item("ghost-admin-key") != cmsUpKeyID+":"+cmsUpKeyHex || r.ghost.linkTagging {
		t.Errorf("posts %d, integrations %v, key back %v, tagging %v", r.ghost.keyPosts, r.ghost.integrations, r.item("ghost-admin-key") != "", r.ghost.linkTagging)
	}
	if b, _ := os.ReadFile(compose); string(b) != edited {
		t.Error("the owner's edited compose was overwritten")
	}

	ctx, _, _ := newTestContext(r.proj, false)
	if err := runCms(ctx, []string{"up", "--port", "2999"}); err == nil || !strings.Contains(err.Error(), "runs on port "+strconv.Itoa(r.ghost.port())) {
		t.Errorf("another --port on a standing Ghost: %v", err)
	}
}

// TestCmsUpRefusesWhenMemGuardBlocked: nothing written, nothing started.
func TestCmsUpRefusesWhenMemGuardBlocked(t *testing.T) {
	r := newCmsUpRig(t)
	t.Setenv("CMS_MEMGUARD", "BLOCKED: memory used 80% (start<75)")
	t.Setenv("CMS_MEMGUARD_EXIT", "1")
	_, err := r.run()
	if err == nil || !strings.Contains(err.Error(), "mem-guard check is blocked") || !strings.Contains(err.Error(), "BLOCKED: memory used 80%") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(r.dir()); !os.IsNotExist(err) {
		t.Errorf("the folder was made under a blocked mem-guard (%v)", err)
	}
	if log := r.argvLog(); strings.Contains(log, "docker") || strings.Contains(log, "security") || len(r.ghost.reqs) > 0 {
		t.Errorf("something ran under a blocked mem-guard:\n%s\n%v", log, r.ghost.reqs)
	}
}

// TestCmsUpLeavesProjectsYamlAlone: the cms connector stays commented; the
// owner turns it on, so projects.yaml is the same byte for byte.
func TestCmsUpLeavesProjectsYamlAlone(t *testing.T) {
	r := newCmsUpRig(t)
	cfg := filepath.Join(filepath.Dir(r.proj.IssueDir), "projects.yaml")
	body := "projects:\n  - name: acme\n    departments:\n      - name: Marketing\n        # connectors:\n        #   - name: cms\n        #     kind: tool\n"
	mustWrite(t, cfg, body)
	r.proj.ConfigPath = cfg
	out, err := r.run()
	if err != nil {
		t.Fatalf("cms up: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(cfg); string(b) != body {
		t.Errorf("projects.yaml changed:\n%s", b)
	}
	assertContains(t, out, "Turn the cms connector on (cms up wrote no config): uncomment the cms entry")
	assertContains(t, out, cfg)
}

// TestCmsUpRefusesAWorker: a worker gets the refusal and its dry run; the
// dry run changes nothing.
func TestCmsUpRefusesAWorker(t *testing.T) {
	r := newCmsUpRig(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "plain")
	if _, err := r.run(); err == nil || !strings.Contains(err.Error(), "workers never stand up the CMS") {
		t.Fatalf("worker: err = %v", err)
	}
	out, err := r.run("--dry-run")
	if err != nil || !strings.Contains(out, "Dry run: cms up (nothing changed)") || !strings.Contains(out, "restart: unless-stopped") {
		t.Fatalf("worker dry run: %v\n%s", err, out)
	}
	if _, err := os.Stat(r.dir()); !os.IsNotExist(err) || r.argvLog() != "" || len(r.ghost.reqs) > 0 {
		t.Errorf("a refused or dry run did something: dir %v, argv %q, requests %v", err, r.argvLog(), r.ghost.reqs)
	}
}

// TestCmsUpSetUpGhostWithoutItsPassword: a Ghost set up by someone else
// (or a Keychain item removed) is refused with the way out, not set up
// again.
func TestCmsUpSetUpGhostWithoutItsPassword(t *testing.T) {
	r := newCmsUpRig(t)
	r.ghost.setUp, r.ghost.email, r.ghost.password = true, "owner@example.com", "not-in-keychain-1234"
	_, err := r.run()
	if err == nil || !strings.Contains(err.Error(), "set up already, but Keychain has no keychain:ghost-owner/acme") || !strings.Contains(err.Error(), "content") {
		t.Fatalf("err = %v", err)
	}
	if r.ghost.setupCalls != 0 || r.ghost.sessions != 0 {
		t.Errorf("setup %d, sessions %d", r.ghost.setupCalls, r.ghost.sessions)
	}
}

// TestCmsUpRefusesOffMacOSAndWithoutDocker: no Keychain, or no docker:
// refused before anything is written.
func TestCmsUpRefusesOffMacOSAndWithoutDocker(t *testing.T) {
	r := newCmsUpRig(t)
	tracker.KeychainOS = "linux"
	if _, err := r.run(); err == nil || !strings.Contains(err.Error(), "only macOS has") {
		t.Fatalf("off macOS: %v", err)
	}
	tracker.KeychainOS = "darwin"
	t.Setenv("PATH", realTempDir(t))
	if _, err := r.run(); err == nil || !strings.Contains(err.Error(), "docker is not on PATH") {
		t.Fatalf("no docker: %v", err)
	}
	if _, err := os.Stat(r.dir()); !os.IsNotExist(err) {
		t.Errorf("the folder was made (%v)", err)
	}
}
