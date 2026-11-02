package viewer

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/autostart"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The Report's Queue card and status.md's Queue: line: absent without a
// queue; with one, the next items with their holds, blocked, done today
// with the SHA, the refill line and what queue next says. The viewer never
// writes the queue.
func TestQueueCard(t *testing.T) {
	proj := setupYOLOProject(t)
	withMockTmuxSessions(t, nil)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	orig, origLoad := autostart.ReadMemory, autostart.ReadLoad
	autostart.ReadMemory = func() autostart.Memory { return autostart.Memory{Found: true, Used: 31, CanStart: true} }
	autostart.ReadLoad = func() autostart.CPU { return autostart.CPU{Found: true, Avg1: 4, Cores: 14} }
	t.Cleanup(func() { autostart.ReadMemory, autostart.ReadLoad = orig, origLoad })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	if page := pageGet(t, ts.URL+"/p/yp/status"); strings.Contains(page, `data-testid="status.queue"`) {
		t.Fatal("Queue card drawn without a queue")
	}
	if md := pageGet(t, ts.URL+"/p/yp/status.md"); strings.Contains(md, "Queue:") {
		t.Fatal("status.md has a Queue: line without a queue")
	}

	now := time.Now()
	if err := queue.Update(proj.IssueDir, func(q *queue.Queue) error {
		q.Add(queue.Item{Slug: "shop", Quote: "go"}, queue.Where{}, now)
		q.Add(queue.Item{Idea: "a bulk review page"}, queue.Where{}, now)
		id, _ := q.Add(queue.Item{Slug: "gone/old"}, queue.Where{}, now)
		q.Find("2").BlockedOn = []string{"#1"}
		it := q.Find(strconv.Itoa(id))
		it.State, it.DoneAt, it.DoneSHA = queue.Done, now, "becb5631234"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(queue.Path(proj.IssueDir))

	page := pageGet(t, ts.URL+"/p/yp/status")
	for _, want := range []string{
		`data-testid="status.queue"`, `data-testid="status.queue.count">2<`, "1 blocked",
		`href="/p/yp/issue/shop">shop</a>`, "idea: a bulk review page", "blocked on #1",
		`data-testid="status.queue.done"`, "becb563", "∞ Out of work: think up more",
		`data-testid="status.queue.verdict">start shop (item 1)<`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("Queue card lacks %q", want)
		}
	}
	md := pageGet(t, ts.URL+"/p/yp/status.md")
	if !strings.Contains(md, "Queue: 2 active · 1 blocked · 1 done today · next shop · now: start shop (item 1) (issue-cli --project yp queue next --start)") {
		t.Errorf("status.md lacks the Queue: line:\n%s", md)
	}
	after, _ := os.ReadFile(queue.Path(proj.IssueDir))
	if string(before) != string(after) {
		t.Error("the viewer wrote the queue")
	}

	// A corrupt file: the card says so, the page still renders.
	os.WriteFile(queue.Path(proj.IssueDir), []byte("{bad"), 0o644)
	if page := pageGet(t, ts.URL+"/p/yp/status"); !strings.Contains(page, `data-testid="status.queue.error"`) {
		t.Error("corrupt queue: no error line")
	}
}
