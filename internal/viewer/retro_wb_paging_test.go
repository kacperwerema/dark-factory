package viewer

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/timing"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// manyRetrosProject is a 300-issue project with n retros, newest first by
// the minute, every third one processed. Each note has the four labelled
// lines and draws its terms from one shared vocabulary, so the selected
// retro's pattern has to be looked up in every other note, as on a board
// that never archived its retros (this repo: 510 notes of 6 KB, 2026-11). Files are aged an hour: the parse caches
// keep only settled files.
func manyRetrosProject(t *testing.T, n int) tracker.Project {
	t.Helper()
	proj := bigProject(t, 300)
	dir := filepath.Join(proj.WorkDir, "retros")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var vocab []string
	for i := 0; i < 120; i++ {
		vocab = append(vocab, fmt.Sprintf("subsystem%03d-handler", i))
	}
	base := time.Now().Add(-time.Hour)
	// Notes on this repo average 6 KB: pad the friction line to that.
	pad := strings.Repeat("The run waited on the shared machine and the agent retried the step by hand. ", 60)
	for i := 0; i < n; i++ {
		var terms []string
		for k := 0; k < 60; k++ {
			terms = append(terms, vocab[(i*7+k*3)%len(vocab)])
		}
		review := "open"
		if i%3 == 2 {
			review = "processed"
		}
		when := base.Add(-time.Duration(i) * time.Minute)
		body := fmt.Sprintf("# Workflow Retrospective\n\nIssue: issue-%04d\nTitle: Retro %03d\nStatus: done\nSystem: S%d\nDate: %s\nReviewStatus: %s\n\n"+
			"Base workflow: the gate asked twice about %s.\nSubsystem workflow for S%d: no recipe for %s.\n"+
			"Missing system-specific instructions: (1) how %s works; (2) where %s lives.\nTooling/workflow friction: %s. %s\n",
			i%300, i, i%7, when.Format(time.RFC3339), review,
			strings.Join(terms[:15], ", "), i%7, strings.Join(terms[15:30], ", "), strings.Join(terms[30:45], ", "), terms[45], strings.Join(terms[46:], ", "), pad)
		p := filepath.Join(dir, fmt.Sprintf("%s-retro-%03d.md", when.Format("20060102-150405"), i))
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, when, when)
	}
	return proj
}

func retroGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d", url, resp.StatusCode)
	}
	return string(b)
}

// retroVsBoard is how much slower than /board, timed just before it, a warm
// /retros may be on a slow machine: the Retro page builds the same company
// header and lot the board's neighbours do, plus its lists and pattern.
const retroVsBoard = 3

// AC1: with 500 unarchived retros the Retro page renders warm in under
// 500 ms, or no slower than retroVsBoard × /board when the machine is
// loaded. Unchanged, the pattern re-split every note once per term of the
// selected retro.
func TestRetrosPageFastWithManyRetros(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 500-retro project")
	}
	withMockTmuxSessions(t, nil)
	proj := manyRetrosProject(t, 500)
	withWorkingDir(t, proj.WorkDir)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	get := func(path string) time.Duration {
		start := time.Now()
		retroGet(t, ts.URL+"/p/big/"+path)
		return time.Since(start)
	}
	if html := retroGet(t, ts.URL+"/p/big/retros"); !strings.Contains(html, `data-testid="retro.pattern"`) {
		t.Fatal("the fixture's selected retro has no pattern: the test would not time the pattern lookup")
	}
	get("board")
	best := time.Hour
	check := timing.Check{Limit: 500 * time.Millisecond, Ratio: retroVsBoard}
	for _, q := range []string{"retros", "retros?all=1", "retros?retro=" + "x", "retros?all=1&to_sort_page=3"} {
		r := check.Run(func() time.Duration { return get(q) }, func() time.Duration { return get("board") })
		best = min(best, r.Best)
		if r.Slow {
			t.Errorf("/%s warm: %s (baseline: /board)", q, r)
		}
	}
	t.Logf("500 retros: warm best %v", best)
}

var retroItemTitle = regexp.MustCompile(`<span class="wb-ritem-title">Retro (\d+)</span>`)

// titles lists the retro numbers a list section shows, in order.
func retroListTitles(t *testing.T, html, key string) []string {
	t.Helper()
	start := strings.Index(html, `data-testid="retro.list.`+key+`"`)
	if start < 0 {
		t.Fatalf("no %s list", key)
	}
	sec := html[start:]
	sec = sec[:strings.Index(sec, "</section>")]
	var out []string
	for _, m := range retroItemTitle.FindAllStringSubmatch(sec, -1) {
		out = append(out, m[1])
	}
	return out
}

// AC2: the show-all view lists the newest retros first and pages the rest,
// retroPageSize at a time, each list on its own; the classic page pages too.
func TestRetrosPagePages(t *testing.T) {
	withMockTmuxSessions(t, nil)
	proj := manyRetrosProject(t, 100) // 67 to sort, 33 processed
	withWorkingDir(t, proj.WorkDir)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	u := ts.URL + "/p/big/retros"

	// The default view is as before: 8 per list and "+N older".
	html := retroGet(t, u)
	if got := retroListTitles(t, html, "to_sort"); len(got) != retroListMax || got[0] != "000" {
		t.Fatalf("default To sort = %v, want the 8 newest from 000", got)
	}
	if !strings.Contains(html, "+59 older") || strings.Contains(html, `data-testid="retro.list.range"`) {
		t.Fatal("default view: want +59 older and no pager")
	}

	// all=1: page 1 of To sort is the 30 newest, with Older only.
	html = retroGet(t, u+"?all=1")
	got := retroListTitles(t, html, "to_sort")
	if len(got) != retroPageSize || got[0] != "000" || got[1] != "001" || got[2] != "003" {
		t.Fatalf("all=1 To sort page 1 = %v, want 30 newest first", got)
	}
	if !strings.Contains(html, ">1–30 of 67<") || !strings.Contains(html, ">1–30 of 33<") {
		t.Fatal("all=1: want the range of both lists")
	}
	if strings.Contains(html, `data-testid="retro.list.newer"`) {
		t.Fatal("page 1 has a Newer link")
	}
	older := regexp.MustCompile(`href="([^"]*to_sort_page=2[^"]*)" data-testid="retro.list.older"`).FindStringSubmatch(html)
	if older == nil {
		t.Fatal("no Older link to To sort page 2")
	}

	// Older → page 2: the next 30, the other list still on page 1.
	html = retroGet(t, ts.URL+strings.ReplaceAll(older[1], "&amp;", "&"))
	got2 := retroListTitles(t, html, "to_sort")
	if len(got2) != retroPageSize || got2[0] == got[0] || !strings.Contains(html, ">31–60 of 67<") || !strings.Contains(html, ">1–30 of 33<") {
		t.Fatalf("page 2 To sort = %v", got2)
	}

	// The processed list pages on its own and keeps To sort's page.
	html = retroGet(t, u+"?all=1&to_sort_page=2&processed_page=2")
	if !strings.Contains(html, ">31–60 of 67<") || !strings.Contains(html, ">31–33 of 33<") {
		t.Fatal("processed_page=2 with to_sort_page=2: want both pages")
	}
	if p := retroListTitles(t, html, "processed"); len(p) != 3 {
		t.Fatalf("processed page 2 = %v, want the 3 oldest", p)
	}
	if !strings.Contains(html, "to_sort_page=2") {
		t.Fatal("links on processed page 2 dropped To sort's page")
	}

	// Past the end or not a number: the last page, or the first.
	if html = retroGet(t, u+"?all=1&to_sort_page=99"); !strings.Contains(html, ">61–67 of 67<") || strings.Count(html, `data-testid="retro.list.older"`) != 1 {
		t.Fatal("to_sort_page=99: want To sort's last page, with only Processed's Older link")
	}
	if html = retroGet(t, u+"?all=1&to_sort_page=x"); !strings.Contains(html, ">1–30 of 67<") {
		t.Fatal("to_sort_page=x: want page 1")
	}

	// The classic page (open retros by default) shows 50 with their bodies
	// per page.
	html = retroGet(t, u+"?classic=1")
	if n := strings.Count(html, `class="retro-card"`); n != classicRetroPage || !strings.Contains(html, "1–50 of 67") {
		t.Fatalf("classic page 1: %d cards", n)
	}
	html = retroGet(t, u+"?classic=1&page=2")
	if n := strings.Count(html, `class="retro-card"`); n != 17 || !strings.Contains(html, "51–67 of 67") || !strings.Contains(html, "← Newer") || strings.Contains(html, "Older →") {
		t.Fatalf("classic page 2: %d cards", n)
	}
}
