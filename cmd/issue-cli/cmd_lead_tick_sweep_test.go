package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/yolo"
)

// sweepProject is tickProject with the gap held (so the tick's own start
// leaves the queue as it is) and a typed workflow with a report type.
func sweepProject(t *testing.T) *tracker.Project {
	t.Helper()
	proj, _, _ := tickProject(t)
	wf := "statuses:\n  - name: \"idea\"\n  - name: \"backlog\"\n  - name: \"in progress\"\n  - name: \"done\"\n" +
		"default_type: feature\ntypes:\n  feature:\n    path: [idea, backlog, in progress, done]\n  report:\n    path: [idea, in progress, done]\n"
	if err := os.WriteFile(proj.WorkflowFile, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	// Memory over auto-start's line: the tick's start holds, so what it
	// queues stays open for the test to read.
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 70, CanStart: true} }
	return proj
}

func yoloOnFor(t *testing.T, proj *tracker.Project) {
	t.Helper()
	now := time.Now()
	if err := yolo.TurnOn(yoloRoot(proj), proj.Slug, yolo.State{Until: now.Add(48 * time.Hour), By: "owner", Words: "go", How: "viewer"}, now); err != nil {
		t.Fatal(err)
	}
}

func sweepItems(t *testing.T, proj *tracker.Project) []queue.Item {
	t.Helper()
	q, err := queue.Load(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []queue.Item
	for _, it := range q.Items {
		if strings.Contains(it.Slug, "sweep") {
			out = append(out, it)
		}
	}
	return out
}

// AC1: at the refill line with YOLO and auto-start on, the tick files a
// retro-sweep run (job:, run:, type report) and queues it with no quote,
// and its queue: line names the sweep and the item.
func TestLeadTickQueuesASweepAtTheRefillLine(t *testing.T) {
	proj := sweepProject(t)
	autostartOnFor(t, proj)
	yoloOnFor(t, proj)
	mustQ(t, proj, "add", "high", "--quote", queueWords)

	out := tick(t, proj)
	items := sweepItems(t, proj)
	if len(items) != 1 {
		t.Fatalf("queued sweeps %+v\n%s", items, out)
	}
	it := items[0]
	if it.Quote != "" || it.State != queue.Open {
		t.Fatalf("sweep item %+v: want open, no owner's words", it)
	}
	day := time.Now().Format("2006-01-02")
	want := "product/queue-sweep-" + day
	if it.Slug != want {
		t.Fatalf("sweep slug %q, want %q", it.Slug, want)
	}
	assertContains(t, out, "queue: 1 open item(s) left (refill at 5)")
	assertContains(t, out, "queued sweep "+want+" as item 2 (YOLO and auto-start on; it starts on auto-start's words): file its proposals when its report: lands")

	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	run, ok := issueExists(issues, want)
	if !ok {
		t.Fatalf("no run issue %s", want)
	}
	if run.Field("job") != "product/queue-sweep" || run.Field("run") != day || run.Type != "report" || run.Status != "idea" {
		t.Fatalf("run issue: job %q run %q type %q status %q", run.Field("job"), run.Field("run"), run.Type, run.Status)
	}
	assertContains(t, run.BodyRaw, "Retro sweep for product")
	assertContains(t, run.BodyRaw, "at the queue's refill line")
}

// AC2: a sweep still open holds the next one, and once it is done a second
// tick the same day queues none; the line is not said again.
func TestLeadTickQueuesOneSweepADay(t *testing.T) {
	proj := sweepProject(t)
	autostartOnFor(t, proj)
	yoloOnFor(t, proj)
	mustQ(t, proj, "add", "high", "--quote", queueWords)

	tick(t, proj)
	if out := tick(t, proj); out != "" {
		t.Fatalf("second tick printed:\n%s", out)
	}
	if n := len(sweepItems(t, proj)); n != 1 {
		t.Fatalf("%d sweeps queued while one is open", n)
	}

	// The sweep is done: today's period exists, nothing new.
	slug := sweepItems(t, proj)[0].Slug
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	run, _ := issueExists(issues, slug)
	data, err := os.ReadFile(run.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(run.FilePath, []byte(strings.Replace(string(data), "status: \"idea\"", "status: \"done\"", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	issues, _ = tracker.LoadIssues(proj.IssueDir)
	if run, _ = issueExists(issues, slug); !proj.IssueClosed(run) {
		t.Fatalf("%s at %q is not closed", slug, run.Status)
	}
	tick(t, proj)
	if n := len(sweepItems(t, proj)); n != 1 {
		t.Fatalf("%d sweeps queued on the day one ran", n)
	}
}

// AC3: YOLO off, or auto-start off or paused: the old line, nothing filed.
func TestLeadTickQueuesNoSweepWithoutYOLOAndAutostart(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(t *testing.T, proj *tracker.Project)
	}{
		{"yolo off", func(t *testing.T, proj *tracker.Project) { autostartOnFor(t, proj) }},
		{"autostart off", func(t *testing.T, proj *tracker.Project) { yoloOnFor(t, proj) }},
		{"autostart paused", func(t *testing.T, proj *tracker.Project) {
			autostartOnFor(t, proj)
			yoloOnFor(t, proj)
			if _, err := autostart.Pause(yoloRoot(proj), proj.Slug, "test", time.Now()); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := sweepProject(t)
			tc.set(t, proj)
			mustQ(t, proj, "add", "high", "--quote", queueWords)
			out := tick(t, proj)
			assertContains(t, out, "queue: 1 open item(s) left (refill at 5)")
			assertNotContains(t, out, "sweep")
			if items := sweepItems(t, proj); len(items) != 0 {
				t.Fatalf("queued %+v", items)
			}
			if m, _ := filepath.Glob(filepath.Join(proj.IssueDir, "Product", "*.md")); len(m) != 0 {
				t.Fatalf("filed %v", m)
			}
		})
	}
}

// AC4: a project with its own job on builtin:retro-sweep gets a run of
// that job, in its department.
func TestLeadTickSweepUsesTheConfiguredRetroSweepJob(t *testing.T) {
	proj := sweepProject(t)
	proj.Departments = []tracker.Department{{Name: "Ops", Jobs: []tracker.Job{{ID: "retros", Name: "Retro sweep", Every: "week", Brief: "builtin:retro-sweep"}}}}
	autostartOnFor(t, proj)
	yoloOnFor(t, proj)
	mustQ(t, proj, "add", "high", "--quote", queueWords)

	out := tick(t, proj)
	items := sweepItems(t, proj)
	want := "ops/retros-" + time.Now().Format("2006-01-02")
	if len(items) != 0 {
		t.Fatalf("an item named sweep %+v: want the job's own run", items)
	}
	assertContains(t, out, "queued sweep "+want+" as item 2")
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	run, ok := issueExists(issues, want)
	if !ok || run.Field("job") != "ops/retros" {
		t.Fatalf("run %v job %q", ok, run.Field("job"))
	}
}

// AC5: the docs say when the tick queues a sweep and who files its
// proposals.
func TestLeadTickSweepIsInTheDocs(t *testing.T) {
	for _, f := range []string{"../../docs/patterns/lead-session.md", "../../docs/agent-dispatch.md"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(data)
		for _, want := range []string{"queued sweep", "builtin:retro-sweep", "its proposals"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: no %q", f, want)
			}
		}
	}
}
