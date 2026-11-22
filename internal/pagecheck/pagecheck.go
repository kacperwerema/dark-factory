// Package pagecheck checks the page rules every marketing department ships
// and measures pages by: a <title> of at most MaxTitle characters including
// the brand suffix, and every text response (HTML, XML, plain text) served
// with an explicit charset=utf-8 in its Content-Type header. A crawler reads
// the header, not a <meta charset>: without it Bing decodes the page as
// Latin-1 and shows mojibake in og:title and og:description.
//
// It only reads: one GET per URL, one at a time, with a timeout and a cap on
// the number of URLs. docs/departments.md, "Page rules".
package pagecheck

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxTitle is the page rule's limit on a <title>, in characters, brand
// suffix included.
const MaxTitle = 60

// UserAgent names the check and nothing else: no project, person or site.
const UserAgent = "dark-factory-page-check/1"

// maxBody caps how much of a response is read (a <title> is in the head).
const maxBody = 2 << 20

// Rule ids, as a Problem names them.
const (
	RuleTitleLength  = "title-length"
	RuleTitleMissing = "title-missing"
	RuleTitleTwice   = "title-duplicate"
	RuleCharset      = "charset"
	RuleFetch        = "fetch"
)

// Problem is one page rule a URL breaks, with the fix.
type Problem struct {
	Rule string `json:"rule"`
	Text string `json:"text"`
	Fix  string `json:"fix"`
}

// Result is one URL's check.
type Result struct {
	URL         string    `json:"url"`
	FinalURL    string    `json:"final_url,omitempty"`
	Status      int       `json:"status,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Charset     string    `json:"charset,omitempty"`
	Title       string    `json:"title,omitempty"`
	TitleLength int       `json:"title_length,omitempty"`
	Problems    []Problem `json:"problems,omitempty"`
	// Notes are facts that break no page rule (a redirect, a page that is
	// not text), so the reader knows what was checked.
	Notes []string `json:"notes,omitempty"`
}

// OK reports whether the URL breaks no page rule.
func (r Result) OK() bool { return len(r.Problems) == 0 }

// Options tune a run.
type Options struct {
	Client   *http.Client  // nil: a client with Timeout
	Timeout  time.Duration // per URL; 0: 10s
	MaxURLs  int           // URLs checked at most, sitemaps expanded; 0: 500
	MaxTitle int           // 0: MaxTitle
	Delay    time.Duration // between requests
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	t := o.Timeout
	if t == 0 {
		t = 10 * time.Second
	}
	return &http.Client{Timeout: t}
}

func (o Options) maxURLs() int {
	if o.MaxURLs > 0 {
		return o.MaxURLs
	}
	return 500
}

func (o Options) maxTitle() int {
	if o.MaxTitle > 0 {
		return o.MaxTitle
	}
	return MaxTitle
}

// Report is a whole run: every URL checked, and the URLs left out by the cap.
type Report struct {
	Results []Result `json:"results"`
	Skipped int      `json:"skipped,omitempty"`
	Failed  int      `json:"failed"`
}

// Run checks each target. A target whose path ends in .xml and holds a
// <urlset> or <sitemapindex> is a sitemap: its own response is checked for
// the charset, then each <url>'s <loc> in it (a sitemap index's sitemaps one level
// down) up to MaxURLs.
func Run(ctx context.Context, targets []string, o Options) Report {
	var rep Report
	seen := map[string]bool{}
	queue := []string{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		queue = append(queue, u)
	}
	for _, t := range targets {
		add(t)
	}
	first := true
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if len(rep.Results) >= o.maxURLs() {
			rep.Skipped++
			continue
		}
		if !first && o.Delay > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(o.Delay):
			}
		}
		first = false
		res, body := check(ctx, u, o)
		// A sitemap index's <loc>s are sitemaps, expanded in turn when
		// fetched; seen keeps a loop from running twice.
		if locs, ok := sitemapLocs(body); ok {
			res.Notes = append(res.Notes, fmt.Sprintf("sitemap: %d URL(s)", len(locs)))
			for _, l := range locs {
				add(l)
			}
		}
		rep.Results = append(rep.Results, res)
	}
	for _, r := range rep.Results {
		if !r.OK() {
			rep.Failed++
		}
	}
	return rep
}

// check fetches one URL and applies the rules; it returns the body read for
// the sitemap expansion.
func check(ctx context.Context, u string, o Options) (Result, []byte) {
	res := Result{URL: u}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		res.Problems = append(res.Problems, Problem{Rule: RuleFetch, Text: err.Error(), Fix: "give a full http(s) URL"})
		return res, nil
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := o.client().Do(req)
	if err != nil {
		res.Problems = append(res.Problems, Problem{Rule: RuleFetch, Text: err.Error(), Fix: "check the URL is reachable from this machine"})
		return res, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	res.Status = resp.StatusCode
	if final := resp.Request.URL.String(); final != u {
		res.FinalURL = final
		res.Notes = append(res.Notes, "redirected to "+final)
	}
	if resp.StatusCode >= 400 {
		res.Problems = append(res.Problems, Problem{Rule: RuleFetch, Text: fmt.Sprintf("HTTP %d", resp.StatusCode), Fix: "a sitemap URL must answer 200"})
		return res, body
	}
	ct := resp.Header.Get("Content-Type")
	res.ContentType = ct
	mt, params, _ := mime.ParseMediaType(ct)
	res.Charset = params["charset"]
	if !isText(mt) {
		res.Notes = append(res.Notes, "not a text response ("+orNone(mt)+"): rules skipped")
		return res, body
	}
	if !strings.EqualFold(res.Charset, "utf-8") {
		res.Problems = append(res.Problems, Problem{
			Rule: RuleCharset,
			Text: fmt.Sprintf("Content-Type %q has no charset=utf-8", ct),
			Fix:  "serve it as " + orNone(mt) + "; charset=utf-8 (set it in the server or the CDN; a <meta charset> is not enough)",
		})
	}
	if mt == "text/html" || mt == "application/xhtml+xml" {
		titles := Titles(body)
		switch {
		case len(titles) == 0:
			res.Problems = append(res.Problems, Problem{Rule: RuleTitleMissing, Text: "no <title>", Fix: "give the page a <title>, the primary phrase first"})
		case len(titles) > 1:
			res.Title = titles[0]
			res.TitleLength = utf8.RuneCountInString(titles[0])
			res.Problems = append(res.Problems, Problem{Rule: RuleTitleTwice, Text: fmt.Sprintf("%d <title> tags", len(titles)), Fix: "keep one <title> in the head"})
		default:
			res.Title = titles[0]
			res.TitleLength = utf8.RuneCountInString(titles[0])
			if max := o.maxTitle(); res.TitleLength > max {
				res.Problems = append(res.Problems, Problem{
					Rule: RuleTitleLength,
					Text: fmt.Sprintf("<title> is %d characters, over %d", res.TitleLength, max),
					Fix:  fmt.Sprintf("at most %d characters with the brand suffix, the primary phrase first; the h1 and og:title may stay longer", max),
				})
			}
		}
	}
	return res, body
}

func orNone(s string) string {
	if s == "" {
		return "no Content-Type"
	}
	return s
}

// isText reports whether a media type is one the charset rule covers: HTML,
// XML (sitemaps, feeds) and plain text (robots.txt, llms.txt). A response
// with no Content-Type counts, so a missing header fails.
func isText(mt string) bool {
	switch {
	case mt == "":
		return true
	case strings.HasPrefix(mt, "text/"):
		return true
	case mt == "application/xml", mt == "application/xhtml+xml", strings.HasSuffix(mt, "+xml"):
		return true
	}
	return false
}

var (
	titleRe   = regexp.MustCompile(`(?is)<title(?:\s[^>]*)?>(.*?)</title\s*>`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	svgRe     = regexp.MustCompile(`(?is)<svg[\s>].*?</svg\s*>`)
	spaceRe   = regexp.MustCompile(`\s+`)
)

// Titles returns the text of each document <title> in an HTML page: entities
// decoded, whitespace collapsed. Comments and inline SVG (whose <title> is an
// image's, not the page's) are left out.
func Titles(body []byte) []string {
	s := commentRe.ReplaceAllString(string(body), "")
	s = svgRe.ReplaceAllString(s, "")
	var out []string
	for _, m := range titleRe.FindAllStringSubmatch(s, -1) {
		t := html.UnescapeString(m[1])
		out = append(out, strings.TrimSpace(spaceRe.ReplaceAllString(t, " ")))
	}
	return out
}

// sitemapLocs returns the <loc> values of a sitemap or sitemap index, and
// whether body is one (a sitemap index's <loc>s are sitemaps). Only a <loc>
// right under a <url> or <sitemap>, in the root's namespace, counts: an
// image or video sitemap's image:loc, video:loc and their kin are media
// under a page, not pages, and fetching them would cost a GET each and eat
// into MaxURLs.
func sitemapLocs(body []byte) (locs []string, ok bool) {
	if len(body) == 0 {
		return nil, false
	}
	dec := xml.NewDecoder(strings.NewReader(string(body)))
	dec.Strict = false
	var root xml.Name
	var path []xml.Name // open elements under the root
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, false
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.StartElement:
			if root.Local == "" {
				root = t.Name
				if root.Local != "urlset" && root.Local != "sitemapindex" {
					return nil, false
				}
				continue
			}
			entry := "url"
			if root.Local == "sitemapindex" {
				entry = "sitemap"
			}
			if len(path) == 1 && path[0].Local == entry && path[0].Space == root.Space &&
				t.Name.Local == "loc" && t.Name.Space == root.Space {
				var v string
				if err := dec.DecodeElement(&v, &t); err == nil {
					locs = append(locs, strings.TrimSpace(v))
				}
				continue // DecodeElement consumed the end tag
			}
			path = append(path, t.Name)
		}
	}
	return locs, root.Local != ""
}
