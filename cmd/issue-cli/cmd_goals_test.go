package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/goals"
	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func goalNum(v float64) *float64 { return &v }

// goalsProject has HQ, Product and Marketing with goals, a role prompt file
// and a metric file for Marketing's file goal.
func goalsProject(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(issues, "seo-pass.md"), "---\ntitle: \"SEO pass\"\nstatus: \"idea\"\nsystem: \"SEO\"\n---\nBody.\n")
	mustWrite(t, filepath.Join(issues, "engine.md"), "---\ntitle: \"Engine\"\nstatus: \"idea\"\n---\nBody.\n")
	for _, d := range []string{"departments", "docs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(root, "departments", "marketing.md"), "You are the Marketing lead.\n")
	mustWrite(t, filepath.Join(root, "docs", "run.json"), `{"run":{"date":"2026-10-28"},"metrics":[{"id":"pages","value":30}]}`)
	return &tracker.Project{
		Name: "acme", Slug: "acme", IssueDir: issues, WorkDir: root,
		Company: &tracker.Company{Mission: "Grow", Goals: []tracker.Goal{
			{ID: "clients", Name: "Paying clients", Target: goalNum(20), Period: "quarter"},
		}},
		Product: &tracker.Department{Name: "Product", Goals: []tracker.Goal{{ID: "bugs", Target: goalNum(5), Direction: "down"}}},
		Departments: []tracker.Department{{
			Name: "Marketing", Mission: "Every site weekly", Role: "SEO and content", Prompt: "departments/marketing.md", Systems: []string{"SEO"},
			Goals: []tracker.Goal{
				{ID: "pages", Name: "Pages refreshed", Target: goalNum(40), Period: "week", Source: "file:docs/run.json#pages", RollsUp: "clients"},
				{ID: "posts", Name: "Posts", Target: goalNum(10), RollsUp: "clients"},
			},
		}},
	}
}

// goalsCtx is a test context on Thursday 2026-10-29.
func goalsCtx(t *testing.T, proj *tracker.Project, jsonOut bool) (*Context, *bytes.Buffer) {
	t.Helper()
	ctx, stdout, _ := newTestContext(proj, jsonOut)
	ctx.Now = func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }
	return ctx, stdout
}

func TestGoalsRollUpOutput(t *testing.T) {
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runGoals(ctx, nil); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "== HQ ==")
	assertContains(t, s, "Paying clients [hq/clients]  — / 20 · 75% (mean of 1 department goal) · per quarter · manual")
	assertContains(t, s, "↳ Marketing: Pages refreshed [marketing/pages]  30 / 40 · 75% · per week · 2026-10-28 · file:docs/run.json#pages")
	assertContains(t, s, "no value yet: issue-cli goal set marketing/posts <value>")
	assertContains(t, s, "== Product ==")
	assertContains(t, s, "Mission: Every site weekly")
	if strings.Contains(s, "goal set hq/clients") {
		t.Errorf("a rolled-up HQ goal should not ask for a manual value:\n%s", s)
	}
}

func TestGoalsOneDepartmentAndJSON(t *testing.T) {
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runGoals(ctx, []string{"marketing"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "== HQ ==") {
		t.Errorf("goals marketing listed HQ:\n%s", out)
	}
	if err := runGoals(ctx, []string{"sales"}); err == nil || !strings.Contains(err.Error(), "departments: hq, product, marketing") {
		t.Errorf("unknown department err = %v", err)
	}

	ctx, out = goalsCtx(t, proj, true)
	if err := runGoals(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var rep goals.Report
	if err := json.Unmarshal([]byte(out.String()), &rep); err != nil {
		t.Fatalf("goals --json: %v\n%s", err, out)
	}
	if rep.HQ == nil || len(rep.HQ.Goals[0].Children) != 2 || len(rep.Departments) != 1 {
		t.Errorf("goals --json = %+v", rep)
	}
}

func TestGoalsNothingConfigured(t *testing.T) {
	proj := listAllProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runGoals(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "No goals configured. Add them in projects.yaml")
	assertContains(t, out.String(), "rolls_up: clients")
}

func TestGoalSet(t *testing.T) {
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runGoal(ctx, []string{"set", "marketing/posts", "4", "--note", "first week"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "✓ marketing/posts = 4 (2026-10-29)")
	assertContains(t, out.String(), "4 / 10 · 40%")
	raw, err := os.ReadFile(goals.ValuesPath(proj))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(raw), `{"goal":"marketing/posts","value":4,"date":"2026-10-29","note":"first week"}`)

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"set", "marketing/nope", "1"}, "goals: hq/clients, product/bugs, marketing/pages, marketing/posts"},
		{[]string{"set", "marketing/pages", "1"}, "reads its value from file:docs/run.json#pages"},
		{[]string{"set", "marketing/posts", "lots"}, "not a number"},
		{[]string{"set", "marketing/posts", "1", "--date", "yesterday"}, "want YYYY-MM-DD"},
		{[]string{"set", "marketing/posts"}, "usage: issue-cli goal set"},
		{nil, "usage: issue-cli goal set"},
	} {
		if err := runGoal(ctx, c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("goal %v: err %v, want %q", c.args, err, c.want)
		}
	}
}

func TestGoalSetNoGoals(t *testing.T) {
	ctx, _ := goalsCtx(t, listAllProject(t), false)
	err := runGoal(ctx, []string{"set", "hq/x", "1"})
	if err == nil || !strings.Contains(err.Error(), "no goals configured") {
		t.Errorf("err = %v", err)
	}
}

func TestDepartmentShow(t *testing.T) {
	proj := goalsProject(t)
	ctx, out := goalsCtx(t, proj, false)
	if err := runDepartment(ctx, []string{"show", "Marketing"}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	assertContains(t, s, "== Marketing ==")
	assertContains(t, s, "Mission: Every site weekly")
	assertContains(t, s, "Role: SEO and content")
	assertContains(t, s, "Role prompt: "+filepath.Join(proj.WorkDir, "departments", "marketing.md"))
	assertContains(t, s, "You are the Marketing lead.")
	assertContains(t, s, "Pages refreshed [marketing/pages]")

	out.Reset()
	if err := runDepartment(ctx, []string{"show", "product"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Role prompt: none")

	proj.Departments[0].Prompt = "departments/missing.md"
	out.Reset()
	if err := runDepartment(ctx, []string{"show", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "! open ")

	ctx, out = goalsCtx(t, proj, true)
	proj.Departments[0].Prompt = "departments/marketing.md"
	if err := runDepartment(ctx, []string{"show", "marketing"}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), `"prompt_content": "You are the Marketing lead.\n"`)

	if err := runDepartment(ctx, []string{"show"}); err == nil {
		t.Error("department show with no name should fail")
	}
	if err := runDepartment(ctx, []string{"show", "sales"}); err == nil || !strings.Contains(err.Error(), "no department") {
		t.Errorf("err = %v", err)
	}
}

func TestDepartmentLine(t *testing.T) {
	proj := goalsProject(t)
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	seo, engine := tracker.FindIssue(issues, "seo-pass"), tracker.FindIssue(issues, "engine")
	if got, want := departmentLine(proj, seo), "Department: Marketing (Every site weekly) — role: departments/marketing.md · goals: issue-cli goals marketing"; got != want {
		t.Errorf("Marketing line = %q, want %q", got, want)
	}
	if got, want := departmentLine(proj, engine), "Department: Product — goals: issue-cli goals product"; got != want {
		t.Errorf("Product line = %q, want %q", got, want)
	}
	proj.Product = nil
	if got := departmentLine(proj, engine); got != "" {
		t.Errorf("no Product config: line = %q, want none", got)
	}
}
