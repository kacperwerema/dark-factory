package viewer

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// readerProject is shelfProject plus Marketing's brand output (a fixture
// copy of ab00) made by ui/child.
func readerProject(t *testing.T) (tracker.Project, *outputs.Output) {
	t.Helper()
	a, _ := shelfProject(t)
	o, err := outputs.Add(&a, outputs.AddInput{Department: "marketing", Title: "Brand and ICP: Dark Factory", Kind: "report", File: filepath.Join("testdata", "outputs-reader", "brand-v1.md"), Issue: "ui/child", By: "agent-ui-child"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return a, o
}

func postOdoc(t *testing.T, target string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// AC1: a markdown output renders as a document with its contents; the
// shelf links its title to the reader and keeps the raw file one click on.
func TestShelfReaderRendersADocumentWithContents(t *testing.T) {
	a, o := readerProject(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	code, html := getBody(t, ts.URL+"/p/agents/shelf/read/"+o.ID)
	if code != 200 {
		t.Fatalf("status %d: %s", code, html)
	}
	assertContains(t, html,
		`data-testid="odoc.page"`,
		`<h1 class="wb-odoc-title" data-testid="odoc.title">Brand and ICP: Dark Factory</h1>`,
		// Contents: h2 and h3 with their ids, indented by level.
		`<a class="wb-odoc-toc-item wb-odoc-toc-item--2" href="#brand" data-testid="odoc.toc.item"><span class="wb-odoc-toc-text">Brand</span>`,
		`<a class="wb-odoc-toc-item wb-odoc-toc-item--3" href="#voice" data-testid="odoc.toc.item">`,
		`<a class="wb-odoc-toc-item wb-odoc-toc-item--4" href="#who-is-not-a-target" data-testid="odoc.toc.item">`,
		// The sheet: rendered markdown, not the raw text.
		`<div class="wb-odoc-sheet wb-md" data-sheet="day" data-testid="odoc.sheet">`,
		`<h2 id="brand">Brand</h2>`,
		`<strong>Core idea: a dark factory with one lamp on, yours.</strong>`,
		`data-testid="odoc.raw"`, `data-testid="odoc.approve"`, `name="back" value="/p/agents/shelf/read/`+o.ID+`"`,
		`data-testid="odoc.none"`, `Goes to the agent as <code>revise:</code> on ui/child.`,
		`Map</a>`, `>Shelf</a>`,
	)
	// The document's own "# " heading is the title, printed once; one
	// version: no Changes tab.
	if strings.Contains(html, `<h1 id="brand-and-icp-dark-factory">`) || strings.Contains(html, `odoc.view.changes`) {
		t.Error("title twice, or Changes with one version")
	}
	_, shelf := getBody(t, ts.URL+"/p/agents/shelf")
	assertContains(t, shelf, `href="/p/agents/shelf/read/`+o.ID+`" rel="noopener noreferrer" data-testid="shelf.title">Brand and ICP: Dark Factory</a>`, `data-testid="shelf.raw"`)
	// Not markdown, or unknown: no reader.
	list, _ := outputs.Load(&a)
	for _, other := range list {
		if other.ID == o.ID {
			continue
		}
		if code, _ := getBody(t, ts.URL+"/p/agents/shelf/read/"+other.ID); code != http.StatusNotFound {
			t.Errorf("%s (%s) read = %d, want 404", other.Title, other.Kind, code)
		}
	}
}

// AC2, AC3, AC5: the owner's browser comments on the whole document and on
// a passage; each is stored with its anchor and lands on the issue trail as
// a revise: comment quoting the passage; anything else is refused.
func TestShelfReaderCommentsAndTrail(t *testing.T) {
	a, o := readerProject(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	base := ts.URL + "/p/agents/shelf/comment"

	// Not the owner: no session, even with the browser's headers (curl, an agent).
	code, out := postOdoc(t, base, map[string]any{"id": o.ID, "text": "hi"}, map[string]string{strangerHeader: "1", "Sec-Fetch-Site": "same-origin"})
	if code != http.StatusForbidden || !strings.Contains(out["error"].(string), "issue-cli output comments") {
		t.Fatalf("agent write = %d %v", code, out)
	}
	code, _ = postOdoc(t, base, map[string]any{"id": o.ID, "text": "hi"}, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if code != http.StatusForbidden {
		t.Fatalf("cross-site write = %d", code)
	}

	code, out = postOdoc(t, base, map[string]any{"id": o.ID, "text": "Lead with what I settle."}, sameOrigin)
	if code != http.StatusCreated || out["trail"] != "posted on ui/child" {
		t.Fatalf("whole doc = %d %v", code, out)
	}
	whole := out["thread"].(string)
	anchor := map[string]string{"heading": "proposal-the-owner-settles-it", "heading_text": "Proposal (the owner settles it)", "quote": `"A dark factory with one light on: yours."`, "prefix": "README) B. ", "suffix": " C. \"Run the company."}
	code, out = postOdoc(t, base, map[string]any{"id": o.ID, "text": "B is the one.\nDrop C.", "anchor": anchor}, sameOrigin)
	if code != http.StatusCreated {
		t.Fatalf("passage = %d %v", code, out)
	}
	passage := out["thread"].(string)
	code, _ = postOdoc(t, base, map[string]any{"id": o.ID, "thread": passage, "text": "And use it in the README."}, sameOrigin)
	if code != http.StatusCreated {
		t.Fatalf("reply = %d", code)
	}
	if code, _ := postOdoc(t, base, map[string]any{"id": o.ID, "text": "  "}, sameOrigin); code != http.StatusBadRequest {
		t.Errorf("empty comment = %d", code)
	}

	all, _ := outputs.LoadComments(&a)
	threads := all[o.ID]
	if len(threads) != 2 || !threads[0].Anchor.Whole() || threads[1].Anchor.Quote != anchor["quote"] || threads[1].Anchor.Heading != anchor["heading"] || threads[1].Version != 1 || len(threads[1].Messages) != 2 {
		t.Fatalf("stored = %+v", threads)
	}

	issues, _ := tracker.LoadIssues(a.IssueDir)
	issue := tracker.FindIssue(issues, "ui/child")
	if issue == nil {
		t.Fatal("no ui/child")
	}
	comments, _ := tracker.LoadComments(issue.FilePath)
	var trail []string
	for _, c := range comments {
		if strings.HasPrefix(c.Text, "revise:") {
			trail = append(trail, c.Text)
			if c.Author != tracker.OwnerAuthor {
				t.Errorf("trail author = %q", c.Author)
			}
		}
	}
	short := o.ID[strings.LastIndex(o.ID, "-")+1:]
	want := []string{
		"revise: output " + short + " v1 · whole document — Lead with what I settle. (thread " + whole + " · issue-cli output comments " + short + ")",
		`revise: output ` + short + ` v1 · Brand › Proposal (the owner settles it) · “"A dark factory with one light on: yours."” — B is the one. Drop C. (thread ` + passage + ` · issue-cli output comments ` + short + `)`,
		`revise: (reply) output ` + short + ` v1 · Brand › Proposal (the owner settles it) · “"A dark factory with one light on: yours."” — And use it in the README. (thread ` + passage + ` · issue-cli output comments ` + short + `)`,
	}
	if strings.Join(trail, "\n") != strings.Join(want, "\n") {
		t.Errorf("trail =\n%s\nwant\n%s", strings.Join(trail, "\n"), strings.Join(want, "\n"))
	}

	// The page: counts, the passage's thread under its heading, marks for
	// the script; the shelf row's open count.
	_, html := getBody(t, ts.URL+"/p/agents/shelf/read/"+o.ID)
	assertContains(t, html, `data-testid="odoc.open">2 open</span>`, `id="thread-`+passage+`"`, `<a href="#proposal-the-owner-settles-it">Brand › Proposal (the owner settles it)</a>`,
		`<span class="wb-odoc-toc-text">Proposal (the owner settles it)</span><span class="wb-odoc-count" title="open comments">1</span>`, `"place":"here"`, `"place":"whole"`)
	_, shelf := getBody(t, ts.URL+"/p/agents/shelf")
	assertContains(t, shelf, `data-testid="shelf.talk">💬 2 open</a>`)

	// Resolve, then the counts drop.
	resolve := ts.URL + "/p/agents/shelf/comment/resolve"
	if code, _ := postOdoc(t, resolve, map[string]any{"id": o.ID, "thread": whole}, map[string]string{strangerHeader: "1"}); code != http.StatusForbidden {
		t.Errorf("agent resolve = %d", code)
	}
	if code, out := postOdoc(t, resolve, map[string]any{"id": o.ID, "thread": whole}, map[string]string{"Origin": ts.URL}); code != http.StatusOK || out["resolved"] != true {
		t.Fatalf("resolve = %d %v", code, out)
	}
	_, html = getBody(t, ts.URL+"/p/agents/shelf/read/"+o.ID)
	assertContains(t, html, `data-testid="odoc.open">1 open · 1 resolved</span>`, `data-testid="odoc.resolved"`)
}

// AC2, AC4: after a revision the passage's thread stays on its passage, a
// thread whose passage went falls back to its heading or folds under "not in
// v2", and Changes shows the diff.
func TestShelfReaderRevisionKeepsCommentsAndShowsChanges(t *testing.T) {
	a, o := readerProject(t)
	kept, err := outputs.AddComment(&a, o.ID, outputs.Anchor{Heading: "proposal-the-owner-settles-it", Quote: `"A dark factory with one light on: yours."`, Prefix: "README) B. ", Suffix: ` C. "Run the company.`}, "B is the one.", "owner", 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	moved, _ := outputs.AddComment(&a, o.ID, outputs.Anchor{Heading: "proposal-the-owner-settles-it", Quote: `"Run the company. Let the agents run the floor."`}, "Drop C.", "owner", 1, time.Now())
	gone, _ := outputs.AddComment(&a, o.ID, outputs.Anchor{Heading: "no-such", HeadingText: "Draft", Quote: "nothing settled until the owner picks."}, "Say what I settle first.", "owner", 1, time.Now())
	if _, err := outputs.Revise(&a, o.ID, filepath.Join("testdata", "outputs-reader", "brand-v2.md"), "answers the comments", "agent-ui-child", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/shelf/read/"+o.ID)
	assertContains(t, html, `data-testid="odoc.version">v2</span>`, `data-testid="odoc.view.changes">Changes since v1</a>`,
		`{"id":"`+kept.ID+`","quote":`, `"place":"here"`, `{"id":"`+moved.ID+`","quote":`, `"heading":"proposal-the-owner-settles-it","place":"heading"}`,
		`The passage is not in v2; the heading still is.`, `data-testid="odoc.gone"><summary>1 not in v2</summary>`, `id="thread-`+gone.ID+`"`,
		`<h2 id="what-you-settle">What you settle</h2>`)
	if strings.Contains(html, `"id":"`+gone.ID+`"`) {
		t.Error("a gone thread is still marked in the sheet")
	}
	_, diff := getBody(t, ts.URL+"/p/agents/shelf/read/"+o.ID+"?view=changes")
	assertContains(t, diff, `data-testid="odoc.diff"`, `+6 lines`, `−2 lines`,
		`<div class="wb-odoc-diff-line wb-odoc-diff-line--del" data-op="−">- C. &#34;Run the company. Let the agents run the floor.&#34;</div>`,
		`<div class="wb-odoc-diff-line wb-odoc-diff-line--add" data-op="&#43;">## What you settle</div>`, `unchanged lines</div>`)
	_, shelf := getBody(t, ts.URL+"/p/agents/shelf")
	assertContains(t, shelf, `data-testid="shelf.version">v2</span>`)
}

func TestShelfReviewReturnsToTheReader(t *testing.T) {
	a, o := readerProject(t)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	for back, want := range map[string]string{
		"/p/agents/shelf/read/" + o.ID: "/p/agents/shelf/read/" + o.ID,
		"https://evil.example/":        "/p/agents/shelf",
	} {
		form := url.Values{"id": {o.ID}, "verdict": {"reject"}, "back": {back}}
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/agents/shelf/review", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("back %q → %q, want %q", back, got, want)
		}
	}
}
