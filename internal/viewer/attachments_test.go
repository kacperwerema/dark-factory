package viewer

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// issue-cli attach saves through tracker.SaveAttachment and prints
// tracker.AttachmentMarkdown: that link, in an issue body, renders as an
// image on the detail page and the viewer serves the file it points at.
func TestAttachedLinkRendersOnTheDetailPage(t *testing.T) {
	srv, proj, _ := secServer(t)
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	name, err := tracker.SaveAttachment(proj.IssueDir, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj.IssueDir, "attachments", name)); err != nil {
		t.Fatalf("not in the upload route's folder: %v", err)
	}
	link := tracker.AttachmentMarkdown(proj.Slug, name, "board shot")
	os.WriteFile(filepath.Join(proj.IssueDir, "shot.md"), []byte("---\ntitle: \"Shot probe\"\nstatus: \"backlog\"\n---\n\n## Evidence\n\n"+link+"\n"), 0o644)

	url := "/p/" + proj.Slug + "/attachments/" + name
	for _, path := range []string{"/p/" + proj.Slug + "/issue/shot-probe", "/p/" + proj.Slug + "/issue/shot-probe?classic=1"} {
		rec := secDo(t, srv, "GET", path, "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		if want := `<img src="` + url + `" alt="board shot"`; !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET %s: no %s", path, want)
		}
	}
	got := secDo(t, srv, "GET", url, "", nil)
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "image/png" || !bytes.Equal(got.Body.Bytes(), buf.Bytes()) {
		t.Errorf("GET %s = %d %q", url, got.Code, got.Header().Get("Content-Type"))
	}
}
