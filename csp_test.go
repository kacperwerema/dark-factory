package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// F-12 (docs/API/security.md): pages carry script-src 'self' with a nonce
// per request, every inline script has it, and the marker never leaves.
func TestPageCSPHasAScriptNonce(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	seen := map[string]bool{}
	for _, path := range []string{
		"/p/test-project/", "/p/test-project/board?classic=1", "/p/test-project/board", "/p/test-project/workboard",
		"/p/test-project/issue/bug-in-login?classic=1", "/p/test-project/issue/bug-in-login", "/p/test-project/status?classic=1",
		"/p/test-project/status", "/p/test-project/agents?classic=1", "/p/test-project/agents", "/p/test-project/retros",
		"/p/test-project/docs", "/p/test-project/graph", "/p/test-project/stats", "/p/test-project/systems", "/",
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body := string(raw)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", path, resp.StatusCode)
			continue
		}
		csp := resp.Header.Get("Content-Security-Policy")
		m := regexp.MustCompile(`script-src 'self' 'nonce-([0-9a-f]{32})'`).FindStringSubmatch(csp)
		if m == nil {
			t.Fatalf("%s: CSP %q has no script nonce", path, csp)
		}
		nonce := m[1]
		if seen[nonce] {
			t.Errorf("%s: nonce %s reused", path, nonce)
		}
		seen[nonce] = true
		if !strings.Contains(csp, "frame-ancestors 'self'") {
			t.Errorf("%s: CSP lost the base policy: %q", path, csp)
		}
		if strings.Contains(body, nonceMarker) {
			t.Errorf("%s: the nonce marker leaked into the page", path)
		}
		for _, tag := range regexp.MustCompile(`<script[^>]*>`).FindAllString(body, -1) {
			if strings.Contains(tag, "src=") || strings.Contains(tag, "application/json") {
				continue
			}
			if !strings.Contains(tag, `nonce="`+nonce+`"`) {
				t.Errorf("%s: inline script without the request's nonce: %s", path, tag)
			}
		}
		if regexp.MustCompile(`<[a-z][^>]*\son[a-z]+=`).MatchString(body) {
			t.Errorf("%s: page has an inline on*= handler: %s", path, regexp.MustCompile(`<[a-z][^>]*\son[a-z]+=[^>]*>`).FindString(body))
		}
	}
}

// Routes that serve untrusted files keep their own policy, unchanged.
func TestOwnPolicyRoutesKeepTheirCSP(t *testing.T) {
	proj, _ := setupPagesProject(t, pagesWorkflow)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	resp, _ := pageResp(t, mustReq(t, "GET", ts.URL+"/p/atlas/page-raw/docs/status/index.html"))
	if csp := resp.Header.Get("Content-Security-Policy"); csp != pageRawCSP {
		t.Errorf("page-raw CSP = %q, want pageRawCSP", csp)
	}
}

// The templates and the HTML Go code builds hold no on*= handler and no
// inline <script> without the nonce; a new one fails here before it fails
// in the browser.
func TestTemplatesHaveNoInlineHandlers(t *testing.T) {
	handler := regexp.MustCompile(`[\s"'](on(?:click|change|input|keydown|keyup|blur|focus|submit|load|error|mouse\w+|dblclick))=`)
	script := regexp.MustCompile(`<script(\s[^>]*)?>`)
	files, _ := filepath.Glob("templates/*.html")
	gofiles, _ := filepath.Glob("*.go")
	for _, f := range gofiles {
		if !strings.HasSuffix(f, "_test.go") {
			files = append(files, f)
		}
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasSuffix(f, ".go") && strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if m := handler.FindStringSubmatch(line); m != nil {
				t.Errorf("%s:%d: inline %s= handler; use data-%s=\"{{onCall …}}\" (csp.go)", f, i+1, m[1], strings.TrimPrefix(m[1], "on"))
			}
			if !strings.HasSuffix(f, ".html") {
				continue
			}
			for _, tag := range script.FindAllString(line, -1) {
				if !strings.Contains(tag, "src=") && !strings.Contains(tag, "application/json") && !strings.Contains(tag, `nonce="{{cspNonce}}"`) {
					t.Errorf("%s:%d: inline script without nonce=\"{{cspNonce}}\": %s", f, i+1, tag)
				}
			}
		}
	}
}

// Every function a data-* call names exists in a template script or in
// static/*.js, so a renamed function doesn't leave a dead button.
func TestDataCallsNameRealFunctions(t *testing.T) {
	var code strings.Builder
	for _, glob := range []string{"templates/*.html", "static/*.js"} {
		files, _ := filepath.Glob(glob)
		for _, f := range files {
			b, _ := os.ReadFile(f)
			code.Write(b)
		}
	}
	all := code.String()
	b, _ := os.ReadFile("handlers_detail.go")
	calls := regexp.MustCompile(`onCall "(\w+)"`).FindAllStringSubmatch(all, -1)
	calls = append(calls, regexp.MustCompile(`dataCall\('\w+', \['(\w+)'`).FindAllStringSubmatch(all, -1)...)
	calls = append(calls, regexp.MustCompile(`data-\w+="\[&#34;(\w+)&#34;`).FindAllStringSubmatch(string(b), -1)...)
	if len(calls) < 50 {
		t.Fatalf("found only %d data calls; the patterns are stale", len(calls))
	}
	for _, c := range calls {
		name := c[1]
		defined := regexp.MustCompile(`function ` + name + `\s*\(|window\.` + name + `\s*=|\b` + name + `\s*=\s*function`)
		if !defined.MatchString(all) {
			t.Errorf("data call to %s, but no function %s is defined in templates or static", name, name)
		}
	}
}

// The writer stamps the nonce even when a template's write splits the
// marker, and leaves other answers alone.
func TestNonceWriterStampsSplitMarker(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page := `<script nonce="` + nonceMarker + `">x()</script>`
		cut := strings.Index(page, nonceMarker) + 7
		w.Write([]byte(page[:cut]))
		w.Write([]byte(page[cut:]))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	nonce := regexp.MustCompile(`'nonce-([0-9a-f]+)'`).FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
	if nonce == nil {
		t.Fatalf("no nonce in %q", rec.Header().Get("Content-Security-Policy"))
	}
	if want := `<script nonce="` + nonce[1] + `">x()</script>`; rec.Body.String() != want {
		t.Errorf("body = %q, want %q", rec.Body.String(), want)
	}

	own := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "sandbox")
		w.Write([]byte(nonceMarker))
	}))
	rec = httptest.NewRecorder()
	own.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Body.String() != nonceMarker {
		t.Errorf("a route with its own policy was rewritten: %q", rec.Body.String())
	}
}

// F-17: a plain-text error that names a file under the home folder shows
// it as ~/…, not the absolute path with the user's name.
func TestErrorBodiesHideTheHomeFolder(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || errorPathScrub == nil {
		t.Skip("no home folder")
	}
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "create failed: open "+filepath.Join(home, "repo", "issues", "x.md")+": permission denied", http.StatusInternalServerError)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", nil))
	body := rec.Body.String()
	if strings.Contains(body, home) || !strings.Contains(body, "open ~/repo/issues/x.md") {
		t.Errorf("error body = %q", body)
	}

	j := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "read " + filepath.Join(home, "x.yaml") + ": bad"})
	}))
	rec = httptest.NewRecorder()
	j.ServeHTTP(rec, httptest.NewRequest("POST", "/", nil))
	if body := rec.Body.String(); strings.Contains(body, home) || !strings.Contains(body, "read ~/x.yaml") {
		t.Errorf("JSON error body = %q", body)
	}
	ok := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"path":"` + home + `"}`))
	}))
	rec = httptest.NewRecorder()
	ok.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), home) {
		t.Errorf("a 200 answer was scrubbed: %q", rec.Body.String())
	}
}
