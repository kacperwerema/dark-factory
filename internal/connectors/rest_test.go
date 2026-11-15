package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func TestRestPath(t *testing.T) {
	c := &tracker.Connector{Name: "stats", Kind: tracker.ConnectorRest, Get: []string{"/v1/stats/*", "/v1/sites", " /v1/sites ", "bad"}}
	for raw, want := range map[string]string{
		"/v1/stats/abc":              "/v1/stats/abc",
		"/v1/stats/abc?date=7&x=a b": "", // a space
		"/v1/stats/a%20b?date=7":     "/v1/stats/a%20b?date=7",
		"/v1/sites":                  "/v1/sites",
		"/v1/stats/abc/def":          "", // * is one segment
		"/v1/stats/":                 "",
		"/v1/stats/./abc":            "",
		"/v1/stats/a%2Fb":            "",
		"/v1//sites":                 "",
		"//host/v1/sites":            "",
		"https://host/v1/sites":      "",
		"v1/sites":                   "",
		"/v1/sites#frag":             "",
		"/v1/sites\\x":               "",
		"/v1/sites\n":                "",
		"bad":                        "",
	} {
		got, err := RestPath("m/stats", c, raw)
		if want == "" {
			if err == nil {
				t.Errorf("%q passed as %q", raw, got)
			} else if !strings.Contains(err.Error(), "reads only GET /v1/stats/*, /v1/sites, bad") {
				t.Errorf("%q: %v", raw, err)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", raw, got, err, want)
		}
	}
}

func TestRestPatternProblem(t *testing.T) {
	for g, bad := range map[string]bool{"/a/*": false, "/a": false, "a": true, "//a": true, "/a/": true, "/a/../b": true, "/a?x": true, "/[": true} {
		if got := RestPatternProblem(g) != ""; got != bad {
			t.Errorf("%q problem = %v, want %v", g, got, bad)
		}
	}
}

func TestRestPatternWide(t *testing.T) {
	for g, want := range map[string]string{
		"/*": "/<resource>/*", "/*/status": "/<resource>/status", "/[a-z]*/x/*": "/<resource>/x/*",
		"/public/v1/analytics/*": "", "/public/v1/posts": "", "/": "", "/a/*": "", "/*/../x": "", "/[": "",
	} {
		if got := RestPatternWide(g); got != (want != "") {
			t.Errorf("%q wide = %v, want %v", g, got, want != "")
		} else if got && RestPatternNarrower(g) != want {
			t.Errorf("%q narrower = %q, want %q", g, RestPatternNarrower(g), want)
		}
	}
}

func TestRestReadScrubsAndStaysOnItsHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method %s", r.Method)
		}
		w.Write([]byte("plain " + r.Header.Get("Authorization")))
	}))
	defer srv.Close()
	h := http.Header{"Authorization": {"Bearer key-1234"}}
	a := RestRead(context.Background(), RestClient(), srv.URL+"/api", h, "/x")
	if !a.OK() || string(a.Body) != `"plain [secret]"` {
		t.Errorf("answer = %+v (%s)", a, a.Body)
	}
	// A base with no path: "@host" would turn the base into userinfo.
	if _, err := restURL("http://127.0.0.1:1", "@evil.example/x"); err == nil {
		t.Error("a path that changes the host passed")
	}
}
