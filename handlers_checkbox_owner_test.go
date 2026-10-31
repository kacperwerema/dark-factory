package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// ownerBoxIssue has a plain box (line 2), a Human Testing box (line 5) and
// a box marked "(owner)" (line 6).
const ownerBoxBody = "## Design\n- [ ] plain\n\n## Human Testing\n- [ ] looked\n- [ ] glows (owner)"

func writeOwnerBoxIssue(t *testing.T, proj tracker.Project) *tracker.Issue {
	t.Helper()
	file := "---\ntitle: \"Boxes below code\"\nstatus: \"human-testing\"\n---\n\n" + ownerBoxBody + "\n"
	if err := os.WriteFile(filepath.Join(proj.IssueDir, "boxes.md"), []byte(file), 0644); err != nil {
		t.Fatal(err)
	}
	return loadTestIssue(t, proj, "boxes-below-code")
}

// postAs posts JSON to the issue's path; browser adds the headers the
// owner's browser sends from the viewer's own page.
func postAs(t *testing.T, url string, browser bool, payload map[string]interface{}) (int, map[string]string) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if browser {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var data map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&data)
	return resp.StatusCode, data
}

// The viewer's toggle and body save tick an owner box only for the owner's
// browser; curl or an agent's script gets 403 and nothing is written.
func TestViewerTicksOwnerBoxesOnlyFromTheOwnersBrowser(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	issue := writeOwnerBoxIssue(t, proj)
	toggle := ts.URL + "/p/test-project/issue/boxes-below-code/checkbox"
	update := ts.URL + "/p/test-project/issue/boxes-below-code"
	hash := tracker.BodyHash(issue.BodyRaw)

	for _, line := range []int{5, 6} {
		code, data := postAs(t, toggle, false, map[string]interface{}{"line": line, "checked": true, "base_body_hash": hash})
		if code != http.StatusForbidden || !strings.Contains(data["error"], "only the owner ticks an owner box") {
			t.Errorf("toggle line %d from a script: %d %v, want 403", line, code, data)
		}
	}
	ticked := strings.Replace(issue.BodyRaw, "- [ ] looked", "- [x] looked", 1)
	if code, data := postAs(t, update, false, map[string]interface{}{"body": ticked, "base_body_hash": hash}); code != http.StatusForbidden {
		t.Errorf("body save ticking HT1 from a script: %d %v, want 403", code, data)
	}
	if got := loadTestIssue(t, proj, "boxes-below-code").BodyRaw; got != issue.BodyRaw {
		t.Fatalf("a refused request wrote the file:\n%s", got)
	}

	// A plain box still toggles from anywhere.
	code, data := postAs(t, toggle, false, map[string]interface{}{"line": 2, "checked": true, "base_body_hash": hash})
	if code != http.StatusOK {
		t.Fatalf("plain box from a script: %d %v", code, data)
	}
	hash = data["body_hash"]

	// The owner's browser ticks them.
	code, data = postAs(t, toggle, true, map[string]interface{}{"line": 5, "checked": true, "base_body_hash": hash})
	if code != http.StatusOK {
		t.Fatalf("the owner's browser ticking HT1: %d %v", code, data)
	}
	ticked = strings.Replace(data["body"], "- [ ] glows", "- [x] glows", 1)
	if code, data := postAs(t, update, true, map[string]interface{}{"body": ticked, "base_body_hash": data["body_hash"]}); code != http.StatusOK {
		t.Fatalf("the owner's browser saving a body that ticks the (owner) box: %d %v", code, data)
	}
	if got := loadTestIssue(t, proj, "boxes-below-code").BodyRaw; !strings.Contains(got, "- [x] looked\n- [x] glows (owner)") {
		t.Fatalf("after the owner's ticks the body is:\n%s", got)
	}
}
