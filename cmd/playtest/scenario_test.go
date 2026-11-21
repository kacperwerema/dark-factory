package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/diskuse"
	"github.com/kacperwerema/dark-factory/internal/fseventsd"
	"github.com/kacperwerema/dark-factory/internal/recent"
	"github.com/kacperwerema/dark-factory/internal/retro"
	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/timing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// TestScenarioEnvTurnsOffTheMachineReading: a scenario viewer reads tmux
// afresh, whatever the caller's environment says, so a step sees the sessions
// its setup just made.
func TestScenarioEnvTurnsOffTheMachineReading(t *testing.T) {
	s := &Scenario{Root: "/tmp/pt-x", Base: "http://127.0.0.1:1234", bins: &Binaries{Dir: "/tmp/bin"}}
	for _, base := range [][]string{{"PATH=/usr/bin"}, {"PATH=/usr/bin", recent.OffEnv + "=0"}} {
		if got := envMap(scenarioEnv(base, s))[recent.OffEnv]; got != "1" {
			t.Errorf("base %q: %s = %q, want 1", base, recent.OffEnv, got)
		}
	}
}

// TestScenarioViewerDoesNotReadTheHostsDisk: a scenario board's viewer is a
// real binary, so diskuse's test-binary switch doesn't hold there; at 86%
// used the host's volume put a Disk row in every session's Needs you count
// (17 slow-tier steps red at 44ba9f6, 2026-11-17). The scenario's env turns
// the machine readings off, so on a host over the line a viewer started
// with it reads no volume and has no verdict, and fseventsd reads nothing.
func TestScenarioViewerDoesNotReadTheHostsDisk(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // no readings file: only the volume counts
	read := 0
	oldVol, oldLive := diskuse.VolumeOf, diskuse.Live
	t.Cleanup(func() { diskuse.VolumeOf, diskuse.Live = oldVol, oldLive })
	diskuse.VolumeOf = func(string) diskuse.Volume {
		read++
		return diskuse.Volume{UsedPct: 86, FreeGB: 159, OK: true}
	}
	// The host itself, outside a scenario: over the line.
	diskuse.Live = diskuse.LiveIn(false, func(string) string { return "" })
	if v := diskuse.Current(time.Now()); !v.Over {
		t.Fatalf("host at 86%%: verdict %+v, want over (the stub is not read)", v)
	}
	s := &Scenario{Root: "/tmp/pt-x", Base: "http://127.0.0.1:1234", bins: &Binaries{Dir: "/tmp/bin"}}
	env := envMap(scenarioEnv([]string{"PATH=/usr/bin"}, s))
	getenv := func(k string) string { return env[k] }
	read = 0
	diskuse.Live = diskuse.LiveIn(false, getenv)
	if v := diskuse.Current(time.Now()); v.Over || read != 0 {
		t.Errorf("scenario viewer on a host at 86%%: verdict %+v after %d volume reads, want no reading", v, read)
	}
	t.Setenv("ISSUE_VIEWER_NO_MACHINE", env["ISSUE_VIEWER_NO_MACHINE"])
	if _, err := fseventsd.Read(time.Now()); err != fseventsd.ErrNone {
		t.Errorf("scenario viewer: fseventsd.Read = %v, want ErrNone (the host's fseventsd is not the board's)", err)
	}
}

// TestScenarioEnvIsolated: a scenario process never sees the caller's tmux
// server, config, HOME or agent identity.
func TestScenarioEnvIsolated(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"TMUX=/tmp/tmux-1000/default,123,0",
		"TMUX_PANE=%3",
		"HOME=/home/someone",
		"XDG_STATE_HOME=/home/someone/.local/state",
		"ISSUE_VIEWER_CONFIG=/home/someone/Work/projects.yaml",
		"ISSUE_VIEWER_ISSUE_SLUG=real/issue",
		"ISSUE_VIEWER_PROJECT=real-project", // a worker's own project named a scenario lead (radio-comments TP4)
		"ISSUE_VIEWER_URL=http://localhost:8080",
		"CLAUDECODE=1",
		"AGENT_NAME=agent-real",
		"GIT_AUTHOR_NAME=Someone",
		"LANG=C.UTF-8",
	}
	s := &Scenario{Root: "/tmp/pt-x", Base: "http://127.0.0.1:1234", bins: &Binaries{Dir: "/tmp/bin"}}
	m := envMap(scenarioEnv(base, s))
	for _, k := range []string{"TMUX", "TMUX_PANE", "ISSUE_VIEWER_ISSUE_SLUG", "ISSUE_VIEWER_PROJECT", "ISSUE_VIEWER_URL", "CLAUDECODE", "AGENT_NAME"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s leaked into the scenario env", k)
		}
	}
	want := map[string]string{
		"HOME":                "/tmp/pt-x/home",
		"XDG_STATE_HOME":      "/tmp/pt-x/state",
		"TMUX_TMPDIR":         "/tmp/pt-x/tmux",
		"ISSUE_VIEWER_CONFIG": "/tmp/pt-x/projects.yaml",
		"GIT_AUTHOR_NAME":     "playtest",
		"LANG":                "C.UTF-8",
		"PATH":                "/tmp/bin:/usr/bin",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q, want %q", k, m[k], v)
		}
	}
	for k, v := range m {
		if strings.Contains(v, "/home/someone") {
			t.Errorf("%s=%s points at the real home", k, v)
		}
	}
}

func TestGuardTmuxNeedsMarker(t *testing.T) {
	root := t.TempDir()
	s := &Scenario{Root: root}
	if err := os.MkdirAll(s.TmuxDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.guardTmux(); err == nil {
		t.Fatal("guardTmux allowed a dir without the marker")
	}
	if _, _, err := s.Tmux(context.Background(), "has-session"); err == nil {
		t.Fatal("Tmux ran without the marker")
	}
	if err := os.WriteFile(filepath.Join(s.TmuxDir(), markerName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.guardTmux(); err != nil {
		t.Fatalf("guardTmux refused a marked dir: %v", err)
	}
}

var (
	testBinsOnce sync.Once
	testBins     *Binaries
	testBinsErr  error
	// testBinsDir is the binaries' folder; TestMain removes it after m.Run.
	testBinsDir string
)

// buildTestBinaries builds issue-viewer and issue-cli once per test run,
// into a folder TestMain removes when the binary ends.
func buildTestBinaries(t *testing.T) *Binaries {
	t.Helper()
	if testing.Short() {
		t.Skip("builds binaries; skipped with -short")
	}
	testBinsOnce.Do(func() {
		dir, err := os.MkdirTemp("", "pt-bins-")
		if err != nil {
			testBinsErr = err
			return
		}
		testBinsDir = dir
		testBins, testBinsErr = BuildBinaries(repoDir(t), dir, io.Discard)
	})
	if testBinsErr != nil {
		t.Fatal(testBinsErr)
	}
	return testBins
}

// TestStartScenarioIsolated starts the basic scenario for real: the board
// serves the fixtures, issue-cli reads the scenario's config, tmux runs on
// the private socket, and Stop removes everything.
func TestStartScenarioIsolated(t *testing.T) {
	bins := buildTestBinaries(t)
	sc, err := StartScenario(filepath.Join(repoDir(t), "playtest", "scenarios"), "basic", bins, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			sc.Stop(false)
		}
	}()

	resp, err := http.Get(sc.Base + "/p/demo/board?classic=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Export issues as CSV") {
		t.Fatalf("board: %d, fixture missing", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, code, err := sc.CLI(ctx, "list")
	if err != nil || code != 0 || !strings.Contains(out, "export-issues-as-csv") {
		t.Fatalf("issue-cli list: exit %d err %v\n%s", code, err, out)
	}
	cfg, _ := os.ReadFile(sc.ConfigPath())
	if strings.Contains(string(cfg), "/Work/projects.yaml") || !strings.Contains(string(cfg), sc.Proj) {
		t.Errorf("scenario config doesn't point at the scenario project:\n%s", cfg)
	}
	if !strings.Contains(string(cfg), `terminal: "none"`) {
		t.Errorf("scenario config must be headless (terminal: none), or dispatch opens a real terminal:\n%s", cfg)
	}

	if _, err := exec.LookPath("tmux"); err == nil {
		if out, code, err := sc.Tmux(ctx, "new-session", "-d", "-s", "pt-probe"); err != nil || code != 0 {
			t.Fatalf("tmux new-session: %v %d %s", err, code, out)
		}
		out, _, _ := sc.Tmux(ctx, "display-message", "-p", "-t", "pt-probe", "#{socket_path}")
		// Tmux names the socket with -S, so tmux prints it as given; a server
		// found through TMUX_TMPDIR prints it resolved (macOS: /private/var).
		dir, err := filepath.EvalSymlinks(sc.TmuxDir())
		if err != nil {
			t.Fatal(err)
		}
		got := strings.TrimSpace(out)
		if got != sc.TmuxSocket() && !strings.HasPrefix(got, dir+"/") {
			t.Errorf("tmux socket %q is not the scenario's %s", got, sc.TmuxSocket())
		}
	}

	root := sc.Root
	sc.Stop(false)
	stopped = true
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("Stop left %s behind", root)
	}
}

func TestStopKeepsUnmarkedDirs(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "precious")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	(&Scenario{Root: root}).Stop(false)
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("Stop removed a dir without the scenario marker")
	}
}

func TestPinIssueTimes(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	os.WriteFile(a, []byte("---\ntitle: a\ncreated: \"2026-01-05\"\n---\n"), 0o644)
	os.WriteFile(b, []byte("---\ntitle: b\n---\n"), 0o644)
	if err := pinIssueTimes(dir); err != nil {
		t.Fatal(err)
	}
	ia, _ := os.Stat(a)
	ib, _ := os.Stat(b)
	if got := ia.ModTime().UTC().Format("2006-01-02"); got != "2026-01-05" {
		t.Errorf("a mtime = %s, want its created date", got)
	}
	if got := ib.ModTime().UTC().Format("2006-01-02"); got != "2026-01-01" {
		t.Errorf("b mtime = %s, want the fixed default", got)
	}
}

// TestGenerateIssues: a generate entry writes count issues with the slug the
// viewer derives from the title, in the system folder, with its frontmatter.
func TestGenerateIssues(t *testing.T) {
	dir := t.TempDir()
	err := generateIssues(dir, []ScenarioGenerate{
		{Count: 3, Dir: "Sim", Title: "Idea %03d", Status: "idea", Created: "2026-09-01", Extra: "priority: \"low\"\n", Body: "Someday."},
		{Count: 2, Title: "Chore #%d: tidy", Status: "backlog"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "Sim", "idea-003.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`title: "Idea 003"`, `status: "idea"`, `system: "Sim"`, `created: "2026-09-01"`, `priority: "low"`, "Someday."} {
		if !strings.Contains(string(data), want) {
			t.Errorf("idea-003.md lacks %s:\n%s", want, data)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "chore-2-tidy.md")); err != nil {
		t.Errorf("no system: written at the issues root as chore-2-tidy.md: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "Sim", "*.md"))
	if len(files) != 3 {
		t.Errorf("Sim holds %d issues, want 3", len(files))
	}
}

// TestCreatedDate: "-3d" is three days before now; a date stays as written.
func TestCreatedDate(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{"-3d": "2026-10-19", "-0d": "2026-10-22", "2026-08-01": "2026-08-01", "-3": "-3"} {
		if got := createdDate(in, now); got != want {
			t.Errorf("createdDate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadScenarioSpec_RejectsBadGenerate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "scenario.yaml"), []byte("generate:\n  - count: 5\n    status: idea\n    title: No number\n"), 0o644)
	if _, err := LoadScenarioSpec(dir); err == nil || !strings.Contains(err.Error(), "generate[0]") {
		t.Errorf("err = %v, want a generate[0] error", err)
	}
}

func TestWriteTranscripts(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	err := writeTranscripts(dir, "/tmp/pt-1/proj", []ScenarioTranscript{
		{Cwd: ".worktrees/seo/page", Messages: []ScenarioMessage{{Ago: "-7d", Model: "m", Input: 5}}},
		{Agent: "lead-acme", Messages: []ScenarioMessage{{Model: "m", Output: 2}}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "-tmp-pt-1-proj--worktrees-seo-page", "session-0.jsonl"))
	if err != nil || !strings.Contains(string(b), `"timestamp":"2026-10-23T12:00:00Z"`) || !strings.Contains(string(b), `"input_tokens":5`) {
		t.Errorf("worktree session = %s (%v)", b, err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "-tmp-pt-1-proj", "session-1.jsonl"))
	if !strings.HasPrefix(string(b), `{"type":"agent-name","agentName":"lead-acme"}`) {
		t.Errorf("root session = %s", b)
	}
}

// waitHTTP gives up within its budget on a board that accepts a request
// and never answers, and returns at once on one that answers.
func TestWaitHTTPBudgetHoldsOnAHangingBoard(t *testing.T) {
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer hang.Close()
	defer close(release)
	// The baseline is a board that answers at the budget.
	late := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer late.Close()
	check := timing.Check{Limit: 10 * time.Second, Ratio: 3, Rounds: 3}
	res := check.Run(timing.Timed(func() {
		if err := waitHTTP(hang.URL, 500*time.Millisecond); err == nil {
			t.Fatal("a hanging board counted as ready")
		}
	}), timing.Timed(func() { waitHTTP(late.URL, boardReadyBudget) }))
	if res.Slow {
		t.Fatalf("waitHTTP on a 500ms budget: %s (baseline: a board that answers at the budget)", res)
	}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	if err := waitHTTP(ok.URL, boardReadyBudget); err != nil {
		t.Fatalf("an answering board: %v", err)
	}
}

// rootOnlyScenario is what stopRecord rebuilds from a serve record: a marked
// Root and nothing else. Its tmux is a stand-in that records what it was
// called with, and $TMUX points at a server it must never reach.
func rootOnlyScenario(t *testing.T) (s *Scenario, record func() string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s = &Scenario{Root: root}
	if err := os.MkdirAll(s.TmuxDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.TmuxDir(), markerName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	rec := filepath.Join(bin, "record")
	// The record sits beside the stand-in, so its body is the same every run.
	standin.Write(t, filepath.Join(bin, "tmux"), "{ echo \"args=$*\"; echo \"cwd=$(pwd -P)\"; env; } > \"${0%/*}/record\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/tmux-shared/default,1,0")
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("HOME", "/home/someone")
	return s, func() string {
		t.Helper()
		data, err := os.ReadFile(rec)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
}

// checkPrivateTmuxCall: the stand-in ran with the scenario's private socket
// and no way to reach the shared server.
func checkPrivateTmuxCall(t *testing.T, s *Scenario, rec string) {
	t.Helper()
	if want := "args=-S " + s.TmuxSocket() + " "; !strings.Contains(rec, want) {
		t.Errorf("tmux did not name the private socket: no %q in its record", want)
	}
	for _, kv := range []string{"TMUX=", "TMUX_PANE="} {
		if strings.Contains("\n"+rec, "\n"+kv) {
			t.Errorf("tmux inherited %s", kv)
		}
	}
	if want := "\nTMUX_TMPDIR=" + s.TmuxDir() + "\n"; !strings.Contains(rec, want) {
		t.Errorf("tmux has no %q", want)
	}
	if want := "\nHOME=" + filepath.Join(s.Root, "home") + "\n"; !strings.Contains(rec, want) {
		t.Errorf("tmux has no %q", want)
	}
}

// TestRootOnlyScenarioCommandIsIsolated: a Scenario rebuilt from only a Root
// never ran Start, so nothing set its env; its commands still get the
// scenario's environment and run under the Root, not in the caller's folder.
func TestRootOnlyScenarioCommandIsIsolated(t *testing.T) {
	s, _ := rootOnlyScenario(t)
	cmd := s.command("true")
	if cmd.Env == nil {
		t.Fatal("cmd.Env is nil: the process inherits the caller's environment")
	}
	got := envMap(cmd.Env)
	for _, k := range []string{"TMUX", "TMUX_PANE"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s=%s reached the command", k, v)
		}
	}
	if got["TMUX_TMPDIR"] != s.TmuxDir() {
		t.Errorf("TMUX_TMPDIR=%q, want %q", got["TMUX_TMPDIR"], s.TmuxDir())
	}
	if cmd.Dir != s.Root {
		t.Errorf("cmd.Dir=%q, want the Root %q", cmd.Dir, s.Root)
	}
}

// TestRootOnlyScenarioTmuxStaysPrivate walks the two ways a Root-only
// Scenario talks to tmux: Tmux, and the kill-server in Stop.
func TestRootOnlyScenarioTmuxStaysPrivate(t *testing.T) {
	s, record := rootOnlyScenario(t)
	if _, code, err := s.Tmux(context.Background(), "has-session"); err != nil || code != 0 {
		t.Fatalf("Tmux: code %d, %v", code, err)
	}
	rec := record()
	checkPrivateTmuxCall(t, s, rec)
	if want := "\ncwd=" + s.Root + "\n"; !strings.Contains(rec, want) {
		t.Errorf("tmux ran outside the Root: no %q in its record", want)
	}

	s.Stop(true)
	rec = record()
	if !strings.Contains(rec, "kill-server") {
		t.Fatal("Stop did not kill the private server")
	}
	checkPrivateTmuxCall(t, s, rec)
}

func TestWriteExcludeKeepsWhatIsThere(t *testing.T) {
	proj := t.TempDir()
	if err := writeExclude(proj, "workflows/"); err != nil {
		t.Fatal(err)
	}
	if err := writeExclude(proj, ".worktrees/"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(proj, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "workflows/\n.worktrees/\n" {
		t.Errorf("exclude = %q", raw)
	}
}

// TestGenerateRetros: a kind: retro entry writes count retros into retros/
// as issue-cli retrospective does, each an hour after the last in its Date
// line and mtime alike, so retro.Load lists them newest (highest) first in
// the same order every run. Issue entries are left to generateIssues.
func TestGenerateRetros(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "retros")
	gens := []ScenarioGenerate{
		{Kind: "retro", Count: 35, Dir: "UI", Title: "Pager %02d", Status: "done", Created: "2026-09-01", Extra: "ReviewStatus: open"},
		{Count: 2, Title: "An issue %d", Status: "idea"},
	}
	if err := generateRetros(dir, gens, time.Now()); err != nil {
		t.Fatal(err)
	}
	entries, err := retro.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 35 {
		t.Fatalf("%d retros, want 35 (the issue entry writes none here)", len(entries))
	}
	first, last := entries[0], entries[34]
	if first.IssueTitle != "Pager 35" || last.IssueTitle != "Pager 01" {
		t.Errorf("order: first %q, last %q; want Pager 35 then … Pager 01", first.IssueTitle, last.IssueTitle)
	}
	if first.IssueSlug != "ui/pager-35" || first.System != "UI" || first.Status != "done" || first.ReviewStatus != "open" {
		t.Errorf("header: %+v", first)
	}
	want := time.Date(2026, 9, 1, 12+34, 0, 0, 0, time.UTC)
	if !first.Time().Equal(want) || !first.ModTime.Equal(want) {
		t.Errorf("Pager 35 dated %v (mtime %v), want %v for both", first.Time(), first.ModTime, want)
	}
	if secs := retro.Sections(first.Body); len(secs) == 0 {
		t.Errorf("default body has no labelled section: %q", first.Body)
	}
	if err := generateRetros(t.TempDir(), []ScenarioGenerate{{Kind: "retro", Count: 1, Title: "R %d", Status: "done", Created: "soon"}}, time.Now()); err == nil {
		t.Error("created: soon was accepted; want an error naming the forms")
	}
}

// TestLoadScenarioSpec_RejectsUnknownKind: a generate kind other than issue
// or retro fails the spec, naming both.
func TestLoadScenarioSpec_RejectsUnknownKind(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "scenario.yaml"), []byte("generate:\n  - kind: bug\n    count: 5\n    status: open\n    title: Bug %d\n"), 0o644)
	_, err := LoadScenarioSpec(dir)
	if err == nil || !strings.Contains(err.Error(), "generate[0]") || !strings.Contains(err.Error(), "issue") || !strings.Contains(err.Error(), "retro") {
		t.Errorf("err = %v, want a generate[0] error naming issue and retro", err)
	}
}

// TestBranchTags: a branch's tags: are made after its commits, on the branch
// tip by default, so a release branch's candidate tag sits on a commit main
// lacks; the history's shas are the same on a second build.
func TestBranchTags(t *testing.T) {
	src := t.TempDir()
	for _, d := range []string{"fix", "patch"} {
		os.MkdirAll(filepath.Join(src, d), 0o755)
		os.WriteFile(filepath.Join(src, d, d+".txt"), []byte(d), 0o644)
	}
	spec := &ScenarioSpec{Branches: []ScenarioBranch{{
		Name: "release/v1.5", Worktree: true,
		Commits: []ScenarioCommit{{Files: "fix", Message: "Fix"}, {Files: "patch", Message: "Patch"}},
		Tags:    []ScenarioTag{{Name: "v1.5.0-rc.1", Ref: "release/v1.5~1"}, {Name: "v1.5.0-rc.2"}},
	}}}
	build := func() map[string]string {
		root := t.TempDir()
		s := &Scenario{Spec: spec, Root: root, Proj: filepath.Join(root, "proj")}
		os.MkdirAll(s.Proj, 0o755)
		os.WriteFile(filepath.Join(s.Proj, "README"), []byte("x"), 0o644)
		for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "first"}} {
			cmd := s.command("git", args...)
			cmd.Env = append(cmd.Env, s.nextCommitDate()...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		if err := s.buildHistory(src); err != nil {
			t.Fatal(err)
		}
		rev := func(args ...string) string {
			out, err := s.command("git", args...).Output()
			if err != nil {
				t.Fatalf("git %v: %v", args, err)
			}
			return strings.TrimSpace(string(out))
		}
		if _, err := os.Stat(filepath.Join(s.Proj, ".worktrees", "release-v1.5")); err != nil {
			t.Errorf("no release worktree: %v", err)
		}
		if err := s.command("git", "merge-base", "--is-ancestor", "v1.5.0-rc.1", "main").Run(); err == nil {
			t.Error("v1.5.0-rc.1 is on main; want it on a commit only the release branch has")
		}
		if got := rev("log", "-1", "--format=%s", "v1.5.0-rc.1"); got != "Fix" {
			t.Errorf("rc.1 tags %q, want Fix", got)
		}
		return map[string]string{
			"rc1": rev("rev-parse", "v1.5.0-rc.1^{}"), "rc2": rev("rev-parse", "v1.5.0-rc.2^{}"),
			"tip": rev("rev-parse", "release/v1.5"), "tag2": rev("rev-parse", "v1.5.0-rc.2"),
		}
	}
	a, b := build(), build()
	if a["rc2"] != a["tip"] {
		t.Errorf("rc.2 on %s, want the branch tip %s", a["rc2"], a["tip"])
	}
	for k := range a {
		if a[k] != b[k] {
			t.Errorf("%s: %s then %s; want the same sha on every build", k, a[k], b[k])
		}
	}
}
