package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/directpost"
	"github.com/kacperwerema/dark-factory/internal/outputs"
)

// fakeMetaToken stands in for a page token: it must never be printed.
const fakeMetaToken = "fake-meta-page-token-4c1d"

// igStandIn answers Instagram's reel calls; calls counts what it saw.
type igStandIn struct {
	*httptest.Server
	mu    sync.Mutex
	calls []string
}

func (s *igStandIn) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.calls...)
}

func newIGStandIn(t *testing.T) *igStandIn {
	s := &igStandIn{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls = append(s.calls, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		_ = r.ParseForm()
		tok := r.Form.Get("access_token")
		if strings.HasPrefix(r.Header.Get("Authorization"), "OAuth ") {
			tok = strings.TrimPrefix(r.Header.Get("Authorization"), "OAuth ")
		}
		w.Header().Set("Content-Type", "application/json")
		if tok != fakeMetaToken {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Invalid OAuth access token " + tok, "code": 190}})
			return
		}
		var v any
		switch r.Method + " " + r.URL.Path {
		case "GET /v24.0/222/content_publishing_limit":
			v = map[string]any{"data": []any{map[string]any{"quota_usage": 1, "config": map[string]int{"quota_total": 100}}}}
		case "POST /v24.0/222/media":
			v = map[string]string{"id": "c1"}
		case "POST /ig-api-upload/v24.0/c1":
			v = map[string]bool{"success": true}
		case "GET /v24.0/c1":
			v = map[string]string{"status_code": "FINISHED"}
		case "POST /v24.0/222/media_publish":
			v = map[string]string{"id": "m1"}
		case "GET /v24.0/m1":
			v = map[string]string{"permalink": "https://www.instagram.com/reel/abc/"}
		default:
			w.WriteHeader(404)
			v = map[string]any{"error": map[string]any{"message": "unknown " + r.URL.Path, "code": 100}}
		}
		json.NewEncoder(w).Encode(v)
	}))
	t.Cleanup(s.Close)
	return s
}

// publishFixture is connectorFixture's Marketing with a direct Instagram
// publisher at a stand-in, a Postiz connector, and publish: routes for
// both; the reel file and its caption.
func publishFixture(t *testing.T) (root string, api *igStandIn, reel, caption string) {
	t.Helper()
	root = connectorFixture(t)
	api = newIGStandIn(t)
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	b, _ := os.ReadFile(cfg)
	mustWrite(t, cfg, strings.Replace(string(b), "        systems: [SEO]\n", `        systems: [SEO]
        publish:
          instagram: instagram
          linkedin: postiz
`, 1)+`          - name: instagram
            kind: tool
            publish: instagram-reels
            url: `+api.URL+`
            secrets:
              META_PAGE_TOKEN: env:ACME_META
            account: {ig_user_id: "222"}
            sends: true
            needs_approved_output: true
          - name: postiz
            kind: mcp
            url: http://localhost:4007/api/mcp
            sends: true
            needs_approved_output: true
            per_run:
              tool: postiz
              start: 'true'
              ready: 'true'
              stop: 'true'
`)
	t.Setenv("ACME_META", fakeMetaToken)
	old := publishPoll
	publishPoll = time.Millisecond
	t.Cleanup(func() { publishPoll = old })
	reel = filepath.Join(root, "reel.mp4")
	mustWrite(t, reel, "fake mp4 bytes")
	caption = filepath.Join(root, "caption.txt")
	mustWrite(t, caption, "A week of dinners in three taps #reel\n")
	return root, api, reel, caption
}

func setIssueStatus(t *testing.T, root, status string) {
	t.Helper()
	mustWrite(t, filepath.Join(root, "issues", "SEO", "page.md"), "---\ntitle: \"Page\"\nstatus: \""+status+"\"\nsystem: \"SEO\"\n---\n\nBody.\n")
}

// TestDirectPublishNeedsTheStampAndTheShelf: a worker's dry run says the
// route and what holds it, and sends nothing; --send is refused before the
// publish status and before the shelf approval; with both it publishes,
// logs it and never prints the key; the same file is not sent twice; a
// lead never sends, and token refresh is the owner's.
func TestDirectPublishNeedsTheStampAndTheShelf(t *testing.T) {
	root, api, reel, caption := publishFixture(t)
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "seo/page")
	pub := []string{"connector", "exec", "marketing/instagram", "--", "publish", "--file", reel, "--caption-file", caption}

	out, err := runCLI(t, pub...)
	if err != nil {
		t.Fatalf("dry run: %s", out)
	}
	for _, want := range []string{
		"route: direct, instagram-reels through connector marketing/instagram",
		"instagram: 1 of 100 posts by API in the last 24 hours",
		`issue seo/page is at "backlog": it sends only at "publish"`,
		"needs an approved output",
		"dry run: nothing was sent",
	} {
		assertContains(t, out, want)
	}

	out, err = runCLI(t, append(pub, "--send")...)
	if err == nil || !strings.Contains(out, "not sent") || !strings.Contains(out, `at "backlog"`) {
		t.Fatalf("--send before the stamp: %v\n%s", err, out)
	}
	for _, c := range api.seen() {
		if strings.HasPrefix(c, "POST") {
			t.Fatalf("something was posted before the stamp: %v", api.seen())
		}
	}

	// At publish, but the shelf hasn't the owner's approval yet.
	setIssueStatus(t, root, "publish")
	p, o := shelfOutput(t, root)
	if out, err = runCLI(t, append(pub, "--send")...); err == nil || !strings.Contains(out, "still in review") {
		t.Fatalf("--send before the shelf approval: %v\n%s", err, out)
	}
	if _, err := outputs.Review(p, o.ID, outputs.StateApproved, "ok", "test", "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, append(pub, "--send", "--ai-label")...)
	if err != nil {
		t.Fatalf("--send: %s", out)
	}
	assertContains(t, out, "published: https://www.instagram.com/reel/abc/")
	assertContains(t, out, "AI label")
	assertContains(t, out, "issue-cli output publish <id> --url https://www.instagram.com/reel/abc/")
	if strings.Contains(out, fakeMetaToken) {
		t.Fatalf("the key was printed:\n%s", out)
	}
	sent := directpost.LoadSent(filepath.Join(root, "issues"))
	if len(sent) != 1 || sent[0].Issue != "seo/page" || sent[0].URL != "https://www.instagram.com/reel/abc/" || sent[0].SHA256 == "" {
		t.Fatalf("published log: %+v", sent)
	}
	logb, _ := os.ReadFile(directpost.LogPath(filepath.Join(root, "issues")))
	if strings.Contains(string(logb), fakeMetaToken) || strings.Contains(string(logb), "three taps") {
		t.Fatalf("the log holds the key or the caption: %s", logb)
	}

	if out, err = runCLI(t, append(pub, "--send")...); err == nil || !strings.Contains(out, "already went out through marketing/instagram") {
		t.Fatalf("a second send: %v\n%s", err, out)
	}

	if out, err = runCLI(t, "connector", "exec", "marketing/instagram", "--", "token", "refresh"); err == nil || !strings.Contains(out, "it is the owner's, in their own shell") {
		t.Fatalf("a worker's token refresh: %v\n%s", err, out)
	}

	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	asLeadOf(t, "marketing")
	other := filepath.Join(root, "other.mp4")
	mustWrite(t, other, "other bytes")
	if out, err = runCLI(t, "connector", "exec", "marketing/instagram", "--", "publish", "--file", other, "--caption-file", caption, "--send"); err == nil || !strings.Contains(out, "only a worker whose reel is at") {
		t.Fatalf("a lead's send: %v\n%s", err, out)
	}
}

// TestDirectPublishWrongKeyNeverPrinted: the stand-in echoes the key it
// got in its error; the command's output says what to run, not the key.
func TestDirectPublishWrongKeyNeverPrinted(t *testing.T) {
	_, _, reel, caption := publishFixture(t)
	t.Setenv("ACME_META", "fake-wrong-token-9z9z9z")
	out, err := runCLI(t, "connector", "exec", "marketing/instagram", "--as-owner", "--", "publish", "--file", reel, "--caption-file", caption, "--send")
	if err == nil || !strings.Contains(out, "token check") {
		t.Fatalf("want the token hint, got %v\n%s", err, out)
	}
	if strings.Contains(out, "fake-wrong-token-9z9z9z") {
		t.Fatalf("the key was printed:\n%s", out)
	}
}

// TestReelRoutesDirectAndPostiz: a department with direct publishing for
// one channel and Postiz for another; the publish step's route command
// names each, with the command that uses it.
func TestReelRoutesDirectAndPostiz(t *testing.T) {
	publishFixture(t)
	out, err := runCLI(t, "reel", "routes", "seo/page")
	if err != nil {
		t.Fatal(out)
	}
	assertContains(t, out, "Marketing publishes:")
	assertContains(t, out, "instagram: direct (instagram-reels, marketing/instagram): issue-cli connector exec marketing/instagram -- publish --file <mp4> --caption-file <txt> [--send]")
	assertContains(t, out, "linkedin: Postiz (marketing/postiz): Marketing's agents post it through that connector")
	out, err = runCLI(t, "health", "lint", "--check", "departments")
	if strings.Contains(out, "publish") {
		t.Fatalf("lint finds fault with the routes (%v):\n%s", err, out)
	}
}

func TestPublishUsageNamesEveryCall(t *testing.T) {
	publishFixture(t)
	out, err := runCLI(t, "connector", "exec", "marketing/instagram", "--as-owner")
	if err == nil {
		t.Fatal("no call should be a usage error")
	}
	for _, want := range []string{"publish --file <mp4>", "token check", "token refresh --as-owner", "issue-cli reel routes"} {
		assertContains(t, out, want)
	}
}
