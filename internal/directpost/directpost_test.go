package directpost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Stand-in values: never real keys.
const (
	fakePageToken = "fake-page-token-1234567890"
	fakeTikTok    = "fake-tiktok-token-1234567890"
	fakePin       = "fake-pinterest-token-1234567890"
)

// loopbackOnly fails any request to a host that isn't this machine, so a
// test can't reach a platform by mistake.
type loopbackOnly struct{}

func (loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("test made a call off this machine: %s", host)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// stand is a stand-in platform: handlers by "METHOD /path", and every
// request seen.
type stand struct {
	t    *testing.T
	mu   sync.Mutex
	seen []string
	srv  *httptest.Server
	h    map[string]http.HandlerFunc
}

func newStand(t *testing.T) *stand {
	s := &stand{t: t, h: map[string]http.HandlerFunc{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		s.mu.Lock()
		s.seen = append(s.seen, key)
		h := s.h[key]
		s.mu.Unlock()
		if h == nil {
			t.Errorf("stand-in: no handler for %s", key)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stand) on(key string, h http.HandlerFunc) { s.h[key] = h }

func (s *stand) count(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, k := range s.seen {
		if k == key {
			n++
		}
	}
	return n
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func client(s *stand, platform string, secrets, account map[string]string) *Client {
	return &Client{
		Platform: platform,
		HTTP:     &http.Client{Transport: loopbackOnly{}, Timeout: 10 * time.Second},
		Hosts:    HostsFor(s.srv.URL),
		Secrets:  secrets,
		Account:  account,
		Poll:     time.Millisecond,
		Sleep:    func(context.Context, time.Duration) error { return nil },
	}
}

func reelFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("not really an mp4, but bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFacebookReelUploadPollPublish(t *testing.T) {
	s := newStand(t)
	s.on("POST /v24.0/111/videos", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("not multipart: %v", err)
		}
		_, fh, err := r.FormFile("source")
		// Facebook's answer to a file part without a .mp4 name.
		if err != nil || !strings.HasSuffix(fh.Filename, ".mp4") {
			reply(w, 400, map[string]any{"error": map[string]any{"message": "There was a problem uploading your video file", "code": 352}})
			return
		}
		if r.FormValue("description") != "The caption" || r.FormValue("access_token") != fakePageToken {
			t.Errorf("fields: description %q", r.FormValue("description"))
		}
		reply(w, 200, map[string]string{"id": "v1"})
	})
	polls := 0
	s.on("GET /v24.0/v1", func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls < 3 {
			reply(w, 200, map[string]any{"status": map[string]string{"video_status": "processing"}})
			return
		}
		reply(w, 200, map[string]any{"status": map[string]string{"video_status": "ready"}, "permalink_url": "/reel/v1/"})
	})
	c := client(s, FacebookReels, map[string]string{MetaPageToken: fakePageToken}, map[string]string{AccountPageID: "111"})
	res, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "The caption"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "v1" || res.URL != "https://www.facebook.com/reel/v1/" || polls != 3 {
		t.Fatalf("got %+v after %d polls", res, polls)
	}
}

func TestFacebookCode352NamesTheFilePart(t *testing.T) {
	s := newStand(t)
	s.on("POST /v24.0/111/videos", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 400, map[string]any{"error": map[string]any{"message": "There was a problem uploading your video file", "code": 352}})
	})
	c := client(s, FacebookReels, map[string]string{MetaPageToken: fakePageToken}, map[string]string{AccountPageID: "111"})
	_, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "x"})
	var e *Error
	if !errors.As(err, &e) || e.Code != "352" || !strings.Contains(err.Error(), "named *.mp4") {
		t.Fatalf("want the 352 cause named, got %v", err)
	}
}

func TestUploadNameEndsInMP4(t *testing.T) {
	for in, want := range map[string]string{"/a/reel.mp4": "reel.mp4", "/a/b.MOV": "b.MOV", "/a/clip": "clip.mp4", "/a/x.webm": "x.mp4"} {
		if got := UploadName(in); got != want {
			t.Errorf("UploadName(%q) = %q, want %q", in, got, want)
		}
	}
}

func instagramStand(t *testing.T, rupload http.HandlerFunc) (*stand, *Client) {
	s := newStand(t)
	s.on("GET /v24.0/222/content_publishing_limit", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"data": []any{map[string]any{"quota_usage": 3, "config": map[string]int{"quota_total": 100}}}})
	})
	s.on("POST /v24.0/222/media", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("media_type") != "REELS" || r.Form.Get("upload_type") != "resumable" || r.Form.Get("thumb_offset") != "1500" {
			t.Errorf("container form: %v", r.Form)
		}
		reply(w, 200, map[string]string{"id": "c1"})
	})
	s.on("POST /ig-api-upload/v24.0/c1", rupload)
	polls := 0
	s.on("GET /v24.0/c1", func(w http.ResponseWriter, r *http.Request) {
		polls++
		code := "IN_PROGRESS"
		if polls > 2 {
			code = "FINISHED"
		}
		reply(w, 200, map[string]string{"status_code": code})
	})
	s.on("POST /v24.0/222/media_publish", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("creation_id") != "c1" {
			t.Errorf("creation_id %q", r.Form.Get("creation_id"))
		}
		reply(w, 200, map[string]string{"id": "m1"})
	})
	s.on("GET /v24.0/m1", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]string{"permalink": "https://www.instagram.com/reel/abc/"})
	})
	return s, client(s, InstagramReels, map[string]string{MetaPageToken: fakePageToken}, map[string]string{AccountIGUserID: "222"})
}

func TestInstagramReelUploadPollPublish(t *testing.T) {
	s, c := instagramStand(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "OAuth "+fakePageToken || r.Header.Get("offset") != "0" || r.Header.Get("file_size") != fmt.Sprint(len(body)) {
			t.Errorf("rupload headers: %v", r.Header)
		}
		reply(w, 200, map[string]bool{"success": true})
	})
	res, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "Hi #reel", HasCover: true, CoverAtMS: 1500, AILabel: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "m1" || res.URL != "https://www.instagram.com/reel/abc/" {
		t.Fatalf("got %+v", res)
	}
	if s.count("GET /v24.0/c1") != 3 {
		t.Fatalf("status polled %d times, want 3", s.count("GET /v24.0/c1"))
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "AI label") {
		t.Fatalf("want the AI-label note, got %v", res.Notes)
	}
}

func TestInstagramProcessingFailedIsTheFileNotTheToken(t *testing.T) {
	s, c := instagramStand(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 400, map[string]any{"debug_info": map[string]any{"retriable": false, "type": "ProcessingFailedError", "message": "Request processing failed"}})
	})
	_, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "x", HasCover: true, CoverAtMS: 1500})
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindRefused {
		t.Fatalf("want a refused-file error, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "refused this MP4") || strings.Contains(msg, "token no longer") || strings.Contains(msg, "too large") {
		t.Fatalf("the message must blame the file: %s", msg)
	}
	if s.count("POST /v24.0/222/media_publish") != 0 {
		t.Fatal("published after a refused upload")
	}
}

func TestInstagramFullQuotaRefusesBeforeUpload(t *testing.T) {
	s := newStand(t)
	s.on("GET /v24.0/222/content_publishing_limit", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"data": []any{map[string]any{"quota_usage": 100, "config": map[string]int{"quota_total": 100}}}})
	})
	c := client(s, InstagramReels, map[string]string{MetaPageToken: fakePageToken}, map[string]string{AccountIGUserID: "222"})
	_, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "x"})
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindRate {
		t.Fatalf("want a rate refusal, got %v", err)
	}
}

func tiktokStand(t *testing.T, wantToken string) *stand {
	s := newStand(t)
	auth := func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+wantToken {
			t.Errorf("%s: Authorization %q", r.URL.Path, r.Header.Get("Authorization"))
		}
	}
	s.on("POST /v2/post/publish/creator_info/query/", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		reply(w, 200, map[string]any{"data": map[string]any{"creator_username": "standin", "privacy_level_options": []string{"PUBLIC_TO_EVERYONE", "SELF_ONLY"}, "max_video_post_duration_sec": 600}, "error": map[string]string{"code": "ok"}})
	})
	s.on("POST /v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		var in struct {
			PostInfo   map[string]any `json:"post_info"`
			SourceInfo map[string]any `json:"source_info"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.PostInfo["privacy_level"] != "SELF_ONLY" || in.PostInfo["is_aigc"] != true || in.SourceInfo["source"] != "FILE_UPLOAD" || in.SourceInfo["total_chunk_count"] != float64(1) {
			t.Errorf("init: %+v", in)
		}
		reply(w, 200, map[string]any{"data": map[string]string{"publish_id": "p1", "upload_url": s.srv.URL + "/upload/p1"}, "error": map[string]string{"code": "ok"}})
	})
	s.on("PUT /upload/p1", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if want := fmt.Sprintf("bytes 0-%d/%d", len(body)-1, len(body)); r.Header.Get("Content-Range") != want || r.Header.Get("Authorization") != "" {
			t.Errorf("upload: Content-Range %q want %q; Authorization sent %v", r.Header.Get("Content-Range"), want, r.Header.Get("Authorization") != "")
		}
		w.WriteHeader(201)
	})
	polls := 0
	s.on("POST /v2/post/publish/status/fetch/", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		polls++
		if polls < 2 {
			reply(w, 200, map[string]any{"data": map[string]any{"status": "PROCESSING_UPLOAD"}, "error": map[string]string{"code": "ok"}})
			return
		}
		reply(w, 200, map[string]any{"data": map[string]any{"status": "PUBLISH_COMPLETE", "publicaly_available_post_id": []int64{7300000000000000001}}, "error": map[string]string{"code": "ok"}})
	})
	return s
}

func TestTikTokUploadPollPublish(t *testing.T) {
	s := tiktokStand(t, fakeTikTok)
	c := client(s, TikTok, map[string]string{TikTokAccessToken: fakeTikTok}, nil)
	res, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "Hi", AILabel: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "https://www.tiktok.com/@standin/video/7300000000000000001" {
		t.Fatalf("got %+v", res)
	}
	if s.count("POST /v2/post/publish/status/fetch/") != 2 || s.count("PUT /upload/p1") != 1 {
		t.Fatalf("calls: %v", s.seen)
	}
	if len(res.Notes) == 0 || !strings.Contains(res.Notes[0], "privately") {
		t.Fatalf("a SELF_ONLY post says so: %v", res.Notes)
	}
}

func TestTikTokRefreshTokenIsExchangedInMemory(t *testing.T) {
	s := tiktokStand(t, "fresh-access-from-refresh")
	s.on("POST /v2/oauth/token/", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "fake-refresh-0987654321" {
			t.Errorf("refresh form: %v", r.Form)
		}
		reply(w, 200, map[string]any{"access_token": "fresh-access-from-refresh", "expires_in": 86400, "refresh_token": "fake-refresh-0987654321"})
	})
	c := client(s, TikTok, map[string]string{TikTokClientKey: "fake-key-123", TikTokClientSecret: "fake-secret-123", TikTokRefreshToken: "fake-refresh-0987654321"}, nil)
	if _, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "Hi", AILabel: true}); err != nil {
		t.Fatal(err)
	}
	if got := c.Redact("token fresh-access-from-refresh"); strings.Contains(got, "fresh-access") {
		t.Fatalf("the fresh token is not redacted: %s", got)
	}
}

func TestTikTokPrivacyTheAccountDoesNotAllowIsRefused(t *testing.T) {
	s := tiktokStand(t, fakeTikTok)
	c := client(s, TikTok, map[string]string{TikTokAccessToken: fakeTikTok}, nil)
	_, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "Hi", Privacy: "FOLLOWER_OF_CREATOR"})
	if err == nil || !strings.Contains(err.Error(), "may not post with privacy") || s.count("POST /v2/post/publish/video/init/") != 0 {
		t.Fatalf("want a refusal before init, got %v", err)
	}
}

func TestTikTokSpamRiskIsARateLimit(t *testing.T) {
	s := newStand(t)
	s.on("POST /v2/post/publish/creator_info/query/", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 403, map[string]any{"error": map[string]string{"code": "spam_risk_too_many_posts", "message": "too many posts"}})
	})
	c := client(s, TikTok, map[string]string{TikTokAccessToken: fakeTikTok}, nil)
	_, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "Hi"})
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindRate {
		t.Fatalf("want a rate limit, got %v", err)
	}
}

func TestTikTokChunks(t *testing.T) {
	if c, n := tiktokChunks(5 << 20); c != 5<<20 || n != 1 {
		t.Errorf("5 MB: %d × %d", c, n)
	}
	if c, n := tiktokChunks(95 << 20); c != TikTokChunk || n != 9 {
		t.Errorf("95 MB: %d × %d", c, n)
	}
}

func pinterestStand(t *testing.T) *stand {
	s := newStand(t)
	auth := func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fakePin {
			t.Errorf("%s: Authorization %q", r.URL.Path, r.Header.Get("Authorization"))
		}
	}
	s.on("GET /v5/boards", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		if r.URL.Query().Get("bookmark") == "" {
			reply(w, 200, map[string]any{"items": []Board{{ID: "b0", Name: "Other"}}, "bookmark": "next"})
			return
		}
		reply(w, 200, map[string]any{"items": []Board{{ID: "b1", Name: "Reels"}}})
	})
	s.on("POST /v5/media", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		reply(w, 201, map[string]any{"media_id": "md1", "upload_url": s.srv.URL + "/store", "upload_parameters": map[string]string{"key": "uploads/md1", "policy": "signed"}})
	})
	s.on("POST /store", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("the key went to the storage upload")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("key") != "uploads/md1" || r.FormValue("policy") != "signed" {
			t.Errorf("upload form: %v %v", err, r.MultipartForm)
		}
		if _, _, err := r.FormFile("file"); err != nil {
			t.Errorf("no file part: %v", err)
		}
		w.WriteHeader(204)
	})
	polls := 0
	s.on("GET /v5/media/md1", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		polls++
		st := "processing"
		if polls > 1 {
			st = "succeeded"
		}
		reply(w, 200, map[string]string{"status": st})
	})
	s.on("POST /v5/pins", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		src, _ := in["media_source"].(map[string]any)
		if in["board_id"] != "b1" || in["title"] != "Title" || src["source_type"] != "video_id" || src["media_id"] != "md1" {
			t.Errorf("pin: %v", in)
		}
		reply(w, 201, map[string]string{"id": "pin1"})
	})
	return s
}

func TestPinterestUploadPollPublish(t *testing.T) {
	s := pinterestStand(t)
	c := client(s, Pinterest, map[string]string{PinterestToken: fakePin}, map[string]string{AccountBoard: "reels"})
	res, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "Desc", Title: "Title", Link: "https://example.com/?utm_source=pinterest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "pin1" || res.URL != "https://www.pinterest.com/pin/pin1/" || s.count("GET /v5/media/md1") != 2 {
		t.Fatalf("got %+v, calls %v", res, s.seen)
	}
}

func TestPinterestMissingBoardIsNeverCreated(t *testing.T) {
	s := pinterestStand(t)
	c := client(s, Pinterest, map[string]string{PinterestToken: fakePin}, nil)
	_, err := c.Publish(context.Background(), Post{File: reelFile(t, "reel.mp4"), Caption: "Desc", Title: "Title", Board: "Nope"})
	if err == nil || !strings.Contains(err.Error(), "never created") || s.count("POST /v5/media") != 0 {
		t.Fatalf("want a refusal naming the boards, got %v", err)
	}
}

func TestProblemsBeforeAnyCall(t *testing.T) {
	long := strings.Repeat("a", InstagramCaptionMax+1)
	got := Problems(InstagramReels, Post{File: "/nope/reel.gif", Caption: long}, nil)
	if len(got) != 2 {
		t.Fatalf("want the file and the caption, got %v", got)
	}
	if got := Problems(Pinterest, Post{File: reelFile(t, "r.mp4")}, nil); len(got) != 2 {
		t.Fatalf("pinterest wants a title and a board: %v", got)
	}
}

// Every way an answer reaches an error, with the key echoed back by the
// stand-in or put in the URL, comes out without the key.
func TestRedactEveryErrorPath(t *testing.T) {
	secrets := map[string]string{MetaPageToken: fakePageToken, MetaAppID: "fake-app-id-55", MetaAppSecret: "fake-app-secret-55", TikTokAccessToken: fakeTikTok, PinterestToken: fakePin}
	s := newStand(t)
	echo := func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		reply(w, 400, map[string]any{
			"error":   map[string]any{"message": "bad " + r.Header.Get("Authorization") + " " + r.URL.RawQuery + " " + r.Form.Encode(), "code": "x"},
			"message": "bad " + r.Header.Get("Authorization"),
		})
	}
	s.on("GET /v24.0/222/content_publishing_limit", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 400, map[string]any{"error": map[string]any{"message": "bad " + r.URL.RawQuery, "code": 190}})
	})
	s.on("GET /v24.0/debug_token", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 400, map[string]any{"error": map[string]any{"message": "bad " + r.URL.RawQuery, "code": 100}})
	})
	s.on("POST /v24.0/111/videos", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		reply(w, 500, map[string]any{"error": map[string]any{"message": "bad " + r.FormValue("access_token"), "code": 1}})
	})
	s.on("POST /v2/post/publish/creator_info/query/", echo)
	s.on("GET /v5/boards", echo)
	s.on("GET /v5/user_account", echo)
	file := reelFile(t, "reel.mp4")
	var errs []error
	for _, platform := range Platforms {
		c := client(s, platform, secrets, map[string]string{AccountPageID: "111", AccountIGUserID: "222", AccountBoard: "b"})
		_, err := c.Publish(context.Background(), Post{File: file, Caption: "x", Title: "t"})
		errs = append(errs, err)
		_, _, err = c.Check(context.Background())
		errs = append(errs, err)
	}
	// A transport error names the URL, with the token in its query.
	dead := &Client{Platform: FacebookReels, HTTP: &http.Client{Transport: loopbackOnly{}}, Hosts: HostsFor("http://127.0.0.1:1"), Secrets: secrets, Account: map[string]string{AccountPageID: "111"}}
	_, _, err := dead.Check(context.Background())
	errs = append(errs, err)
	for i, err := range errs {
		if err == nil {
			t.Errorf("call %d: want an error", i)
			continue
		}
		for name, v := range secrets {
			if strings.Contains(err.Error(), v) {
				t.Errorf("call %d leaks %s: %s", i, name, err)
			}
		}
	}
}

func TestMetaTokenCheckAndRefresh(t *testing.T) {
	s := newStand(t)
	s.on("GET /v24.0/debug_token", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("input_token") != fakePageToken {
			t.Errorf("input_token")
		}
		reply(w, 200, map[string]any{"data": map[string]any{"is_valid": true, "type": "PAGE", "profile_id": "111", "expires_at": 0, "data_access_expires_at": time.Now().Add(3 * 24 * time.Hour).Unix(), "scopes": MetaScopes}})
	})
	s.on("GET /v24.0/111", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"instagram_business_account": map[string]string{"id": "222"}})
	})
	s.on("GET /v24.0/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fb_exchange_token") != "pasted-user-token-xyz" {
			t.Errorf("exchange: %v", r.URL.Query())
		}
		reply(w, 200, map[string]string{"access_token": "long-user-token-xyz"})
	})
	s.on("GET /v24.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("access_token") != "long-user-token-xyz" {
			t.Errorf("accounts with %q", r.URL.Query().Get("access_token"))
		}
		reply(w, 200, map[string]any{"data": []map[string]string{{"id": "999", "access_token": "other-page"}, {"id": "111", "access_token": "new-page-token-xyz"}}})
	})
	secrets := map[string]string{MetaPageToken: fakePageToken, MetaAppID: "fake-app-id-55", MetaAppSecret: "fake-app-secret-55"}
	c := client(s, InstagramReels, secrets, map[string]string{AccountPageID: "111", AccountIGUserID: "222"})
	lines, bad, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Data access ends in 3 days: bad, and said.
	if !bad || !strings.Contains(strings.Join(lines, "\n"), "data access") {
		t.Fatalf("want bad with the data-access line: %v", lines)
	}
	got, err := c.Refresh(context.Background(), "pasted-user-token-xyz")
	if err != nil {
		t.Fatal(err)
	}
	if got[MetaPageToken] != "new-page-token-xyz" || len(got) != 1 {
		t.Fatalf("refresh: %v", got)
	}
	if r := c.Redact("x pasted-user-token-xyz long-user-token-xyz new-page-token-xyz"); strings.Contains(r, "xyz") {
		t.Fatalf("not redacted: %s", r)
	}
}

func TestPinterestRefresh(t *testing.T) {
	s := newStand(t)
	s.on("POST /v5/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") || r.Form.Get("refresh_token") != "fake-pin-refresh-123" {
			t.Errorf("refresh: %v", r.Form)
		}
		reply(w, 200, map[string]any{"access_token": "new-pin-access", "expires_in": 2592000, "refresh_token": "new-pin-refresh"})
	})
	c := client(s, Pinterest, map[string]string{PinterestAppID: "fake-id-1", PinterestAppSecret: "fake-secret-1", PinterestRefreshTok: "fake-pin-refresh-123"}, nil)
	got, err := c.Refresh(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got[PinterestToken] != "new-pin-access" || got[PinterestRefreshTok] != "new-pin-refresh" {
		t.Fatalf("got %v", got)
	}
}

func TestHostsAreThePlatformsUnlessAStandIn(t *testing.T) {
	h := HostsFor("")
	if h.Graph != "https://graph.facebook.com/v24.0" || h.Rupload != "https://rupload.facebook.com/ig-api-upload/v24.0" || h.TikTok != "https://open.tiktokapis.com" || h.Pinterest != "https://api.pinterest.com/v5" {
		t.Fatalf("hosts: %+v", h)
	}
	if HostsFor("http://127.0.0.1:9/").Graph != "http://127.0.0.1:9/v24.0" {
		t.Fatal("stand-in base")
	}
}

func TestPublishedLogRefusesTheSameFileTwice(t *testing.T) {
	dir := t.TempDir()
	file := reelFile(t, "reel.mp4")
	sha, err := FileHash(file)
	if err != nil {
		t.Fatal(err)
	}
	if AlreadySent(dir, "video/instagram", sha) != nil {
		t.Fatal("empty log")
	}
	if err := AppendSent(dir, Sent{Connector: "video/instagram", Platform: InstagramReels, File: "reel.mp4", SHA256: sha, ID: "m1", URL: "u", Date: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if s := AlreadySent(dir, "video/instagram", sha); s == nil || s.ID != "m1" {
		t.Fatalf("got %v", s)
	}
	if AlreadySent(dir, "video/tiktok", sha) != nil {
		t.Fatal("another connector may post the same file")
	}
	st, _ := os.Stat(LogPath(dir))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("log mode %v", st.Mode().Perm())
	}
}
