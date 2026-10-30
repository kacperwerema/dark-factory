package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/agentrun"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// setupPlaytestProject has one issue at human-testing whose worktree holds
// a playtest session naming it, and one with no session.
func setupPlaytestProject(t *testing.T, workflowExtra string) (tracker.Project, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("issues/CLI/lead-tab.md", "---\ntitle: \"Lead tab\"\nstatus: \"human-testing\"\n---\n\nBody.\n")
	write("issues/CLI/bare.md", "---\ntitle: \"Bare\"\nstatus: \"human-testing\"\n---\n\nBody.\n")
	write("workflow.yaml", "worktree: true\n"+workflowExtra+"statuses:\n  - name: \"testing\"\n  - name: \"human-testing\"\n  - name: \"done\"\n")
	write(".worktrees/cli/lead-tab/playtest/sessions/lead-tab.yaml", "session: lead-tab\nissue: cli/lead-tab\n")
	return tracker.Project{Name: "Play", Slug: "pt", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")},
		filepath.Join(root, ".worktrees", "cli", "lead-tab")
}

type madeWindow struct {
	name, dir string
	env, cmd  []string
}

// fakePlaytestTmux replaces tmux: windows made, opened, and whether one lives.
func fakePlaytestTmux(t *testing.T) (*[]madeWindow, *[]string, *bool) {
	t.Helper()
	var made []madeWindow
	var opened []string
	live := false
	origCreate, origLive, origAttach := createCheckWindow, checkWindowLive, attachAgent
	createCheckWindow = func(_ *tracker.Project, name, dir string, env, command []string, steps *[]DispatchStep) bool {
		made = append(made, madeWindow{name, dir, env, command})
		*steps = append(*steps, agentrun.Step{Name: "Create tmux window " + name, Status: "ok"})
		return true
	}
	checkWindowLive = func(*tracker.Project, string) bool { return live }
	attachAgent = func(_ *tracker.Project, name string, _ *[]agentrun.Step) bool {
		opened = append(opened, name)
		return true
	}
	t.Cleanup(func() { createCheckWindow, checkWindowLive, attachAgent = origCreate, origLive, origAttach })
	return &made, &opened, &live
}

func TestRunPlaytestOpensThePlaytestWindow(t *testing.T) {
	t.Setenv("PLAYTEST_CHROME", "/opt/chrome")
	proj, wt := setupPlaytestProject(t, "")
	made, opened, live := fakePlaytestTmux(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	code, resp := postJSON(t, ts.URL+"/p/pt/issue/cli/lead-tab/playtest", `{}`)
	if code != 200 || resp["status"] != "started" || resp["session"] != "playtest-cli-lead-tab" || resp["since"] == "" {
		t.Fatalf("start: %d %v", code, resp)
	}
	if len(*made) != 1 || (*made)[0].name != "playtest-cli-lead-tab" || (*made)[0].dir != wt {
		t.Fatalf("created = %+v, want playtest-cli-lead-tab in the worktree", *made)
	}
	m := (*made)[0]
	if cmd := strings.Join(m.cmd, " "); !strings.Contains(cmd, "make playtest ISSUE=cli/lead-tab;") || !strings.Contains(cmd, "Press Enter to close") {
		t.Fatalf("command = %q", cmd)
	}
	if env := strings.Join(m.env, " "); !strings.Contains(env, "PLAYTEST_PROJECT=pt") || !strings.Contains(env, "PLAYTEST_CHROME=/opt/chrome") {
		t.Fatalf("env = %q", env)
	}
	if len(*opened) != 1 || (*opened)[0] != "playtest-cli-lead-tab" {
		t.Fatalf("opened = %v", *opened)
	}

	// A second click while it runs only brings the window back.
	*live = true
	if code, resp := postJSON(t, ts.URL+"/p/pt/issue/cli/lead-tab/playtest", `{}`); code != 200 || resp["status"] != "already-running" || len(*made) != 1 || len(*opened) != 2 {
		t.Fatalf("second click: %d %v made=%d", code, resp, len(*made))
	}

	// Tick passed boxes: the report, in the same window name.
	*live = false
	if code, resp := postJSON(t, ts.URL+"/p/pt/issue/cli/lead-tab/playtest?report=1", `{}`); code != 200 || resp["status"] != "started" {
		t.Fatalf("report: %d %v", code, resp)
	}
	if cmd := strings.Join((*made)[1].cmd, " "); (*made)[1].name != "playtest-cli-lead-tab" || !strings.Contains(cmd, "make playtest-report ISSUE=cli/lead-tab APPLY=1;") {
		t.Fatalf("report window = %+v", (*made)[1])
	}

	// No session names the issue: nothing starts.
	if code, resp := postJSON(t, ts.URL+"/p/pt/issue/cli/bare/playtest", `{}`); code != 404 || resp["status"] != "no-playtest" || len(*made) != 2 {
		t.Fatalf("no session: %d %v", code, resp)
	}
}

func TestRunPlaytestUsesTheProjectCommands(t *testing.T) {
	proj, _ := setupPlaytestProject(t, "playtest_command: \"./play.sh {slug}\"\nplaytest_report: \"./report.sh {slug}\"\n")
	made, _, _ := fakePlaytestTmux(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	postJSON(t, ts.URL+"/p/pt/issue/cli/lead-tab/playtest", `{}`)
	postJSON(t, ts.URL+"/p/pt/issue/cli/lead-tab/playtest?report=1", `{}`)
	if len(*made) != 2 || !strings.Contains(strings.Join((*made)[0].cmd, " "), "./play.sh cli/lead-tab;") || !strings.Contains(strings.Join((*made)[1].cmd, " "), "./report.sh cli/lead-tab;") {
		t.Fatalf("made = %+v", *made)
	}
}

func TestRunPlaytestCrossOriginIsRefused(t *testing.T) {
	proj, _ := setupPlaytestProject(t, "")
	made, _, _ := fakePlaytestTmux(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/pt/issue/cli/lead-tab/playtest", nil)
	req.Header.Set("Origin", "https://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || len(*made) != 0 {
		t.Fatalf("cross-origin: %d, made %d", res.StatusCode, len(*made))
	}
}

// The page polls GET: running while the window lives, then each step's
// verdict from the judge runs since the click.
func TestPlaytestStateReadsTheJudgeRunsSinceTheClick(t *testing.T) {
	proj, wt := setupPlaytestProject(t, "")
	_, _, live := fakePlaytestTmux(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	run := func(dir, mode, started, steps string) {
		p := filepath.Join(wt, ".playtest", "runs", "lead-tab", dir, "results.yaml")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("session: lead-tab\nissue: cli/lead-tab\nmode: "+mode+"\nstarted: \""+started+"\"\nsteps:\n"+steps), 0o644)
	}
	run("20261030-090000-judge", "judge", "2026-10-30T09:00:00Z", "  - id: TP1\n    status: pass\n")
	run("20261030-110000-auto", "auto", "2026-10-30T11:00:00Z", "  - id: TP1\n    status: fail\n")
	run("20261030-110500-judge", "judge", "2026-10-30T11:05:00Z",
		"  - id: TP1\n    title: The band\n    status: pass\n  - id: TP2\n    judge:\n      status: fail\n  - id: TP3\n    status: skipped\n")

	get := func(q string) playtestState {
		t.Helper()
		res, err := http.Get(ts.URL + "/p/pt/issue/cli/lead-tab/playtest" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var st playtestState
		if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return st
	}

	*live = true
	if st := get("?since=2026-10-30T11:00:00Z"); !st.Running || st.Session != "playtest-cli-lead-tab" {
		t.Fatalf("while running: %+v", st)
	}
	*live = false
	st := get("?since=2026-10-30T11:00:00Z")
	if st.Running || len(st.Runs) != 1 || st.Passed != 1 || st.Failed != 1 || st.Open != 1 {
		t.Fatalf("after: %+v, want the 11:05 judge run only (not the auto run, not the 09:00 one)", st)
	}
	if s := st.Runs[0].Steps; s[0].ID != "TP1" || s[0].Verdict != "pass" || s[0].Title != "The band" || s[1].Verdict != "fail" || s[2].Verdict != "" {
		t.Fatalf("steps = %+v", s)
	}
	// No since (the click only brought the window back): the newest judge run.
	if st := get(""); len(st.Runs) != 1 || !strings.HasPrefix(st.Runs[0].Started, "2026-10-30T11:05:00") {
		t.Fatalf("no since: %+v", st)
	}
}

func TestPlaytestRunURLOnlyWhenDue(t *testing.T) {
	if u := playtestRunURL("/p/pt/issue/x", Attention{}, nil); u != "" {
		t.Errorf("nothing due: %q", u)
	}
	if u := playtestRunURL("/p/pt/issue/x", Attention{PlaytestDue: true, Playtests: []string{"s"}}, nil); u != "/p/pt/issue/x/playtest" {
		t.Errorf("due: %q", u)
	}
	if n := playtestWindowName("cli/lead.tab"); n != "playtest-cli-lead-tab" {
		t.Errorf("window name = %q", n)
	}
}

func TestPlaytestButtonOnThePages(t *testing.T) {
	in := tabInput(&tracker.Issue{Slug: "cli/lead-tab", Status: "human-testing"})
	in.Prefix = "/p/pt"
	in.Att.PlaytestDue, in.Att.Playtests = true, []string{"lead-tab"}
	n := buildIssueTab(in).Needs
	if n.State != "playtest" || n.RunURL != "/p/pt/issue/cli/lead-tab/playtest" || n.Command != "make playtest ISSUE=cli/lead-tab" {
		t.Fatalf("needs = %+v", n)
	}
	in.WF = &tracker.WorkflowConfig{PlaytestCommand: "./play.sh {slug}"}
	if n := buildIssueTab(in).Needs; n.Command != "./play.sh cli/lead-tab" {
		t.Fatalf("custom command = %q", n.Command)
	}

	m := stationMove("pt", nil, &tracker.Issue{Slug: "cli/lead-tab", Status: "human-testing"}, "/p/pt/issue/cli/lead-tab", needsYou{Amber: true, Wait: "1h"}, in.Att, nil, nil, time.Now())
	if len(m.Tasks) != 1 || m.Tasks[0].Kind != "playtest" || m.Tasks[0].RunURL != "/p/pt/issue/cli/lead-tab/playtest" {
		t.Fatalf("station tasks = %+v", m.Tasks)
	}

	ev := buildEvidence(evidenceIn{Now: time.Now(), RunPlaytest: "/p/pt/issue/cli/lead-tab/playtest", PlaytestCommand: "make playtest ISSUE=cli/lead-tab"})
	if ev.RunPlaytest == "" || ev.PlaytestCommand == "" {
		t.Fatalf("evidence = %+v", ev)
	}
}

// playtest: agents (factory/playtest-is-per-project-off-on-dark-factory):
// no button and the POST refuses.
func TestRunPlaytestOffWhenTheOwnerPlaytestIs(t *testing.T) {
	proj, _ := setupPlaytestProject(t, "playtest: agents\n")
	made, _, _ := fakePlaytestTmux(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	if code, resp := postJSON(t, ts.URL+"/p/pt/issue/cli/lead-tab/playtest", `{}`); code != 404 || resp["status"] != "off" || len(*made) != 0 {
		t.Fatalf("agents: %d %v", code, resp)
	}
	wf := &tracker.WorkflowConfig{Playtest: "agents"}
	if u := playtestRunURL("/p/pt/issue/x", Attention{PlaytestDue: true}, wf); u != "" {
		t.Errorf("agents: button URL %q", u)
	}
}
