package goalwork

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/goals"
	"github.com/kacperwerema/dark-factory/internal/queue"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

var now = time.Date(2026, 11, 16, 10, 0, 0, 0, time.Local)

func num(v float64) *float64 { return &v }

// orchard is a placeholder project with two HQ goals and a Sales goal
// filled by its weekly job, rolling up into hq/clients.
func orchard(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{
		Slug: "orchard", IssueDir: issues, WorkDir: root,
		Company: &tracker.Company{Goals: []tracker.Goal{
			{ID: "clients", Name: "Paying clients", Target: num(20)},
			{ID: "revenue", Name: "Revenue", Target: num(1000)},
		}},
	}
}

func issue(t *testing.T, p *tracker.Project, name, front string) {
	t.Helper()
	body := "---\ntitle: \"" + name + "\"\ncreated: \"2026-11-01\"\n" + front + "---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(p.IssueDir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func load(t *testing.T, p *tracker.Project) []*tracker.Issue {
	t.Helper()
	issues, err := p.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

func keys(r *Report) []string {
	var out []string
	for _, g := range r.Gaps {
		out = append(out, g.Kind+":"+g.Key)
	}
	return out
}

func TestOpenIssueCoversItsGoal(t *testing.T) {
	p := orchard(t)
	issue(t, p, "case-studies", "status: backlog\ngoal: hq/clients\n")
	issue(t, p, "old-pricing", "status: done\ngoal: hq/revenue\n")
	issue(t, p, "parked-pricing", "status: parked\ngoal: revenue\n")
	r := Find(p, load(t, p), nil, now)
	if got := strings.Join(keys(r), ","); got != "goal:hq/revenue" {
		t.Errorf("gaps = %s, want goal:hq/revenue only (closed and parked issues are no work)", got)
	}
	if r.NoGoals {
		t.Error("NoGoals on a project with goals")
	}
}

func TestQueueItemCoversItsGoal(t *testing.T) {
	p := orchard(t)
	items := []queue.Item{
		{ID: 1, Idea: "pricing page", Goal: "hq/revenue", State: queue.Open},
		{ID: 2, Idea: "case studies", Goal: "hq/clients", State: queue.Dropped},
	}
	r := Find(p, load(t, p), items, now)
	if got := strings.Join(keys(r), ","); got != "goal:hq/clients" {
		t.Errorf("gaps = %s, want goal:hq/clients (a dropped item is no work)", got)
	}
}

// AC3: a goal whose source is a job's metrics file has the job behind it,
// and its HQ goal is covered through the roll-up.
func TestGoalWithAJobHasWork(t *testing.T) {
	p := orchard(t)
	p.Departments = append(p.Departments, tracker.Department{
		Name: "Sales",
		Goals: []tracker.Goal{
			{ID: "visits", Name: "Visits", Target: num(500), RollsUp: "clients", Source: "file:" + goals.JobFileSource("Sales", "weekly-funnel") + "#visits"},
		},
		Jobs: []tracker.Job{{ID: "weekly-funnel", Name: "Weekly funnel review", Every: "week", On: "mon", At: "08:30"}},
	})
	r := Find(p, load(t, p), nil, now)
	if got := strings.Join(keys(r), ","); got != "goal:hq/revenue" {
		t.Errorf("gaps = %s, want goal:hq/revenue only: sales/visits has its job, hq/clients its child", got)
	}
}

func TestEpicNeedsAnOpenChild(t *testing.T) {
	p := orchard(t)
	issue(t, p, "busy-epic", "status: in progress\ntype: epic\ngoal: hq/clients\n")
	issue(t, p, "busy-child", "status: backlog\nparent: busy-epic\n")
	issue(t, p, "idle-epic", "status: in progress\ntype: epic\ngoal: hq/revenue\n")
	issue(t, p, "idle-child", "status: done\nparent: idle-epic\n")
	issue(t, p, "queued-epic", "status: in progress\ntype: epic\n")
	issue(t, p, "closed-epic", "status: done\ntype: epic\n")
	items := []queue.Item{{ID: 1, Slug: "queued-epic", State: queue.Open}}
	r := Find(p, load(t, p), items, now)
	if got := strings.Join(keys(r), ","); got != "epic:idle-epic" {
		t.Errorf("gaps = %s, want epic:idle-epic only", got)
	}
	if g := r.Gaps[0]; !strings.Contains(g.Fix, "--parent idle-epic") || !strings.Contains(g.Fix, "--project orchard") {
		t.Errorf("epic fix = %q", g.Fix)
	}
}

func TestResolveAndCheck(t *testing.T) {
	p := orchard(t)
	p.Product = &tracker.Department{Name: "Product", Goals: []tracker.Goal{{ID: "clients", Name: "Clients onboarded"}, {ID: "bugs"}}}
	for ref, want := range map[string]string{"hq/clients": "hq/clients", "HQ/Revenue": "hq/revenue", "bugs": "product/bugs", "clients": "", "nope": "", "hq/nope": ""} {
		got, err := Check(p, ref, now)
		if got != want || (err == nil) != (want != "") {
			t.Errorf("Check(%q) = %q, %v; want %q", ref, got, err, want)
		}
		if err != nil && !strings.Contains(err.Error(), "hq/clients, hq/revenue") {
			t.Errorf("Check(%q) error doesn't list the keys: %v", ref, err)
		}
	}
	_, err := Check(&tracker.Project{Slug: "bare", IssueDir: t.TempDir()}, "x", now)
	if err == nil || !strings.Contains(err.Error(), "no goals") {
		t.Errorf("Check on a project with no goals = %v", err)
	}
}

func TestLookFirstCapsAndSaysNoGoals(t *testing.T) {
	p := &tracker.Project{Slug: "orchard"}
	var r Report
	for _, k := range []string{"a", "b", "c", "d"} {
		r.Gaps = append(r.Gaps, Gap{Kind: KindGoal, Key: "hq/" + k, Label: strings.ToUpper(k)})
	}
	got := r.LookFirst(p, 2)
	want := "look first at: hq/a (A), hq/b (B), +2 more: issue-cli --project orchard health lint --check goal-without-work"
	if got != want {
		t.Errorf("LookFirst = %q\nwant      %q", got, want)
	}
	if got := (&Report{}).LookFirst(p, 2); got != "" {
		t.Errorf("covered LookFirst = %q, want empty", got)
	}
	if got := (&Report{NoGoals: true}).LookFirst(p, 2); !strings.HasPrefix(got, "no goals set") {
		t.Errorf("no-goals LookFirst = %q", got)
	}
}
