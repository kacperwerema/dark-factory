package viewer

// Regression tests for the security review's findings (docs/API/security.md).
// Each test names the barrier it keeps; scripts/security-check.sh runs the
// same attacks against a live sandbox viewer.

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func secServer(t *testing.T) (*Server, tracker.Project, string) {
	t.Helper()
	proj, root := setupTestProject(t)
	srv, err := NewServer([]tracker.Project{proj})
	if err != nil {
		t.Fatal(err)
	}
	return srv, proj, root
}

func secDo(t *testing.T, srv *Server, method, path, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Host = "localhost"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

// Issue create: system is one folder name, and create never overwrites.
func TestSecurity_CreateIssueStaysInIssuesDir(t *testing.T) {
	srv, proj, root := secServer(t)
	for _, system := range []string{"../../escape", "/tmp/abs", "a/b", `a\b`, "..", ".hidden", "x\ny"} {
		rec := secDo(t, srv, "POST", "/p/test-project/issues/create", "application/json",
			[]byte(`{"title":"escape probe","system":`+secJSONString(system)+`}`))
		if rec.Code < 400 {
			t.Errorf("system %q = %d %s, want refused", system, rec.Code, rec.Body)
		}
	}
	var stray []string
	filepath.Walk(filepath.Dir(root), func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.Contains(filepath.Base(p), "escape-probe") {
			stray = append(stray, p)
		}
		return nil
	})
	if len(stray) > 0 {
		t.Errorf("files written: %v", stray)
	}

	before, _ := os.ReadFile(filepath.Join(proj.IssueDir, "bug-in-login.md"))
	rec := secDo(t, srv, "POST", "/p/test-project/issues/create", "application/json", []byte(`{"title":"Bug in login","body":"REPLACED"}`))
	if rec.Code < 400 {
		t.Errorf("create over an existing issue = %d, want refused", rec.Code)
	}
	if after, _ := os.ReadFile(filepath.Join(proj.IssueDir, "bug-in-login.md")); !bytes.Equal(before, after) {
		t.Error("an existing issue was overwritten by create")
	}
}

func secJSONString(s string) string {
	b := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
	return `"` + b + `"`
}

// The agent named in a request is the first word of a shell command line:
// shell text is refused before anything starts.
func TestSecurity_AgentFromRequestIsACommandName(t *testing.T) {
	srv, _, _ := secServer(t)
	evil := `{"agent":"touch /tmp/pwn;claude","force":true}`
	for _, path := range []string{
		"/p/test-project/retros/review",
		"/p/test-project/issue/bug-in-login/dispatch",
		"/p/test-project/lead/spawn",
	} {
		rec := secDo(t, srv, "POST", path, "application/json", []byte(evil))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "refused") {
			t.Errorf("POST %s with a shell agent = %d %q, want 400", path, rec.Code, rec.Body)
		}
	}
	for agent, ok := range map[string]bool{"claude": true, "codex": true, "my-agent_2": true, "touch x;claude": false, "$(id)": false, "-rf": false, "": false, "a b": false} {
		if agentrun.ValidAgentType(agent) != ok {
			t.Errorf("ValidAgentType(%q) = %v", agent, !ok)
		}
	}
	got := agentrun.LaunchCommandWith(nil, "x;id", "s", "/p/$(id)/prompt.txt", nil)
	if got != `'x;id' "$(cat '/p/$(id)/prompt.txt')"` {
		t.Errorf("launch line quotes nothing: %s", got)
	}
}

func TestSecurity_AgentSessionNameIsTmuxAndShellSafe(t *testing.T) {
	for _, slug := range []string{"x$(id)/a", "a:b.c", "a*b?c", "UI/Fix it", `q"'`} {
		if name := tracker.AgentSessionName(slug); !regexp.MustCompile(`^agent-[a-z0-9_-]+$`).MatchString(name) {
			t.Errorf("AgentSessionName(%q) = %q", slug, name)
		}
	}
	if got := tracker.AgentSessionName("ui/fix-it"); got != "agent-ui-fix-it" {
		t.Errorf("plain slug changed: %q", got)
	}
}

// An upload is saved under the sniffed image type's extension, and
// /attachments/ serves only those files, never a listing, symlink or HTML.
func TestSecurity_UploadCannotServeHTML(t *testing.T) {
	srv, proj, root := secServer(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "x.html")
	fw.Write([]byte("GIF89a<script>alert(1)</script>"))
	mw.Close()
	rec := secDo(t, srv, "POST", "/p/test-project/upload", mw.FormDataContentType(), body.Bytes())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `.gif"`) {
		t.Fatalf("upload = %d %s, want saved as .gif", rec.Code, rec.Body)
	}
	url := regexp.MustCompile(`"url":"([^"]+)"`).FindStringSubmatch(rec.Body.String())[1]
	got := secDo(t, srv, "GET", url, "", nil)
	if ct := got.Header().Get("Content-Type"); got.Code != http.StatusOK || ct != "image/gif" {
		t.Errorf("GET %s = %d %q", url, got.Code, ct)
	}
	if !strings.Contains(got.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("attachment CSP = %q", got.Header().Get("Content-Security-Policy"))
	}

	dir := filepath.Join(proj.IssueDir, "attachments")
	secret := filepath.Join(root, "secret.txt")
	os.WriteFile(secret, []byte("SECRET"), 0644)
	os.Symlink(secret, filepath.Join(dir, "0123456789abcdef0123456789abcdef.png"))
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("<script>1</script>"), 0644)
	for _, p := range []string{
		"/p/test-project/attachments/",
		"/p/test-project/attachments/0123456789abcdef0123456789abcdef.png",
		"/p/test-project/attachments/page.html",
		"/p/test-project/attachments/..%2f..%2fsecret.txt",
	} {
		if rec := secDo(t, srv, "GET", p, "", nil); rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "SECRET") {
			t.Errorf("GET %s = %d %q, want refused", p, rec.Code, rec.Body)
		}
	}
}

// Issue bodies come from agents and GitHub: raw HTML and javascript: links
// never reach the page; plain HTML comments (the data marker) do.
func TestSecurity_IssueBodyRendersNoScript(t *testing.T) {
	srv, proj, _ := secServer(t)
	body := "hi <img src=x onerror=alert(1)>\n\n<script>alert(2)</script>\n\n[l](javascript:alert(3))\n\n<!-- x --><script>alert(4)</script>\n\n<!-- note to agents -->\n"
	os.WriteFile(filepath.Join(proj.IssueDir, "xss.md"), []byte("---\ntitle: \"Xss probe\"\nstatus: \"backlog\"\n---\n\n"+body), 0644)
	for _, path := range []string{"/p/test-project/issue/xss-probe", "/p/test-project/issue/xss-probe?classic=1"} {
		rec := secDo(t, srv, "GET", path, "", nil)
		html := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		// The raw body may show escaped (the editor's textarea); live markup
		// may not.
		for _, bad := range []string{"<img src=x onerror", `<img src="x" onerror`, "<script>alert(2)", `href="javascript:`, "<script>alert(4)"} {
			if strings.Contains(html, bad) {
				t.Errorf("%s renders %q", path, bad)
			}
		}
	}
	for s, want := range map[string]bool{
		"<!-- data statuses=a,b -->": true, "<!-- x -->\n": true,
		"<!-- x --><script>": false, "<!-- a --!> <script>": false, "<!--><script>": false, "<!---><b>": false, "<b>": false,
	} {
		if tracker.SafeHTMLComment(s) != want {
			t.Errorf("SafeHTMLComment(%q) = %v", s, !want)
		}
	}
}

// An approval's status is quoted to the agent's session: only a status of
// the workflow is taken.
func TestSecurity_ApproveTakesOnlyAWorkflowStatus(t *testing.T) {
	srv, _, _ := secServer(t)
	token, _ := srv.owner.newSession()
	req := httptest.NewRequest("POST", "/p/test-project/issue/bug-in-login/approve", strings.NewReader(`{"status":"done\u001b[201~\nrm -rf ~"}`))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: ownerCookie, Value: token})
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("approve with a crafted status = %d %s, want 400", rec.Code, rec.Body)
	}
}

// Text the page writes with innerHTML from server data is escaped first.
func TestSecurity_TemplatesEscapeDispatchSteps(t *testing.T) {
	for _, f := range []string{"templates/detail.html", "templates/board.html", "templates/_project_actions.html", "static/auto-refresh.js"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"+ step.name\n", "(' + step.detail + ')", "Failed: ' + err.message", "' + iss.assignee"} {
			if strings.Contains(string(data), bad) {
				t.Errorf("%s puts %q into HTML unescaped", f, strings.TrimSpace(bad))
			}
		}
	}
}
