package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/telemetry"
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
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 4, Avg5: 4, Cores: 14} }
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

// stubDeadLaunch makes every start what agentrun.Start answers for an agent
// that exited at once.
func stubDeadLaunch(t *testing.T) {
	t.Helper()
	orig := startAgent
	startAgent = func(_ *tracker.Project, spec agentrun.Spec) agentrun.Response {
		return agentrun.Response{Status: "error", Session: spec.Session, Steps: []agentrun.Step{
			{Name: "Agent running", Status: "error", Detail: "claude exited right after the launch; the session was removed"}}}
	}
	t.Cleanup(func() { startAgent = orig })
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
	if class, _ := classifyError(&invocation{}, err); class != telemetry.ErrHold {
		t.Errorf("an auto-start hold is classed %q, want %q", class, telemetry.ErrHold)
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
	want := "start high (memory 31% < 50%, load 4.0/4.0 on 14 cores (max 14), 0/2 workers)\n  issue-cli --project proj dispatch high --autostart\n"
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
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 16, Avg5: 16, Cores: 14} }
	ctx, stdout, _ = newTestContext(proj, false)
	runAutostart(ctx, []string{"next"})
	if got := strings.TrimSpace(stdout.String()); got != "hold: load 16.0/16.0 on 14 cores (max 14)" {
		t.Errorf("load: %q", got)
	}
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 4, Avg5: 4, Cores: 14} }
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
	for _, w := range []string{`dispatch: "` + autostartWords + `"`, "auto-start, turned on by lead-proj in the lead chat", "memory 31% < 50%, load 4.0/4.0 on 14 cores (max 14), 0/2 workers", "— recorded by lead-proj"} {
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
	if class, _ := classifyError(&invocation{}, err); class != telemetry.ErrHold {
		t.Errorf("the machine gap is classed %q, want %q", class, telemetry.ErrHold)
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
	if got := strings.TrimSpace(stdout.String()); got != "hold: load 4.0/4.0 on 14 cores (max 3.5)" {
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

// Two projects on one machine: the heavier one's waiting ask holds the
// lighter one's start with the turn's words; once it stops asking, the
// lighter one starts.
func TestAutostart_TurnBetweenProjects(t *testing.T) {
	proj := makeAutostartProject(t)
	started := stubStart(t)
	now := time.Date(2026, 11, 15, 14, 0, 0, 0, time.Local)
	autostartOnFor(t, proj)
	atlas := makeAutostartProjectAt(t, "atlas")
	atlas.StartWeight = 3
	autostartOnFor(t, atlas)

	ctx, stdout, _ := newTestContext(atlas, false)
	ctx.Now = func() time.Time { return now }
	if err := runAutostart(ctx, []string{"next"}); err != nil || !strings.HasPrefix(stdout.String(), "start high") {
		t.Fatalf("atlas next: %v %q", err, stdout.String())
	}
	ctx, _, _ = newTestContext(proj, false)
	ctx.Now = func() time.Time { return now.Add(time.Minute) }
	err := runDispatch(ctx, []string{"high", "--autostart"})
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "hold: atlas' turn (weight 3; proj next in 2 starts)") || len(*started) != 0 {
		t.Fatalf("proj during atlas' turn: %v, started %v", err, *started)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	ctx.Now = func() time.Time { return now.Add(time.Minute) }
	runAutostart(ctx, []string{"status"})
	if !strings.Contains(stdout.String(), "share of the start gap (24h, by start_weight:): atlas ×3 0 starts · proj ×1 0 starts") {
		t.Errorf("status lacks the share:\n%s", stdout.String())
	}

	ctx, _, _ = newTestContext(atlas, false)
	ctx.Now = func() time.Time { return now.Add(2 * time.Minute) }
	if err := runDispatch(ctx, []string{"high", "--autostart"}); err != nil || len(*started) != 1 {
		t.Fatalf("atlas' start: %v, started %v", err, *started)
	}
	share, _ := autostart.LoadShare(autostart.SharedDir())
	if len(share.Starts) != 1 || share.Starts[0].Project != "atlas" || share.Credit["atlas"] >= 0 {
		t.Fatalf("share after atlas' start = %+v", share)
	}

	// atlas stops asking: 2 gaps on, proj starts.
	ctx, _, _ = newTestContext(proj, false)
	ctx.Now = func() time.Time { return now.Add(13 * time.Minute) }
	if err := runDispatch(ctx, []string{"high", "--autostart"}); err != nil || len(*started) != 2 {
		t.Fatalf("proj after atlas went quiet: %v, started %v", err, *started)
	}
}

func TestAutostartWeightCommand(t *testing.T) {
	proj := makeAutostartProject(t)
	cfg := filepath.Join(proj.WorkDir, "projects.yaml")
	orig := "projects:\n  - name: Proj\n    slug: proj\n    issues: ./issues # board\n"
	if err := os.WriteFile(cfg, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	proj.ConfigPath = cfg
	for _, args := range [][]string{{"weight"}, {"weight", "0", "--quote", "x"}, {"weight", "two", "--quote", "x"}, {"weight", "3"}} {
		ctx, _, _ := newTestContext(proj, false)
		if err := runAutostart(ctx, args); exitOf(err) != 2 {
			t.Errorf("%v: %v", args, err)
		}
	}
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"weight", "3", "--quote", "this one comes first"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(cfg); string(got) != orig+"    start_weight: 3\n" {
		t.Errorf("projects.yaml =\n%s", got)
	}
	if !strings.Contains(stdout.String(), "✓ start_weight: 3 for proj (was 1)") || !strings.Contains(stdout.String(), `"this one comes first" (set by lead-proj)`) {
		t.Errorf("weight = %q", stdout.String())
	}

	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "high")
	ctx, _, _ = newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"weight", "5", "--quote", "x"}); err == nil || !strings.Contains(err.Error(), "workers never") {
		t.Errorf("worker: %v", err)
	}
	if got, _ := os.ReadFile(cfg); string(got) != orig+"    start_weight: 3\n" {
		t.Errorf("a worker changed projects.yaml:\n%s", got)
	}
}

// A lead running with --project set to another project signs the record
// with its own name: lead-dark-factory setting atlas' weight is "set by
// lead-dark-factory", not lead-atlas (the 2026-11-15 record). Auto-start's
// on and pause carry the same name; the target's own lead keeps its name.
func TestAutostartRecordsNameTheCallingLeadNotTheTargets(t *testing.T) {
	proj := makeAutostartProjectAt(t, "atlas")
	cfg := filepath.Join(proj.WorkDir, "projects.yaml")
	if err := os.WriteFile(cfg, []byte("projects:\n  - name: Atlas\n    slug: atlas\n    issues: ./issues\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj.ConfigPath = cfg
	t.Setenv("ISSUE_VIEWER_PROJECT", "dark-factory")

	ctx, stdout, _ := newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"weight", "3", "--quote", "atlas comes first"}); err != nil {
		t.Fatal(err)
	}
	if out := stdout.String(); !strings.Contains(out, `"atlas comes first" (set by lead-dark-factory)`) || strings.Contains(out, "lead-atlas") {
		t.Errorf("weight from lead-dark-factory = %q", out)
	}
	ctx, _, _ = newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"on", "--quote", "start atlas work"}); err != nil {
		t.Fatal(err)
	}
	if st, _ := autostart.Load(yoloRoot(proj), "atlas"); st == nil || st.By != "lead-dark-factory" {
		t.Errorf("on: state = %+v, want By lead-dark-factory", st)
	}
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"pause"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "paused by lead-dark-factory") {
		t.Errorf("pause = %q", stdout.String())
	}

	// atlas' own lead still signs as lead-atlas.
	t.Setenv("ISSUE_VIEWER_PROJECT", "atlas")
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runAutostart(ctx, []string{"weight", "2", "--quote", "x"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "(set by lead-atlas)") {
		t.Errorf("weight from lead-atlas = %q", stdout.String())
	}
}

// A lead with no ISSUE_VIEWER_PROJECT (spawned before it was exported)
// signs with its tmux session name; a department lead of another project
// keeps its project's prefix.
func TestWhoAmIKeepsTheCallersOwnLeadName(t *testing.T) {
	proj := &tracker.Project{Slug: "atlas", Departments: []tracker.Department{{Name: "Marketing"}}}
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	t.Setenv("ISSUE_VIEWER_PROJECT", "")
	orig := currentTmuxNames
	t.Cleanup(func() { currentTmuxNames = orig })

	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	currentTmuxNames = func() (string, string) { return "lead-dark-factory", "" }
	if me := whoAmI(proj, nil); me.Role != tracker.RoleProjectLead || me.Name != "lead-dark-factory" {
		t.Errorf("tmux lead-dark-factory: %+v", me)
	}
	currentTmuxNames = func() (string, string) { return "", "" }
	if me := whoAmI(proj, nil); me.Name != "lead-atlas" {
		t.Errorf("no session name: %+v, want lead-atlas", me)
	}

	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "department:marketing")
	t.Setenv("ISSUE_VIEWER_PROJECT", "dark-factory")
	if me := whoAmI(proj, nil); me.Role != tracker.RoleDepartmentLead || me.Name != "lead-dark-factory-marketing" {
		t.Errorf("department lead of dark-factory: %+v", me)
	}
	t.Setenv("ISSUE_VIEWER_PROJECT", "atlas")
	if me := whoAmI(proj, nil); me.Name != "lead-atlas-marketing" {
		t.Errorf("atlas' own department lead: %+v", me)
	}
}
