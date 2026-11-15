package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/telemetry"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

const queueWords = "Ok, ensure you have plenty of things to iterate on"

func runQ(t *testing.T, proj *tracker.Project, args ...string) (string, error) {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, false)
	err := runQueue(ctx, args)
	return stdout.String(), err
}

func mustQ(t *testing.T, proj *tracker.Project, args ...string) string {
	t.Helper()
	out, err := runQ(t, proj, args...)
	if err != nil {
		t.Fatalf("queue %v: %v\n%s", args, err, out)
	}
	return out
}

func lastComment(t *testing.T, proj *tracker.Project, slug string) string {
	t.Helper()
	comments, _ := tracker.LoadComments(loadLeadIssue(t, proj, slug).FilePath)
	if len(comments) == 0 {
		return ""
	}
	return comments[len(comments)-1].Text
}

func TestQueue_CommandsOnASandbox(t *testing.T) {
	proj := makeAutostartProject(t)

	out := mustQ(t, proj, "add", "high", "--quote", queueWords)
	assertContains(t, out, "✓ Queued item 1: high")
	out = mustQ(t, proj, "add", "low")
	assertContains(t, out, "no owner's words on it")
	mustQ(t, proj, "add", "--idea", "a bulk review page", "--quote", queueWords, "--top")
	if _, err := runQ(t, proj, "add", "high"); err == nil || !strings.Contains(err.Error(), "already item 1") {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := runQ(t, proj, "add", "nosuch"); err == nil || !strings.Contains(err.Error(), "no issue nosuch") {
		t.Fatalf("missing slug: %v", err)
	}
	if _, err := runQ(t, proj, "add"); exitOf(err) != 2 {
		t.Fatalf("add with nothing: %v", err)
	}

	out = mustQ(t, proj, "list")
	for _, want := range []string{
		"Queue (3 active, 0 done today)",
		"3. [ ] idea: a bulk review page · an idea: file it, then issue-cli --project proj queue link 3 <slug>",
		"1. [ ] high · ready",
		"2. [ ] low · no owner's words",
		"∞ " + queue.RefillText,
		"Tick: issue-cli --project proj queue next --start",
	} {
		assertContains(t, out, want)
	}
	if strings.Index(out, "3. [ ]") > strings.Index(out, "1. [ ]") {
		t.Errorf("--top didn't put the idea first:\n%s", out)
	}

	mustQ(t, proj, "move", "low", "--top")
	mustQ(t, proj, "block", "1", "--on", "base")
	mustQ(t, proj, "quote", "2", "--quote", queueWords)
	mustQ(t, proj, "link", "3", "blocked")
	out = mustQ(t, proj, "list")
	assertContains(t, out, "1. [ ] high · blocked on base")
	assertContains(t, out, "2. [ ] low · ready")
	assertContains(t, out, "3. [ ] blocked · depends on base (in progress)")
	if strings.Index(out, "2. [ ] low") > strings.Index(out, "1. [ ] high") {
		t.Errorf("move --top:\n%s", out)
	}
	if _, err := runQ(t, proj, "block", "1", "--on", "nosuch"); err == nil {
		t.Fatal("blocked on a missing issue")
	}
	mustQ(t, proj, "unblock", "1")
	mustQ(t, proj, "drop", "3", "--reason", "folded into high")
	mustQ(t, proj, "done", "2", "--sha", "abc1234", "--note", "by hand")

	ctx, stdout, _ := newTestContext(proj, true)
	if err := runQueue(ctx, []string{"list", "--all"}); err != nil {
		t.Fatal(err)
	}
	var got queueListJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Slug != "high" || len(got.DoneToday) != 1 || got.DoneToday[0].DoneSHA != "abc1234" || len(got.All) != 3 || got.Refill == "" {
		t.Fatalf("list --json = %+v", got)
	}

	// Workers never change the queue.
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "high")
	for _, sub := range [][]string{{"add", "low"}, {"move", "1", "--top"}, {"drop", "1"}, {"next", "--start"}} {
		if _, err := runQ(t, proj, sub...); err == nil || !strings.Contains(err.Error(), "workers") {
			t.Errorf("worker %v: %v", sub, err)
		}
	}
	if _, err := runQ(t, proj, "list"); err != nil {
		t.Errorf("a worker may read the queue: %v", err)
	}
}

func TestQueueNextStart_QuoteRuleAndMarks(t *testing.T) {
	proj := makeAutostartProject(t)
	started := stubStart(t)
	mustQ(t, proj, "add", "low")
	mustQ(t, proj, "add", "high", "--quote", queueWords, "--how", "in the CEO chat")

	// low has no owner's words and auto-start is off: high starts.
	out := mustQ(t, proj, "next")
	assertContains(t, out, "start high (item 2; memory 31% < 50%, load 4.0 < 14 on 14 cores, 0 workers, no worker cap)")
	assertContains(t, out, "1. low: no owner's words")
	out = mustQ(t, proj, "next", "--start")
	if len(*started) != 1 || (*started)[0] != "high" {
		t.Fatalf("started = %v\n%s", *started, out)
	}
	assertContains(t, out, "✓ Started queue item 2: high (marked [>])")
	assertContains(t, out, "next in the queue: 1. low")
	c := lastComment(t, proj, "high")
	for _, want := range []string{`dispatch: "` + queueWords + `"`, "queue item 2, in the CEO chat", "memory 31% < 50%", "— recorded by lead-proj"} {
		assertContains(t, c, want)
	}
	q, _ := queue.Load(proj.IssueDir)
	if it := q.Find("2"); it.State != queue.Started || it.StartedBy != "lead-proj" {
		t.Fatalf("item 2 = %+v", it)
	}

	// The machine-wide gap holds the next start; low still has no words.
	_, err := runQ(t, proj, "next", "--start")
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "gap 5m") || len(*started) != 1 {
		t.Fatalf("second start: %v, started %v", err, *started)
	}
	if class, _ := classifyError(&invocation{}, err); class != telemetry.ErrHold {
		t.Errorf("a hold is classed %q, want %q", class, telemetry.ErrHold)
	}

	// Past the gap, still nothing: low has no owner's words.
	os.Remove(filepath.Join(autostart.SharedDir(), "last.json"))
	_, err = runQ(t, proj, "next", "--start")
	if exitOf(err) != 1 || !strings.Contains(err.Error(), "nothing ready to start (first: low, no owner's words") {
		t.Fatalf("no words: %v", err)
	}
	if class, _ := classifyError(&invocation{}, err); class != telemetry.ErrHold {
		t.Errorf("nothing to start is classed %q, want %q", class, telemetry.ErrHold)
	}

	// The owner's standing start: low starts on auto-start's words.
	autostartOnFor(t, proj, "--max-workers", "3")
	out = mustQ(t, proj, "next", "--start")
	if len(*started) != 2 || (*started)[1] != "low" {
		t.Fatalf("standing start: %v\n%s", *started, out)
	}
	assertContains(t, lastComment(t, proj, "low"), `dispatch: "`+autostartWords+`" (queue item 1, auto-start: in the lead chat`)

	// Only "think up more" is left.
	os.Remove(filepath.Join(autostart.SharedDir(), "last.json"))
	out = mustQ(t, proj, "next")
	assertContains(t, out, "hold: nothing open in the queue")
	assertContains(t, out, "∞ "+queue.RefillText)
}

// A worker that dies at launch is no start: the item stays open for the next
// tick, and the machine-wide gap isn't stamped.
func TestQueueNextStart_DeadLaunchLeavesTheItemOpen(t *testing.T) {
	proj := makeAutostartProject(t)
	stubDeadLaunch(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords, "--how", "in the CEO chat")
	out, err := runQ(t, proj, "next", "--start")
	if err == nil || !strings.Contains(err.Error(), "dispatching high failed") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	assertContains(t, out, "exited right after the launch")
	q, _ := queue.Load(proj.IssueDir)
	if it := q.Find("1"); it.State != queue.Open {
		t.Fatalf("item = %+v", it)
	}
	if last := lastComment(t, proj, "high"); strings.Contains(last, "dispatch:") {
		t.Fatalf("comment after a dead launch: %q", last)
	}
	stubStart(t)
	if out := mustQ(t, proj, "next", "--start"); !strings.Contains(out, "✓ Started queue item 1: high") {
		t.Fatalf("next tick: %s", out)
	}
}

func TestQueueNextStart_MachineChecks(t *testing.T) {
	proj := makeAutostartProject(t)
	started := stubStart(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords)

	cases := []struct {
		name  string
		setup func()
		want  string
	}{
		{"memory over the limit", func() {
			autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 60, CanStart: true} }
		}, "memory 60% ≥ 50%"},
		{"mem-guard says no", func() {
			autostart.ReadMemory = func() autostart.Memory {
				return autostart.Memory{Found: true, Used: 40, CanStart: false, Note: "BLOCKED"}
			}
		}, "mem-guard says no: BLOCKED"},
		{"no mem-guard", func() { autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{} } }, "mem-guard not found"},
		{"CPU load", func() {
			autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 31, CanStart: true} }
			autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 15, Cores: 14} }
		}, "load 15.0 on 14 cores (max 14)"},
	}
	for _, c := range cases {
		c.setup()
		_, err := runQ(t, proj, "next", "--start")
		if exitOf(err) != 1 || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 4, Cores: 14} }

	// The queue's own limits: a cap of 1 with one live worker holds.
	mustQ(t, proj, "limits", "--below", "40", "--max-workers", "1")
	agentrun.ListSessions = func() []agentrun.Session { return []agentrun.Session{{Name: "agent-low"}} }
	if _, err := runQ(t, proj, "next", "--start"); exitOf(err) != 1 || !strings.Contains(err.Error(), "1 workers live (cap 1)") {
		t.Errorf("cap: %v", err)
	}
	agentrun.ListSessions = func() []agentrun.Session { return nil }

	// Local CI red on main holds every start.
	wf := "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\nci:\n  run:\n    - \"true\"\n"
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	ciDir := filepath.Join(proj.WorkDir, ".ci")
	os.MkdirAll(ciDir, 0o755)
	os.WriteFile(filepath.Join(ciDir, "state.json"), []byte(`{"branch":"main","last_sha":"deadbeefcafe","state":"red"}`), 0o644)
	if _, err := runQ(t, proj, "next", "--start"); exitOf(err) != 1 || !strings.Contains(err.Error(), "local CI red on main at deadbee") {
		t.Errorf("CI red: %v", err)
	}
	os.WriteFile(filepath.Join(ciDir, "state.json"), []byte(`{"branch":"main","last_sha":"deadbeefcafe","state":"green"}`), 0o644)
	if out, err := runQ(t, proj, "next", "--start"); err != nil || len(*started) != 1 {
		t.Fatalf("green: %v %v\n%s", err, *started, out)
	}
	if !strings.Contains(lastComment(t, proj, "high"), "memory 31% < 40%") {
		t.Errorf("comment = %q", lastComment(t, proj, "high"))
	}
}

func TestQueue_BlockedItemsWaitAndShippedItemsReconcile(t *testing.T) {
	proj := makeAutostartProject(t)
	started := stubStart(t)
	mustQ(t, proj, "add", "base", "--quote", queueWords)
	mustQ(t, proj, "add", "high", "--quote", queueWords, "--blocked-on", "1")
	mustQ(t, proj, "add", "low", "--quote", queueWords)

	// base is in progress with no worker: it starts first (any status).
	mustQ(t, proj, "next", "--start")
	os.Remove(filepath.Join(autostart.SharedDir(), "last.json"))
	out := mustQ(t, proj, "next")
	assertContains(t, out, "start low (item 3")
	assertContains(t, out, "1. base: started, no live worker")
	assertContains(t, out, "2. high: blocked on #1")

	// base ships some other way (a lead's move): the list reconciles it
	// and high is free.
	issue := loadLeadIssue(t, proj, "base")
	data, _ := os.ReadFile(issue.FilePath)
	os.WriteFile(issue.FilePath, []byte(strings.Replace(string(data), `status: "in progress"`, `status: "done"`, 1)), 0o644)
	out = mustQ(t, proj, "next")
	assertContains(t, out, "start high (item 2")
	if len(*started) != 1 {
		t.Fatalf("started = %v", *started)
	}
}

// gitIn runs git in dir.
func queueGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestQueue_TransitionToDoneMarksTheItemWithItsSHA(t *testing.T) {
	proj := makeDoneParkedFixture(t, "shipping", "", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	root := filepath.Dir(proj.IssueDir)
	queueGit(t, root, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644)
	queueGit(t, root, "add", "a.txt")
	queueGit(t, root, "commit", "-q", "-m", "base")
	queueGit(t, root, "checkout", "-q", "-b", "work/cli/sample")
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o644)
	queueGit(t, root, "add", "b.txt")
	queueGit(t, root, "commit", "-q", "-m", "feat: the work")
	tip := queueGit(t, root, "rev-parse", "HEAD")
	queueGit(t, root, "checkout", "-q", "main")
	queueGit(t, root, "merge", "-q", "--ff-only", "work/cli/sample")
	// main moves on after the merge: the item keeps the work tip.
	os.WriteFile(filepath.Join(root, "c.txt"), []byte("c"), 0o644)
	queueGit(t, root, "add", "c.txt")
	queueGit(t, root, "commit", "-q", "-m", "later")

	mustQ(t, proj, "add", "cli/sample", "--quote", queueWords)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Queue item 1 marked done at "+tip[:7])
	q, _ := queue.Load(proj.IssueDir)
	if it := q.Find("1"); it.State != queue.Done || it.DoneSHA != tip || it.DoneAt.IsZero() {
		t.Fatalf("item = %+v, want done at %s", it, tip)
	}
}

func TestQueue_ShipSHAFallsBackToMain(t *testing.T) {
	root := t.TempDir()
	queueGit(t, root, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644)
	queueGit(t, root, "add", "a.txt")
	queueGit(t, root, "commit", "-q", "-m", "base")
	head := queueGit(t, root, "rev-parse", "HEAD")
	if got, _ := queueShipSHA(root, "no/branch", ""); got != head {
		t.Fatalf("no work branch: %q, want main %q", got, head)
	}
	if got, _ := queueShipSHA(t.TempDir(), "x", ""); got != "" {
		t.Fatalf("not a repo: %q", got)
	}
}

func TestQueueLine_InWhoami(t *testing.T) {
	proj := makeAutostartProject(t)
	ctx, stdout, _ := newTestContext(proj, false)
	runLeadWhoami(ctx, nil)
	assertNotContains(t, stdout.String(), "Queue:")
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	ctx, stdout, _ = newTestContext(proj, false)
	if err := runLeadWhoami(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, stdout.String(), "Queue: 1 active, next high. At each check-in: issue-cli --project proj queue next --start")
}

// queueShipRepo is a repo on main with work/<slug> one commit ahead.
func queueShipRepo(t *testing.T, root, mainBranch, slug string) (base, tip string) {
	t.Helper()
	queueGit(t, root, "init", "-q", "-b", mainBranch)
	queueGit(t, root, "config", "maintenance.auto", "false")
	queueGit(t, root, "config", "gc.auto", "0")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644)
	queueGit(t, root, "add", "a.txt")
	queueGit(t, root, "commit", "-q", "-m", "base")
	base = queueGit(t, root, "rev-parse", "HEAD")
	queueGit(t, root, "checkout", "-q", "-b", "work/"+slug)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o644)
	queueGit(t, root, "add", "b.txt")
	queueGit(t, root, "commit", "-q", "-m", "feat: the work")
	tip = queueGit(t, root, "rev-parse", "HEAD")
	queueGit(t, root, "checkout", "-q", mainBranch)
	return base, tip
}

// Reaching done with work/<slug> ahead of main stores no SHA (main's head
// has nothing to do with the issue) and a "not on main" note.
func TestQueueShipSHANotOnMain(t *testing.T) {
	proj := makeDoneParkedFixture(t, "shipping", "", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	root := filepath.Dir(proj.IssueDir)
	base, _ := queueShipRepo(t, root, "main", "cli/sample")
	if sha, note := queueShipSHA(root, "cli/sample", ""); sha != "" || note != queue.NotOnMain {
		t.Fatalf("queueShipSHA = %q, %q; want no sha and %q (main is %s)", sha, note, queue.NotOnMain, base)
	}

	mustQ(t, proj, "add", "cli/sample", "--quote", queueWords)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	assertContains(t, stdout.String(), "✓ Queue item 1 marked done (not on main)")
	q, _ := queue.Load(proj.IssueDir)
	if it := q.Find("1"); it.State != queue.Done || it.DoneSHA != "" || it.Note != queue.NotOnMain {
		t.Fatalf("item = %+v, want done, no sha, note %q", it, queue.NotOnMain)
	}
}

// The main branch is the project's: ci.branch when set, not main/master.
func TestQueueShipSHAUsesTheProjectsMainBranch(t *testing.T) {
	root := t.TempDir()
	_, tip := queueShipRepo(t, root, "trunk", "cli/sample")
	queueGit(t, root, "branch", "main") // a stale main at the base
	queueGit(t, root, "checkout", "-q", "-b", "develop")
	queueGit(t, root, "merge", "-q", "--ff-only", "work/cli/sample")
	queueGit(t, root, "checkout", "-q", "trunk")
	if sha, note := queueShipSHA(root, "cli/sample", "develop"); sha != tip || note != "" {
		t.Fatalf("merged into ci.branch develop: %q, %q; want the tip %s", sha, note, tip)
	}
	// Without ci.branch the main checkout's branch (trunk) decides: the
	// work isn't on it, whatever main holds.
	if sha, note := queueShipSHA(root, "cli/sample", ""); sha != "" || note != queue.NotOnMain {
		t.Fatalf("not on trunk: %q, %q", sha, note)
	}
}

// queue prints "done (not on main)" for such an item.
func TestQueueListShowsNotOnMain(t *testing.T) {
	proj := makeAutostartProject(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	if err := queue.Update(proj.IssueDir, func(q *queue.Queue) error {
		q.MarkDone("high", "", time.Now())
		q.Find("1").Note = queue.NotOnMain
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out := mustQ(t, proj)
	assertContains(t, out, "1. [x] done (not on main) high")
	out = mustQ(t, proj, "list", "--all")
	assertContains(t, out, "high · done (not on main)")
}

// queueDoneNotOnMain is a project whose item 1 (cli/sample) reached done
// while work/cli/sample was ahead of main: done, no SHA, "not on main".
func queueDoneNotOnMain(t *testing.T) (proj *tracker.Project, root, tip string) {
	t.Helper()
	proj = makeDoneParkedFixture(t, "shipping", "", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	root = filepath.Dir(proj.IssueDir)
	_, tip = queueShipRepo(t, root, "main", "cli/sample")
	mustQ(t, proj, "add", "cli/sample", "--quote", queueWords)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runTransition(ctx, []string{"cli/sample", "--to", "done"}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	assertContains(t, stdout.String(), "marked done (not on main)")
	return proj, root, tip
}

// The lead lands the branch by hand, then runs queue done: the item gets
// the work tip as its SHA, and the line says so.
func TestQueueDoneFillsTheSHAOnceTheBranchLands(t *testing.T) {
	proj, root, tip := queueDoneNotOnMain(t)
	q, _ := queue.Load(proj.IssueDir)
	doneAt := q.Find("1").DoneAt
	queueGit(t, root, "merge", "-q", "--ff-only", "work/cli/sample")
	out := mustQ(t, proj, "done", "1")
	assertContains(t, out, "✓ Item 1 done at "+tip[:7]+": work/cli/sample is on main")
	q, _ = queue.Load(proj.IssueDir)
	it := q.Find("1")
	if it.DoneSHA != tip || it.Note != "" || !it.DoneAt.Equal(doneAt) {
		t.Fatalf("item = %+v, want sha %s, no note, done_at kept %v", it, tip, doneAt)
	}
}

// queue list fills the SHA by itself once the branch is on main, and
// saves it.
func TestQueueListFillsAMissingSHA(t *testing.T) {
	proj, root, tip := queueDoneNotOnMain(t)
	out := mustQ(t, proj)
	assertContains(t, out, "done (not on main) cli/sample")
	queueGit(t, root, "merge", "-q", "--ff-only", "work/cli/sample")
	out = mustQ(t, proj)
	assertContains(t, out, "1. [x] "+tip[:7]+" cli/sample")
	q, _ := queue.Load(proj.IssueDir)
	if it := q.Find("1"); it.DoneSHA != tip || it.Note != "" {
		t.Fatalf("saved item = %+v, want sha %s and no note", it, tip)
	}
}

// An item reconcile marked done (the issue closed another way) gets its
// SHA too, once its branch is on main.
func TestQueueReconciledItemGetsItsSHA(t *testing.T) {
	proj := makeDoneParkedFixture(t, "shipping", "", "")
	t.Setenv("ISSUE_VIEWER_LEAD_SCOPE", "project")
	t.Setenv("ISSUE_VIEWER_ISSUE_SLUG", "")
	root := filepath.Dir(proj.IssueDir)
	_, tip := queueShipRepo(t, root, "main", "cli/sample")
	queueGit(t, root, "merge", "-q", "--ff-only", "work/cli/sample")
	mustQ(t, proj, "add", "cli/sample", "--quote", queueWords)
	if err := queue.Update(proj.IssueDir, func(q *queue.Queue) error {
		q.MarkDone("cli/sample", "", time.Now())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, mustQ(t, proj), "1. [x] "+tip[:7]+" cli/sample")
}

// While the branch is still not on main, queue done says so: no ✓.
func TestQueueDoneSaysNotOnMain(t *testing.T) {
	proj, _, _ := queueDoneNotOnMain(t)
	out := mustQ(t, proj, "done", "1")
	assertNotContains(t, out, "✓")
	assertContains(t, out, "work/cli/sample isn't on main yet")
	q, _ := queue.Load(proj.IssueDir)
	if it := q.Find("1"); it.State != queue.Done || it.DoneSHA != "" || it.Note != queue.NotOnMain {
		t.Fatalf("item = %+v, want done, no sha, %q", it, queue.NotOnMain)
	}
}

// queue done again on an item that has its SHA keeps it.
func TestQueueDoneKeepsAStoredSHA(t *testing.T) {
	proj := makeAutostartProject(t)
	mustQ(t, proj, "add", "high", "--quote", queueWords)
	mustQ(t, proj, "done", "1", "--sha", "abc1234def")
	assertContains(t, mustQ(t, proj, "done", "1"), "✓ Item 1 done")
	q, _ := queue.Load(proj.IssueDir)
	if it := q.Find("1"); it.DoneSHA != "abc1234def" {
		t.Fatalf("item = %+v, want the stored sha kept", it)
	}
}
