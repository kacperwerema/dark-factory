package viewer

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Report's Auto-start card: absent while off; on, it shows the owner's
// words, the verdict and the last start; Pause, Resume and Turn off change
// the state.
func TestAutostartCard(t *testing.T) {
	proj := setupYOLOProject(t)
	withMockTmuxSessions(t, nil)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	orig := autostart.ReadMemory
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 31, CanStart: true} }
	origLoad := autostart.ReadLoad
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 4.28, Avg5: 4.28, Cores: 14} }
	t.Cleanup(func() { autostart.ReadMemory, autostart.ReadLoad = orig, origLoad })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/yp/status")
	if strings.Contains(page, `data-testid="status.autostart"`) {
		t.Fatal("Auto-start card drawn while off")
	}

	now := time.Now()
	if err := autostart.TurnOn(proj.WorkDir, proj.Slug, autostart.State{By: "lead-yp", How: "in the lead chat", Words: "start a job under 50%"}, now); err != nil {
		t.Fatal(err)
	}
	if err := autostart.WithBudget(autostart.SharedDir(), func(*autostart.Start) (*autostart.Start, error) {
		return &autostart.Start{Project: "golf", Slug: "g/x", At: now.Add(-time.Hour), Memory: 22}, nil
	}); err != nil {
		t.Fatal(err)
	}
	page = pageGet(t, ts.URL+"/p/yp/status")
	for _, want := range []string{
		`data-testid="status.autostart"`, "on below 50%", "“start a job under 50%”", "turned on by lead-yp in the lead chat",
		"start shop (memory 31% &lt; 50%, load 4.3/4.3 on 14 cores (max 14), 0/2 workers)", `data-testid="status.autostart.load"`, "CPU: load 4.3/4.3 on 14 cores (max 14)", "Last start on this machine: golf g/x", `data-testid="status.autostart.pause"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("Auto-start card lacks %q", want)
		}
	}
	if board := pageGet(t, ts.URL+"/p/yp/board?classic=1"); !strings.Contains(board, `data-testid="nav.autostart"`) || !strings.Contains(board, "on below 50% · cap 2") {
		t.Error("sidebar lacks the Auto-start line")
	}
	md := pageGet(t, ts.URL+"/p/yp/status.md")
	if !strings.Contains(md, `Auto-start: on below 50% memory, cap 2 (owner: "start a job under 50%") · load 4.3/4.3 on 14 cores (max 14) · now: start shop`) {
		t.Errorf("status.md lacks the Auto-start line:\n%s", md)
	}

	post := func(action string) {
		t.Helper()
		resp, err := http.Post(ts.URL+"/p/yp/autostart/"+action, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", action, resp.StatusCode, body)
		}
	}
	post("pause")
	page = pageGet(t, ts.URL+"/p/yp/status")
	for _, want := range []string{">paused<", "hold: auto-start is paused (paused by the owner (viewer))", `data-testid="status.autostart.resume"`} {
		if !strings.Contains(page, want) {
			t.Errorf("paused card lacks %q", want)
		}
	}
	post("resume")
	if st, _ := autostart.Load(proj.WorkDir, proj.Slug); !st.On || st.Paused {
		t.Fatalf("after resume: %+v", st)
	}
	post("off")
	if st, _ := autostart.Load(proj.WorkDir, proj.Slug); st.On || st.EndReason != "turned off by the owner (viewer)" {
		t.Fatalf("after off: %+v", st)
	}
	if strings.Contains(pageGet(t, ts.URL+"/p/yp/status"), `data-testid="status.autostart"`) {
		t.Error("Auto-start card drawn after off")
	}
}

// The Auto-start and Queue cards and status.md's line show each project's
// start_weight: and starts over the last day; the page only reads the
// share, never records an ask.
func TestAutostartCardShowsShare(t *testing.T) {
	proj := setupYOLOProject(t)
	proj.StartWeight = 3
	withMockTmuxSessions(t, nil)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	orig, origLoad := autostart.ReadMemory, autostart.ReadLoad
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 31, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 4, Avg5: 4, Cores: 14} }
	t.Cleanup(func() { autostart.ReadMemory, autostart.ReadLoad = orig, origLoad })
	now := time.Now()
	if err := autostart.TurnOn(proj.WorkDir, proj.Slug, autostart.State{By: "lead-yp", How: "in the lead chat", Words: "start a job under 50%"}, now); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	page := pageGet(t, ts.URL+"/p/yp/status")
	if !strings.Contains(page, "start_weight: 3 · no other project waited for the gap in the last day") {
		t.Error("card lacks the weight alone")
	}
	if _, err := os.Stat(filepath.Join(autostart.SharedDir(), "share.json")); !os.IsNotExist(err) {
		t.Fatalf("a page load wrote the share (%v)", err)
	}

	// dark-factory waits now (weight 1), and started twice today.
	err := autostart.WithShare(autostart.SharedDir(), func(b *autostart.Budget) (*autostart.Start, error) {
		v := autostart.Verdict{Waiting: true, Below: 50}
		b.Decided("dark-factory", 1, v, now)
		b.Started("dark-factory", "df/a", v, now.Add(-2*time.Hour))
		b.Started("dark-factory", "df/b", v, now.Add(-time.Hour))
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(autostart.SharedDir(), "share.json"))
	page = pageGet(t, ts.URL+"/p/yp/status")
	want := "share of the start gap (24h, by start_weight:): yp ×3 0 starts · dark-factory ×1 2 starts"
	if !strings.Contains(page, `data-testid="status.autostart.share"`) || !strings.Contains(page, want) {
		t.Errorf("Auto-start card lacks %q", want)
	}
	md := pageGet(t, ts.URL+"/p/yp/status.md")
	if !strings.Contains(md, "0/2 workers) · "+want) {
		t.Errorf("status.md's Auto-start line lacks the share:\n%s", md)
	}
	after, _ := os.ReadFile(filepath.Join(autostart.SharedDir(), "share.json"))
	if string(before) != string(after) {
		t.Error("a page load changed the share")
	}
}
