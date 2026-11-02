package viewer

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// accessFixture is a stand-in Cloudflare Access team: a signing key, its
// certs endpoint, and a checker pointed at it.
type accessFixture struct {
	key     *rsa.PrivateKey
	kid     string
	certs   *httptest.Server
	fetches atomic.Int32
	checker *identityChecker
	now     time.Time
}

const (
	testTeam     = "team.cloudflareaccess.com"
	testAudience = "aud-tag-123"
	testOwner    = "owner@example.com"
)

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &accessFixture{key: key, kid: "kid-1", now: time.Unix(1_800_000_000, 0)}
	f.certs = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.fetches.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": f.kid, "kty": "RSA", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(f.certs.Close)
	cfg := &tracker.IdentityConfig{Owners: []string{"Owner@Example.com"}}
	cfg.CloudflareAccess.TeamDomain = testTeam
	cfg.CloudflareAccess.Audience = testAudience
	cfg.CloudflareAccess.CertsURL = f.certs.URL
	f.checker, err = newIdentityChecker(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.checker.now = func() time.Time { return f.now }
	return f
}

// token signs claims (defaults: a valid owner token) with header alg/kid.
func (f *accessFixture) token(t *testing.T, edit func(hdr, claims map[string]any)) string {
	t.Helper()
	hdr := map[string]any{"alg": "RS256", "kid": f.kid}
	claims := map[string]any{
		"iss": "https://" + testTeam, "aud": []string{testAudience},
		"exp": f.now.Add(time.Hour).Unix(), "nbf": f.now.Add(-time.Minute).Unix(),
		"email": testOwner,
	}
	if edit != nil {
		edit(hdr, claims)
	}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(hdr) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok " + verifiedIdentity(r)))
	})
}

func gateStatus(t *testing.T, h http.Handler, host string, hdr map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/p/x/", nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// The barrier for going public: without identity: in projects.yaml, a
// public name or a proxied request is refused, whatever else is set.
func TestIdentityGate_FailsClosedWithoutIdentity(t *testing.T) {
	h := identityGate(nil, okHandler())
	for _, tc := range []struct {
		host string
		hdr  map[string]string
	}{
		{"factory.example.com", nil},
		{"viewer.lan:8080", nil},
		{"192.168.1.20:8080", nil},
		{"factory.example.com", map[string]string{cfAccessHeader: "anything"}},
		// A tunnel that rewrites Host to localhost still sends its headers.
		{"localhost:8080", map[string]string{"Cf-Ray": "abc"}},
		{"127.0.0.1:8080", map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}},
		{"127.0.0.1:8080", map[string]string{"X-Forwarded-For": "203.0.113.9"}},
		{"localhost", map[string]string{"Forwarded": "for=203.0.113.9"}},
	} {
		code, body := gateStatus(t, h, tc.host, tc.hdr)
		if code != http.StatusForbidden || !strings.Contains(body, "no identity check") {
			t.Errorf("host %s %v = %d %q, want 403 naming the missing identity check", tc.host, tc.hdr, code, body)
		}
	}
	for _, host := range []string{"localhost:8080", "127.0.0.1", "[::1]:8080", "app.localhost"} {
		if code, _ := gateStatus(t, h, host, nil); code != http.StatusOK {
			t.Errorf("local host %s = %d, want 200", host, code)
		}
	}
}

func TestIdentityGate_FullRoutesFailClosed(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, err := NewServer([]tracker.Project{proj})
	if err != nil {
		t.Fatal(err)
	}
	srv.allowedHosts = allowedHostsFor(defaultHost, []string{"factory.example.com"})
	for _, path := range []string{"/", "/p/test-project/", "/p/test-project/issues.json", "/static/style.css"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "factory.example.com"
		rec := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s on a public Host without identity = %d, want 403", path, rec.Code)
		}
	}
	req := httptest.NewRequest("POST", "/p/test-project/issues/create", strings.NewReader(`{"title":"x"}`))
	req.Host = "factory.example.com"
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST create on a public Host without identity = %d, want 403", rec.Code)
	}
}

func TestIdentityGate_VerifiesCloudflareAccessToken(t *testing.T) {
	f := newAccessFixture(t)
	h := identityGate(f.checker, okHandler())
	public := "factory.example.com"

	code, body := gateStatus(t, h, public, map[string]string{cfAccessHeader: f.token(t, nil)})
	if code != http.StatusOK || body != "ok "+testOwner {
		t.Fatalf("valid owner token = %d %q, want 200 with the owner's email", code, body)
	}

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	bad := map[string]string{
		"no token":      "",
		"garbage":       "a.b.c",
		"wrong issuer":  f.token(t, func(_, c map[string]any) { c["iss"] = "https://evil.cloudflareaccess.com" }),
		"wrong aud":     f.token(t, func(_, c map[string]any) { c["aud"] = "other-app" }),
		"expired":       f.token(t, func(_, c map[string]any) { c["exp"] = f.now.Add(-time.Hour).Unix() }),
		"no exp":        f.token(t, func(_, c map[string]any) { delete(c, "exp") }),
		"not yet valid": f.token(t, func(_, c map[string]any) { c["nbf"] = f.now.Add(time.Hour).Unix() }),
		"not an owner":  f.token(t, func(_, c map[string]any) { c["email"] = "someone@example.com" }),
		"no email":      f.token(t, func(_, c map[string]any) { delete(c, "email") }),
		"alg none":      f.token(t, func(h, _ map[string]any) { h["alg"] = "none" }),
		"alg HS256":     f.token(t, func(h, _ map[string]any) { h["alg"] = "HS256" }),
		"unknown kid":   f.token(t, func(h, _ map[string]any) { h["kid"] = "kid-2" }),
		"tampered": func() string {
			tok := f.token(t, nil)
			parts := strings.Split(tok, ".")
			claims, _ := json.Marshal(map[string]any{"iss": "https://" + testTeam, "aud": testAudience, "exp": f.now.Add(time.Hour).Unix(), "email": "attacker@example.com"})
			return parts[0] + "." + base64.RawURLEncoding.EncodeToString(claims) + "." + parts[2]
		}(),
		"other key": func() string {
			saved := f.key
			f.key = other
			defer func() { f.key = saved }()
			return f.token(t, nil)
		}(),
	}
	for name, tok := range bad {
		if code, body := gateStatus(t, h, public, map[string]string{cfAccessHeader: tok}); code != http.StatusForbidden {
			t.Errorf("%s: = %d %q, want 403", name, code, body)
		}
	}

	// A local request needs no token and carries no identity.
	if code, body := gateStatus(t, h, "localhost:8080", nil); code != http.StatusOK || body != "ok " {
		t.Errorf("local = %d %q", code, body)
	}
}

// Made-up key ids can't make the viewer fetch the certs on every request.
func TestIdentityChecker_RefetchesKeysAtMostOncePerMinute(t *testing.T) {
	f := newAccessFixture(t)
	h := identityGate(f.checker, okHandler())
	for i := 0; i < 20; i++ {
		tok := f.token(t, func(h, _ map[string]any) { h["kid"] = "made-up" })
		gateStatus(t, h, "factory.example.com", map[string]string{cfAccessHeader: tok})
	}
	if n := f.fetches.Load(); n != 1 {
		t.Errorf("certs fetched %d times for 20 unknown kids, want 1", n)
	}
	f.now = f.now.Add(2 * refetchEvery)
	if code, _ := gateStatus(t, h, "factory.example.com", map[string]string{cfAccessHeader: f.token(t, nil)}); code != http.StatusOK {
		t.Errorf("valid token after the refetch window = %d", code)
	}
}

func TestNewIdentityChecker_IncompleteConfigRefusesToStart(t *testing.T) {
	if c, err := newIdentityChecker(nil); c != nil || err != nil {
		t.Errorf("nil config = %v, %v; want no checker, no error", c, err)
	}
	for name, cfg := range map[string]*tracker.IdentityConfig{
		"empty": {},
		"no owners": func() *tracker.IdentityConfig {
			c := &tracker.IdentityConfig{}
			c.CloudflareAccess.TeamDomain, c.CloudflareAccess.Audience = testTeam, testAudience
			return c
		}(),
		"no audience": func() *tracker.IdentityConfig {
			c := &tracker.IdentityConfig{Owners: []string{testOwner}}
			c.CloudflareAccess.TeamDomain = testTeam
			return c
		}(),
	} {
		if _, err := newIdentityChecker(cfg); err == nil {
			t.Errorf("%s: no error, want the viewer to refuse to start", name)
		}
	}
}

func TestSecurityHeaders_OnEveryAnswer(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, err := NewServer([]tracker.Project{proj})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, host string }{
		{"/p/test-project/", "localhost"},
		{"/p/test-project/issues.json", "localhost"},
		{"/p/test-project/", "attacker.example"}, // 421
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.Host = tc.host
		rec := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rec, req)
		h := rec.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "SAMEORIGIN" ||
			!strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'self'") || h.Get("Referrer-Policy") == "" {
			t.Errorf("%s (Host %s): headers %v", tc.path, tc.host, h)
		}
	}
}

func TestSecurityHeaders_CapsRequestBody(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, err := NewServer([]tracker.Project{proj})
	if err != nil {
		t.Fatal(err)
	}
	big := `{"title":"` + strings.Repeat("a", maxBodyBytes+1) + `"}`
	req := httptest.NewRequest("POST", "/p/test-project/issues/create", strings.NewReader(big))
	req.Host = "localhost"
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code < 400 {
		t.Errorf("a %d-byte body = %d, want refused", len(big), rec.Code)
	}
}
