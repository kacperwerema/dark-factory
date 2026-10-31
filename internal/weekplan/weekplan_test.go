package weekplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/jobs"
	"github.com/michal-franc/issue-viewer/internal/outputs"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// Saturday 2026-10-31 12:00, ISO week 44; the window starts 10-24 12:00.
var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local)

func f(v float64) *float64 { return &v }

// sandbox is a project with GTM over Marketing, goals on both, and its
// issues and logs written under t.TempDir().
func sandbox(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	p := &tracker.Project{
		Slug: "acme", Name: "Acme", IssueDir: filepath.Join(root, "issues"), WorkDir: root,
		Departments: []tracker.Department{
			{Name: "GTM", Goals: []tracker.Goal{{ID: "pipeline", Name: "New client conversations", Target: f(8), Period: "month"}},
				Jobs: []tracker.Job{{ID: JobID, Name: "Week plan and report", Every: "week", On: "mon", At: "08:00", Brief: "departments/gtm/jobs/weekly-plan.md"}}},
			{Name: "Marketing", Parent: "gtm", Systems: []string{"SEO"}, Goals: []tracker.Goal{
				{ID: "pages", Name: "Client pages refreshed", Target: f(10), Period: "week"},
				{ID: "posts", Name: "Posts published", Target: f(2), Period: "week"},
			}},
			{Name: "Finance"},
		},
	}
	if err := os.MkdirAll(p.IssueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeIssue(t *testing.T, p *tracker.Project, rel, front, body string) {
	t.Helper()
	path := filepath.Join(p.IssueDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	title := strings.TrimSuffix(filepath.Base(rel), ".md")
	data := "---\ntitle: \"" + title + "\"\n" + front + "\n---\n" + body + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func load(t *testing.T, p *tracker.Project) []*tracker.Issue {
	t.Helper()
	issues, err := tracker.LoadIssues(p.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

func gather(t *testing.T, p *tracker.Project, name string, js []jobs.Status) *Week {
	t.Helper()
	d, err := Resolve(p, name)
	if err != nil {
		t.Fatal(err)
	}
	return Gather(Input{Project: p, Issues: load(t, p), Jobs: js, Now: now}, d)
}

// A week with nothing in it says so under every heading, and names the
// command that would fill it (Settled rule "Only real data, honest empty
// states").
func TestEmptyWeekSaysSo(t *testing.T) {
	p := sandbox(t)
	w := gather(t, p, "finance", nil)
	if w.Group || len(w.Sections) != 1 || w.Key != "2026-W44" {
		t.Fatalf("week = %+v", w)
	}
	md := Markdown(w)
	for _, want := range []string{
		"# Finance · week 2026-W44", "looking back over 2026-10-24 – 2026-10-31",
		"**0 goals moved · 0 outputs shipped, 0 reviewed · 0 blocked**",
		"No week plan for 2026-W44 yet", "None configured: add `goals:` to Finance",
		"Nothing on the shelf this week", "No recurring jobs (`issue-cli jobs finance`",
		"None open (`issue-cli connections --kind request`)", "Nothing blocked.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("empty week lacks %q:\n%s", want, md)
		}
	}
}

func TestResolveRefusesHQProductAndUnknown(t *testing.T) {
	p := sandbox(t)
	for _, name := range []string{"hq", "Product", "sales", ""} {
		if _, err := Resolve(p, name); err == nil || !strings.Contains(err.Error(), "gtm (group), marketing, finance") {
			t.Errorf("Resolve(%q) = %v", name, err)
		}
	}
	if d, err := Resolve(p, "GTM"); err != nil || d.Name != "GTM" {
		t.Errorf("Resolve(GTM) = %+v %v", d, err)
	}
}

// AC1: the group's week reads its departments' goals (and how far each
// moved), outputs, jobs, handoffs and what is blocked on whom, and picks up
// the run issue's ## Week plan.
func TestGroupWeekReadsGoalsOutputsJobsHandoffsAndBlocked(t *testing.T) {
	p := sandbox(t)
	for _, e := range []goals.Entry{
		{Goal: "gtm/pipeline", Value: 1, Date: "2026-10-20"},
		{Goal: "gtm/pipeline", Value: 3, Date: "2026-10-28"},
		{Goal: "marketing/pages", Value: 4, Date: "2026-10-29"},
		{Goal: "marketing/posts", Value: 1, Date: "2026-10-01"},
	} {
		if err := goals.Append(p, e); err != nil {
			t.Fatal(err)
		}
	}
	day := func(d string) string { return d + "T10:00:00+01:00" }
	for _, e := range []outputs.Event{
		{Op: outputs.OpAdd, ID: "o1", Date: day("2026-10-27"), Department: "marketing", Title: "Acme: pricing page", Kind: "page", URL: "https://example.test/p"},
		{Op: outputs.OpAdd, ID: "o2", Date: day("2026-10-26"), Department: "marketing", Title: "Acme: launch post", Kind: "post", URL: "https://example.test/b"},
		{Op: outputs.OpReview, ID: "o2", Date: day("2026-10-28"), State: outputs.StateApproved, By: "owner", Quote: "ok"},
		{Op: outputs.OpPublish, ID: "o2", Date: day("2026-10-29"), URL: "https://example.test/b"},
		{Op: outputs.OpAdd, ID: "o3", Date: day("2026-09-01"), Department: "marketing", Title: "Old page", Kind: "page", URL: "https://example.test/o"},
		{Op: outputs.OpReview, ID: "o3", Date: day("2026-09-02"), State: outputs.StateRejected, By: "owner", Quote: "no"},
	} {
		if err := outputs.Append(p, e); err != nil {
			t.Fatal(err)
		}
	}
	writeIssue(t, p, "SEO/refresh.md", "status: \"idea\"\ndepartment: \"Marketing\"\nrequested_by: \"gtm\"", "")
	writeIssue(t, p, "SEO/wait.md", "status: \"idea\"\ndepartment: \"Marketing\"\nblocked_on: \"factory/crm\"", "")
	writeIssue(t, p, "Factory/crm.md", "status: \"in progress\"", "")
	writeIssue(t, p, "SEO/done.md", "status: \"done\"\ndepartment: \"Marketing\"\nblocked_on: \"factory/crm\"", "")
	writeIssue(t, p, "GTM/weekly-plan-2026-w44.md", "status: \"plan\"\ndepartment: \"GTM\"\njob: \"gtm/weekly-plan\"\nrun: \"2026-W44\"",
		"Brief.\n\n## Week plan\n\n### Marketing\n\n- Finish the pricing page (o1 waits on review).\n\n## Plan\n\n- [x] Week pack read")
	js := []jobs.Status{
		{Key: "gtm/weekly-plan", Name: "Week plan and report", Department: "GTM", Schedule: "every Mon 08:00", Due: true, Verdict: "waits for the owner", LastSlug: "gtm/weekly-plan-2026-w43", LastStatus: "done"},
		{Key: "finance/close", Name: "Close", Department: "Finance", Schedule: "monthly"},
	}

	w := gather(t, p, "gtm", js)
	if !w.Group || len(w.Sections) != 2 || w.Sections[0].Name != "GTM" || w.Sections[1].Name != "Marketing" {
		t.Fatalf("sections = %+v", w.Sections)
	}
	gtm, mkt := w.Sections[0], w.Sections[1]

	if g := gtm.Goals[0]; !g.Moved || g.Move != "+2 this week (from 1)" || g.Value != "3" {
		t.Errorf("pipeline moved 1 → 3: %+v", g)
	}
	if g := mkt.Goals[0]; !g.Moved || g.Move != "first value this week" {
		t.Errorf("pages has its first value: %+v", g)
	}
	if g := mkt.Goals[1]; g.Moved || g.Move != "no change recorded this week" {
		t.Errorf("posts didn't move: %+v", g)
	}
	if len(mkt.Review) != 1 || mkt.Review[0].ID != "o1" || len(mkt.Published) != 1 || mkt.Published[0].ID != "o2" ||
		len(mkt.Reviewed) != 1 || mkt.Reviewed[0].ID != "o2" {
		t.Errorf("outputs: review %+v published %+v reviewed %+v (o3 is outside the window)", mkt.Review, mkt.Published, mkt.Reviewed)
	}
	if len(gtm.Jobs) != 1 || gtm.Jobs[0].Verdict != "waits for the owner" || len(mkt.Jobs) != 0 {
		t.Errorf("only GTM's job: %+v / %+v", gtm.Jobs, mkt.Jobs)
	}
	if len(gtm.HandoffsOut) != 1 || gtm.HandoffsOut[0].Slug != "seo/refresh" || gtm.HandoffsOut[0].Other != "Marketing" ||
		len(mkt.HandoffsIn) != 1 || mkt.HandoffsIn[0].Other != "GTM" {
		t.Errorf("handoffs: gtm out %+v, marketing in %+v", gtm.HandoffsOut, mkt.HandoffsIn)
	}
	if len(mkt.Blocked) != 1 || mkt.Blocked[0].Slug != "seo/wait" || mkt.Blocked[0].On != "Product (factory/crm, in progress)" {
		t.Errorf("blocked (a done issue is not): %+v", mkt.Blocked)
	}
	want := Counts{GoalsMoved: 2, Shipped: 1, Reviewed: 1, WaitingReview: 1, Blocked: 1}
	if w.Counts != want {
		t.Errorf("counts = %+v, want %+v", w.Counts, want)
	}
	if w.PlanIssue != "gtm/weekly-plan-2026-w44" || !strings.HasPrefix(w.Plan, "### Marketing") || strings.Contains(w.Plan, "Week pack read") {
		t.Errorf("plan from %q: %q", w.PlanIssue, w.Plan)
	}
	md := Markdown(w)
	for _, want := range []string{
		"**2 goals moved · 1 output shipped, 1 reviewed · 1 blocked** · 1 waiting on the owner's review",
		"From gtm/weekly-plan-2026-w44:\n\n### Marketing",
		"- New client conversations: 3 of 8 (38%) · +2 this week (from 1) · `gtm/pipeline`",
		"Waiting on the owner's review:\n\n- o1 Acme: pricing page (page, review)",
		"- Week plan and report (`gtm/weekly-plan`), every Mon 08:00 · due: waits for the owner · last run gtm/weekly-plan-2026-w43 (done)",
		"GTM asked:\n\n- seo/refresh (refresh, idea) to Marketing",
		"- seo/wait (wait, idea): on Product (factory/crm, in progress) · blocked_on: factory/crm",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
}

// AC2's file: --write writes the report under the work files with a
// header the room reads; the same week again replaces it, other weeks stay.
func TestWriteReplacesTheWeekAndLatestReadsIt(t *testing.T) {
	p := sandbox(t)
	if Latest(p, "gtm") != nil {
		t.Fatal("no report yet")
	}
	old := filepath.Join(Dir(p, "gtm"), "2026-W43.md")
	w43 := &Week{Box: "gtm", Name: "GTM", Group: true, Key: "2026-W43", From: now.Add(-14 * 24 * time.Hour), To: now.Add(-7 * 24 * time.Hour)}
	if _, _, err := Write(p, w43); err != nil {
		t.Fatal(err)
	}
	w := gather(t, p, "gtm", nil)
	path, replaced, err := Write(p, w)
	if err != nil || replaced || path != filepath.Join(p.IssueDir, ".reports", "gtm", "2026-W44.md") {
		t.Fatalf("Write = %s %v %v", path, replaced, err)
	}
	w.Counts.Blocked = 3
	if _, replaced, err = Write(p, w); err != nil || !replaced {
		t.Fatalf("second write replaces: %v %v", replaced, err)
	}
	h := Latest(p, "gtm")
	if h == nil || h.Week != "2026-W44" || h.Counts.Blocked != 3 || h.Path != path {
		t.Fatalf("Latest = %+v", h)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("week 43's report must stay: %v", err)
	}
	if got := Weeks(p, "gtm"); strings.Join(got, ",") != "2026-W43,2026-W44" {
		t.Errorf("Weeks = %v", got)
	}
	// A report is never read as an issue.
	if issues := load(t, p); len(issues) != 0 {
		t.Errorf("reports read as issues: %d", len(issues))
	}
	// A file without the header (hand-made) is skipped by Latest.
	if err := os.WriteFile(filepath.Join(Dir(p, "gtm"), "2026-W45.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h := Latest(p, "gtm"); h == nil || h.Week != "2026-W44" {
		t.Errorf("Latest skips a headerless file: %+v", h)
	}
}

func TestValidKeyAndBodySection(t *testing.T) {
	for k, want := range map[string]bool{"2026-W44": true, "2026-W01": true, "2026-W4": false, "2026-W54": false, "../x": false, "2026-W44.md": false, "": false} {
		if ValidKey(k) != want {
			t.Errorf("ValidKey(%q) = %v", k, !want)
		}
	}
	body := "intro\n## Week plan\n\n### A\n- x\n## Next\nno"
	if got := BodySection(body, "week plan"); got != "### A\n- x" {
		t.Errorf("BodySection = %q", got)
	}
	if BodySection(body, "Missing") != "" {
		t.Error("missing section")
	}
}
