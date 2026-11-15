package socials

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// Postiz (AGPL-3.0, github.com/gitroomhq/postiz-app), used unchanged over
// HTTP as a separate program (epic D-4). Nothing of its code is here: the
// table below is our own data naming the env vars its providers read
// (libraries/nestjs-libraries/src/integrations/social/*.provider.ts at
// v2.25.3) and the waits the spike report found (o-20261102-a64d).
//
// Calls, all through its public API with the org key (Authorization: <key>):
//   GET    /api/public/v1/is-connected         the key check
//   GET    /api/public/v1/debug/channels       states with refreshNeeded (the
//                                              instance's first, superadmin user)
//   GET    /api/public/v1/integrations         the fallback, no expiry
//   GET    /api/public/v1/social/<id>          a network's sign-in URL ({url});
//                                              for an app-password network a state
//   POST   /api/integrations/social-connect/<id>  {state, code: base64(fields JSON),
//                                              timezone}: Postiz's own connect,
//                                              the one its sign-in page posts
//   DELETE /api/public/v1/integrations/<id>    disconnect (also deletes the
//                                              channel's posts)

// PostizTool is the per_run: tool: id Marketing's social connector uses.
const PostizTool = "postiz"

// PostizKeyService is the Keychain item of the Postiz API key.
const PostizKeyService = "postiz"

// PostizEnvService holds the developer-app values, one account per env var.
const PostizEnvService = "postiz-env"

func init() { Register(PostizTool, postiz{}) }

type postiz struct{}

func (postiz) Name() string { return "Postiz" }

func idSecret(id, secret string) []Var {
	return []Var{{Name: id, Label: "Client ID"}, {Name: secret, Label: "Client secret"}}
}

var meta = idSecret("FACEBOOK_APP_ID", "FACEBOOK_APP_SECRET")

// postizNetworks is the table the page shows, the networks the spike named
// first and then the rest with an app of their own; ConnectElsewhere ones
// go to Postiz's own UI.
var postizNetworks = []Network{
	{ID: "bluesky", Name: "Bluesky", Connect: ConnectPassword,
		Fields: []Field{
			{Key: "service", Label: "Service", Default: "https://bsky.social"},
			{Key: "identifier", Label: "Handle"},
			{Key: "password", Label: "App password", Secret: true},
		},
		App:  "No app: an app password (Bluesky Settings → Privacy and security → App passwords), never your account password.",
		Wait: "none, free"},
	{ID: "linkedin", Name: "LinkedIn (profile)", Connect: ConnectOAuth, Vars: idSecret("LINKEDIN_CLIENT_ID", "LINKEDIN_CLIENT_SECRET"),
		App:  "A LinkedIn developer app with the Share on LinkedIn and Sign In with LinkedIn using OpenID Connect products.",
		Wait: "minutes, self-serve, free"},
	{ID: "linkedin-page", Name: "LinkedIn (company page)", Connect: ConnectOAuth, Vars: idSecret("LINKEDIN_CLIENT_ID", "LINKEDIN_CLIENT_SECRET"), Step: true,
		App:  "The same LinkedIn app, with Community Management API access.",
		Wait: "days to weeks (LinkedIn approves the API)"},
	{ID: "x", Name: "X", Connect: ConnectOAuth, Vars: []Var{{Name: "X_API_KEY", Label: "API key"}, {Name: "X_API_SECRET", Label: "API key secret"}},
		App:  "An X developer app with read and write access (OAuth 1.0a).",
		Wait: "minutes, but pay-per-use: money per post (check docs.x.com)"},
	{ID: "threads", Name: "Threads", Connect: ConnectOAuth, Vars: idSecret("THREADS_APP_ID", "THREADS_APP_SECRET"), HTTPS: true,
		App:  "A Meta app with the Threads API.",
		Wait: "days to weeks (Meta App Review)"},
	{ID: "facebook", Name: "Facebook page", Connect: ConnectOAuth, Vars: meta, HTTPS: true, Step: true,
		App:  "A Meta app with Facebook Login for Business; one app serves Facebook and Instagram.",
		Wait: "days to weeks (Meta business verification and App Review)"},
	{ID: "instagram", Name: "Instagram (via Facebook)", Connect: ConnectOAuth, Vars: meta, HTTPS: true, Step: true,
		App:  "The same Meta app; a professional Instagram account linked to a Facebook page.",
		Wait: "days to weeks (Meta business verification and App Review)"},
	{ID: "instagram-standalone", Name: "Instagram (standalone)", Connect: ConnectOAuth, Vars: idSecret("INSTAGRAM_APP_ID", "INSTAGRAM_APP_SECRET"), HTTPS: true,
		App:  "A Meta app with Instagram API with Instagram Login; fewer features than via Facebook.",
		Wait: "days to weeks (Meta App Review)"},
	{ID: "tiktok", Name: "TikTok", Connect: ConnectOAuth, Vars: idSecret("TIKTOK_CLIENT_ID", "TIKTOK_CLIENT_SECRET"), HTTPS: true,
		App:  "A TikTok developer app with the Content Posting API.",
		Wait: "1–2 weeks for app review; posts stay private until TikTok's audit"},
	{ID: "youtube", Name: "YouTube", Connect: ConnectOAuth, Vars: idSecret("YOUTUBE_CLIENT_ID", "YOUTUBE_CLIENT_SECRET"),
		App:  "A Google Cloud project with the YouTube Data API and an OAuth client.",
		Wait: "days to weeks (OAuth verification); unverified uploads may be forced private"},
	{ID: "pinterest", Name: "Pinterest", Connect: ConnectOAuth, Vars: idSecret("PINTEREST_CLIENT_ID", "PINTEREST_CLIENT_SECRET"),
		App:  "A Pinterest developer app.",
		Wait: "days (Pinterest reviews the app)"},
	{ID: "reddit", Name: "Reddit", Connect: ConnectOAuth, Vars: idSecret("REDDIT_CLIENT_ID", "REDDIT_CLIENT_SECRET"),
		App:  "A Reddit app of type web app. Reddit closed new app access on 2026-10-31: only an app you already hold, or one Reddit approves, works.",
		Wait: "minutes for an app you hold; Reddit's approval for a new one"},
	{ID: "mastodon", Name: "Mastodon", Connect: ConnectOAuth, Vars: idSecret("MASTODON_CLIENT_ID", "MASTODON_CLIENT_SECRET"),
		App:  "An application in your Mastodon instance's Development settings (mastodon.social unless Postiz's MASTODON_URL says otherwise).",
		Wait: "minutes"},
	{ID: "discord", Name: "Discord", Connect: ConnectOAuth,
		Vars: []Var{{Name: "DISCORD_CLIENT_ID", Label: "Client ID"}, {Name: "DISCORD_CLIENT_SECRET", Label: "Client secret"}, {Name: "DISCORD_BOT_TOKEN_ID", Label: "Bot token"}},
		App:  "A Discord application with a bot.",
		Wait: "minutes"},
	{ID: "telegram", Name: "Telegram", Connect: ConnectElsewhere, Vars: []Var{{Name: "TELEGRAM_TOKEN", Label: "Bot token"}},
		App: "A bot from @BotFather; Postiz connects it in its own UI.", Wait: "minutes"},
	{ID: "nostr", Name: "Nostr", Connect: ConnectElsewhere, App: "Connected in Postiz's own UI.", Wait: "none"},
	{ID: "lemmy", Name: "Lemmy", Connect: ConnectElsewhere, App: "Connected in Postiz's own UI.", Wait: "none"},
	{ID: "mastodon-custom", Name: "Mastodon (your own instance)", Connect: ConnectElsewhere, App: "Connected in Postiz's own UI with your instance's address.", Wait: "minutes"},
}

func (postiz) Networks() []Network { return postizNetworks }

// Base drops the MCP path from the connector's url:
// http://127.0.0.1:4007/api/mcp → http://127.0.0.1:4007.
func (postiz) Base(c *tracker.Connector) string {
	if c == nil {
		return ""
	}
	return PostizBase(c.URL)
}

// PostizBase is the Postiz address in an MCP url: (or the address itself).
func PostizBase(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	for _, suffix := range []string{"/api/mcp", "/mcp"} {
		u = strings.TrimSuffix(u, suffix)
	}
	return u
}

func (postiz) RedirectURI(base, id string) string { return base + "/integrations/social/" + id }

func (postiz) UI(base string) string { return base + "/launches" }

func (postiz) KeyRef(c *tracker.Connector) (tracker.SecretRef, bool) {
	if c == nil {
		return tracker.SecretRef{}, false
	}
	for k, v := range c.Headers {
		if !strings.EqualFold(k, "Authorization") {
			continue
		}
		hv, err := tracker.ParseHeaderValue(v)
		if err != nil {
			return tracker.SecretRef{}, false
		}
		return hv.Ref, true
	}
	return tracker.SecretRef{}, false
}

func (postiz) EnvRef(v string) tracker.SecretRef {
	return tracker.SecretRef{Scheme: "keychain", Name: PostizEnvService, Account: v}
}

func (postiz) NewClient(base, key string, hc *http.Client) Client {
	if hc == nil {
		hc = HTTPClient()
	}
	return &postizClient{base: strings.TrimRight(base, "/"), key: key, hc: hc}
}

type postizClient struct {
	base, key string
	hc        *http.Client
}

// do sends one call. The error names the path and the status, and Postiz's
// msg only when it doesn't repeat anything sent: never the key or a field.
func (c *postizClient) do(ctx context.Context, method, path string, body any, out any, sent ...string) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return fmt.Errorf("Postiz %s: bad address", path)
	}
	req.Header.Set("Authorization", c.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("Postiz %s: not answering (%s)", path, transportWords(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrKey
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Postiz %s: %d%s", path, resp.StatusCode, safeMsg(raw, append(sent, c.key)...))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("Postiz %s: answer not understood", path)
	}
	return nil
}

// safeMsg is Postiz's error message, " (…)", when it repeats nothing sent.
func safeMsg(raw []byte, sent ...string) string {
	var m struct {
		Msg     any `json:"msg"`
		Message any `json:"message"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	msg := m.Msg
	if msg == nil {
		msg = m.Message
	}
	s, ok := msg.(string)
	if !ok || s == "" || len(s) > 200 {
		return ""
	}
	for _, v := range sent {
		if v != "" && strings.Contains(s, v) {
			return ""
		}
	}
	return " (" + s + ")"
}

func transportWords(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "connection refused"
	case strings.Contains(s, "deadline") || strings.Contains(s, "Timeout"):
		return "timed out"
	}
	return "no answer"
}

func (c *postizClient) KeyOK(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/api/public/v1/is-connected", nil, nil)
}

func (c *postizClient) Status(ctx context.Context) (Status, error) {
	var health []struct {
		ID              string     `json:"id"`
		Provider        string     `json:"providerIdentifier"`
		Disabled        bool       `json:"disabled"`
		RefreshNeeded   bool       `json:"refreshNeeded"`
		InBetweenSteps  bool       `json:"inBetweenSteps"`
		TokenExpiration *time.Time `json:"tokenExpiration"`
		DeletedAt       *time.Time `json:"deletedAt"`
	}
	err := c.do(ctx, http.MethodGet, "/api/public/v1/debug/channels", nil, &health)
	if err == nil {
		st := Status{ExpiryKnown: true}
		now := time.Now()
		for _, h := range health {
			if h.DeletedAt != nil {
				continue
			}
			ch := Channel{ID: h.ID, Provider: h.Provider, State: StateConnected}
			switch {
			case h.Disabled:
				ch.State = StateDisabled
			case h.InBetweenSteps:
				ch.State = StateStep
			case h.RefreshNeeded, h.TokenExpiration != nil && h.TokenExpiration.Before(now) && !h.TokenExpiration.IsZero():
				ch.State = StateExpired
			}
			st.Channels = append(st.Channels, ch)
		}
		return st, nil
	}
	if err == ErrKey {
		return Status{}, err
	}
	var list []struct {
		ID       string `json:"id"`
		Provider string `json:"identifier"`
		Disabled bool   `json:"disabled"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/public/v1/integrations", nil, &list); err != nil {
		return Status{}, err
	}
	var st Status
	for _, l := range list {
		ch := Channel{ID: l.ID, Provider: l.Provider, State: StateConnected}
		if l.Disabled {
			ch.State = StateDisabled
		}
		st.Channels = append(st.Channels, ch)
	}
	return st, nil
}

func (c *postizClient) AuthURL(ctx context.Context, id string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/public/v1/social/"+url.PathEscape(id), nil, &out); err != nil {
		return "", err
	}
	u, err := url.Parse(out.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("Postiz gave no sign-in address for %s", id)
	}
	return out.URL, nil
}

func (c *postizClient) ConnectFields(ctx context.Context, id string, fields map[string]string) error {
	var st struct {
		URL string `json:"url"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/public/v1/social/"+url.PathEscape(id), nil, &st); err != nil {
		return err
	}
	if st.URL == "" || strings.ContainsAny(st.URL, "/:") {
		return fmt.Errorf("Postiz gave no connect state for %s", id)
	}
	code, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var sent []string
	for _, v := range fields {
		sent = append(sent, v)
	}
	body := map[string]string{"state": st.URL, "code": base64.StdEncoding.EncodeToString(code), "timezone": "0"}
	return c.do(ctx, http.MethodPost, "/api/integrations/social-connect/"+url.PathEscape(id), body, nil, sent...)
}

func (c *postizClient) Disconnect(ctx context.Context, channel string) error {
	return c.do(ctx, http.MethodDelete, "/api/public/v1/integrations/"+url.PathEscape(channel), nil, nil)
}

// Override maps each var into the postiz service from the start step's
// environment (${VAR:-}: empty when unset, as Postiz's own file has it).
func (postiz) Override(vars []string) []byte {
	var b strings.Builder
	b.WriteString("# Written by the factory's Connect socials page: references only, never a\n# value. The values come from Keychain into the start step's environment.\nservices:\n  postiz:\n    environment:\n")
	for _, v := range vars {
		fmt.Fprintf(&b, "      %s: ${%s:-}\n", v, v)
	}
	return []byte(b.String())
}
