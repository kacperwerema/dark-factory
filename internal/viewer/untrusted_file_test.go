package viewer

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// gzipBytes is a small gzipped blob, the shape of a pprof profile.
func gzipBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte("profile\x00\x01\x02"))
	zw.Close()
	return buf.Bytes()
}

// assertUntrustedHeaders checks the headers serveUntrustedFile sets: text
// inline as text/plain, anything else a download, both unrunnable.
func assertUntrustedHeaders(t *testing.T, label string, h http.Header, text bool, name string) {
	t.Helper()
	wantType, wantDisp := "application/octet-stream", "attachment; filename="+name
	if text {
		wantType, wantDisp = "text/plain; charset=utf-8", "inline; filename="+name
	}
	if got := h.Get("Content-Type"); got != wantType {
		t.Errorf("%s: Content-Type %q, want %q", label, got, wantType)
	}
	if got := h.Get("Content-Disposition"); got != wantDisp {
		t.Errorf("%s: Content-Disposition %q, want %q", label, got, wantDisp)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("%s: no nosniff", label)
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("%s: CSP %q, want sandbox and default-src 'none'", label, csp)
	}
}

// A file attachment (issue-cli attach of a non-image) is served from
// /attachments/<hash>/<name>: text inline as text/plain, a profile as a
// download, an HTML file never as HTML; a link or a bad path is a 404.
func TestAttachmentFileServedAsTextOrDownload(t *testing.T) {
	srv, proj, _ := secServer(t)
	pprof := gzipBytes(t)
	cases := []struct {
		name string
		data []byte
		text bool
	}{
		{"cpu.pprof", pprof, false},
		{"top.txt", []byte("Showing: 12s\n 4.1s os.Stat\n"), true},
		{"page.html", []byte("<html><script>alert(1)</script></html>"), true},
	}
	for _, c := range cases {
		rel, err := tracker.SaveAttachmentFile(proj.IssueDir, c.name, c.data)
		if err != nil {
			t.Fatalf("save %s: %v", c.name, err)
		}
		if !tracker.AttachmentFilePattern.MatchString(rel) || filepath.Base(rel) != c.name {
			t.Fatalf("saved %s as %q", c.name, rel)
		}
		url := tracker.AttachmentURL(proj.Slug, rel)
		rec := secDo(t, srv, "GET", url, "", nil)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), c.data) {
			t.Fatalf("GET %s = %d", url, rec.Code)
		}
		assertUntrustedHeaders(t, url, rec.Header(), c.text, c.name)
	}

	// A hash folder that is a link out, and paths the pattern refuses.
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "leak.txt"), []byte("secret"), 0o644)
	os.Symlink(outside, filepath.Join(tracker.AttachmentsDir(proj.IssueDir), strings.Repeat("a", 32)))
	for _, p := range []string{
		strings.Repeat("a", 32) + "/leak.txt",
		strings.Repeat("b", 32) + "/.hidden",
		strings.Repeat("b", 32) + "/a/b.txt",
		"short/top.txt",
	} {
		url := "/p/" + proj.Slug + "/attachments/" + p
		if rec := secDo(t, srv, "GET", url, "", nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", url, rec.Code)
		}
	}
}

// /files/ serves a non-image only inside <issues>/_evidence/ (as text or a
// download); a .txt, .go or .yaml anywhere else in the project stays a 404.
func TestProjectFileServesEvidenceFilesOnly(t *testing.T) {
	proj, tmp := setupTestProject(t)
	proj.WorkDir = tmp
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(tmp, "config.yaml"), []byte("token: x\n"), 0o644)
	os.WriteFile(filepath.Join(proj.IssueDir, "notes.txt"), []byte("not evidence\n"), 0o644)
	if _, err := tracker.SaveEvidence(proj.IssueDir, "bug-in-login", "AC1-top.txt", []byte("Showing: 12s\n"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.SaveEvidence(proj.IssueDir, "bug-in-login", "AC1-cpu.pprof", gzipBytes(t), false); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	rel, _ := filepath.Rel(tmp, tracker.EvidenceDir(proj.IssueDir, "bug-in-login"))
	base := ts.URL + "/p/test-project/files/" + filepath.ToSlash(rel) + "/"
	for name, text := range map[string]bool{"AC1-top.txt": true, "AC1-cpu.pprof": false} {
		resp, err := http.Get(base + name)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", name, resp.StatusCode)
		}
		assertUntrustedHeaders(t, name, resp.Header, text, name)
	}
	relIssues, _ := filepath.Rel(tmp, proj.IssueDir)
	for _, p := range []string{"main.go", "config.yaml", filepath.ToSlash(relIssues) + "/notes.txt", filepath.ToSlash(relIssues) + "/_evidence"} {
		resp, err := http.Get(ts.URL + "/p/test-project/files/" + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /files/%s = %d, want 404", p, resp.StatusCode)
		}
	}
}

// The Evidence tab lists a non-image evidence file in its Files card with
// its step, size and a link, and keeps it out of the Stills card.
func TestEvidenceTabListsFiles(t *testing.T) {
	proj, tmp := setupTestProject(t)
	proj.WorkDir = tmp
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	base := ts.URL + "/p/test-project/issue/bug-in-login?tab=evidence"
	if _, ev := getBody(t, base); strings.Contains(ev, `data-testid="evidence.files"`) {
		t.Error("the Files card shows with no files")
	}
	if _, err := tracker.SaveEvidence(proj.IssueDir, "bug-in-login", "AC1-top.txt", bytes.Repeat([]byte("x"), 2048), false); err != nil {
		t.Fatal(err)
	}
	code, ev := getBody(t, base)
	if code != 200 {
		t.Fatalf("evidence tab: %d", code)
	}
	for _, want := range []string{
		`data-testid="evidence.files"`, "Files · 1", `data-testid="evidence.file.0"`,
		`href="/p/test-project/files/issues/_evidence/bug-in-login/AC1-top.txt"`,
		`<span class="chip ev-mono">AC1</span>`, ">2 KB<",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence tab: missing %s", want)
		}
	}
	if strings.Contains(ev, `data-testid="evidence.stills"`) {
		t.Error("a text file shows in the Stills card")
	}
}
