package viewer

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// hallEditRepo makes the project's folder a git repository on main with
// its workflow file committed, the user's git config kept out.
func hallEditRepo(t *testing.T, p tracker.Project) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Owner")
	t.Setenv("GIT_AUTHOR_EMAIL", "owner@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Owner")
	t.Setenv("GIT_COMMITTER_EMAIL", "owner@example.com")
	dir := filepath.Dir(p.WorkflowFile)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", filepath.Base(p.WorkflowFile)}, {"commit", "-q", "-m", "start"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func hallEditServer(t *testing.T, p tracker.Project) string {
	t.Helper()
	stubAgentsGit(t)
	stubLeadPane(t, "")
	withMockTmuxSessions(t, nil)
	ts := newTestServer(t, []tracker.Project{p})
	t.Cleanup(ts.Close)
	return ts.URL
}

var (
	formTemplate = regexp.MustCompile(`(?s)<template\b.*?</template>`)
	formField    = regexp.MustCompile(`(?s)<input([^>]*)>|<textarea[^>]*\bname="([^"]+)"[^>]*>\n?(.*?)</textarea>|<select[^>]*\bname="([^"]+)"[^>]*>(.*?)</select>`)
	formAttr     = regexp.MustCompile(`\b(name|value|type)="([^"]*)"`)
	formSelected = regexp.MustCompile(`<option value="([^"]*)" selected>`)
)

// formValues is what the page's form posts as rendered: inputs (checked
// boxes only), textareas and selected options in document order, the +
// Add row template left out.
func formValues(page string) url.Values {
	v := url.Values{}
	page = formTemplate.ReplaceAllString(page, "")
	for _, m := range formField.FindAllStringSubmatch(page, -1) {
		switch {
		case m[1] != "" || strings.HasPrefix(m[0], "<input"):
			attrs := map[string]string{}
			for _, a := range formAttr.FindAllStringSubmatch(m[1], -1) {
				attrs[a[1]] = html.UnescapeString(a[2])
			}
			if attrs["name"] == "" || (attrs["type"] == "checkbox" && !strings.Contains(m[1], " checked")) {
				continue
			}
			val := attrs["value"]
			v.Add(attrs["name"], val)
		case m[2] != "":
			v.Add(m[2], html.UnescapeString(m[3]))
		case m[4] != "":
			if s := formSelected.FindStringSubmatch(m[5]); s != nil {
				v.Add(m[4], html.UnescapeString(s[1]))
			}
		}
	}
	return v
}

func hallPost(t *testing.T, u string, v url.Values) (int, string, *http.Response) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm(u, v)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(strings.Builder)
	_, _ = io.Copy(b, resp.Body)
	return resp.StatusCode, b.String(), resp
}

func TestHallEditFormAndSave(t *testing.T) {
	p := hallPartProject(t)
	dir := hallEditRepo(t, p)
	base := hallEditServer(t, p)

	// The station page has Edit.
	_, part := getBody(t, base+"/p/agents/hall/station/testing")
	if !strings.Contains(part, `href="/p/agents/hall/station/testing/edit"`) || !strings.Contains(part, `data-testid="hall.part.edit"`) {
		t.Fatalf("no Edit on the station page:\n%s", part)
	}

	code, page := getBody(t, base+"/p/agents/hall/station/testing/edit")
	if code != http.StatusOK {
		t.Fatalf("edit form %d:\n%s", code, page)
	}
	for _, want := range []string{
		`data-testid="hall.edit"`, `in <span class="wb-mono-inline">workflow.yaml</span>`,
		`name="e0-gate" value="on" checked`, // the gate into testing, on
		`name="e0-type"`, `section_checkboxes_checked Implementation`,
		`systems.UI.transitions: in progress → testing`, // read-only overlay
	} {
		if !strings.Contains(page, want) {
			t.Errorf("edit form lacks %q", want)
		}
	}
	// The gate is the switch, not a row.
	if strings.Count(page, `<option value="require_human_approval" selected>`) != 0 {
		t.Error("the gate into testing shows as an action row")
	}

	// Gate off: the preview shows the diff, Confirm, and changes nothing yet.
	v := formValues(page)
	v.Del("e0-gate")
	before, _ := os.ReadFile(p.WorkflowFile)
	code, pv, _ := hallPost(t, base+"/p/agents/hall/station/testing/edit/preview", v)
	if code != http.StatusOK || !strings.Contains(pv, `<span class="wb-diff-del">-      - type: require_human_approval</span>`) ||
		!strings.Contains(pv, `data-testid="hall.edit.confirm"`) || !strings.Contains(pv, "gate into testing removed") {
		t.Fatalf("preview %d:\n%s", code, pv)
	}
	if now, _ := os.ReadFile(p.WorkflowFile); string(now) != string(before) {
		t.Fatal("preview wrote the file")
	}

	// Confirm without a reason is refused.
	code, body, _ := hallPost(t, base+"/p/agents/hall/station/testing/edit/save", v)
	if code != http.StatusBadRequest || !strings.Contains(body, "say why in the reason box") {
		t.Errorf("save without reason %d:\n%s", code, body)
	}

	v.Set("reason", "testing needs no stamp from me")
	code, _, resp := hallPost(t, base+"/p/agents/hall/station/testing/edit/save", v)
	if code != http.StatusSeeOther {
		t.Fatalf("save %d", code)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/p/agents/hall/station/testing?saved=") {
		t.Fatalf("redirect %q", loc)
	}
	out, _ := exec.Command("git", "-C", dir, "log", "-1", "--format=%s%n%b").Output()
	if msg := string(out); !strings.Contains(msg, "workflow(workflow.yaml): testing: gate into testing removed") || !strings.Contains(msg, `Owner: "testing needs no stamp from me"`) {
		t.Errorf("commit message:\n%s", msg)
	}
	_, part = getBody(t, base+loc)
	if !strings.Contains(part, `data-testid="hall.part.saved"`) {
		t.Error("the station page doesn't say it was saved")
	}
	if wf, _ := tracker.LoadWorkflow(p.WorkflowFile); wf.RequiredHumanApproval("in progress", "testing") != "" {
		t.Error("the transition into testing still needs approval")
	}

	// The old form's hash is stale now.
	code, body, _ = hallPost(t, base+"/p/agents/hall/station/testing/edit/preview", v)
	if code != http.StatusConflict || !strings.Contains(body, "changed since the editor opened it") {
		t.Errorf("stale preview %d:\n%s", code, body)
	}
}

func TestHallEditRefusesBadActions(t *testing.T) {
	p := hallPartProject(t)
	base := hallEditServer(t, p)
	_, page := getBody(t, base+"/p/agents/hall/station/in-progress/edit")
	v := formValues(page)
	types := v["e0-type"]
	types[0] = "launch_rocket"
	v["e0-type"] = types
	code, pv, _ := hallPost(t, base+"/p/agents/hall/station/in-progress/edit/preview", v)
	if code != http.StatusOK || !strings.Contains(pv, `unknown action type &#34;launch_rocket&#34;`) || strings.Contains(pv, `data-testid="hall.edit.confirm"`) {
		t.Errorf("bad action preview %d:\n%s", code, pv)
	}
	// The unknown type stays selected, so the form shows what was sent.
	if !strings.Contains(pv, `<option value="launch_rocket" selected>`) {
		t.Error("the unknown type was dropped from the form")
	}
}

func TestHallEditYOLONote(t *testing.T) {
	p := hallPartProject(t)
	now := time.Now()
	if err := yolo.TurnOn(p.WorkDir, p.Slug, yolo.State{Until: now.Add(time.Hour), Words: "go"}, now); err != nil {
		t.Fatal(err)
	}
	base := hallEditServer(t, p)
	_, page := getBody(t, base+"/p/agents/hall/station/testing/edit")
	v := formValues(page)
	v.Del("e0-gate")
	_, pv, _ := hallPost(t, base+"/p/agents/hall/station/testing/edit/preview", v)
	if !strings.Contains(pv, `data-testid="hall.edit.yolo"`) || !strings.Contains(pv, "gate into testing removed. YOLO's gate list changes with it.") {
		t.Errorf("no YOLO note:\n%s", pv)
	}
}

// The form posts back exactly what the file holds: on this repo's own
// workflow.yaml, every station's form submitted untouched changes nothing.
func TestHallEditFormRoundTripsTheRealWorkflow(t *testing.T) {
	p := hallPartProject(t)
	raw, err := os.ReadFile(repoPath("workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p.WorkflowFile, raw, 0o644)
	base := hallEditServer(t, p)
	for _, st := range p.LoadWorkflow().Statuses {
		key := hallPartKey(st.Name)
		code, page := getBody(t, base+"/p/agents/hall/station/"+key+"/edit")
		if code != http.StatusOK {
			t.Fatalf("%s: %d", st.Name, code)
		}
		code, pv, _ := hallPost(t, base+"/p/agents/hall/station/"+key+"/edit/preview", formValues(page))
		if code != http.StatusOK || !strings.Contains(pv, "The edit changes nothing in") {
			t.Errorf("%s: an untouched form is a change (%d):\n%s", st.Name, code, between(pv, `data-testid="hall.edit.preview"`, "</section>"))
		}
	}
}

func between(s, from, to string) string {
	if i := strings.Index(s, from); i >= 0 {
		s = s[i:]
		if j := strings.Index(s, to); j >= 0 {
			return s[:j]
		}
	}
	return s
}

func TestHallEditCrossOriginRefused(t *testing.T) {
	p := hallPartProject(t)
	base := hallEditServer(t, p)
	for _, action := range []string{"preview", "save"} {
		req, _ := http.NewRequest("POST", base+"/p/agents/hall/station/testing/edit/"+action, strings.NewReader("reason=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://evil.example")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s cross-origin = %d, want 403", action, resp.StatusCode)
		}
	}
}

func TestHallEditNoFile(t *testing.T) {
	p := hallPartProject(t)
	os.Remove(p.WorkflowFile)
	// No local workflow.yaml to fall back on either.
	t.Chdir(t.TempDir())
	base := hallEditServer(t, p)
	_, part := getBody(t, base+"/p/agents/hall/station/testing")
	if strings.Contains(part, `data-testid="hall.part.edit"`) {
		t.Error("Edit shown without a workflow file")
	}
	if code, _ := getBody(t, base+"/p/agents/hall/station/testing/edit"); code != http.StatusNotFound {
		t.Errorf("edit without a file = %d", code)
	}
}
