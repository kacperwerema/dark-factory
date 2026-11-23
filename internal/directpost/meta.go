package directpost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// metaAnswer is the error part of a Graph API answer, and rupload's
// debug_info.
type metaAnswer struct {
	Error *struct {
		Message      string `json:"message"`
		Type         string `json:"type"`
		Code         int    `json:"code"`
		ErrorSubcode int    `json:"error_subcode"`
		UserMsg      string `json:"error_user_msg"`
	} `json:"error"`
	DebugInfo *struct {
		Retriable *bool  `json:"retriable"`
		Type      string `json:"type"`
		Message   string `json:"message"`
	} `json:"debug_info"`
}

// Meta codes that mean a rate limit, and a token that no longer works.
var (
	metaRateCodes  = map[int]bool{4: true, 17: true, 32: true, 613: true, 80001: true, 80002: true}
	metaTokenCodes = map[int]bool{102: true, 190: true}
)

// FacebookNamedFileCode is Facebook's answer to a /videos upload whose
// file part has no filename.
const FacebookNamedFileCode = 352

// metaCheck turns a Graph answer into an Error when it is one.
func (c *Client) metaCheck(step string, status int, body []byte) error {
	var a metaAnswer
	jsonErr := json.Unmarshal(body, &a)
	if d := a.DebugInfo; d != nil && d.Type == "ProcessingFailedError" && d.Retriable != nil && !*d.Retriable {
		return c.fail(step, status, d.Type, "Instagram refused this MP4 (ProcessingFailedError, not retriable)",
			"the file itself: re-encode it (H.264 video, AAC audio, yuv420p, -movflags +faststart) and publish again; the token and the file size are not the cause", KindRefused)
	}
	if a.Error == nil && a.DebugInfo == nil && status < 300 {
		if jsonErr != nil {
			return c.fail(step, status, "", "the answer is not JSON: "+snippet(body), "", "")
		}
		return nil
	}
	if a.Error == nil {
		msg := snippet(body)
		if d := a.DebugInfo; d != nil {
			msg = strings.TrimSpace(d.Type + ": " + d.Message)
		}
		return c.fail(step, status, "", msg, c.statusHint(status), c.statusKind(status))
	}
	e := a.Error
	msg := e.Message
	if e.UserMsg != "" {
		msg += " — " + e.UserMsg
	}
	code := strconv.Itoa(e.Code)
	if e.ErrorSubcode != 0 {
		code += "/" + strconv.Itoa(e.ErrorSubcode)
	}
	switch {
	case e.Code == FacebookNamedFileCode:
		return c.fail(step, status, code, msg, "Facebook wants the upload's file part named *.mp4", KindRefused)
	case metaRateCodes[e.Code] || status == http.StatusTooManyRequests:
		return c.fail(step, status, code, msg, "rate limit: try again in an hour", KindRate)
	case metaTokenCodes[e.Code] || status == http.StatusUnauthorized:
		return c.fail(step, status, code, msg, "the page token no longer works: connector exec <dept>/<name> -- token check, then the owner runs token refresh", KindToken)
	}
	return c.fail(step, status, code, msg, "", "")
}

func (c *Client) statusHint(status int) string {
	switch c.statusKind(status) {
	case KindRate:
		return "rate limit: try again later"
	case KindToken:
		return "the token was refused: connector exec <dept>/<name> -- token check"
	}
	return ""
}

func (c *Client) statusKind(status int) string {
	switch status {
	case http.StatusTooManyRequests:
		return KindRate
	case http.StatusUnauthorized, http.StatusForbidden:
		return KindToken
	}
	return ""
}

// graphGet reads path under base with the page token as access_token.
func (c *Client) graphGet(ctx context.Context, base, path string, q url.Values, step string, out any) error {
	if q == nil {
		q = url.Values{}
	}
	if q.Get("access_token") == "" {
		q.Set("access_token", c.secret(MetaPageToken))
	}
	req, err := newRequest(ctx, http.MethodGet, base+path+"?"+q.Encode(), nil)
	if err != nil {
		return c.fail(step, 0, "", err.Error(), "", "")
	}
	return c.metaDo(req, step, out)
}

// graphPost posts form under base with the page token in the body.
func (c *Client) graphPost(ctx context.Context, base, path string, form url.Values, step string, out any) error {
	if form.Get("access_token") == "" {
		form.Set("access_token", c.secret(MetaPageToken))
	}
	req, err := newRequest(ctx, http.MethodPost, base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return c.fail(step, 0, "", err.Error(), "", "")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.metaDo(req, step, out)
}

func (c *Client) metaDo(req *http.Request, step string, out any) error {
	status, body, err := c.send(req, step)
	if err != nil {
		return err
	}
	if err := c.metaCheck(step, status, body); err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return c.fail(step, status, "", "the answer is not what was expected: "+snippet(body), "", "")
		}
	}
	return nil
}

// ---------- Facebook ----------

// UploadName is the name a video's file part goes up with: its own name
// when it ends in .mp4 or .mov, else with .mp4 added (code 352 otherwise).
func UploadName(file string) string {
	name := filepath.Base(file)
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".mov":
		return name
	}
	return strings.TrimSuffix(name, filepath.Ext(name)) + ".mp4"
}

func videoType(file string) string {
	if strings.EqualFold(filepath.Ext(file), ".mov") {
		return "video/quicktime"
	}
	return "video/mp4"
}

// facebookVideoForm is the multipart body of a /videos upload: the fields,
// then the file as source with its filename.
func facebookVideoForm(file string, fields map[string]string) (*bytes.Buffer, string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, k := range []string{"description", "title", "access_token"} {
		if v := fields[k]; v != "" {
			_ = w.WriteField(k, v)
		}
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="source"; filename=%q`, UploadName(file)))
	h.Set("Content-Type", videoType(file))
	part, err := w.CreatePart(h)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

func (c *Client) publishFacebook(ctx context.Context, p Post) (Result, error) {
	page := c.account(AccountPageID)
	if page == "" || c.secret(MetaPageToken) == "" {
		return Result{}, fmt.Errorf("%s: needs account: page_id and %s", c.Platform, MetaPageToken)
	}
	const step = "video upload"
	body, ctype, err := facebookVideoForm(p.File, map[string]string{"description": p.Caption, "title": p.Title, "access_token": c.secret(MetaPageToken)})
	if err != nil {
		return Result{}, fmt.Errorf("%s: %s: %v", c.Platform, step, err)
	}
	c.step("facebook: uploading %s (%d bytes)", UploadName(p.File), body.Len())
	req, err := newRequest(ctx, http.MethodPost, c.Hosts.GraphVideo+"/"+url.PathEscape(page)+"/videos", body)
	if err != nil {
		return Result{}, c.fail(step, 0, "", err.Error(), "", "")
	}
	req.Header.Set("Content-Type", ctype)
	var up struct {
		ID string `json:"id"`
	}
	if err := c.metaDo(req, step, &up); err != nil {
		return Result{}, err
	}
	if up.ID == "" {
		return Result{}, c.fail(step, 0, "", "the answer has no video id", "", "")
	}
	// From here the video is on the page: a slow processing is a note, not
	// an error, so the post is logged and never sent twice.
	res := Result{ID: up.ID, URL: "https://www.facebook.com/" + up.ID, Notes: aiNote("Facebook", p)}
	link, state, err := c.facebookWait(ctx, up.ID)
	switch {
	case err != nil:
		res.Notes = append(res.Notes, "uploaded; its processing could not be read: "+err.Error())
	case state != "ready":
		res.Notes = append(res.Notes, fmt.Sprintf("uploaded; still processing (%s): Facebook publishes it when done", state))
	}
	if link != "" {
		res.URL = absFacebook(link)
	}
	return res, nil
}

func absFacebook(u string) string {
	if strings.HasPrefix(u, "/") {
		return "https://www.facebook.com" + u
	}
	return u
}

// facebookWait polls the video until Facebook has processed it.
func (c *Client) facebookWait(ctx context.Context, id string) (link, state string, err error) {
	every, until := c.pollEvery(15*time.Second), c.waitAtMost(10*time.Minute)
	deadline := c.now().Add(until)
	state = "unknown"
	for {
		var r struct {
			PermalinkURL string `json:"permalink_url"`
			Status       struct {
				VideoStatus     string `json:"video_status"`
				PublishingPhase struct {
					Status string `json:"status"`
				} `json:"publishing_phase"`
			} `json:"status"`
		}
		q := url.Values{"fields": {"status,permalink_url"}}
		if e := c.graphGet(ctx, c.Hosts.Graph, "/"+url.PathEscape(id), q, "video status", &r); e == nil {
			if r.PermalinkURL != "" {
				link = r.PermalinkURL
			}
			if r.Status.VideoStatus != "" {
				state = r.Status.VideoStatus
			}
			if state == "ready" || r.Status.PublishingPhase.Status == "complete" {
				return link, "ready", nil
			}
			if state == "error" {
				return link, state, nil
			}
			c.step("facebook: processing (%s)", state)
		} else {
			// Facebook refuses reads of a video still processing (code 100).
			err = e
		}
		if !c.now().Before(deadline) {
			return link, state, nil
		}
		if e := c.sleep(ctx, every); e != nil {
			return link, state, e
		}
	}
}

// ---------- Instagram ----------

// Quota is Instagram's content publishing limit for 24 hours.
type Quota struct {
	Used, Total int
	Known       bool
}

func (q Quota) String() string {
	if !q.Known {
		return "instagram: daily publishing quota not reported"
	}
	return fmt.Sprintf("instagram: %d of %d posts by API in the last 24 hours", q.Used, q.Total)
}

// Full says the quota leaves no post.
func (q Quota) Full() bool { return q.Known && q.Used >= q.Total }

func (c *Client) instagramQuota(ctx context.Context) (Quota, error) {
	ig := c.account(AccountIGUserID)
	var r struct {
		Data []struct {
			QuotaUsage int `json:"quota_usage"`
			Config     struct {
				QuotaTotal int `json:"quota_total"`
			} `json:"config"`
		} `json:"data"`
	}
	q := url.Values{"fields": {"config,quota_usage"}}
	if err := c.graphGet(ctx, c.Hosts.Graph, "/"+url.PathEscape(ig)+"/content_publishing_limit", q, "publishing limit", &r); err != nil {
		return Quota{}, err
	}
	if len(r.Data) == 0 || r.Data[0].Config.QuotaTotal == 0 {
		return Quota{}, nil
	}
	return Quota{Used: r.Data[0].QuotaUsage, Total: r.Data[0].Config.QuotaTotal, Known: true}, nil
}

func (c *Client) publishInstagram(ctx context.Context, p Post) (Result, error) {
	ig := c.account(AccountIGUserID)
	token := c.secret(MetaPageToken)
	if ig == "" || token == "" {
		return Result{}, fmt.Errorf("%s: needs account: ig_user_id and %s", c.Platform, MetaPageToken)
	}
	q, err := c.instagramQuota(ctx)
	if err != nil {
		return Result{}, err
	}
	if q.Full() {
		return Result{}, c.fail("publishing limit", 0, "", fmt.Sprintf("%d of %d posts by API used in the last 24 hours", q.Used, q.Total), "rate limit: try again tomorrow", KindRate)
	}
	form := url.Values{"media_type": {"REELS"}, "upload_type": {"resumable"}, "caption": {p.Caption}, "share_to_feed": {"true"}}
	if p.HasCover {
		form.Set("thumb_offset", strconv.Itoa(p.CoverAtMS))
	}
	var box struct {
		ID string `json:"id"`
	}
	if err := c.graphPost(ctx, c.Hosts.Graph, "/"+url.PathEscape(ig)+"/media", form, "reel container", &box); err != nil {
		return Result{}, err
	}
	if box.ID == "" {
		return Result{}, c.fail("reel container", 0, "", "the answer has no container id", "", "")
	}
	data, err := os.ReadFile(p.File)
	if err != nil {
		return Result{}, fmt.Errorf("%s: video upload: %v", c.Platform, err)
	}
	c.step("instagram: uploading %s (%d bytes)", filepath.Base(p.File), len(data))
	// The upload goes to our own rupload host, never to a uri from the
	// answer, so the token's header can't be sent elsewhere.
	req, err := newRequest(ctx, http.MethodPost, c.Hosts.Rupload+"/"+url.PathEscape(box.ID), bytes.NewReader(data))
	if err != nil {
		return Result{}, c.fail("video upload", 0, "", err.Error(), "", "")
	}
	req.Header.Set("Authorization", "OAuth "+token)
	req.Header.Set("offset", "0")
	req.Header.Set("file_size", strconv.Itoa(len(data)))
	var up struct {
		Success *bool `json:"success"`
	}
	if err := c.metaDo(req, "video upload", &up); err != nil {
		return Result{}, err
	}
	if up.Success != nil && !*up.Success {
		return Result{}, c.fail("video upload", 0, "", "the upload answered success: false", "", "")
	}
	if err := c.instagramWait(ctx, box.ID); err != nil {
		return Result{}, err
	}
	var pub struct {
		ID string `json:"id"`
	}
	if err := c.graphPost(ctx, c.Hosts.Graph, "/"+url.PathEscape(ig)+"/media_publish", url.Values{"creation_id": {box.ID}}, "publish", &pub); err != nil {
		return Result{}, err
	}
	res := Result{ID: pub.ID, URL: "https://www.instagram.com/", Notes: aiNote("Instagram", p)}
	var link struct {
		Permalink string `json:"permalink"`
	}
	if err := c.graphGet(ctx, c.Hosts.Graph, "/"+url.PathEscape(pub.ID), url.Values{"fields": {"permalink"}}, "permalink", &link); err != nil {
		res.Notes = append(res.Notes, "published; its link could not be read: "+err.Error())
	} else if link.Permalink != "" {
		res.URL = link.Permalink
	}
	return res, nil
}

// instagramWait polls the container until it is FINISHED.
func (c *Client) instagramWait(ctx context.Context, id string) error {
	every, until := c.pollEvery(30*time.Second), c.waitAtMost(10*time.Minute)
	deadline := c.now().Add(until)
	last := ""
	for {
		var r struct {
			StatusCode string `json:"status_code"`
			Status     string `json:"status"`
		}
		if err := c.graphGet(ctx, c.Hosts.Graph, "/"+url.PathEscape(id), url.Values{"fields": {"status_code,status"}}, "container status", &r); err != nil {
			return err
		}
		switch r.StatusCode {
		case "FINISHED":
			return nil
		case "ERROR", "EXPIRED":
			msg := "the container is " + r.StatusCode
			if r.Status != "" {
				msg += ": " + r.Status
			}
			return c.fail("container status", 0, r.StatusCode, msg, "", "")
		}
		last = strings.TrimSpace(r.StatusCode + " " + r.Status)
		c.step("instagram: processing (%s)", last)
		if !c.now().Before(deadline) {
			return c.fail("container status", 0, "", fmt.Sprintf("not ready after %s (%s)", until, last), "publish again later: the container stays for 24 hours", "")
		}
		if err := c.sleep(ctx, every); err != nil {
			return err
		}
	}
}
