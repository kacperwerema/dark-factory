package pagecheck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// site is a stand-in site: each path answers with its Content-Type and
// body; methods records every request's method.
type site struct {
	mu      sync.Mutex
	methods []string
	agents  []string
	pages   map[string][2]string // path → content type, body
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.methods = append(s.methods, r.Method)
	s.agents = append(s.agents, r.UserAgent())
	s.mu.Unlock()
	if r.URL.Path == "/old" {
		http.Redirect(w, r, "/ok", http.StatusMovedPermanently)
		return
	}
	p, ok := s.pages[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header()["Content-Type"] = []string{p[0]}
	fmt.Fprint(w, p[1])
}

func page(title string) string {
	return "<!doctype html><html><head><meta charset=\"utf-8\">" + title + "</head><body><h1>A heading that may be much longer than sixty characters, by the rule</h1></body></html>"
}

const utf8HTML = "text/html; charset=utf-8"

func newSite(t *testing.T) (*site, *httptest.Server) {
	t.Helper()
	s := &site{pages: map[string][2]string{
		// 60 characters exactly, Polish letters and an entity counted once each.
		"/ok":         {utf8HTML, page("<title>Obiady na tydzień: przepisy &amp; lista zakupów dla domu — Marka</title>")},
		"/long":       {utf8HTML, page("<title>Przepisy na szybki obiad dla całej rodziny w 30 minut — Marka</title>")},
		"/latin1":     {"text/html", page("<title>Short — Brand</title>")},
		"/upper":      {"text/html; charset=UTF-8", page("<title>Short — Brand</title>")},
		"/none":       {utf8HTML, page("")},
		"/two":        {utf8HTML, page("<title>One</title><title>Two</title>")},
		"/svg":        {utf8HTML, page("<title>Page</title>") + "<svg><title>An icon with a very long accessible name that is not the page title at all</title></svg>"},
		"/robots.txt": {"text/plain", "User-agent: *\nAllow: /\n"},
		"/logo.png":   {"image/png", "\x89PNG"},
	}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func rules(r Result) string {
	var ids []string
	for _, p := range r.Problems {
		ids = append(ids, p.Rule)
	}
	return strings.Join(ids, ",")
}

func TestPageRules(t *testing.T) {
	s, srv := newSite(t)
	cases := []struct{ path, rules string }{
		{"/ok", ""},
		{"/long", RuleTitleLength},
		{"/latin1", RuleCharset},
		{"/upper", ""},
		{"/none", RuleTitleMissing},
		{"/two", RuleTitleTwice},
		{"/svg", ""},
		{"/robots.txt", RuleCharset},
		{"/logo.png", ""},
		{"/missing", RuleFetch},
		{"/old", ""},
	}
	var targets []string
	for _, c := range cases {
		targets = append(targets, srv.URL+c.path)
	}
	rep := Run(context.Background(), targets, Options{})
	if len(rep.Results) != len(cases) {
		t.Fatalf("results = %d, want %d", len(rep.Results), len(cases))
	}
	failed := 0
	for i, c := range cases {
		r := rep.Results[i]
		if got := rules(r); got != c.rules {
			t.Errorf("%s: rules = %q, want %q (%+v)", c.path, got, c.rules, r)
		}
		if c.rules != "" {
			failed++
			for _, p := range r.Problems {
				if p.Fix == "" || p.Text == "" {
					t.Errorf("%s: problem without text or fix: %+v", c.path, p)
				}
			}
		}
	}
	if rep.Failed != failed {
		t.Errorf("Failed = %d, want %d", rep.Failed, failed)
	}
	if r := rep.Results[0]; r.TitleLength != 60 {
		t.Errorf("/ok title %q counted %d, want 60 (runes, entities decoded)", r.Title, r.TitleLength)
	}
	if r := rep.Results[1]; r.TitleLength <= MaxTitle || !strings.Contains(r.Problems[0].Text, fmt.Sprint(r.TitleLength)) {
		t.Errorf("/long: %+v", r)
	}
	if r := rep.Results[len(cases)-1]; !strings.HasSuffix(r.FinalURL, "/ok") {
		t.Errorf("/old: redirect not named: %+v", r)
	}
	for i, m := range s.methods {
		if m != http.MethodGet {
			t.Errorf("request %d was %s: page-check only GETs", i, m)
		}
		if s.agents[i] != UserAgent {
			t.Errorf("request %d user agent %q, want %q", i, s.agents[i], UserAgent)
		}
	}
}

func TestSitemapExpands(t *testing.T) {
	s, srv := newSite(t)
	s.pages["/sitemap.xml"] = [2]string{"application/xml; charset=utf-8", `<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>` + srv.URL + `/pages.xml</loc></sitemap><sitemap><loc>` + srv.URL + `/sitemap.xml</loc></sitemap></sitemapindex>`}
	s.pages["/pages.xml"] = [2]string{"application/xml", `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>` + srv.URL + `/ok</loc></url><url><loc> ` + srv.URL + `/long </loc></url><url><loc>` + srv.URL + `/upper</loc></url></urlset>`}
	rep := Run(context.Background(), []string{srv.URL + "/sitemap.xml"}, Options{})
	var got []string
	for _, r := range rep.Results {
		got = append(got, strings.TrimPrefix(r.URL, srv.URL)+"="+rules(r))
	}
	want := "/sitemap.xml= /pages.xml=charset /ok= /long=title-length /upper="
	if strings.Join(got, " ") != want {
		t.Errorf("got %q\nwant %q", strings.Join(got, " "), want)
	}
	if rep.Failed != 2 {
		t.Errorf("Failed = %d, want 2", rep.Failed)
	}

	capped := Run(context.Background(), []string{srv.URL + "/sitemap.xml"}, Options{MaxURLs: 3})
	if len(capped.Results) != 3 || capped.Skipped != 2 {
		t.Errorf("cap 3: %d results, %d skipped; want 3 and 2", len(capped.Results), capped.Skipped)
	}
}

func TestTitles(t *testing.T) {
	got := Titles([]byte("<!-- <title>old</title> --><TITLE lang=\"pl\">\n  Zupa\n  &#8211; Marka </TITLE>"))
	if len(got) != 1 || got[0] != "Zupa – Marka" {
		t.Errorf("Titles = %q", got)
	}
}

// An image or video sitemap's extension entries (image:loc, video:loc and
// their kin) are media under a page, not pages: page-check reads only the
// <loc> of each <url>, so a big sitemap costs one GET per page and MaxURLs
// counts pages only.
func TestSitemapSkipsExtensionLocs(t *testing.T) {
	s, srv := newSite(t)
	s.pages["/sitemap.xml"] = [2]string{"application/xml; charset=utf-8", `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:image="http://www.google.com/schemas/sitemap-image/1.1" xmlns:video="http://www.google.com/schemas/sitemap-video/1.1">
<url><loc>` + srv.URL + `/ok</loc><image:image><image:loc>` + srv.URL + `/logo.png</image:loc></image:image><image:image><image:loc>` + srv.URL + `/a.jpg</image:loc></image:image></url>
<url><loc>` + srv.URL + `/upper</loc><video:video><video:loc>` + srv.URL + `/v.mp4</video:loc><video:content_loc>` + srv.URL + `/c.mp4</video:content_loc><video:player_loc>` + srv.URL + `/p</video:player_loc></video:video></url>
<url><loc>` + srv.URL + `/long</loc></url></urlset>`}
	rep := Run(context.Background(), []string{srv.URL + "/sitemap.xml"}, Options{})
	var got []string
	for _, r := range rep.Results {
		got = append(got, strings.TrimPrefix(r.URL, srv.URL))
	}
	if want := "/sitemap.xml /ok /upper /long"; strings.Join(got, " ") != want {
		t.Errorf("checked %q, want %q", strings.Join(got, " "), want)
	}
	if n := len(s.methods); n != 4 {
		t.Errorf("%d GETs, want 4: the sitemap and one per page", n)
	}
	if !strings.Contains(strings.Join(rep.Results[0].Notes, " "), "sitemap: 3 URL(s)") {
		t.Errorf("sitemap note %q, want 3 URLs", rep.Results[0].Notes)
	}

	s.methods = nil
	capped := Run(context.Background(), []string{srv.URL + "/sitemap.xml"}, Options{MaxURLs: 3})
	if len(capped.Results) != 3 || capped.Skipped != 1 || len(s.methods) != 3 {
		t.Errorf("cap 3: %d results, %d skipped, %d GETs; want 3, 1 and 3 (pages only)", len(capped.Results), capped.Skipped, len(s.methods))
	}
}
