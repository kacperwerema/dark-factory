package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// A kind: rest connector is a read-only REST API. connector exec makes one
// GET per path it is given, in its own process, with the connector's
// headers: secrets: no child process ever holds the key, and the client
// has no other method, so no write call can be reached however a call is
// worded. Every path must match one of the connector's get: patterns.

// RestTimeout is how long one GET may take.
const RestTimeout = 30 * time.Second

// RestMaxBody is the most of an answer read; a longer one is an error.
const RestMaxBody = 4 << 20

// RestClient follows no redirect: a 3xx is the answer, not a new request,
// so a server can't send the key's request somewhere else.
func RestClient() *http.Client {
	return &http.Client{
		Timeout:       RestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// RestPatterns are c's get: patterns, trimmed, each once, in config order.
func RestPatterns(c *tracker.Connector) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range c.Get {
		g = strings.TrimSpace(g)
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	return out
}

// RestPatternProblem says why a get: entry can't be used, "" when it is fine.
func RestPatternProblem(g string) string {
	g = strings.TrimSpace(g)
	switch {
	case !strings.HasPrefix(g, "/"):
		return "must start with /"
	case strings.HasPrefix(g, "//"):
		return "must be a path, not //host"
	case strings.ContainsAny(g, "?#\\"):
		return "is a path only (no ?, # or \\): the query is the caller's"
	case path.Clean(g) != g:
		return "must be a clean path (no .., ., // or trailing /)"
	}
	if _, err := path.Match(g, "/"); err != nil {
		return "is not a valid pattern (" + err.Error() + ")"
	}
	return ""
}

// RestPatternWide is a well-formed get: entry whose first segment is a
// wildcard (/*, /*/status, /[a-z]*): it matches a route at the API's first
// level whatever its name, including one that changes state on GET.
func RestPatternWide(g string) bool {
	g = strings.TrimSpace(g)
	if RestPatternProblem(g) != "" {
		return false
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(g, "/"), "/")
	return strings.ContainsAny(first, "*[")
}

// RestPatternNarrower is g with its first segment named: the form lint's
// fix suggests for a wide pattern (/*/status → /<resource>/status).
func RestPatternNarrower(g string) string {
	_, rest, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(g), "/"), "/")
	if !ok {
		return "/<resource>/*"
	}
	return "/<resource>/" + rest
}

// RestPath refuses a path c may not read: anything but a clean path
// under its url: (a scheme, a host, a fragment, a dot segment, a doubled
// slash, an encoded slash or dot), or a path no get: pattern matches.
// It returns the path to request, with its query.
func RestPath(addr string, c *tracker.Connector, raw string) (string, error) {
	refuse := func(why string) (string, error) {
		return "", fmt.Errorf("connector %s reads only GET %s: %q %s", addr, strings.Join(RestPatterns(c), ", "), raw, why)
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return refuse("is not a path under its url: (start it with one /)")
	}
	if strings.ContainsAny(raw, "\\#") || strings.IndexFunc(raw, unicode.IsControl) >= 0 || strings.ContainsRune(raw, ' ') {
		return refuse("holds a character a path can't (\\, #, a space or a control character)")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil {
		return refuse("is not a path")
	}
	if path.Clean(u.Path) != u.Path || strings.Contains(strings.ToLower(u.RawPath), "%2f") {
		return refuse("is not a clean path (no .., ., // or encoded /)")
	}
	for _, g := range RestPatterns(c) {
		if RestPatternProblem(g) != "" {
			continue
		}
		if ok, _ := path.Match(g, u.Path); ok {
			out := u.EscapedPath()
			if u.RawQuery != "" {
				out += "?" + u.RawQuery
			}
			return out, nil
		}
	}
	return refuse("matches none of them")
}

// RestAnswer is one GET's line on stdout. Body is the answer as JSON, or
// as a JSON string when it isn't JSON; Error says why it isn't a 2xx.
type RestAnswer struct {
	Path   string          `json:"path"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// OK is a 2xx answer.
func (a RestAnswer) OK() bool { return a.Status >= 200 && a.Status <= 299 }

// RestRead makes one GET of p (from RestPath) under base with header.
// A secret the answer repeats is replaced by [secret]; an error never
// names a header's value.
func RestRead(ctx context.Context, hc *http.Client, base string, header http.Header, p string) RestAnswer {
	a := RestAnswer{Path: p}
	target, err := restURL(base, p)
	if err != nil {
		a.Error = err.Error()
		return a
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		a.Error = "bad address"
		return a
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		a.Error = "not answering (" + restTransportWords(err) + ")"
		return a
	}
	defer resp.Body.Close()
	a.Status = resp.StatusCode
	raw, err := io.ReadAll(io.LimitReader(resp.Body, RestMaxBody+1))
	switch {
	case err != nil:
		a.Error = "the answer could not be read"
		return a
	case len(raw) > RestMaxBody:
		a.Error = fmt.Sprintf("the answer is over %d MB", RestMaxBody>>20)
		return a
	}
	raw = scrubSecrets(raw, header)
	if t := bytes.TrimSpace(raw); len(t) > 0 {
		if json.Valid(t) {
			a.Body = json.RawMessage(t)
		} else {
			a.Body, _ = json.Marshal(string(t))
		}
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		a.Error = "the key was refused"
	case resp.StatusCode >= 300 && resp.StatusCode <= 399:
		a.Error = "a redirect, not followed"
	case !a.OK():
		a.Error = "not a 2xx answer"
	}
	return a
}

// restURL is base + p, refused when the result leaves base's host.
func restURL(base, p string) (string, error) {
	b, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || b.Host == "" {
		return "", errors.New("the connector's url: is not an address")
	}
	full := strings.TrimRight(base, "/") + p
	u, err := url.Parse(full)
	if err != nil || u.Host != b.Host || u.Scheme != b.Scheme {
		return "", errors.New("the path leaves the connector's url:")
	}
	return full, nil
}

// scrubSecrets replaces each header value the answer repeats, and its
// part after a prefix ("Bearer <key>"), with [secret].
func scrubSecrets(raw []byte, header http.Header) []byte {
	for _, vs := range header {
		for _, v := range vs {
			for _, s := range []string{v, v[strings.LastIndex(v, " ")+1:]} {
				if len(s) >= 4 {
					raw = bytes.ReplaceAll(raw, []byte(s), []byte("[secret]"))
				}
			}
		}
	}
	return raw
}

func restTransportWords(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return "timed out"
		}
		err = ue.Err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "connection refused"
	case strings.Contains(msg, "no such host"):
		return "no such host"
	}
	return "transport error"
}
