package directpost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// TikTok's Content Posting API, direct post with the file uploaded: no
// public URL of the video is needed. An app TikTok hasn't audited may post
// only privately (SELF_ONLY), which is the default here.

// TikTok's chunk rules: one chunk up to 64 MB; above it 10 MB chunks, the
// last one taking the rest.
const (
	TikTokSingleChunkMax = 64 << 20
	TikTokChunk          = 10 << 20
)

// TikTokPrivate is the privacy level used unless --privacy names another.
const TikTokPrivate = "SELF_ONLY"

// tiktokAnswer is the error part every TikTok answer carries: code "ok"
// is success.
type tiktokAnswer struct {
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		LogID   string `json:"log_id"`
	} `json:"error"`
	// OAuth answers use these instead.
	OAuthError string `json:"error_description"`
}

func (c *Client) tiktokCheck(step string, status int, body []byte) error {
	var a tiktokAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		if status >= 300 {
			return c.fail(step, status, "", snippet(body), c.statusHint(status), c.statusKind(status))
		}
		return c.fail(step, status, "", "the answer is not JSON: "+snippet(body), "", "")
	}
	if a.Error != nil && a.Error.Code != "" && a.Error.Code != "ok" {
		code := a.Error.Code
		switch {
		case strings.HasPrefix(code, "spam_risk") || code == "rate_limit_exceeded" || code == "reached_active_user_cap" || status == http.StatusTooManyRequests:
			return c.fail(step, status, code, a.Error.Message, "rate limit: TikTok limits posts per account a day; try again later", KindRate)
		case code == "access_token_invalid" || code == "scope_not_authorized" || status == http.StatusUnauthorized:
			return c.fail(step, status, code, a.Error.Message, "the token was refused: connector exec <dept>/<name> -- token check; the owner runs token refresh", KindToken)
		case code == "unaudited_client_can_only_post_to_private_accounts":
			return c.fail(step, status, code, a.Error.Message, "until TikTok audits the app it posts only privately: --privacy SELF_ONLY", KindRefused)
		}
		return c.fail(step, status, code, a.Error.Message, "", "")
	}
	if a.OAuthError != "" || status >= 300 {
		msg := a.OAuthError
		if msg == "" {
			msg = snippet(body)
		}
		return c.fail(step, status, "", msg, c.statusHint(status), c.statusKind(status))
	}
	return nil
}

// tiktokToken is the access token: a refresh token, when set with the
// app's key and secret, is exchanged in memory for a fresh one (TikTok's
// access token lasts 24 hours); nothing is written.
func (c *Client) tiktokToken(ctx context.Context) (string, error) {
	if c.secret(TikTokRefreshToken) != "" && c.secret(TikTokClientKey) != "" && c.secret(TikTokClientSecret) != "" {
		t, err := c.tiktokRefresh(ctx)
		if err != nil {
			return "", err
		}
		// Redact the new token too (Secrets is set: the refresh token is in it).
		c.Secrets["tiktok-fresh-access"] = t.Access
		return t.Access, nil
	}
	if t := c.secret(TikTokAccessToken); t != "" {
		return t, nil
	}
	return "", fmt.Errorf("%s: needs %s, or %s with %s and %s", c.Platform, TikTokAccessToken, TikTokRefreshToken, TikTokClientKey, TikTokClientSecret)
}

func (c *Client) tiktokPost(ctx context.Context, token, path string, in any, step string, out any) error {
	req, err := newRequest(ctx, http.MethodPost, c.Hosts.TikTok+path, jsonBody(in))
	if err != nil {
		return c.fail(step, 0, "", err.Error(), "", "")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	status, body, err := c.send(req, step)
	if err != nil {
		return err
	}
	if err := c.tiktokCheck(step, status, body); err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return c.fail(step, status, "", "the answer is not what was expected: "+snippet(body), "", "")
		}
	}
	return nil
}

// CreatorInfo is what TikTok says the account may post now.
type CreatorInfo struct {
	Username       string   `json:"creator_username"`
	PrivacyOptions []string `json:"privacy_level_options"`
	MaxDurationSec int      `json:"max_video_post_duration_sec"`
}

func (i CreatorInfo) String() string {
	return fmt.Sprintf("tiktok: account %s may post now; privacy levels %s; videos up to %d s", orDash(i.Username), strings.Join(i.PrivacyOptions, ", "), i.MaxDurationSec)
}

// privacy is the level to post with: want when the account allows it,
// SELF_ONLY when want is empty.
func (i CreatorInfo) privacy(want string) (string, error) {
	if want == "" {
		want = TikTokPrivate
	}
	for _, o := range i.PrivacyOptions {
		if o == want {
			return want, nil
		}
	}
	return "", fmt.Errorf("tiktok: the account may not post with privacy %s now (it allows %s)", want, strings.Join(i.PrivacyOptions, ", "))
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (c *Client) tiktokCreatorWith(ctx context.Context, token string) (CreatorInfo, error) {
	var r struct {
		Data CreatorInfo `json:"data"`
	}
	if err := c.tiktokPost(ctx, token, "/v2/post/publish/creator_info/query/", struct{}{}, "creator info", &r); err != nil {
		return CreatorInfo{}, err
	}
	return r.Data, nil
}

func (c *Client) tiktokCreator(ctx context.Context) (CreatorInfo, error) {
	token, err := c.tiktokToken(ctx)
	if err != nil {
		return CreatorInfo{}, err
	}
	return c.tiktokCreatorWith(ctx, token)
}

// tiktokChunks is the chunk size and count TikTok wants for size bytes.
func tiktokChunks(size int64) (chunk int64, count int) {
	if size <= TikTokSingleChunkMax {
		return size, 1
	}
	return TikTokChunk, int(size / TikTokChunk)
}

func (c *Client) publishTikTok(ctx context.Context, p Post) (Result, error) {
	token, err := c.tiktokToken(ctx)
	if err != nil {
		return Result{}, err
	}
	info, err := c.tiktokCreatorWith(ctx, token)
	if err != nil {
		return Result{}, err
	}
	privacy, err := info.privacy(p.Privacy)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(p.File)
	if err != nil {
		return Result{}, fmt.Errorf("%s: video upload: %v", c.Platform, err)
	}
	size := int64(len(data))
	chunk, count := tiktokChunks(size)
	postInfo := map[string]any{"title": p.Caption, "privacy_level": privacy, "is_aigc": p.AILabel}
	if p.HasCover {
		postInfo["video_cover_timestamp_ms"] = p.CoverAtMS
	}
	in := map[string]any{
		"post_info":   postInfo,
		"source_info": map[string]any{"source": "FILE_UPLOAD", "video_size": size, "chunk_size": chunk, "total_chunk_count": count},
	}
	var init struct {
		Data struct {
			PublishID string `json:"publish_id"`
			UploadURL string `json:"upload_url"`
		} `json:"data"`
	}
	if err := c.tiktokPost(ctx, token, "/v2/post/publish/video/init/", in, "post init", &init); err != nil {
		return Result{}, err
	}
	if init.Data.PublishID == "" || init.Data.UploadURL == "" {
		return Result{}, c.fail("post init", 0, "", "the answer has no publish_id or upload_url", "", "")
	}
	for i := 0; i < count; i++ {
		first := int64(i) * chunk
		last := first + chunk - 1
		if i == count-1 {
			last = size - 1
		}
		c.step("tiktok: uploading chunk %d of %d", i+1, count)
		req, err := newRequest(ctx, http.MethodPut, init.Data.UploadURL, bytes.NewReader(data[first:last+1]))
		if err != nil {
			return Result{}, c.fail("video upload", 0, "", err.Error(), "", "")
		}
		req.Header.Set("Content-Type", videoType(p.File))
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, last, size))
		req.ContentLength = last - first + 1
		status, body, err := c.send(req, "video upload")
		if err != nil {
			return Result{}, err
		}
		if status >= 300 {
			return Result{}, c.fail("video upload", status, "", snippet(body), c.statusHint(status), c.statusKind(status))
		}
	}
	id, err := c.tiktokWait(ctx, token, init.Data.PublishID)
	if err != nil {
		return Result{}, err
	}
	res := Result{ID: init.Data.PublishID}
	if id != "" && info.Username != "" {
		res.ID = id
		res.URL = "https://www.tiktok.com/@" + url.PathEscape(info.Username) + "/video/" + id
	}
	if privacy == TikTokPrivate {
		res.Notes = append(res.Notes, "posted privately (SELF_ONLY): only the account sees it; make it public in the TikTok app, or publish with --privacy once TikTok has audited the app")
	}
	if res.URL == "" {
		res.URL = "https://www.tiktok.com/"
		res.Notes = append(res.Notes, "TikTok gave no public post id: find the post on the account's profile")
	}
	return res, nil
}

// tiktokWait polls the post until TikTok has published it; the public
// post id when it gives one.
func (c *Client) tiktokWait(ctx context.Context, token, publishID string) (string, error) {
	every, until := c.pollEvery(10*time.Second), c.waitAtMost(10*time.Minute)
	deadline := c.now().Add(until)
	for {
		var r struct {
			Data struct {
				Status     string        `json:"status"`
				FailReason string        `json:"fail_reason"`
				PostIDs    []json.Number `json:"publicaly_available_post_id"`
			} `json:"data"`
		}
		if err := c.tiktokPost(ctx, token, "/v2/post/publish/status/fetch/", map[string]string{"publish_id": publishID}, "post status", &r); err != nil {
			return "", err
		}
		switch r.Data.Status {
		case "PUBLISH_COMPLETE", "SEND_TO_USER_INBOX":
			if len(r.Data.PostIDs) > 0 {
				return r.Data.PostIDs[0].String(), nil
			}
			return "", nil
		case "FAILED":
			return "", c.fail("post status", 0, r.Data.FailReason, "TikTok could not publish it", "", KindRefused)
		}
		c.step("tiktok: processing (%s)", r.Data.Status)
		if !c.now().Before(deadline) {
			return "", c.fail("post status", 0, "", "not published after "+until.String()+" ("+r.Data.Status+")", "check the account's profile before publishing again", "")
		}
		if err := c.sleep(ctx, every); err != nil {
			return "", err
		}
	}
}

// tokens is a refreshed token pair.
type tokens struct {
	Access, Refresh string
	Expires         time.Time
}

func (c *Client) tiktokRefresh(ctx context.Context) (tokens, error) {
	const step = "token refresh"
	form := url.Values{
		"client_key":    {c.secret(TikTokClientKey)},
		"client_secret": {c.secret(TikTokClientSecret)},
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.secret(TikTokRefreshToken)},
	}
	req, err := newRequest(ctx, http.MethodPost, c.Hosts.TikTok+"/v2/oauth/token/", strings.NewReader(form.Encode()))
	if err != nil {
		return tokens{}, c.fail(step, 0, "", err.Error(), "", "")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	status, body, err := c.send(req, step)
	if err != nil {
		return tokens{}, err
	}
	if err := c.tiktokCheck(step, status, body); err != nil {
		return tokens{}, err
	}
	var r struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.AccessToken == "" {
		return tokens{}, c.fail(step, status, "", "the answer has no access_token", "the refresh token may have ended: the owner signs in again", KindToken)
	}
	return tokens{Access: r.AccessToken, Refresh: r.RefreshToken, Expires: c.now().Add(time.Duration(r.ExpiresIn) * time.Second)}, nil
}

func itoa(n int) string { return strconv.Itoa(n) }
