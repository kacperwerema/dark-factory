package viewer

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

func paneFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/ceo-pane/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stubLeadPanes makes the sessions in panes live (any other is off) and
// answers each capture with its pane; a pane "hang" blocks until the
// capture's context ends.
func stubLeadPanes(t *testing.T, panes map[string]string) {
	t.Helper()
	origHas, origCtx, origCap := agentrun.HasSession, agentrun.CaptureHistoryCtx, agentrun.CaptureHistory
	t.Cleanup(func() {
		agentrun.HasSession, agentrun.CaptureHistoryCtx, agentrun.CaptureHistory = origHas, origCtx, origCap
	})
	name := func(target string) string { return strings.TrimSuffix(strings.TrimPrefix(target, "="), ":") }
	agentrun.HasSession = func(target string) bool { _, ok := panes[name(target)]; return ok }
	agentrun.CaptureHistoryCtx = func(ctx context.Context, target string, lines int) (string, error) {
		p := panes[name(target)]
		if p == "hang" {
			<-ctx.Done()
			return "", ctx.Err()
		}
		return p, nil
	}
	agentrun.CaptureHistory = func(target string, lines int) (string, error) { return panes[name(target)], nil }
}

// switcherProjects are two projects: agents with a GTM group holding
// Marketing, and second with no department.
func switcherProjects(t *testing.T) []tracker.Project {
	a := setupAgentsProject(t)
	a.Departments = []tracker.Department{{Name: "GTM"}, {Name: "Marketing", Parent: "GTM"}}
	b := setupAgentsProject(t)
	b.Name, b.Slug = "Second", "second"
	return []tracker.Project{a, b}
}

// switcherRow is the row the testid names, from its <a to its </a>.
func switcherRow(t *testing.T, html, testid string) string {
	t.Helper()
	i := strings.Index(html, `data-testid="`+testid+`"`)
	if i < 0 {
		t.Fatalf("no row %s in:\n%s", testid, html)
	}
	start := strings.LastIndex(html[:i], "<a ")
	end := strings.Index(html[i:], "</a>")
	return html[start : i+end]
}

func rowState(row string) string {
	m := regexp.MustCompile(`data-state="([a-z]+)"`).FindStringSubmatch(row)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestCEOSwitcherListsEveryLeadWithState(t *testing.T) {
	projects := switcherProjects(t)
	stubLeadPanes(t, map[string]string{
		"lead-agents":           paneFixture(t, "working.txt"),
		"lead-agents-gtm":       paneFixture(t, "menu.txt"),
		"lead-agents-marketing": "",
		"lead-second":           "> hi\n\n⏺ Hello.\n",
	})
	ts := newTestServer(t, projects)
	defer ts.Close()

	code, html := getBody(t, ts.URL+"/p/agents/lead?part=switcher&dept=gtm")
	if code != 200 {
		t.Fatalf("switcher: %d %s", code, html)
	}
	for testid, want := range map[string]string{
		"lead_chat.switcher.lead.agents":           leadWorking,
		"lead_chat.switcher.lead.agents.gtm":       leadYou,
		"lead_chat.switcher.lead.agents.marketing": leadUnknown, // live, empty screen
		"lead_chat.switcher.lead.second":           leadIdle,
	} {
		if got := rowState(switcherRow(t, html, testid)); got != want {
			t.Errorf("%s: state %q, want %q", testid, got, want)
		}
	}
	for _, want := range []string{
		`data-testid="lead_chat.switcher.project.agents"`,
		`data-testid="lead_chat.switcher.project.second"`,
		`href="/leads"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("switcher lacks %s", want)
		}
	}
	// The current chat is the GTM lead's, and only it.
	if !strings.Contains(switcherRow(t, html, "lead_chat.switcher.lead.agents.gtm"), `aria-current="page"`) || strings.Count(html, `aria-current="page"`) != 1 {
		t.Error("the GTM lead's row is not the one current row")
	}
	// Marketing nests under GTM.
	if !strings.Contains(switcherRow(t, html, "lead_chat.switcher.lead.agents.marketing"), "--depth: 2") {
		t.Error("Marketing is not nested under its group")
	}

	// The CEO row's count is the project's Needs-you count.
	var ny NeedsYouJSON
	if err := json.Unmarshal([]byte(pageGet(t, ts.URL+"/p/agents/needs-you.json")), &ny); err != nil {
		t.Fatal(err)
	}
	ceo := switcherRow(t, html, "lead_chat.switcher.lead.agents")
	if ny.Count > 0 && !strings.Contains(ceo, ">"+strconv.Itoa(ny.Count)+" on you<") {
		t.Errorf("CEO row %q lacks the Needs-you count %d", ceo, ny.Count)
	}
	if ny.Count == 0 && strings.Contains(ceo, " on you<") {
		t.Errorf("CEO row %q shows a count with nothing waiting", ceo)
	}
}

func TestCEOSwitcherOffLeadsStayOff(t *testing.T) {
	projects := switcherProjects(t)
	stubLeadPanes(t, map[string]string{})
	ts := newTestServer(t, projects)
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/lead?part=switcher")
	for _, id := range []string{"lead_chat.switcher.lead.agents", "lead_chat.switcher.lead.agents.gtm", "lead_chat.switcher.lead.second"} {
		if got := rowState(switcherRow(t, html, id)); got != leadOff {
			t.Errorf("%s: %q, want off", id, got)
		}
	}
	// Rows are links only: nothing in the sheet starts a lead.
	if strings.Contains(html, "data-spawn-lead") || strings.Contains(html, "/lead/spawn") {
		t.Error("the switcher offers to start a lead")
	}
}

func TestCEOSwitcherHungCaptureIsUnknownAndDoesNotPileUp(t *testing.T) {
	orig := leadStateBudget
	leadStateBudget = 300 * time.Millisecond
	t.Cleanup(func() { leadStateBudget = orig })
	projects := switcherProjects(t)[:1]
	stubLeadPanes(t, map[string]string{"lead-agents": "hang", "lead-agents-gtm": paneFixture(t, "working.txt")})
	var inFlight, most atomic.Int32
	hung := agentrun.CaptureHistoryCtx
	agentrun.CaptureHistoryCtx = func(ctx context.Context, target string, lines int) (string, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		return hung(ctx, target, lines)
	}
	ts := newTestServer(t, projects)
	defer ts.Close()

	before := runtime.NumGoroutine()
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, html := getBody(t, ts.URL+"/p/agents/lead?part=switcher")
			if got := rowState(switcherRow(t, html, "lead_chat.switcher.lead.agents")); got != leadUnknown {
				t.Errorf("hung CEO: %q, want unknown", got)
			}
			if got := rowState(switcherRow(t, html, "lead_chat.switcher.lead.agents.gtm")); got != leadWorking {
				t.Errorf("GTM lead: %q, want working", got)
			}
		}()
	}
	wg.Wait()
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("ten opens took %s with a hung capture", took)
	}
	// One capture per lead at most: a second open waits for the running one.
	if m := most.Load(); m > 2 {
		t.Errorf("%d captures ran at once for 2 leads", m)
	}
	// Idle keep-alive connections from the ten clients are not captures.
	ts.CloseClientConnections()
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+4 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+4 {
		t.Errorf("goroutines %d after, %d before: captures piled up", n, before)
	}
}

func TestPaneLeadState(t *testing.T) {
	for name, tc := range map[string]struct{ pane, want string }{
		"working": {paneFixture(t, "working.txt"), leadWorking},
		"menu":    {paneFixture(t, "menu.txt"), leadYou},
		"idle":    {"> hi\n\n⏺ Hello.\n", leadIdle},
		"empty":   {"", leadUnknown},
	} {
		if got := paneLeadState(cleanPane(tc.pane)); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestCEOChatPhoneControls(t *testing.T) {
	proj := setupAgentsProject(t)
	stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	_, html := getBody(t, ts.URL+"/p/agents/lead?dept=")
	for _, want := range []string{
		`data-switch="?part=switcher"`,
		`data-testid="lead_chat.switch"`,
		`data-testid="lead_chat.mic"`,
		`data-testid="lead_chat.mic_lang"`,
		`accept="image/*" capture="environment"`,
		`data-testid="lead_chat.camera"`,
		`data-testid="lead_chat.keys_more"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// The mic is hidden until the page finds the browser's recognizer.
	if !regexp.MustCompile(`data-mic aria-pressed="false" hidden`).MatchString(html) {
		t.Error("the mic is not hidden by default")
	}
}

// phonePhoto is a JPEG the way a phone camera writes one: an EXIF APP1
// segment right after the start marker.
func phonePhoto(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	exif := append([]byte("Exif\x00\x00"), []byte("MM\x00\x2a\x00\x00\x00\x08\x00\x00")...)
	app1 := append([]byte{0xFF, 0xE1, byte((len(exif) + 2) >> 8), byte(len(exif) + 2)}, exif...)
	return append(append(append([]byte(nil), b[:2]...), app1...), b[2:]...)
}

func TestCEOChatPhonePhotoLandsAsImageTurn(t *testing.T) {
	proj := setupAgentsProject(t)
	st := stubLeadChat(t, true)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, up := ceoImagePost(t, ts.URL+"/p/agents/lead/image", "IMG_0042.jpg", phonePhoto(t))
	if code != 200 {
		t.Fatalf("upload: %d %v", code, up)
	}
	path, url := up["path"].(string), up["url"].(string)
	if !strings.HasSuffix(path, ".jpg") {
		t.Fatalf("stored as %s, want a .jpg", path)
	}
	body, _ := json.Marshal(map[string]any{"text": "the hall board", "images": []string{path}})
	if code, out := leadChatPost(t, ts.URL+"/p/agents/lead/send", "application/json", string(body)); code != 200 {
		t.Fatalf("send: %d %v", code, out)
	}
	if len(st.pastes) != 1 || !strings.Contains(st.pastes[0], "[Image: "+path+"]") {
		t.Fatalf("pasted %q, want the image line", st.pastes)
	}
	if testing.Verbose() {
		t.Logf("paste %q", st.pastes[0])
	}
	// The pane echoes the paste: the owner's turn shows the photo.
	// (The TUI shows the paste's blank line as nothing.)
	pane := "> " + strings.ReplaceAll(strings.ReplaceAll(st.pastes[0], "\n\n", "\n"), "\n", "\n  ") + "\n⏺ I see the board.\n"
	agentrun.CaptureHistory = func(string, int) (string, error) { return pane, nil }
	_, html := getBody(t, ts.URL+"/p/agents/lead")
	for _, want := range []string{`data-testid="lead_chat.turn.images"`, `<img src="` + url + `"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the owner's turn lacks %s", want)
		}
	}
}
