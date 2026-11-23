// Package directpost publishes a finished reel straight to a platform's
// own API, beside the Postiz route (internal/socials): Instagram Reels and
// Facebook Reels through Meta's Graph API, TikTok's Content Posting API and
// Pinterest's video pins. A department's connector with publish: <platform>
// is one publisher; issue-cli makes the calls itself, so the keys are read
// into its process only, never handed to a child or printed: every line and
// error goes through Client.Redact.
//
// The calls follow the ones a project ran in production for Instagram and
// Facebook (factory/direct-reel-publishing-connectors-instagram-facebook-tiktok-pinterest-meta-page-token-beside-postiz-ported-from-scriptssocial),
// with its two lessons: Facebook /videos wants the file part named *.mp4
// (else code 352), and Instagram's rupload ProcessingFailedError with
// retriable:false is one MP4 refused, not the token or the size. TikTok and
// Pinterest video follow the platforms' docs and are untried until a first
// real post.
package directpost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Platforms a publish: connector names.
const (
	InstagramReels = "instagram-reels"
	FacebookReels  = "facebook-reels"
	TikTok         = "tiktok"
	Pinterest      = "pinterest"
)

// Platforms is every publish: value, in the order docs list them.
var Platforms = []string{InstagramReels, FacebookReels, TikTok, Pinterest}

// Known says whether platform is a publish: value.
func Known(platform string) bool {
	for _, p := range Platforms {
		if p == platform {
			return true
		}
	}
	return false
}

// Secret names a publisher reads from its connector's secrets:.
const (
	MetaPageToken       = "META_PAGE_TOKEN"
	MetaAppID           = "META_APP_ID"
	MetaAppSecret       = "META_APP_SECRET"
	TikTokAccessToken   = "TIKTOK_ACCESS_TOKEN"
	TikTokClientKey     = "TIKTOK_CLIENT_KEY"
	TikTokClientSecret  = "TIKTOK_CLIENT_SECRET"
	TikTokRefreshToken  = "TIKTOK_REFRESH_TOKEN"
	PinterestToken      = "PINTEREST_ACCESS_TOKEN"
	PinterestAppID      = "PINTEREST_APP_ID"
	PinterestAppSecret  = "PINTEREST_APP_SECRET"
	PinterestRefreshTok = "PINTEREST_REFRESH_TOKEN"
)

// Account ids a publisher reads from its connector's account: (ids, not
// secrets).
const (
	AccountPageID   = "page_id"
	AccountIGUserID = "ig_user_id"
	AccountBoard    = "board"
)

// Needs is what a platform's publisher must have in its connector: each
// entry of Secrets is a set of alternatives (one of them set is enough),
// Account the account: ids, Optional the secrets only token check or
// refresh reads.
type Needs struct {
	Secrets  [][]string
	Account  []string
	Optional []string
}

// Require is each platform's Needs.
func Require(platform string) Needs {
	switch platform {
	case InstagramReels:
		return Needs{Secrets: [][]string{{MetaPageToken}}, Account: []string{AccountIGUserID}, Optional: []string{MetaAppID, MetaAppSecret}}
	case FacebookReels:
		return Needs{Secrets: [][]string{{MetaPageToken}}, Account: []string{AccountPageID}, Optional: []string{MetaAppID, MetaAppSecret}}
	case TikTok:
		return Needs{Secrets: [][]string{{TikTokAccessToken, TikTokRefreshToken}}, Optional: []string{TikTokClientKey, TikTokClientSecret, TikTokRefreshToken, TikTokAccessToken}}
	case Pinterest:
		return Needs{Secrets: [][]string{{PinterestToken}}, Optional: []string{PinterestAppID, PinterestAppSecret, PinterestRefreshTok}}
	}
	return Needs{}
}

// MetaVersion is the Graph API version the Meta calls name.
const MetaVersion = "v24.0"

// Hosts are the API bases a Client calls. A connector's url: replaces all
// of them with one base, which is how a test points it at a stand-in.
type Hosts struct {
	Graph      string // https://graph.facebook.com/v24.0
	GraphVideo string // https://graph-video.facebook.com/v24.0
	Rupload    string // https://rupload.facebook.com/ig-api-upload/v24.0
	TikTok     string // https://open.tiktokapis.com
	Pinterest  string // https://api.pinterest.com/v5
}

// HostsFor is the platforms' own hosts, or every one under base when base
// is set.
func HostsFor(base string) Hosts {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return Hosts{
			Graph:      "https://graph.facebook.com/" + MetaVersion,
			GraphVideo: "https://graph-video.facebook.com/" + MetaVersion,
			Rupload:    "https://rupload.facebook.com/ig-api-upload/" + MetaVersion,
			TikTok:     "https://open.tiktokapis.com",
			Pinterest:  "https://api.pinterest.com/v5",
		}
	}
	return Hosts{
		Graph:      base + "/" + MetaVersion,
		GraphVideo: base + "/" + MetaVersion,
		Rupload:    base + "/ig-api-upload/" + MetaVersion,
		TikTok:     base,
		Pinterest:  base + "/v5",
	}
}

// Client is one publisher's calls, with its secrets and account ids.
type Client struct {
	Platform string
	HTTP     *http.Client
	Hosts    Hosts
	Secrets  map[string]string
	Account  map[string]string
	// Poll is how often processing is asked about; Wait the most it is
	// waited for. Zero takes each platform's default.
	Poll, Wait time.Duration
	// Sleep waits between polls (tests make it instant).
	Sleep func(context.Context, time.Duration) error
	// Progress, when set, gets each step as it happens, already redacted.
	Progress func(string)
	// Now is the clock (tests fix it).
	Now func() time.Time
}

// HTTPClient is the client a publisher uses: long enough for a video
// upload, and no redirect, so a key's request is never sent on.
func HTTPClient() *http.Client {
	return &http.Client{
		Timeout:       10 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (c *Client) secret(name string) string { return c.Secrets[name] }

func (c *Client) account(name string) string { return strings.TrimSpace(c.Account[name]) }

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) step(format string, a ...any) {
	if c.Progress != nil {
		c.Progress(c.Redact(fmt.Sprintf(format, a...)))
	}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) pollEvery(def time.Duration) time.Duration {
	if c.Poll > 0 {
		return c.Poll
	}
	return def
}

func (c *Client) waitAtMost(def time.Duration) time.Duration {
	if c.Wait > 0 {
		return c.Wait
	}
	return def
}

func (c *Client) httpc() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return HTTPClient()
}

var secretParam = regexp.MustCompile(`(access_token|input_token|client_secret|refresh_token|fb_exchange_token|client_key)=[^&\s"']+`)

// Redact replaces each of the client's secret values, and any secret
// parameter in a URL, with ***.
func (c *Client) Redact(s string) string {
	var vals []string
	for _, v := range c.Secrets {
		if len(v) >= 4 {
			vals = append(vals, v)
		}
	}
	// Longest first, so a value holding another is replaced whole.
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	for _, v := range vals {
		s = strings.ReplaceAll(s, v, "***")
	}
	return secretParam.ReplaceAllString(s, "$1=***")
}

// Error is one refused or failed call: the platform, the step, the HTTP
// status, the platform's code and message, and what to do about it.
type Error struct {
	Platform, Step string
	Status         int
	Code           string
	Message        string
	Hint           string
	// Kind sorts it: KindRate, KindToken, KindRefused or "".
	Kind string
}

// Error kinds.
const (
	KindRate    = "rate"
	KindToken   = "token"
	KindRefused = "refused"
)

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", e.Platform, e.Step)
	if e.Status != 0 {
		fmt.Fprintf(&b, ": HTTP %d", e.Status)
	}
	if e.Code != "" {
		fmt.Fprintf(&b, ", code %s", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.Hint != "" {
		fmt.Fprintf(&b, " (%s)", e.Hint)
	}
	return b.String()
}

// fail is an Error with message and hint redacted.
func (c *Client) fail(step string, status int, code, msg, hint, kind string) *Error {
	return &Error{Platform: c.Platform, Step: step, Status: status, Code: code, Message: c.Redact(msg), Hint: hint, Kind: kind}
}

// MaxAnswer is the most of an answer read.
const MaxAnswer = 4 << 20

// send makes req and returns its status and body. A transport error is
// redacted: net/http names the URL, which may hold a token.
func (c *Client) send(req *http.Request, step string) (int, []byte, error) {
	res, err := c.httpc().Do(req)
	if err != nil {
		return 0, nil, c.fail(step, 0, "", "no connection: "+err.Error(), "", "")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxAnswer))
	if err != nil {
		return res.StatusCode, nil, c.fail(step, res.StatusCode, "", "the answer could not be read: "+err.Error(), "", "")
	}
	return res.StatusCode, body, nil
}

func newRequest(ctx context.Context, method, u string, body io.Reader) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, method, u, body)
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// snippet is the start of an answer that isn't JSON, for an error.
func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
