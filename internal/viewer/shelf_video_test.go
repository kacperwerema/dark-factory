package viewer

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// videoShelf is shelfProject with a 4 KB "video" from ui/child on the
// shelf; poster says whether a stand-in ffmpeg wrote its poster.
func videoShelf(t *testing.T, poster bool) (tracker.Project, *outputs.Output) {
	t.Helper()
	a, _ := shelfProject(t)
	bin := t.TempDir()
	if poster {
		standin.Write(t, filepath.Join(bin, "ffmpeg"), "for a; do last=$a; done\nprintf jpg > \"$last\"\n")
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	} else {
		t.Setenv("PATH", bin)
	}
	src := filepath.Join(t.TempDir(), "final.mp4")
	if err := os.WriteFile(src, bytes.Repeat([]byte("0123456789abcdef"), 256), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := outputs.Add(&a, outputs.AddInput{Department: "marketing", Title: "Draft: What is Dark Factory", Kind: "video", File: src, Issue: "ui/child", By: "agent-ui-child"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if poster != (o.Poster != "") {
		t.Fatalf("poster = %q, want one: %v (%v)", o.Poster, poster, o.PosterErr)
	}
	return a, o
}

// AC2: the player's seeks are Range requests; each answers 206 with the
// part asked for, the video's type and the shelf's sandbox headers.
func TestShelfFileServesVideoRanges(t *testing.T) {
	a, o := videoShelf(t, false)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	url := ts.URL + "/p/agents/shelf/file/" + o.File
	check := func(resp *http.Response, code int) {
		t.Helper()
		if resp.StatusCode != code {
			t.Errorf("status %d, want %d", resp.StatusCode, code)
		}
		h := resp.Header
		if h.Get("Content-Type") != "video/mp4" || h.Get("Accept-Ranges") != "bytes" || h.Get("X-Content-Type-Options") != "nosniff" ||
			!strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox; default-src 'none'") {
			t.Errorf("headers = %v", h)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Range", "bytes=16-31")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	check(resp, http.StatusPartialContent)
	if resp.Header.Get("Content-Range") != "bytes 16-31/4096" || string(body) != "0123456789abcdef" {
		t.Errorf("range: %q %q", resp.Header.Get("Content-Range"), body)
	}
	// An open-ended range, as a browser's first media request.
	req.Header.Set("Range", "bytes=4000-")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	check(resp, http.StatusPartialContent)
	if resp.Header.Get("Content-Range") != "bytes 4000-4095/4096" {
		t.Errorf("open range: %q", resp.Header.Get("Content-Range"))
	}
	// The whole file.
	resp, err = http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	check(resp, http.StatusOK)
	// HEAD, as some players probe first.
	resp, err = http.Head(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	check(resp, http.StatusOK)
}

// AC1 (server side): the reader and the Station render the player with the
// poster, metadata preload only; the shelf row's thumbnail is the poster.
func TestVideoOutputPlaysOnTheReaderAndStation(t *testing.T) {
	a, o := videoShelf(t, true)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	file := "/p/agents/shelf/file/" + o.File
	poster := file + outputs.PosterSuffix
	player := `<video class="wb-vid" controls preload="metadata" playsinline src="` + file + `" poster="` + poster + `" data-testid="shelf.video"><source src="` + file + `" type="video/mp4">`

	code, html := getBody(t, ts.URL+"/p/agents/shelf/read/"+o.ID)
	if code != 200 {
		t.Fatalf("reader: %d %s", code, html)
	}
	assertContains(t, html, `class="wb-odoc wb-odoc--video"`, `data-testid="odoc.player"`, player,
		`data-testid="odoc.vidinfo">video/mp4 · 0.0 MB · poster: frame at 1 s`, `On the whole video`, `say the time`,
		`data-testid="odoc.approve"`, `data-testid="odoc.raw"`)
	for _, gone := range []string{`data-testid="odoc.toc"`, `data-testid="odoc.sheet"`, `data-testid="odoc.sheet.night"`, "autoplay"} {
		if strings.Contains(html, gone) {
			t.Errorf("the video reader has %s", gone)
		}
	}

	_, html = getBody(t, ts.URL+"/p/agents/issue/ui/child")
	assertContains(t, html, `data-testid="station.outputs"`, `Outputs · 2`, `🎬`, `data-testid="station.video">`+player)
	if strings.Count(html, `data-testid="shelf.video"`) != 1 {
		t.Error("the Station shows a player for a non-video output")
	}

	_, html = getBody(t, ts.URL+"/p/agents/shelf")
	assertContains(t, html, `<div class="wb-shelf-thumb wb-shelf-thumb--video"><img src="`+poster+`"`, `href="/p/agents/shelf/read/`+o.ID+`"`)
	if code, body := getBody(t, ts.URL+poster); code != 200 || body != "jpg" {
		t.Errorf("poster: %d %q", code, body)
	}
}

// A video without a poster (ffmpeg missing, or added before posters) still
// plays; the row shows 🎬; a comment on it goes to the trail as on the
// whole video.
func TestVideoWithoutPoster(t *testing.T) {
	a, o := videoShelf(t, false)
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/shelf/read/"+o.ID)
	assertContains(t, html, `preload="metadata" playsinline src="/p/agents/shelf/file/`+o.File+`" data-testid="shelf.video"`, `· no poster`)
	if strings.Contains(html, "poster=") {
		t.Error("a poster attribute without a poster")
	}
	_, html = getBody(t, ts.URL+"/p/agents/shelf")
	assertContains(t, html, `<div class="wb-shelf-thumb wb-shelf-thumb--video"><span aria-hidden="true">🎬</span>`)

	th, err := outputs.AddComment(&a, o.ID, outputs.Anchor{}, "cut the intro at 0:03", tracker.OwnerAuthor, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := reviseComment(o, th, nil, "cut the intro at 0:03", false); !strings.Contains(got, "· whole video — cut the intro at 0:03") {
		t.Errorf("trail line = %q", got)
	}
	_, html = getBody(t, ts.URL+"/p/agents/shelf/read/"+o.ID)
	assertContains(t, html, `Whole video`, `cut the intro at 0:03`, `1 open`)

	// A file gone from the shelf: the missing state, no player.
	if err := os.Remove(filepath.Join(outputs.FilesDir(&a), filepath.FromSlash(o.File))); err != nil {
		t.Fatal(err)
	}
	_, html = getBody(t, ts.URL+"/p/agents/shelf/read/"+o.ID)
	assertContains(t, html, `data-testid="odoc.missing">The video is gone from the shelf`)
	if strings.Contains(html, `data-testid="shelf.video"`) {
		t.Error("a player for a missing video")
	}
}

// A reel's QA report kept beside the video (output add --report, issue-cli
// reel shelve) shows under the player in the reader.
func TestVideoReportShowsUnderThePlayer(t *testing.T) {
	a, _ := shelfProject(t)
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	video, report := filepath.Join(dir, "reel.mp4"), filepath.Join(dir, "reel.mp4.qa.md")
	if err := os.WriteFile(video, []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report, []byte("## Reel QA: pass\n\n| Check | Result |\n|---|---|\n| loudness | pass |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := outputs.Add(&a, outputs.AddInput{Department: "marketing", Title: "Reel", Kind: "video", File: video, Report: report, Issue: "ui/child"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{a})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/shelf/read/"+o.ID)
	assertContains(t, html, `data-testid="odoc.report"`, `Reel QA: pass`, `<td>loudness</td>`)

	// Without a report, no block.
	v2, _ := videoShelf(t, false)
	ts2 := newTestServer(t, []tracker.Project{v2})
	defer ts2.Close()
	list, _ := outputs.Load(&v2)
	_, html = getBody(t, ts2.URL+"/p/agents/shelf/read/"+list[0].ID)
	if strings.Contains(html, `odoc.report`) {
		t.Error("a report block without a report")
	}
}
