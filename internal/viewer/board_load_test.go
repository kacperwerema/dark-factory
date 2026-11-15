package viewer

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/memguard"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// settle backdates a board's files: a file younger than the cache's window
// is read again on purpose, and a live board is mostly old files.
func settle(t *testing.T, proj tracker.Project) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	for _, root := range []string{proj.IssueDir, proj.WorkflowFile} {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				os.Chtimes(path, old, old)
			}
			return nil
		})
	}
}

// TestPagesLoadTheBoardOnce is the audit's causes 3 to 6 on one page: a page
// walks each project's board once, and a warm one parses no issue and no
// workflow, reads no comments from disk and renders no body. Each page is
// counted on a board of 4 issues and one of 16: none may grow with the board.
func TestPagesLoadTheBoardOnce(t *testing.T) {
	pages := []string{
		"/p/gitcalls/",
		"/p/gitcalls/shelf",
		"/p/gitcalls/needs-you",
		"/p/gitcalls/hall",
		"/p/gitcalls/company",
		"/p/gitcalls/status",
		"/p/gitcalls/status.json",
		"/p/gitcalls/workboard?classic=1",
		"/p/gitcalls/agents",
		"/p/gitcalls/stats",
		"/p/gitcalls/health",
		"/p/gitcalls/systems",
		"/p/gitcalls/hq/report",
		"/p/gitcalls/memory",
		"/p/gitcalls/lead",
		"/p/gitcalls/yolo/review",
		"/needs-you",
		"/",
		"/crew",
		"/leads",
		"/stats",
	}
	count := func(n int) map[string]tracker.LoadCounts {
		b := newGitBoard(t, n)
		settle(t, b.proj)
		srv, err := NewServer([]tracker.Project{b.proj})
		if err != nil {
			t.Fatal(err)
		}
		h := srv.Routes()
		got := map[string]tracker.LoadCounts{}
		for _, page := range pages {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://127.0.0.1:8080"+page, nil))
			before := tracker.ReadLoadCounts()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:8080"+page, nil))
			if rec.Code != 200 {
				t.Fatalf("%s: %d\n%s", page, rec.Code, rec.Body.String())
			}
			after := tracker.ReadLoadCounts()
			got[page] = tracker.LoadCounts{
				BoardWalks:     after.BoardWalks - before.BoardWalks,
				IssueParses:    after.IssueParses - before.IssueParses,
				FileReads:      after.FileReads - before.FileReads,
				WorkflowParses: after.WorkflowParses - before.WorkflowParses,
				CommentReads:   after.CommentReads - before.CommentReads,
				BodyRenders:    after.BodyRenders - before.BodyRenders,
				DecisionParses: after.DecisionParses - before.DecisionParses,
			}
		}
		return got
	}
	small, big := count(4), count(16)
	for _, page := range pages {
		s, b := small[page], big[page]
		t.Logf("%s: 4 issues %+v, 16 issues %+v", page, s, b)
		if b.BoardWalks > 1 {
			t.Errorf("%s walked the board %d times, want 1", page, b.BoardWalks)
		}
		if b.IssueParses != 0 || b.WorkflowParses != 0 || b.BodyRenders != 0 {
			t.Errorf("%s warm: %d issue parses, %d workflow parses, %d bodies rendered, want none", page, b.IssueParses, b.WorkflowParses, b.BodyRenders)
		}
		if b.DecisionParses != 0 {
			t.Errorf("%s warm parsed %d issues' decisions, want none", page, b.DecisionParses)
		}
		if b.FileReads != 0 {
			t.Errorf("%s warm read %d issue files, want none", page, b.FileReads)
		}
		if b.CommentReads > s.CommentReads {
			t.Errorf("%s read comments from disk %d times on 4 issues and %d on 16: a read per issue", page, s.CommentReads, b.CommentReads)
		}
	}
}

// The issue pages are the ones that show a body: once, and one board load.
func TestIssuePageRendersTheBodyOnce(t *testing.T) {
	b := newGitBoard(t, 3)
	settle(t, b.proj)
	srv, err := NewServer([]tracker.Project{b.proj})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Routes()
	for _, page := range []string{"/p/gitcalls/issue/cli/ht-01", "/p/gitcalls/issue/cli/ht-01?classic=1"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://127.0.0.1:8080"+page, nil))
		before := tracker.ReadLoadCounts()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:8080"+page, nil))
		after := tracker.ReadLoadCounts()
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<p>Body.</p>") {
			t.Fatalf("%s: %d, body not shown", page, rec.Code)
		}
		if got := after.BodyRenders - before.BodyRenders; got != 1 {
			t.Errorf("%s rendered the body %d times, want 1", page, got)
		}
		if got := after.BoardWalks - before.BoardWalks; got != 1 {
			t.Errorf("%s walked the board %d times, want 1", page, got)
		}
	}
}

// A page shows the board as of when it starts, but a write and the page after
// it never see an older one.
func TestAWriteShowsOnTheNextRequest(t *testing.T) {
	b := newGitBoard(t, 2)
	settle(t, b.proj)
	srv, err := NewServer([]tracker.Project{b.proj})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Routes()
	get := func() string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:8080/p/gitcalls/issues.json", nil))
		return rec.Body.String()
	}
	get()
	path := filepath.Join(b.proj.IssueDir, "CLI", "new.md")
	if err := os.WriteFile(path, []byte("---\ntitle: \"Fresh one\"\nstatus: \"testing\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if body := get(); !strings.Contains(body, "Fresh one") {
		t.Fatalf("a new issue is missing from the next request:\n%s", body)
	}
}

// A write keeps the plain server: what it reads has to be the file. The
// handler sees the project without a shared load.
func TestOneBoardLoadLeavesWritesAlone(t *testing.T) {
	b := newGitBoard(t, 2)
	srv, err := NewServer([]tracker.Project{b.proj})
	if err != nil {
		t.Fatal(err)
	}
	var walks int64
	h := srv.oneBoardLoad(func(s *Server, _ http.ResponseWriter, _ *http.Request) {
		before := tracker.ReadLoadCounts().BoardWalks
		p := s.findProject("gitcalls")
		p.LoadIssues()
		p.LoadIssues()
		walks = tracker.ReadLoadCounts().BoardWalks - before
	})
	for method, want := range map[string]int64{"GET": 1, "HEAD": 1, "POST": 2} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "http://127.0.0.1:8080/", nil))
		if walks != want {
			t.Errorf("%s: %d walks, want %d", method, walks, want)
		}
	}
}

// Work that outlives the request reads the board again, and the server's own
// projects never carry a request's load.
func TestSharedLoadEndsWithTheRequest(t *testing.T) {
	b := newGitBoard(t, 1)
	srv, err := NewServer([]tracker.Project{b.proj})
	if err != nil {
		t.Fatal(err)
	}
	var kept *tracker.Project
	var sidebar *tracker.Project
	srv.oneBoardLoad(func(s *Server, _ http.ResponseWriter, _ *http.Request) {
		kept = s.findProject("gitcalls")
		sidebar = srv.projectInFlight("gitcalls")
	}).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://127.0.0.1:8080/", nil))
	if sidebar != kept {
		t.Fatalf("the sidebar's project isn't the request's")
	}
	if p := srv.projectInFlight("gitcalls"); p != srv.findProject("gitcalls") {
		t.Fatalf("a finished request's project is still in flight")
	}
	before := tracker.ReadLoadCounts().BoardWalks
	kept.LoadIssues()
	kept.LoadIssues()
	if got := tracker.ReadLoadCounts().BoardWalks - before; got != 2 {
		t.Fatalf("after the request %d walks, want 2", got)
	}
}

func TestSharedLoadUnderConcurrentRequests(t *testing.T) {
	b := newGitBoard(t, 6)
	settle(t, b.proj)
	srv, err := NewServer([]tracker.Project{b.proj})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Routes()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			page := []string{"/p/gitcalls/status", "/p/gitcalls/hall", "/needs-you"}[i%3]
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:8080"+page, nil))
			if rec.Code != 200 {
				t.Errorf("%s: %d", page, rec.Code)
			}
		}(i)
	}
	wg.Wait()
}

// An edit on disk shows on the next request, even the second of two writes
// with the same size and mtime, and an unchanged board isn't read at all.
func TestAnEditOnDiskShowsOnTheNextRequest(t *testing.T) {
	b := newGitBoard(t, 3)
	settle(t, b.proj)
	srv, err := NewServer([]tracker.Project{b.proj})
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Routes()
	get := func(page string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:8080"+page, nil))
		return rec.Code, rec.Body.String()
	}
	page := "/p/gitcalls/issue/cli/ht-01"
	get(page)

	before := tracker.ReadLoadCounts()
	if code, body := get(page); code != 200 || !strings.Contains(body, "<p>Body.</p>") {
		t.Fatalf("settled page: %d, body not shown", code)
	}
	after := tracker.ReadLoadCounts()
	if after.IssueParses != before.IssueParses || after.FileReads != before.FileReads {
		t.Fatalf("an unchanged board was read again: %d parses, %d reads", after.IssueParses-before.IssueParses, after.FileReads-before.FileReads)
	}

	path := filepath.Join(b.proj.IssueDir, "CLI", "ht-01.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	write := func(word string) time.Time {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Replace(string(original), "Body.", word, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.ModTime()
	}
	stamp := write("Bod1.")
	if _, body := get(page); !strings.Contains(body, "<p>Bod1.</p>") {
		t.Fatal("an edit on disk did not show on the next request")
	}
	write("Bod2.") // same size, and the same mtime as the write before
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, body := get(page); !strings.Contains(body, "<p>Bod2.</p>") {
		t.Fatal("a second write with the same size and mtime did not show")
	}

	added := filepath.Join(b.proj.IssueDir, "CLI", "ht-new.md")
	if err := os.WriteFile(added, []byte("---\ntitle: \"HT new\"\nstatus: \"testing\"\n---\n\nNew.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, body := get("/p/gitcalls/issue/cli/ht-new"); code != 200 || !strings.Contains(body, "<p>New.</p>") {
		t.Fatalf("a new file: %d, not shown", code)
	}
	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	if code, _ := get("/p/gitcalls/issue/cli/ht-new"); code != 404 {
		t.Fatalf("a removed file: %d, want 404", code)
	}
}

// The map needs one line of the engine's release card, not its Report: the
// Report reads mem-guard (auto-start's card), which is how this tells them
// apart.
func TestMapDoesNotBuildTheEngineReport(t *testing.T) {
	b := newGitBoard(t, 2)
	settle(t, b.proj)
	srv, err := NewServer([]tracker.Project{b.proj})
	if err != nil {
		t.Fatal(err)
	}
	srv.engine = b.proj.Slug
	if err := autostart.TurnOn(ciRootFor(&b.proj), b.proj.Slug, autostart.State{Words: "go", By: "owner", How: "test"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	reads := 0
	orig := autostart.ReadMemory
	autostart.ReadMemory = func() memguard.Reading { reads++; return memguard.Reading{} }
	t.Cleanup(func() { autostart.ReadMemory = orig })

	h := srv.Routes()
	get := func(page string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:8080"+page, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d", page, rec.Code)
		}
	}
	get("/")
	if reads != 0 {
		t.Fatalf("the map read mem-guard %d times: it built the Report", reads)
	}
	get("/p/gitcalls/status")
	if reads == 0 {
		t.Fatal("the Report did not read mem-guard: this test no longer tells them apart")
	}
}
