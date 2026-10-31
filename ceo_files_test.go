package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// ceoFilePost uploads data as name to …/lead/file; header pairs are added
// to the request (strangerHeader drops the owner's session).
func ceoFilePost(t *testing.T, url, name string, data []byte, header ...string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// ceoHostileZip is a zip whose entries climb out of wherever it is unpacked.
func ceoHostileZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"../../escaped.txt", "../escaped-too.txt", "/abs-escaped.txt", "ok/inside.txt"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte("planted by " + name))
	}
	zw.Close()
	return buf.Bytes()
}

// projectFiles lists every file under root, relative.
func projectFiles(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out[rel] = true
		}
		return nil
	})
	return out
}

func TestCEOFileName(t *testing.T) {
	long := strings.Repeat("a", 300) + ".zip"
	longExt := strings.Repeat("a", 200) + "." + strings.Repeat("b", 20)
	for in, want := range map[string]string{
		"company-floor-v2.zip": "company-floor-v2.zip",
		"../../x":              "x",
		"a/b.txt":              "b.txt",
		`..\c.txt`:             "c.txt",
		".env":                 "env",
		"...pdf":               "pdf",
		"":                     "file",
		"...":                  "file",
		"résumé v2.pdf":        "r__sum___v2.pdf",
		"notes (1).md":         "notes__1_.md",
		long:                   strings.Repeat("a", 116) + ".zip",
		longExt:                longExt[:120],
	} {
		got := ceoFileName(in)
		if got != want {
			t.Errorf("ceoFileName(%q) = %q, want %q", in, got, want)
		}
		if !ceoFileRel.MatchString(strings.Repeat("0", 32) + "/" + got) {
			t.Errorf("ceoFileName(%q) = %q does not match the stored-file pattern", in, got)
		}
	}
}

// A .zip, a .pdf and a .md are stored (0600 in a 0700 hash folder) and the
// send pastes one [File: <path>] line each after the text.
func TestCEOFileUploadAndSend(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	var paths []string
	for name, data := range map[string][]byte{
		"company-floor-v2.zip": ceoHostileZip(t),
		"pricing.pdf":          []byte("%PDF-1.4\n%fake but enough\n"),
		"notes.md":             []byte("# Notes\n\nShip it.\n"),
	} {
		code, out := ceoFilePost(t, ts.URL+"/p/agents/lead/file", name, data)
		if code != 200 {
			t.Fatalf("%s: %d %v", name, code, out)
		}
		path, _ := out["path"].(string)
		if !filepath.IsAbs(path) || filepath.Base(path) != name || filepath.Dir(filepath.Dir(path)) != ceoFileDir(&proj) {
			t.Errorf("%s stored at %q, want <files dir>/<hash>/%s", name, path, name)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
			t.Errorf("%s: stored bytes differ", name)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
			t.Errorf("%s: mode %v, want 0600", name, info.Mode().Perm())
		}
		if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0700 {
			t.Errorf("%s: hash folder mode %v, want 0700", name, info.Mode().Perm())
		}
		if out["name"] != name || out["size"] != float64(len(data)) || out["url"] != "/p/agents/lead/file/"+filepath.Base(filepath.Dir(path))+"/"+name {
			t.Errorf("%s: answer %v", name, out)
		}
		paths = append(paths, path)
	}
	// The same bytes again are the same file.
	if _, again := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "notes.md", []byte("# Notes\n\nShip it.\n")); again["path"] != paths[len(paths)-1] && again["path"] != paths[0] && again["path"] != paths[1] {
		t.Errorf("second upload at %v", again["path"])
	}

	img := ceoImagePostPath(t, ts.URL)
	body, _ := json.Marshal(map[string]any{"text": "build the floor from the zip", "images": []string{img}, "files": paths})
	if code, out := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", string(body)); code != 200 {
		t.Fatalf("send: %d %v", code, out)
	}
	want := "build the floor from the zip\n\n[Image: " + img + "]\n[File: " + paths[0] + "]\n[File: " + paths[1] + "]\n[File: " + paths[2] + "]"
	if len(st.pastes) != 1 || st.pastes[0] != want {
		t.Errorf("pasted %q\nwant %q", st.pastes, want)
	}
	// Files only is a message too.
	body, _ = json.Marshal(map[string]any{"text": "", "files": paths[:1]})
	if code, out := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", string(body)); code != 200 || st.pastes[1] != "[File: "+paths[0]+"]" {
		t.Errorf("files-only send: %d %v %q", code, out, st.pastes)
	}
}

func ceoImagePostPath(t *testing.T, base string) string {
	t.Helper()
	_, up := ceoImagePost(t, base+"/p/agents/lead/image", "shot.png", ceoTestPNG)
	return up["path"].(string)
}

// A department lead's chat stores into the same project store and its send
// pastes the line into that lead's session.
func TestCEOFileDepartmentChat(t *testing.T) {
	proj := setupDeptLeadProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, out := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "brief.md", []byte("brief"))
	if code != 200 {
		t.Fatalf("upload: %d %v", code, out)
	}
	path := out["path"].(string)
	body, _ := json.Marshal(map[string]any{"text": "read this", "files": []string{path}})
	if code, out := leadChatPost(t, ts.URL+"/p/agents/lead/send?dept=marketing", "application/json", string(body)); code != 200 {
		t.Fatalf("send: %d %v", code, out)
	}
	if st.session != "lead-agents-marketing" || len(st.pastes) != 1 || st.pastes[0] != "read this\n\n[File: "+path+"]" {
		t.Errorf("pasted %q into %q", st.pastes, st.session)
	}
	if code, _ := leadChatPost(t, ts.URL+"/p/agents/lead/send?dept=sales", "application/json", string(body)); code != 404 {
		t.Errorf("unknown department: %d, want 404", code)
	}
}

func TestCEOFileUploadRefusals(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, out := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "big.zip", make([]byte, ceoFileMax+1))
	if code != 413 || out["status"] != "too-large" || !strings.Contains(out["error"].(string), "50 MB") {
		t.Errorf("over the limit: %d %v, want 413 naming 50 MB", code, out)
	}
	code, out = ceoFilePost(t, ts.URL+"/p/agents/lead/file", "empty.txt", nil)
	if code != 400 || out["status"] != "empty" {
		t.Errorf("empty: %d %v, want 400 empty", code, out)
	}
	resp, err := http.Post(ts.URL+"/p/agents/lead/file", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("not multipart: %d, want 400", resp.StatusCode)
	}
	// A body over the whole-request cap (50 MB + 64 KB) trips it before the
	// file part even starts: still 413 naming the limit.
	{
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		mw.WriteField("junk", strings.Repeat("j", ceoFileMax+128<<10))
		fw, _ := mw.CreateFormFile("file", "late.zip")
		fw.Write([]byte("late"))
		mw.Close()
		resp, err := http.Post(ts.URL+"/p/agents/lead/file", mw.FormDataContentType(), &buf)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode != 413 || out["status"] != "too-large" || !strings.Contains(anyString(out["error"]), "50 MB") {
			t.Errorf("over the request cap: %d %v, want 413 naming 50 MB", resp.StatusCode, out)
		}
	}
	// Exactly the limit is fine.
	if code, out := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "edge.bin", make([]byte, ceoFileMax)); code != 200 {
		t.Errorf("at the limit: %d %v", code, out)
	}
	entries, _ := os.ReadDir(ceoFileDir(&proj))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ceoFileTempPrefix) {
			t.Errorf("refused upload left %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("store holds %d entries, want only the one accepted file's folder", len(entries))
	}
}

func TestLeadChatSendFileRefusals(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, up := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "notes.md", []byte("notes"))
	path := up["path"].(string)
	img := ceoImagePostPath(t, ts.URL)
	dir := ceoFileDir(&proj)
	for name, files := range map[string][]string{
		"outside the folder": {filepath.Join(projectRoot(&proj), "workflow.yaml")},
		"not stored":         {filepath.Join(dir, strings.Repeat("ab", 16), "notes.md")},
		"dot-dot":            {filepath.Join(dir, "..", "..", "..", "..", "workflow.yaml")},
		"climbs back in":     {dir + "/" + strings.Repeat("ab", 16) + "/../" + filepath.Base(filepath.Dir(path)) + "/../../files/x"},
		"relative":           {filepath.Base(filepath.Dir(path)) + "/notes.md"},
		"an image":           {img},
		"too many":           {path, path, path, path, path, path},
	} {
		body, _ := json.Marshal(map[string]any{"text": "hi", "files": files})
		if code, out := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", string(body)); code != 400 || out["status"] != "bad-file" {
			t.Errorf("%s: %d %v, want 400 bad-file", name, code, out)
		}
	}
	if len(st.pastes) != 0 {
		t.Errorf("refused sends pasted %q", st.pastes)
	}
}

// An attached .html or .svg downloads as bytes, never as a page on the
// viewer's origin.
func TestCEOFileDownloadIsAttachment(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	for name, data := range map[string]string{
		"page.html": `<html><script>alert(document.cookie)</script></html>`,
		"pic.svg":   `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
	} {
		_, up := ceoFilePost(t, ts.URL+"/p/agents/lead/file", name, []byte(data))
		resp, err := http.Get(ts.URL + up["url"].(string))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		h := resp.Header
		if resp.StatusCode != 200 || string(got) != data {
			t.Errorf("%s: %d %q", name, resp.StatusCode, got)
		}
		if h.Get("Content-Type") != "application/octet-stream" {
			t.Errorf("%s: Content-Type %q", name, h.Get("Content-Type"))
		}
		if h.Get("Content-Disposition") != `attachment; filename=`+name {
			t.Errorf("%s: Content-Disposition %q", name, h.Get("Content-Disposition"))
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s: nosniff %q CSP %q", name, h.Get("X-Content-Type-Options"), h.Get("Content-Security-Policy"))
		}
	}
	for _, bad := range []string{
		"/p/agents/lead/file/" + strings.Repeat("ab", 16) + "/missing.md",
		"/p/agents/lead/file/../../workflow.yaml",
		"/p/agents/lead/file/" + strings.Repeat("ab", 16) + "/..%2F..%2Fworkflow.yaml",
		"/p/agents/lead/file/x",
	} {
		resp, err := http.Get(ts.URL + bad)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("GET %s = %d, want 404", bad, resp.StatusCode)
		}
	}
}

// The viewer never opens a zip: one with ../ entries leaves nothing in the
// project but the zip itself.
func TestCEOFileZipNotExtracted(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	root := projectRoot(&proj)
	parent := filepath.Dir(root)
	before := projectFiles(t, parent)
	data := ceoHostileZip(t)
	_, up := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "../../evil.zip", data)
	path := up["path"].(string)
	if filepath.Base(path) != "evil.zip" || !strings.HasPrefix(path, ceoFileDir(&proj)+string(filepath.Separator)) {
		t.Fatalf("stored at %q", path)
	}
	body, _ := json.Marshal(map[string]any{"text": "unzip", "files": []string{path}})
	leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", string(body))
	st.pane = "> unzip\n  [File: " + path + "]\n⏺ ok\n"
	getBody(t, ts.URL+"/p/agents/lead")
	resp, _ := http.Get(ts.URL + up["url"].(string))
	resp.Body.Close()

	after := projectFiles(t, parent)
	for f := range after {
		if before[f] {
			continue
		}
		if !strings.HasPrefix(filepath.Join(parent, f), ceoFileDir(&proj)) || strings.Contains(f, "escaped") {
			t.Errorf("new file outside the store or unpacked: %s", f)
		}
	}
	for f := range after {
		if strings.Contains(f, "escaped") || strings.HasSuffix(f, "inside.txt") {
			t.Errorf("zip entry unpacked: %s", f)
		}
	}
}

// Upload and download need the owner; the upload refuses a cross-origin
// POST even from the owner.
func TestCEOFileOwnerOnly(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, out := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "notes.md", []byte("x"), strangerHeader, "1")
	if code != 403 || !strings.Contains(out["error"].(string), "/owner") {
		t.Errorf("stranger upload: %d %v, want 403 naming /owner", code, out)
	}
	code, out = ceoFilePost(t, ts.URL+"/p/agents/lead/file", "notes.md", []byte("x"), "Origin", "http://evil.example")
	if code != 403 {
		t.Errorf("cross-origin upload: %d %v, want 403", code, out)
	}
	if entries, _ := os.ReadDir(ceoFileDir(&proj)); len(entries) != 0 {
		t.Errorf("refused uploads stored %d entries", len(entries))
	}
	_, up := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "notes.md", []byte("x"))
	req, _ := http.NewRequest(http.MethodGet, ts.URL+up["url"].(string), nil)
	req.Header.Set(strangerHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("stranger download: %d, want 403", resp.StatusCode)
	}
	// Images keep working without sign-in (class W).
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "shot.png")
	fw.Write(ceoTestPNG)
	mw.Close()
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/p/agents/lead/image", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(strangerHeader, "1")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("stranger image: %d, want 200", resp.StatusCode)
	}
}

// A symlink planted in the store never leads a write or a read out of it.
func TestCEOFileSymlinkRefused(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0600)
	dir := ceoFileDir(&proj)
	os.MkdirAll(dir, 0700)
	data := []byte("planted content")
	// The hash folder this content would go to is a link out.
	_, up := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "probe.txt", data)
	hash := filepath.Base(filepath.Dir(up["path"].(string)))
	os.RemoveAll(filepath.Join(dir, hash))
	if err := os.Symlink(outside, filepath.Join(dir, hash)); err != nil {
		t.Fatal(err)
	}
	if code, out := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "x.txt", data); code == 200 {
		t.Errorf("upload into a linked hash folder: %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(outside, "x.txt")); err == nil {
		t.Error("the upload wrote outside the store")
	}
	resp, err := http.Get(ts.URL + "/p/agents/lead/file/" + hash + "/secret.txt")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("GET through a linked hash folder: %d, want 404", resp.StatusCode)
	}
	body, _ := json.Marshal(map[string]any{"text": "hi", "files": []string{filepath.Join(dir, hash, "secret.txt")}})
	if code, _ := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", string(body)); code != 400 {
		t.Errorf("send through a linked hash folder: %d, want 400", code)
	}
	// A file that is a link is not a stored file either.
	real := strings.Repeat("cd", 16)
	os.MkdirAll(filepath.Join(dir, real), 0700)
	os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, real, "secret.txt"))
	resp, err = http.Get(ts.URL + "/p/agents/lead/file/" + real + "/secret.txt")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("GET a linked file: %d, want 404", resp.StatusCode)
	}
	// The files folder itself a link: nothing is written through it.
	os.RemoveAll(dir)
	os.Symlink(outside, dir)
	if code, _ := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "y.txt", []byte("y")); code == 200 {
		t.Errorf("upload into a linked files folder: %d", code)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Errorf("outside folder has %d entries, want only secret.txt", len(entries))
	}
}

// errAfter is a body that gives n bytes and then fails, as a client that
// drops mid-upload.
type errAfter struct {
	r io.Reader
}

func (e *errAfter) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err == io.EOF {
		return n, errors.New("connection reset")
	}
	return n, err
}

// An upload cut short leaves no temp file; an old temp file left by a crash
// is removed by the next upload.
func TestCEOFileAbortedUploadLeavesNothing(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	srv, err := NewServer([]tracker.Project{proj})
	if err != nil {
		t.Fatal(err)
	}
	h := asOwner(t, srv)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "half.zip")
	fw.Write(bytes.Repeat([]byte("z"), 1<<16))
	// No closing boundary: the body ends mid-part with an error.
	req := httptest.NewRequest(http.MethodPost, "/p/agents/lead/file", &errAfter{r: bytes.NewReader(buf.Bytes())})
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("aborted upload: %d %s, want 400", rec.Code, rec.Body)
	}
	dir := ceoFileDir(&proj)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("aborted upload left %d entries", len(entries))
	}

	os.MkdirAll(dir, 0700)
	stale := filepath.Join(dir, ceoFileTempPrefix+"deadbeef")
	os.WriteFile(stale, []byte("half"), 0600)
	old := time.Now().Add(-2 * ceoFileTempAge)
	os.Chtimes(stale, old, old)
	fresh := filepath.Join(dir, ceoFileTempPrefix+"cafe")
	os.WriteFile(fresh, []byte("in flight"), 0600)
	ts := httptest.NewServer(h)
	defer ts.Close()
	if code, out := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "ok.md", []byte("ok")); code != 200 {
		t.Fatalf("upload: %d %v", code, out)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("an hour-old temp file was not swept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a fresh temp file (an upload in flight) was swept")
	}
}

// The owner's turn shows stored files as download chips and keeps any
// other [File: …] line as text; a wrapped path still matches.
func TestCEOTurnFiles(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, up := ceoFilePost(t, ts.URL+"/p/agents/lead/file", "company-floor-v2.zip", ceoHostileZip(t))
	path, url := up["path"].(string), up["url"].(string)
	cut := len(path) / 2
	pane := "> build it\n  [File: " + path[:cut] + "\n  " + path[cut:] + "]\n  [File: /etc/passwd]\n⏺ On it.\n"
	agentrun.CaptureHistory = func(target string, lines int) (string, error) { return pane, nil }

	code, html := getBody(t, ts.URL+"/p/agents/lead")
	if code != 200 {
		t.Fatalf("page: %d", code)
	}
	for _, want := range []string{
		`data-testid="lead_chat.turn.files"`,
		`<a class="wb-file-chip" href="` + url + `" download="company-floor-v2.zip"`,
		`<span class="wb-file-name">company-floor-v2.zip</span>`,
		`<span class="wb-file-badge" aria-hidden="true">ZIP</span>`,
		`data-file-url="/p/agents/lead/file"`,
		`data-testid="lead_chat.file"`,
		"[File: /etc/passwd]",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(html, `accept="image/png`) {
		t.Error("the file input still takes only images")
	}
	var m LeadChatModel
	resp, err := http.Get(ts.URL + "/p/agents/lead.json")
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if len(m.Turns) == 0 || len(m.Turns[0].Files) != 1 || m.Turns[0].Files[0].Path != path || m.Turns[0].Text[0] != "build it  [File: /etc/passwd]" {
		t.Errorf("lead.json first turn %+v", m.Turns)
	}
}

// A project under a folder with a space still gets its chips: the pane's
// whitespace is removed from the folder as well before comparing.
func TestCEOTurnFilesRootWithSpace(t *testing.T) {
	proj := setupAgentsProject(t)
	proj.WorkDir = filepath.Join(t.TempDir(), "My Projects", "x")
	dir := ceoFileDir(&proj)
	hash := strings.Repeat("ab", 16)
	os.MkdirAll(filepath.Join(dir, hash), 0700)
	os.WriteFile(filepath.Join(dir, hash, "notes.md"), []byte("notes"), 0600)
	path := filepath.Join(dir, hash, "notes.md")

	text, files := ceoTurnFiles(&proj, "/p/agents", "read\n\n[File: "+path+"]")
	if text != "read" || len(files) != 1 || files[0].Path != path || files[0].URL != "/p/agents/lead/file/"+hash+"/notes.md" || files[0].Size != 5 {
		t.Errorf("got %q %+v", text, files)
	}
	// Send compares the exact path, spaces and all.
	if lines, err := ceoFileLines(&proj, []string{path}); err != nil || lines != "[File: "+path+"]" {
		t.Errorf("ceoFileLines = %q, %v", lines, err)
	}
}

func anyString(v any) string { s, _ := v.(string); return s }

func TestCEOFileType(t *testing.T) {
	for name, want := range map[string]string{"floor.zip": "ZIP", "notes.md": "MD", "Makefile": "FILE", "a.markdown": "MARKD", "x.": "FILE"} {
		if got := ceoFileType(name); got != want {
			t.Errorf("ceoFileType(%q) = %q, want %q", name, got, want)
		}
	}
}
