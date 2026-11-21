package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/pagecheck"
)

// runPageCheckCLI runs page-check with no project configured: the check
// works in any project and outside one.
func runPageCheckCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("ISSUE_VIEWER_CONFIG", filepath.Join(t.TempDir(), "none.yaml"))
	t.Chdir(t.TempDir())
	var out, errw strings.Builder
	err := runTraced(append([]string{"page-check"}, args...), strings.NewReader(""), &out, &errw, &invocation{})
	return out.String() + errw.String(), err
}

func pageCheckSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("page-check sent %s %s: it only GETs", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			fmt.Fprintf(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>http://%[1]s/short</loc></url><url><loc>http://%[1]s/guide</loc></url></urlset>`, r.Host)
		case "/short":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<html><head><title>Obiad w 20 minut — Marka</title></head></html>")
		case "/guide":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><head><title>Jak zaplanować obiady na cały tydzień i nie wydać fortuny — Marka</title></head></html>")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPageCheckCommand(t *testing.T) {
	srv := pageCheckSite(t)

	out, err := runPageCheckCLI(t, "--delay", "0s", srv.URL+"/sitemap.xml")
	var code *exitCodeError
	if !errors.As(err, &code) || code.Code != 1 {
		t.Fatalf("a failing page must exit 1, got %v\n%s", err, out)
	}
	for _, want := range []string{
		"✓ " + srv.URL + "/sitemap.xml",
		"note: sitemap: 2 URL(s)",
		"✓ " + srv.URL + "/short",
		"✗ " + srv.URL + "/guide",
		"title-length: <title> is 65 characters, over 60",
		"fix: at most 60 characters with the brand suffix, the primary phrase first",
		`charset: Content-Type "text/html" has no charset=utf-8`,
		"fix: serve it as text/html; charset=utf-8",
		"page-check: FAIL (1 of 3 URL(s) break a page rule)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	out, err = runPageCheckCLI(t, "--delay", "0s", srv.URL+"/short")
	if err != nil || !strings.Contains(out, "page-check: PASS (1 URL(s))") {
		t.Fatalf("a passing page must exit 0 with PASS, got %v\n%s", err, out)
	}

	out, _ = runPageCheckCLI(t, "--delay", "0s", "--json", srv.URL+"/sitemap.xml")
	var rep pagecheck.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if rep.Failed != 1 || len(rep.Results) != 3 || rep.Results[2].TitleLength != 65 {
		t.Errorf("--json report: %+v", rep)
	}

	out, err = runPageCheckCLI(t)
	if err == nil || !strings.Contains(out+err.Error(), "page-check needs a URL") {
		t.Errorf("no URL: want a usage error, got %v\n%s", err, out)
	}
}
