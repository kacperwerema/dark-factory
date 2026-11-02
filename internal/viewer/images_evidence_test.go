package viewer

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// LoadIssues skips issues/_evidence/, but an issue that links a shot kept
// there still shows it: the link rewrites to /files/ and the file serves.
func TestEvidenceFolderLinksStillServe(t *testing.T) {
	proj, tmpDir := setupTestProject(t)
	evDir := filepath.Join(tmpDir, "issues", "_evidence", "floor")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(evDir, "after.png"), []byte("\x89PNG\r\n\x1a\nfake"), 0o644)
	os.WriteFile(filepath.Join(evDir, "README.md"), []byte("# mockup notes, no frontmatter\n"), 0o644)
	os.MkdirAll(filepath.Join(tmpDir, "issues", "Factory"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "issues", "Factory", "floor.md"),
		[]byte("---\ntitle: \"Floor\"\n---\n\n![after](../_evidence/floor/after.png)\n"), 0o644)

	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	var slug string
	for _, is := range issues {
		if strings.HasPrefix(is.Slug, "_evidence") {
			t.Fatalf("evidence file loaded as issue %s", is.Slug)
		}
		if is.Title == "Floor" {
			slug = is.Slug
		}
	}
	if slug == "" {
		t.Fatal("issue Floor not loaded")
	}

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/issue/" + slug + "?classic=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	want := `/p/test-project/files/issues/_evidence/floor/after.png`
	if !strings.Contains(string(body), want) {
		t.Fatalf("issue page has no %s", want)
	}

	resp, err = http.Get(ts.URL + want)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("%s: status %d, want 200", want, resp.StatusCode)
	}
}
