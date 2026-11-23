package directpost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Pinterest API v5, a video pin: register the media, upload the file to
// the upload URL Pinterest gives with its parameters, wait for it to be
// processed, then the pin. A board is found by name and never created.

type pinterestAnswer struct {
	Code    *int   `json:"code"`
	Message string `json:"message"`
}

func (c *Client) pinterestCheck(step string, status int, body []byte) error {
	if status < 300 {
		return nil
	}
	var a pinterestAnswer
	msg := snippet(body)
	code := ""
	if json.Unmarshal(body, &a) == nil && a.Message != "" {
		msg = a.Message
		if a.Code != nil {
			code = itoa(*a.Code)
		}
	}
	return c.fail(step, status, code, msg, c.statusHint(status), c.statusKind(status))
}

func (c *Client) pinterestCall(ctx context.Context, method, path string, in any, step string, out any) error {
	var body io.Reader
	if in != nil {
		body = jsonBody(in)
	}
	req, err := newRequest(ctx, method, c.Hosts.Pinterest+path, body)
	if err != nil {
		return c.fail(step, 0, "", err.Error(), "", "")
	}
	req.Header.Set("Authorization", "Bearer "+c.secret(PinterestToken))
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	status, raw, err := c.send(req, step)
	if err != nil {
		return err
	}
	if err := c.pinterestCheck(step, status, raw); err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return c.fail(step, status, "", "the answer is not what was expected: "+snippet(raw), "", "")
		}
	}
	return nil
}

// Board is one of the account's boards.
type Board struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) pinterestBoards(ctx context.Context) ([]Board, error) {
	var all []Board
	bookmark := ""
	for page := 0; page < 50; page++ {
		q := url.Values{"page_size": {"100"}}
		if bookmark != "" {
			q.Set("bookmark", bookmark)
		}
		var r struct {
			Items    []Board `json:"items"`
			Bookmark string  `json:"bookmark"`
		}
		if err := c.pinterestCall(ctx, http.MethodGet, "/boards?"+q.Encode(), nil, "boards", &r); err != nil {
			return nil, err
		}
		all = append(all, r.Items...)
		if r.Bookmark == "" {
			break
		}
		bookmark = r.Bookmark
	}
	return all, nil
}

func (c *Client) pinterestBoard(ctx context.Context, name string) (Board, error) {
	boards, err := c.pinterestBoards(ctx)
	if err != nil {
		return Board{}, err
	}
	var names []string
	for _, b := range boards {
		if strings.EqualFold(strings.TrimSpace(b.Name), strings.TrimSpace(name)) {
			return b, nil
		}
		names = append(names, b.Name)
	}
	return Board{}, fmt.Errorf("%s: no board %q (the account has: %s); boards are never created here: the owner makes it, or name another with --board", c.Platform, name, strings.Join(names, ", "))
}

func (c *Client) publishPinterest(ctx context.Context, p Post) (Result, error) {
	if c.secret(PinterestToken) == "" {
		return Result{}, fmt.Errorf("%s: needs %s", c.Platform, PinterestToken)
	}
	name := p.Board
	if name == "" {
		name = c.account(AccountBoard)
	}
	board, err := c.pinterestBoard(ctx, name)
	if err != nil {
		return Result{}, err
	}
	var media struct {
		MediaID          string            `json:"media_id"`
		UploadURL        string            `json:"upload_url"`
		UploadParameters map[string]string `json:"upload_parameters"`
	}
	if err := c.pinterestCall(ctx, http.MethodPost, "/media", map[string]string{"media_type": "video"}, "media register", &media); err != nil {
		return Result{}, err
	}
	if media.MediaID == "" || media.UploadURL == "" {
		return Result{}, c.fail("media register", 0, "", "the answer has no media_id or upload_url", "", "")
	}
	if err := c.pinterestUpload(ctx, media.UploadURL, media.UploadParameters, p.File); err != nil {
		return Result{}, err
	}
	if err := c.pinterestWait(ctx, media.MediaID); err != nil {
		return Result{}, err
	}
	src := map[string]any{"source_type": "video_id", "media_id": media.MediaID}
	if p.HasCover {
		src["cover_image_key_frame_time"] = float64(p.CoverAtMS) / 1000
	} else {
		src["cover_image_key_frame_time"] = 0
	}
	pin := map[string]any{"board_id": board.ID, "title": p.Title, "description": p.Caption, "media_source": src}
	if p.Link != "" {
		pin["link"] = p.Link
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := c.pinterestCall(ctx, http.MethodPost, "/pins", pin, "pin", &r); err != nil {
		return Result{}, err
	}
	return Result{ID: r.ID, URL: "https://www.pinterest.com/pin/" + url.PathEscape(r.ID) + "/", Notes: aiNote("Pinterest", p)}, nil
}

// pinterestUpload posts the file to the upload URL as a form: Pinterest's
// parameters first, then the file. No key goes with it: the URL is the
// storage's, signed by the parameters.
func (c *Client) pinterestUpload(ctx context.Context, upload string, params map[string]string, file string) error {
	const step = "video upload"
	if u, err := url.Parse(upload); err != nil || u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return c.fail(step, 0, "", "the upload URL is not https", "", "")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("%s: %s: %v", c.Platform, step, err)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_ = w.WriteField(k, params[k])
	}
	part, err := w.CreateFormFile("file", UploadName(file))
	if err != nil {
		return err
	}
	_, _ = part.Write(data)
	if err := w.Close(); err != nil {
		return err
	}
	c.step("pinterest: uploading %s (%d bytes)", filepath.Base(file), len(data))
	req, err := newRequest(ctx, http.MethodPost, upload, &buf)
	if err != nil {
		return c.fail(step, 0, "", err.Error(), "", "")
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	status, body, err := c.send(req, step)
	if err != nil {
		return err
	}
	if status >= 300 {
		return c.fail(step, status, "", snippet(body), "", "")
	}
	return nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}

func (c *Client) pinterestWait(ctx context.Context, id string) error {
	every, until := c.pollEvery(10*time.Second), c.waitAtMost(10*time.Minute)
	deadline := c.now().Add(until)
	for {
		var r struct {
			Status string `json:"status"`
		}
		if err := c.pinterestCall(ctx, http.MethodGet, "/media/"+url.PathEscape(id), nil, "media status", &r); err != nil {
			return err
		}
		switch r.Status {
		case "succeeded":
			return nil
		case "failed":
			return c.fail("media status", 0, "failed", "Pinterest could not process the video", "", KindRefused)
		}
		c.step("pinterest: processing (%s)", r.Status)
		if !c.now().Before(deadline) {
			return c.fail("media status", 0, "", "not processed after "+until.String()+" ("+r.Status+")", "", "")
		}
		if err := c.sleep(ctx, every); err != nil {
			return err
		}
	}
}

func (c *Client) pinterestRefresh(ctx context.Context) (tokens, error) {
	const step = "token refresh"
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.secret(PinterestRefreshTok)}}
	req, err := newRequest(ctx, http.MethodPost, c.Hosts.Pinterest+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tokens{}, c.fail(step, 0, "", err.Error(), "", "")
	}
	basic := base64.StdEncoding.EncodeToString([]byte(c.secret(PinterestAppID) + ":" + c.secret(PinterestAppSecret)))
	req.Header.Set("Authorization", "Basic "+basic)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	status, body, err := c.send(req, step)
	if err != nil {
		return tokens{}, err
	}
	if err := c.pinterestCheck(step, status, body); err != nil {
		return tokens{}, err
	}
	var r struct {
		AccessToken  string `json:"access_token"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.AccessToken == "" {
		return tokens{}, c.fail(step, status, "", "the answer has no access_token", "", KindToken)
	}
	return tokens{Access: r.AccessToken, Refresh: r.RefreshToken, Expires: c.now().Add(time.Duration(r.ExpiresIn) * time.Second)}, nil
}
