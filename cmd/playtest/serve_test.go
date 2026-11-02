package main

import (
	"bytes"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestServeTakenPortLeavesViewerJSON: serve asked for a port another
// process holds serves on a free one and says so, the board answers, the
// shared viewer.json (here the caller's own XDG_STATE_HOME) is untouched,
// and stop kills the board and removes everything it staged.
func TestServeTakenPortLeavesViewerJSON(t *testing.T) {
	bins := buildTestBinaries(t)
	caller := t.TempDir()
	t.Setenv("HOME", filepath.Join(caller, "home"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(caller, "state"))
	shared := filepath.Join(caller, "state", "ai-workflow-engine", "viewer.json")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte(`{"config":"/home/someone/Work/projects.yaml","url":"http://127.0.0.1:8090"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(shared, old, old); err != nil {
		t.Fatal(err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	taken := l.Addr().(*net.TCPAddr).Port

	repo := t.TempDir() // where the record goes
	dir, err := os.MkdirTemp("", "pt-serve-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer removeMarked(dir, serveMarker)
	rec, notes, err := Serve(repo, serveOpts{Scenario: "basic", ScenarioDir: filepath.Join(repoDir(t), "playtest", "scenarios"), Port: taken, Dir: dir}, bins, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stopRecord(repo, rec, false, io.Discard)
		}
	}()
	head := rec.Board("head")
	if head == nil || strings.HasSuffix(head.URL, ":"+strconv.Itoa(taken)) {
		t.Fatalf("head board %+v is on the taken port %d", head, taken)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "port "+strconv.Itoa(taken)+" is taken") {
		t.Errorf("notes = %q, want the taken port named", notes)
	}
	resp, err := http.Get(head.URL + "/p/demo/board?classic=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("board answered %d", resp.StatusCode)
	}
	if !rec.live() {
		t.Error("record not live while its board runs")
	}
	if _, err := readRecord(repo, "basic"); err != nil {
		t.Errorf("no record: %v", err)
	}
	if _, _, err := Serve(repo, serveOpts{Scenario: "basic", ScenarioDir: filepath.Join(repoDir(t), "playtest", "scenarios")}, bins, io.Discard); err == nil || !strings.Contains(err.Error(), "already served") {
		t.Errorf("second serve of the same scenario: %v", err)
	}
	fi, err := os.Stat(shared)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Errorf("shared viewer.json mtime %s, want %s: the served board wrote it", fi.ModTime(), old)
	}

	var out bytes.Buffer
	stopRecord(repo, rec, false, &out)
	stopped = true
	if boardAlive(*head) {
		t.Error("board still running after stop")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("stop left %s behind", dir)
	}
	if _, err := readRecord(repo, "basic"); err == nil {
		t.Error("stop left the record")
	}
	if !strings.Contains(out.String(), "stopped head board") {
		t.Errorf("stop said %q", out.String())
	}
}

// TestStopRefusesAForeignPid: a record whose pid is now some other process
// (its command line doesn't name the board's projects.yaml) kills nothing,
// and a folder without the marker stays.
func TestStopRefusesAForeignPid(t *testing.T) {
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleep.Process.Kill(); _ = sleep.Wait() }()
	repo := t.TempDir()
	dir := t.TempDir()
	keep := filepath.Join(dir, "precious")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &ServeRecord{Scenario: "basic", Dir: dir, Boards: []ServedBoard{{Label: "head", PID: sleep.Process.Pid, Root: dir, Config: "/nowhere/projects.yaml"}}}
	if err := writeRecord(repo, rec); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := cmdStop(repo, opts{scenario: "basic"}, &out, io.Discard); code != 0 {
		t.Fatalf("stop exit %d", code)
	}
	if err := sleep.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("stop killed a process that is not the board: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("stop removed a folder without the serve marker")
	}
	if !strings.Contains(out.String(), "was not running") {
		t.Errorf("stop said %q", out.String())
	}
}

// TestExtraConfigStaged: extra_config: keys land at the top of the staged
// projects.yaml; the stager's own keys are refused.
func TestExtraConfigStaged(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "scenario.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("project:\n  slug: demo\nextra_config:\n  telemetry_data: x\n  allowed_hosts: [board.example.invalid]\n")
	spec, err := LoadScenarioSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := scenarioConfig(spec, "http://127.0.0.1:1", "/tmp/pt-x/proj", "")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		TelemetryData string   `yaml:"telemetry_data"`
		AllowedHosts  []string `yaml:"allowed_hosts"`
		ViewerURL     string   `yaml:"viewer_url"`
		Projects      []struct {
			Slug string `yaml:"slug"`
		} `yaml:"projects"`
	}
	if err := yaml.Unmarshal([]byte(cfg), &got); err != nil {
		t.Fatalf("staged config is not YAML: %v\n%s", err, cfg)
	}
	if got.TelemetryData != "x" || len(got.AllowedHosts) != 1 || got.ViewerURL == "" || len(got.Projects) != 1 || got.Projects[0].Slug != "demo" {
		t.Errorf("staged config %+v from\n%s", got, cfg)
	}
	for _, bad := range []string{
		"extra_config:\n  projects: []\n",
		"extra_config:\n  viewer_url: http://x\n",
		"telemetry_data: collector\nextra_config:\n  telemetry_data: x\n",
	} {
		write(bad)
		if _, err := LoadScenarioSpec(dir); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// No extra_config: the staged config is what it always was.
	write("project:\n  slug: demo\n")
	spec, _ = LoadScenarioSpec(dir)
	cfg, _ = scenarioConfig(spec, "http://127.0.0.1:1", "/p", "")
	if !strings.HasPrefix(cfg, "viewer_url: ") {
		t.Errorf("plain scenario config changed:\n%s", cfg)
	}
}

// standInRecord writes a live-looking serve record for two stub boards:
// pids are this test process, which cmdlineOf accepts for the test.
func standInRecord(t *testing.T, repo, head, base string) {
	t.Helper()
	orig := cmdlineOf
	cmdlineOf = func(pid int, want string) bool { return pid == os.Getpid() && want == "stand-in" }
	t.Cleanup(func() { cmdlineOf = orig })
	rec := &ServeRecord{Scenario: "company-floor", Project: "demo", Boards: []ServedBoard{
		{Label: "head", URL: head, PID: os.Getpid(), Config: "stand-in"},
	}}
	if base != "" {
		rec.Boards = append(rec.Boards, ServedBoard{Label: "base", Ref: "main", URL: base, PID: os.Getpid(), Config: "stand-in"})
	}
	if err := writeRecord(repo, rec); err != nil {
		t.Fatal(err)
	}
}

func TestResolveShotURL(t *testing.T) {
	repo := t.TempDir()
	if _, _, err := resolveShotURL(repo, "/p/demo/company", "", "main"); err == nil || !strings.Contains(err.Error(), "make serve SCENARIO=<name> BASE=main") {
		t.Errorf("no serve: %v", err)
	}
	if _, _, err := resolveShotURL(repo, "http://127.0.0.1:1/p/demo", "", "main"); err == nil || !strings.Contains(err.Error(), "path URL") {
		t.Errorf("full URL with BASE: %v", err)
	}
	if h, b, err := resolveShotURL(repo, "http://127.0.0.1:1/p/demo", "", ""); err != nil || h != "http://127.0.0.1:1/p/demo" || b != "" {
		t.Errorf("full URL: %q %q %v", h, b, err)
	}
	standInRecord(t, repo, "http://127.0.0.1:1", "")
	if h, _, err := resolveShotURL(repo, "/p/demo/company?x=1", "", ""); err != nil || h != "http://127.0.0.1:1/p/demo/company?x=1" {
		t.Errorf("path on the served board: %q %v", h, err)
	}
	if _, _, err := resolveShotURL(repo, "/p/demo/company", "", "main"); err == nil || !strings.Contains(err.Error(), "BASE=main") {
		t.Errorf("served without a base, shot with BASE: %v", err)
	}
}

// TestShotWithBaseWritesBoth: make shot URL=/p/<project>/company BASE=main
// on a served scenario shoots the head board into OUT and the base board
// into OUT.base, and HTML=1 saves both pages' DOM for a diff. The boards
// are stubs: each answers with its own name.
func TestShotWithBaseWritesBoth(t *testing.T) {
	if _, err := ChromePath(); err != nil {
		t.Skip(err)
	}
	var mu sync.Mutex
	var paths []string
	stub := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, name+" "+r.URL.Path)
			mu.Unlock()
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<!doctype html><html><body><h1>"+name+"</h1></body></html>")
		}))
	}
	head, base := stub("head"), stub("base")
	defer head.Close()
	defer base.Close()
	repo := t.TempDir()
	standInRecord(t, repo, head.URL, base.URL)
	out := filepath.Join(t.TempDir(), "company.png")
	var stdout, stderr bytes.Buffer
	if code := cmdShot(repo, opts{url: "/p/demo/company", baseRef: "main", out: out, html: true}, &stdout, &stderr); code != 0 {
		t.Fatalf("shot exit %d: %s", code, stderr.String())
	}
	for _, f := range []string{out, baseOut(out)} {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		_, err = png.DecodeConfig(fh)
		fh.Close()
		if err != nil {
			t.Errorf("%s is not a PNG: %v", f, err)
		}
	}
	for f, want := range map[string]string{htmlOut(out): "<h1>head</h1>", htmlOut(baseOut(out)): "<h1>base</h1>"} {
		data, err := os.ReadFile(f)
		if err != nil || !strings.Contains(string(data), want) {
			t.Errorf("%s: %v, want %s in\n%s", f, err, want, data)
		}
	}
	mu.Lock()
	got := strings.Join(paths, ",")
	mu.Unlock()
	if !strings.Contains(got, "head /p/demo/company") || !strings.Contains(got, "base /p/demo/company") {
		t.Errorf("boards saw %s", got)
	}
	if !strings.Contains(stdout.String(), "diff -u "+htmlOut(baseOut(out))+" "+htmlOut(out)) {
		t.Errorf("no diff command in %q", stdout.String())
	}
}

func TestBaseOut(t *testing.T) {
	if got := baseOut("/a/shot.png"); got != "/a/shot.base.png" {
		t.Error(got)
	}
	if got := htmlOut("/a/shot.base.png"); got != "/a/shot.base.html" {
		t.Error(got)
	}
}
