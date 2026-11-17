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

// The Evidence tab lists the stills issue-cli evidence add saved in the
// issue's _evidence/<slug>/ folder, each with its step, served through
// /files/; with none the card is left out.
func TestEvidenceTabShowsStills(t *testing.T) {
	proj, tmp := setupTestProject(t)
	proj.WorkDir = tmp
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	base := ts.URL + "/p/test-project/issue/bug-in-login?tab=evidence"

	if _, ev := getBody(t, base); strings.Contains(ev, `data-testid="evidence.stills"`) {
		t.Error("the Stills card shows with no stills")
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.SaveEvidence(proj.IssueDir, "bug-in-login", "TP1-after.png", buf.Bytes(), false); err != nil {
		t.Fatal(err)
	}
	// A save's temp file is not a still.
	os.WriteFile(filepath.Join(tracker.EvidenceDir(proj.IssueDir, "bug-in-login"), ".save-1"), []byte("x"), 0o644)

	code, ev := getBody(t, base)
	if code != 200 {
		t.Fatalf("evidence tab: %d", code)
	}
	src := "/p/test-project/files/issues/_evidence/bug-in-login/TP1-after.png"
	for _, want := range []string{
		`data-testid="evidence.stills"`, "Stills · 1", `data-testid="evidence.still.0"`,
		`src="` + src + `"`, `<span class="chip ev-mono">TP1</span>`, ">issues/_evidence/bug-in-login/</span>",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence tab: missing %s", want)
		}
	}
	resp, err := http.Get(ts.URL + src)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET %s = %d, want 200", src, resp.StatusCode)
	}
}
