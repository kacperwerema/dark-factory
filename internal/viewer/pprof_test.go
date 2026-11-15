package viewer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The owner's signed-in browser reads the running viewer's goroutines and
// heap through the whole middleware stack.
func TestPprofServesTheOwner(t *testing.T) {
	srv, _ := ownerTestServer(t)
	cookie := pairOwner(t, srv)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	for path, want := range map[string]string{
		"/debug/pprof/":                  "goroutine",
		"/debug/pprof/goroutine?debug=1": "goroutine profile:",
		"/debug/pprof/heap?debug=1":      "heap profile:",
	} {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.AddCookie(cookie)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
			t.Errorf("GET %s = %d, want 200 with %q: %.200s", path, res.StatusCode, want, body)
		}
	}
}

// Without the owner's cookie the profiles stay shut, and a POST (pprof's
// symbol lookup takes one) gets past nothing even with it.
func TestPprofRefusals(t *testing.T) {
	srv, _ := ownerTestServer(t)
	cookie := pairOwner(t, srv)
	if rec := ownerDo(srv, "GET", "/debug/pprof/heap", "", nil); rec.Code != http.StatusForbidden || rec.Header().Get(ownerSignInHeader) != "/owner" {
		t.Errorf("stranger: %d, header %q; want 403 with the sign-in header", rec.Code, rec.Header().Get(ownerSignInHeader))
	}
	if rec := ownerDo(srv, "POST", "/debug/pprof/symbol", "", map[string]string{"Origin": "http://127.0.0.1:8080"}, cookie); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST as the owner = %d, want 405", rec.Code)
	}
}

// A public request is refused even when it is the owner's: a verified
// identity (what the identity gate sets) opens O routes, not this one.
func TestPprofStaysOnLoopback(t *testing.T) {
	srv, _ := ownerTestServer(t)
	cookie := pairOwner(t, srv)
	for name, set := range map[string]func(*http.Request){
		"public name": func(r *http.Request) { r.Host = "factory.example.com" },
		"tunnel":      func(r *http.Request) { r.Header.Set("Cf-Ray", "1") },
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/debug/pprof/heap", nil)
			req.Host = "127.0.0.1:8080"
			req.AddCookie(cookie)
			set(req)
			req = req.WithContext(context.WithValue(req.Context(), identityKey{}, "owner@example.com"))
			rec := httptest.NewRecorder()
			srv.handlePprof(rec, req)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "only on loopback") {
				t.Errorf("= %d %q, want 403 only on loopback", rec.Code, rec.Body.String())
			}
		})
	}
}
