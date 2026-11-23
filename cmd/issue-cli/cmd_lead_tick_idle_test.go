package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/outputs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// nightWorkflow has a report type whose human-testing gate needs its
// output on the shelf (the bundled development template's rule).
const nightWorkflow = "statuses:\n  - name: \"idea\"\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"human-testing\"\n  - name: \"done\"\n" +
	"default_type: feature\ntypes:\n  feature:\n    path: [idea, backlog, in progress, done]\n  report:\n    path: [idea, in progress, human-testing, done]\n" +
	"    transitions:\n      - from: \"in progress\"\n        to: \"human-testing\"\n        actions:\n          - type: \"validate\"\n            rule: \"has_output: report\"\n"

// nightProject is the dark-factory board of 2026-11-20 at 22:20: two
// report workers finished (report: comments with follow-ups, outputs on
// the shelf), one at human-testing, one at idea waiting on an owner box;
// both sessions idle and live; an empty queue; YOLO and auto-start on. It
// returns the live sessions, which a close removes.
func nightProject(t *testing.T) (*tracker.Project, *[]string) {
	t.Helper()
	proj, _, closed := tickProject(t)
	if err := os.WriteFile(proj.WorkflowFile, []byte(nightWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	retroFile(t, proj, "20261101-090000-r1.md", "", "2026-11-01T09:00:00Z", "Base workflow: fine")
	for _, name := range []string{"low", "blocked", "base", "high", "claimed"} {
		os.Remove(filepath.Join(proj.IssueDir, name+".md"))
	}
	write := func(slug, status, extra string) {
		body := "---\ntitle: \"" + slug + "\"\nstatus: \"" + status + "\"\ntype: \"report\"\n---\n\nResearch it.\n" + extra
		if err := os.WriteFile(filepath.Join(proj.IssueDir, slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lead-floor", "human-testing", "")
	write("payments", "idea", "\n## Research\n- [ ] Legal checklist: which items need a lawyer (owner)\n")
	file := filepath.Join(t.TempDir(), "r.md")
	if err := os.WriteFile(file, []byte("# report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 20, 22, 0, 0, 0, time.Local)
	for _, slug := range []string{"lead-floor", "payments"} {
		if _, err := outputs.Add(proj, outputs.AddInput{Department: "product", Title: "Report " + slug, Kind: "report", File: file, Issue: slug}, at); err != nil {
			t.Fatal(err)
		}
	}
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	for slug, text := range map[string]string{
		"lead-floor": "report: lead floor 446 report is on the shelf. Follow-ups for the lead to file:\n- tick news for reports\n- idle check\n- free owner-waiting slots",
		"payments":   "report: payments module report is on the shelf. Follow-ups (11 proposed issues, sizes S/M/L, section 6): kit core; Stripe adapter; Paddle.",
	} {
		issue := tracker.FindIssue(issues, slug)
		if err := tracker.AddCommentBy(issue.FilePath, 0, text, "cli", tracker.AgentSessionName(slug)); err != nil {
			t.Fatal(err)
		}
	}

	// The day's queue: its last item shipped at 23:00.
	writeIssue(t, proj, "shipped", "done")
	mustQ(t, proj, "add", "shipped", "--quote", queueWords)

	live := []string{tracker.AgentSessionName("lead-floor"), tracker.AgentSessionName("payments")}
	origList, origSweep, origClose, origIdle := agentrun.ListSessions, sweepFinished, closeWorkerSession, idleWorkerSessions
	t.Cleanup(func() {
		agentrun.ListSessions, sweepFinished, closeWorkerSession, idleWorkerSessions = origList, origSweep, origClose, origIdle
	})
	agentrun.ListSessions = func() []agentrun.Session {
		var out []agentrun.Session
		for _, n := range live {
			out = append(out, agentrun.Session{Name: n})
		}
		return out
	}
	// Both agents sit at their prompt: the real sweep's choice among them
	// is the shelf's (agentrun.ShelvedReports).
	sweepFinished = func(p *tracker.Project, issues []*tracker.Issue, _ map[string]bool) []agentrun.Finished {
		var out []agentrun.Finished
		shelved := agentrun.ShelvedReports(p, issues)
		for _, n := range live {
			if r, ok := shelved[n]; ok {
				out = append(out, agentrun.Finished{Name: n, Slug: r.Slug, Shelved: agentrun.OutputLabel(r.Output)})
			}
		}
		return out
	}
	closeWorkerSession = func(_ *tracker.Project, name string) error {
		*closed = append(*closed, name)
		for i, n := range live {
			if n == name {
				live = append(live[:i], live[i+1:]...)
				break
			}
		}
		return nil
	}
	idleWorkerSessions = func(*tracker.Project, []*tracker.Issue) []string { return append([]string(nil), live...) }
	// Memory in the 50–75% band of the night: two workers at most.
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 40, CanStart: true} }
	autostartOnFor(t, proj)
	yoloOnFor(t, proj)
	return proj, &live
}

// The night of 2026-11-20 replayed: the first tick says both reports'
// follow-ups and frees both idle report sessions; with the queue empty it
// queues a sweep (a second one the same day, the first having run);
// an hour on with nothing started it wakes the lead with the idle line,
// and again the next hour.
func TestLeadTickReplaysTheNightOf1120(t *testing.T) {
	proj, live := nightProject(t)
	stubNoStart(t)
	t0 := time.Date(2026, 11, 20, 22, 20, 0, 0, time.Local)

	out, news := watchTickNews(t, proj, t0)
	for _, want := range []string{
		"report: lead-floor's report has 3 follow-ups to file: lead floor 446 report is on the shelf.",
		"report: payments's report has 11 follow-ups to file: payments module report is on the shelf.",
		"closed: agent-lead-floor (lead-floor waits on the owner's review at human-testing",
		"closed: agent-payments (payments waits on the owner's review at idea",
	} {
		assertContains(t, out, want)
	}
	if news < 2 {
		t.Fatalf("the reports are no news (%d):\n%s", news, out)
	}
	if len(*live) != 0 {
		t.Fatalf("sessions still live after the tick: %v\n%s", *live, out)
	}

	// The day's first sweep (queued by that tick) ran and closed; the
	// queue is empty again, so the next tick queues another.
	assertContains(t, out, "queued sweep product/queue-sweep-2026-11-20 as item")
	closeSweeps(t, proj)
	out, _ = watchTickNews(t, proj, t0.Add(10*time.Minute))
	assertContains(t, out, "queue: nothing open left")
	assertContains(t, out, "queued sweep product/queue-sweep-2026-11-20-2 as item")
	closeSweeps(t, proj)

	// No idle line until the idle time is up.
	if out, _ := watchTickNews(t, proj, t0.Add(50*time.Minute)); strings.Contains(out, "idle:") {
		t.Fatalf("idle before the idle time:\n%s", out)
	}
	closeSweeps(t, proj)
	out, news = watchTickNews(t, proj, t0.Add(61*time.Minute))
	assertContains(t, out, "idle: nothing started for 1h1m (since 22:20) with room to start (the factory runs 24/7): the queue is empty: file work and queue it")
	if news == 0 {
		t.Fatalf("idle is no news:\n%s", out)
	}
	closeSweeps(t, proj)
	out, _ = watchTickNews(t, proj, t0.Add(90*time.Minute))
	if strings.Contains(out, "idle:") {
		t.Fatalf("idle said twice in one stretch:\n%s", out)
	}
	// Four sweeps a day at most: none more today.
	if strings.Contains(out, "queued sweep") {
		t.Fatalf("a fifth sweep the same day:\n%s", out)
	}
	assertContains(t, firstOut(watchTickNews(t, proj, t0.Add(121*time.Minute))), "idle: nothing started for 2h1m (since 22:20)")
}

func firstOut(s string, _ int) string { return s }

// closeSweeps moves every queue-sweep run to done and drops its item, as
// the lead does once it filed the proposals.
func closeSweeps(t *testing.T, proj *tracker.Project) {
	t.Helper()
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	for _, issue := range issues {
		if !strings.Contains(issue.Slug, "queue-sweep") || issue.Status == "done" {
			continue
		}
		data, err := os.ReadFile(issue.FilePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(issue.FilePath, []byte(strings.Replace(string(data), "status: \""+issue.Status+"\"", "status: \"done\"", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// stubNoStart makes every start fail loudly: nothing in the replay may
// start (an item queued by the tick waits on auto-start's words and the
// test's tick holds it by the gap).
func stubNoStart(t *testing.T) {
	t.Helper()
	orig := startAgent
	startAgent = func(_ *tracker.Project, spec agentrun.Spec) agentrun.Response {
		t.Errorf("started %s", spec.IssueSlug)
		return agentrun.Response{Status: "error"}
	}
	t.Cleanup(func() { startAgent = orig })
}

// Idle is said only when there is room: a machine hold (memory) or a cap
// of busy workers is not idle, however long nothing started.
func TestLeadTickIdleNotWhenBusy(t *testing.T) {
	proj, live := nightProject(t)
	stubNoStart(t)
	t0 := time.Date(2026, 11, 20, 22, 20, 0, 0, time.Local)
	watchTickNews(t, proj, t0)
	*live = nil
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 70, CanStart: true} }
	writeIssue(t, proj, "next", "backlog")
	mustQ(t, proj, "add", "next", "--quote", queueWords)
	if out, _ := watchTickNews(t, proj, t0.Add(3*time.Hour)); strings.Contains(out, "idle:") {
		t.Fatalf("idle under a memory hold:\n%s", out)
	}
}

func TestFollowUpCount(t *testing.T) {
	for text, want := range map[string]int{
		"report: done. Follow-ups (11 proposed issues, section 6): a; b":    11,
		"report: done; follow-ups: a; b; c. Owner choices: section 7.":      3,
		"report: done; follow-ups: none":                                    0,
		"report: done.\nFollow-ups:\n- one\n- two\n\nThat's all.":           2,
		"report: the findings, nothing to file":                             0,
		"report: x. Follow-ups for the lead to file: kit core; Stripe; P24": 3,
	} {
		if got := followUpCount(text); got != want {
			t.Errorf("followUpCount(%q) = %d, want %d", text, got, want)
		}
	}
}

// The worker cap filled by sessions sitting idle at their prompt is room:
// the idle line names them. leads.idle_minutes sets the stretch, and a
// negative value turns the line off.
func TestLeadTickIdleNamesIdleSessionsOnTheSlots(t *testing.T) {
	proj, live := nightProject(t)
	stubNoStart(t)
	t0 := time.Date(2026, 11, 20, 22, 20, 0, 0, time.Local)
	watchTickNews(t, proj, t0) // closes the two shelved reports
	if ctx, out, _ := newTestContext(proj, false); runAutostart(ctx, []string{"off"}) != nil {
		t.Fatalf("autostart off:\n%s", out)
	}
	autostartOnFor(t, proj, "--max-workers", "1")
	writeIssue(t, proj, "stuck", "in progress")
	writeIssue(t, proj, "next", "backlog")
	mustQ(t, proj, "add", "next", "--quote", queueWords)
	*live = []string{tracker.AgentSessionName("stuck")}
	wf := nightWorkflow + "leads:\n  idle_minutes: 30\n"
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := watchTickNews(t, proj, t0.Add(31*time.Minute))
	assertContains(t, out, "idle: nothing started for 31m (since 22:20) with room to start (the factory runs 24/7): the worker slots are held by idle sessions (agent-stuck)")

	if err := os.WriteFile(proj.WorkflowFile, []byte(nightWorkflow+"leads:\n  idle_minutes: -1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := watchTickNews(t, proj, t0.Add(65*time.Minute)); strings.Contains(out, "idle:") {
		t.Fatalf("idle with idle_minutes: -1:\n%s", out)
	}
}

// yoloGateWorkflow is nightWorkflow with the owner's approval on a
// report's human-testing → done, the gate YOLO passes.
const yoloGateWorkflow = nightWorkflow + "      - from: \"human-testing\"\n        to: \"done\"\n        actions:\n          - type: \"require_human_approval\"\n            status: \"done\"\n"

// Under YOLO a finished report whose worker has gone moves through its
// human-testing gate at the next tick; one whose worker is live, an
// experiment's, and one nobody worked (no assignee) stay.
func TestLeadTickMovesThroughAYoloGateOnceTheWorkerHasGone(t *testing.T) {
	proj, live := nightProject(t)
	stubNoStart(t)
	*live = nil
	if err := os.WriteFile(proj.WorkflowFile, []byte(yoloGateWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(slug, extra string) {
		body := "---\ntitle: \"" + slug + "\"\nstatus: \"human-testing\"\ntype: \"report\"\n" + extra + "---\n\nResearch it.\n"
		if err := os.WriteFile(filepath.Join(proj.IssueDir, slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("gone", "assignee: \"agent-gone\"\n")
	write("working", "assignee: \"agent-working\"\n")
	write("trial", "assignee: \"agent-trial\"\nlabels: [\"experiment\"]\n")
	write("nobody", "")
	origHas := agentrun.HasSession
	agentrun.HasSession = func(target string) bool { return strings.Contains(target, "agent-working") }
	t.Cleanup(func() { agentrun.HasSession = origHas })

	out, _ := watchTickNews(t, proj, time.Date(2026, 11, 21, 3, 0, 0, 0, time.Local))
	assertContains(t, out, "moved: gone human-testing → done (YOLO passed the gate; its worker has gone")
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	for slug, want := range map[string]string{"gone": "done", "working": "human-testing", "trial": "human-testing", "nobody": "human-testing"} {
		if got := tracker.FindIssue(issues, slug).Status; got != want {
			t.Errorf("%s at %s, want %s\n%s", slug, got, want, out)
		}
	}
}

// yolo --help says the Human Testing approval passes like any gate (D-1 of
// cli/under-yolo-transition-human-testing-documentation-dry-run-says-ready-the),
// as internal/yolo/eligible.go does.
func TestYoloHelpSaysHumanTestingPasses(t *testing.T) {
	help := strings.Join(strings.Fields(yoloCommand.LongHelp), " ")
	if strings.Contains(help, "Human Testing approval itself") {
		t.Fatalf("yolo --help still lists Human Testing among what YOLO never passes:\n%s", yoloCommand.LongHelp)
	}
	assertContains(t, help, "The Human Testing approval passes like any other gate")
	assertContains(t, help, "moved on by the lead's tick")
}
