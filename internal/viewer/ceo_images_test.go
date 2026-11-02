package viewer

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ceoTestPNG is a 1×1 PNG.
var ceoTestPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xa75\x81\x84\x00\x00\x00\x00IEND\xaeB`\x82")

func ceoImagePost(t *testing.T, url, name string, data []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(data)
	mw.Close()
	resp, err := http.Post(url, mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestCEOImageUpload(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, out := ceoImagePost(t, ts.URL+"/p/agents/lead/image", "shot.png", ceoTestPNG)
	if code != 200 {
		t.Fatalf("upload: %d %v", code, out)
	}
	path, _ := out["path"].(string)
	if !filepath.IsAbs(path) || filepath.Dir(path) != ceoImageDir(&proj) || !strings.HasSuffix(path, ".png") {
		t.Errorf("path %q not an absolute .png in %s", path, ceoImageDir(&proj))
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, ceoTestPNG) {
		t.Error("stored image differs from the upload")
	}
	// The same image again is the same file.
	if _, again := ceoImagePost(t, ts.URL+"/p/agents/lead/image", "other.png", ceoTestPNG); again["path"] != path {
		t.Errorf("second upload at %v, want %s", again["path"], path)
	}
	// The thumbnail URL serves it through /files/.
	url, _ := out["url"].(string)
	resp, err := http.Get(ts.URL + url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("thumbnail %s: %d %s", url, resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestCEOImageUploadRefusals(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	big := append(append([]byte{}, ceoTestPNG...), make([]byte, ceoImageMax)...)
	for _, c := range []struct {
		name   string
		data   []byte
		code   int
		status string
	}{
		{"notes.txt", []byte("just text"), 415, "not-image"},
		// The type is read from the content, not the name.
		{"fake.png", []byte("just text"), 415, "not-image"},
		{"x.svg", svg, 415, "not-image"},
		{"big.png", big, 413, "too-large"},
	} {
		code, out := ceoImagePost(t, ts.URL+"/p/agents/lead/image", c.name, c.data)
		if code != c.code || out["status"] != c.status {
			t.Errorf("%s: %d %v, want %d %s", c.name, code, out, c.code, c.status)
		}
	}
	if entries, _ := os.ReadDir(ceoImageDir(&proj)); len(entries) != 0 {
		t.Errorf("refused uploads stored %d files", len(entries))
	}
}

func TestLeadChatSendImages(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, up := ceoImagePost(t, ts.URL+"/p/agents/lead/image", "shot.png", ceoTestPNG)
	path := up["path"].(string)
	body, _ := json.Marshal(map[string]any{"text": "look at this", "images": []string{path}})
	if code, out := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", string(body)); code != 200 {
		t.Fatalf("send: %d %v", code, out)
	}
	// Images only is a message too.
	body, _ = json.Marshal(map[string]any{"text": "", "images": []string{path}})
	if code, out := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", string(body)); code != 200 {
		t.Fatalf("images-only send: %d %v", code, out)
	}
	want := []string{"look at this\n\n[Image: " + path + "]", "[Image: " + path + "]"}
	if len(st.pastes) != 2 || st.pastes[0] != want[0] || st.pastes[1] != want[1] {
		t.Errorf("pastes %q, want %q", st.pastes, want)
	}
}

func TestLeadChatSendImageRefusals(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, up := ceoImagePost(t, ts.URL+"/p/agents/lead/image", "shot.png", ceoTestPNG)
	path := up["path"].(string)
	outside := filepath.Join(projectRoot(&proj), "workflow.yaml")
	missing := filepath.Join(ceoImageDir(&proj), strings.Repeat("ab", 16)+".png")
	sneaky := filepath.Join(ceoImageDir(&proj), "..", "..", "..", "..", "workflow.yaml")
	for name, images := range map[string][]string{
		"outside the folder": {outside},
		"not stored":         {missing},
		"dot-dot":            {sneaky},
		"relative":           {filepath.Base(path)},
		"too many":           {path, path, path, path, path, path},
	} {
		body, _ := json.Marshal(map[string]any{"text": "hi", "images": images})
		if code, out := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", string(body)); code != 400 || out["status"] != "bad-image" {
			t.Errorf("%s: %d %v, want 400 bad-image", name, code, out)
		}
	}
	if len(st.pastes) != 0 {
		t.Errorf("refused sends pasted %q", st.pastes)
	}
}

func TestCEOTurnImages(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	_, up := ceoImagePost(t, ts.URL+"/p/agents/lead/image", "shot.png", ceoTestPNG)
	path, url := up["path"].(string), up["url"].(string)
	// The TUI wraps a long path: the continuation is indented and joined
	// with a space (paneParagraphs).
	cut := len(path) / 2
	pane := "> look at this\n  [Image: " + path[:cut] + "\n  " + path[cut:] + "]\n  [Image: /etc/passwd]\n⏺ I see it.\n"
	agentrun.CaptureHistory = func(target string, lines int) (string, error) { return pane, nil }

	code, html := getBody(t, ts.URL+"/p/agents/lead")
	if code != 200 {
		t.Fatalf("page: %d", code)
	}
	for _, want := range []string{
		`data-testid="lead_chat.turn.images"`,
		`<img src="` + url + `"`,
		`data-testid="lead_chat.attach_image"`,
		`data-image-url="/p/agents/lead/image"`,
		// A path that isn't a chat image stays text.
		"[Image: /etc/passwd]",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// The Terminal view keeps the raw pane; the turn's bubble drops the line.
	if !strings.Contains(html, `<div class="wb-turn-bubble">look at this  [Image: /etc/passwd]</div>`) {
		t.Error("the turn's bubble is not the text without the chat image's line")
	}

	var m LeadChatModel
	resp, err := http.Get(ts.URL + "/p/agents/lead.json")
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if len(m.Turns) == 0 || len(m.Turns[0].Images) != 1 || m.Turns[0].Images[0].Path != path || m.Turns[0].Text[0] != "look at this  [Image: /etc/passwd]" {
		t.Errorf("lead.json first turn %+v", m.Turns)
	}
}
