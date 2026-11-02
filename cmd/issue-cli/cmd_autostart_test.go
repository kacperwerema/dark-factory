package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const autostartWords = "by default if we're too low on memory < 50% we should start a new job"

// makeAutostartProject is a project with three backlog issues (one high,
// one needing a done dependency, one low), run as the project lead, no
// live tmux sessions, mem-guard reading 31% and the budget in a temp dir.
func makeAutostartProject(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	issue := func(title, status, extra string) string {
		return "---\ntitle: \"" + title + "\"\nstatus: \"" + status + "\"\n" + extra + "---\n\nBody of " + title + ".\n"
	}
	write("issues/low.md", issue("Low", "backlog", "priority: \"low\"\n"))
	write("issues/blocked.md", issue("Blocked", "backlog", "priority: \"critical\"\ndepends_on: \"base\"\n"))
	write("issues/base.md", issue("Base", "in progress", ""))
	write("issues/high.md", issue("High", "backlog", "priority: \"high\"\n"))
	write("issues/claimed.md", issue("Claimed", "backlog", "priority: \"critical\"\nassignee: \"someone\"\n"))
	write("workflow.yaml", "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\n")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("TMUX_TMPDIR", "")
	origHas, origList, origMem, origLoad := agentrun.HasSession, agentrun.ListSessions, autostart.ReadMemory, autostart.ReadLoad
	agentrun.HasSession = func(string) bool { return false }
	agentrun.ListSessions = func() []agentrun.Session { return nil }
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 31, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 4, Cores: 14} }
	t.Cleanup(func() {
		agentrun.HasSession, agentrun.ListSessions, autostart.ReadMemory, autostart.ReadLoad = origHas, origList, origMem, origLoad
	})
	return &tracker.Project{Slug: "proj", Name: "Proj", IssueDir: filepath.Join(root, "issues"), WorkDir: root, WorkflowFile: filepath.Join(root, "workflow.yaml")}
}

func stubStart(t *testing.T) *[]string {
	t.Helper()
	var started []string
	orig := startAgent
	startAgent = func(_ *tracker.Project, spec agentrun.Spec) agentrun.Response {
		started = append(started, spec.IssueSlug)
		return agentrun.Response{Status: "dispatched", Session: spec.Session}
	}
	t.Cleanup(func() { startAgent = orig })
	return &started
}

func autostartOnFor(t *testing.T, proj *tracker.Project, extra ...string) {
	t.Helper()
	ctx, _, _ := newTestContext(proj, false)
	if err := runAutostart(ctx, append([]string{"on", "--quote", autostartWords}, extra...)); err != nil {
		t.Fatal(err)
	}
}

func TestAutostart_OffByDefault(t *testing.T) {
	proj := makeAutostartProject(t)
	started := stubStart(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"next"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "hold: auto-start is off" {
		t.Fatalf("next = %q", got)
	}
	ctx, _, _ = newTestContext(proj, false)
	err := runDispatch(ctx, []string{"high", "--autostart"})
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "hold: auto-start is off") || len(*started) != 0 {
		t.Fatalf("dispatch --autostart while off: %v (exit %d), started %v", err, exitOf(err), *started)
	}
}

func TestAutostart_OnStoresWordsAndWorkersAreRefused(t *testing.T) {
	proj := makeAutostartProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"on"}); exitOf(err) != 2 {
		t.Fatalf("on without --quote: %v", err)
	}
	autostartOnFor(t, proj, "--max-workers", "3")
	st, _ := autostart.Load(yoloRoot(proj), "proj")
	if !st.On || st.Words != autostartWords || st.By != "lead-proj" || st.How != "in the lead chat" || st.MaxWorkers != 3 || st.Since.IsZero() {
		t.Fatalf("state = %+v", st)
	}

	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "high")
	for _, sub := range [][]string{{"on", "--quote", "x"}, {"pause"}, {"resume"}, {"off"}} {
		ctx, _, _ = newTestContext(proj, false)
		if err := runAutostart(ctx, sub); err == nil || !strings.Contains(err.Error(), "workers never") {
			t.Errorf("worker %v: %v", sub, err)
		}
	}

	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"off", "--reason", "owner said stop"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "owner said stop by lead-proj") {
		t.Errorf("off = %q", stdout.String())
	}
	if h := autostart.History(yoloRoot(proj), "proj"); len(h) != 1 || h[0].Words != autostartWords {
		t.Fatalf("history = %+v", h)
	}
}

func TestAutostart_NextPicksTheFirstReadyIssue(t *testing.T) {
	proj := makeAutostartProject(t)
	autostartOnFor(t, proj)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"next"}); err != nil {
		t.Fatal(err)
	}
	want := "start high (memory 31% < 50%, load 4.0 < 14 on 14 cores, 0/2 workers)\n  issue-cli --project proj dispatch high --autostart\n"
	if stdout.String() != want {
		t.Fatalf("next =\n%s\nwant\n%s", stdout.String(), want)
	}

	// A live worker on high: the next ready one is low (blocked waits on base).
	agentrun.HasSession = func(name string) bool { return name == "=agent-high:" }
	ctx, stdout, _ = newTestContext(proj, false)
	runAutostart(ctx, []string{"next"})
	if !strings.HasPrefix(stdout.String(), "start low ") {
		t.Fatalf("next with high live = %q", stdout.String())
	}

	// Paused, then memory high, then the cap.
	ctx, _, _ = newTestContext(proj, false)
	runAutostart(ctx, []string{"pause", "--reason", "plan"})
	ctx, stdout, _ = newTestContext(proj, false)
	runAutostart(ctx, []string{"next"})
	if got := strings.TrimSpace(stdout.String()); got != "hold: auto-start is paused (plan)" {
		t.Errorf("paused: %q", got)
	}
	ctx, _, _ = newTestContext(proj, false)
	runAutostart(ctx, []string{"resume"})
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 55, CanStart: true} }
	ctx, stdout, _ = newTestContext(proj, false)
	runAutostart(ctx, []string{"next"})
	if got := strings.TrimSpace(stdout.String()); got != "hold: memory 55% ≥ 50%" {
		t.Errorf("memory: %q", got)
	}
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 20, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 16, Cores: 14} }
	ctx, stdout, _ = newTestContext(proj, false)
	runAutostart(ctx, []string{"next"})
	if got := strings.TrimSpace(stdout.String()); got != "hold: load 16.0 on 14 cores (max 14)" {
		t.Errorf("load: %q", got)
	}
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 4, Cores: 14} }
	agentrun.ListSessions = func() []agentrun.Session {
		return []agentrun.Session{{Name: "agent-high"}, {Name: "agent-low"}, {Name: "lead-proj"}}
	}
	ctx, stdout, _ = newTestContext(proj, false)
	runAutostart(ctx, []string{"next"})
	if got := strings.TrimSpace(stdout.String()); got != "hold: 2 workers live (cap 2)" {
		t.Errorf("cap: %q", got)
	}
}

func TestDispatchAutostart_QuotesTheStoredWordsAndSharesTheBudget(t *testing.T) {
	proj := makeAutostartProject(t)
	started := stubStart(t)
	now := time.Date(2026, 10, 30, 14, 0, 0, 0, time.Local)
	autostartOnFor(t, proj)

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDispatch(ctx, []string{"high", "--autostart", "--quote", "x"}); exitOf(err) != 2 {
		t.Fatalf("--autostart with --quote: %v", err)
	}
	ctx.Now = func() time.Time { return now }
	if err := runDispatch(ctx, []string{"high", "--autostart"}); err != nil {
		t.Fatalf("dispatch --autostart: %v\n%s", err, stdout.String())
	}
	if len(*started) != 1 || (*started)[0] != "high" {
		t.Fatalf("started = %v", *started)
	}
	comments, _ := tracker.LoadComments(loadLeadIssue(t, proj, "high").FilePath)
	last := comments[len(comments)-1].Text
	for _, w := range []string{`dispatch: "` + autostartWords + `"`, "auto-start, turned on by lead-proj in the lead chat", "memory 31% < 50%, load 4.0 < 14 on 14 cores, 0/2 workers", "— recorded by lead-proj"} {
		if !strings.Contains(last, w) {
			t.Errorf("comment %q missing %q", last, w)
		}
	}

	// Another project on the same machine, a minute later: the gap holds it.
	other := makeAutostartProjectAt(t, "golf")
	autostartOnFor(t, other)
	ctx, _, _ = newTestContext(other, false)
	ctx.Now = func() time.Time { return now.Add(time.Minute) }
	err := runDispatch(ctx, []string{"high", "--autostart"})
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "hold: proj started high 1m ago (gap 5m)") || len(*started) != 1 {
		t.Fatalf("second project within the gap: %v, started %v", err, *started)
	}

	// An issue outside the next order is never auto-started.
	ctx, _, _ = newTestContext(proj, false)
	ctx.Now = func() time.Time { return now.Add(time.Hour) }
	err = runDispatch(ctx, []string{"blocked", "--autostart"})
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "depends on base (in progress)") {
		t.Fatalf("blocked: %v", err)
	}
}

// makeAutostartProjectAt is another project with the same issues that
// shares the first one's state dir (the same machine).
func makeAutostartProjectAt(t *testing.T, slug string) *tracker.Project {
	t.Helper()
	state := os.Getenv("XDG_STATE_HOME")
	proj := makeAutostartProject(t)
	t.Setenv("XDG_STATE_HOME", state)
	proj.Slug = slug
	return proj
}

func TestDispatch_NoQuoteHintsAutostart(t *testing.T) {
	proj := makeAutostartProject(t)
	autostartOnFor(t, proj)
	ctx, _, _ := newTestContext(proj, false)
	err := runDispatch(ctx, []string{"high"})
	if exitOf(err) != 2 || !strings.Contains(err.Error(), "Auto-start is on for this project: issue-cli --project proj autostart next") {
		t.Fatalf("dispatch without --quote: %v", err)
	}
}

// --max-load sets the window's load limit; status names it, and the
// default reads as the core count.
func TestAutostart_MaxLoad(t *testing.T) {
	proj := makeAutostartProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"on", "--quote", "use the CPU", "--max-load", "3.5"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "load under 3.5") {
		t.Errorf("on = %q", stdout.String())
	}
	if st, _ := autostart.Load(yoloRoot(proj), proj.Slug); st.MaxLoad != 3.5 {
		t.Fatalf("max_load = %v", st.MaxLoad)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	runAutostart(ctx, []string{"next"})
	if got := strings.TrimSpace(stdout.String()); got != "hold: load 4.0 on 14 cores (max 3.5)" {
		t.Errorf("next = %q", got)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	runAutostart(ctx, []string{"status"})
	if !strings.Contains(stdout.String(), "memory under 50% · load under 3.5 ·") {
		t.Errorf("status = %q", stdout.String())
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"on", "--quote", "x", "--max-load", "-1"}); err == nil {
		t.Error("negative --max-load accepted")
	}
}
