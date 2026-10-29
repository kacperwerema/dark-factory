package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

func num(v float64) *float64 { return &v }

// project is a project rooted in a temp dir with HQ, Product and one
// department, like a projects.yaml would load.
func project(t *testing.T) *tracker.Project {
	t.Helper()
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.MkdirAll(issues, 0o755); err != nil {
		t.Fatal(err)
	}
	return &tracker.Project{
		Slug: "acme", IssueDir: issues, WorkDir: root,
		Company: &tracker.Company{Mission: "Grow", Goals: []tracker.Goal{
			{ID: "clients", Name: "Paying clients", Target: num(20)},
			{ID: "revenue", Name: "Revenue", Target: num(1000), Unit: "EUR", Period: "month"},
		}},
		Product: &tracker.Department{Name: "Product", Goals: []tracker.Goal{
			{ID: "bugs", Name: "Open bugs", Target: num(5), Direction: "down", RollsUp: "clients"},
		}},
		Departments: []tracker.Department{{Name: "Marketing", Mission: "Every site weekly", Goals: []tracker.Goal{
			{ID: "pages", Name: "Pages refreshed", Target: num(40), Period: "week", RollsUp: "clients",
				Source: "file:docs/seo/run.json#pages_refreshed"},
		}}},
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Thursday 2026-10-29: week starts Monday 10-26, month 10-01, quarter 10-01.
var now = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func TestResolveManualFileAndRollUp(t *testing.T) {
	p := project(t)
	writeFile(t, filepath.Join(p.WorkDir, "docs/seo/run.json"),
		`{"run":{"date":"2026-10-28"},"metrics":[{"id":"pages_refreshed","value":30}]}`)
	for _, e := range []Entry{
		{Goal: "product/bugs", Value: 10, Date: "2026-10-27"},
		{Goal: "hq/revenue", Value: 400, Date: "2026-09-30", Note: "September"},
	} {
		if err := Append(p, e); err != nil {
			t.Fatal(err)
		}
	}
	rep := Resolve(p, now)

	pages := rep.Goal("marketing/pages")
	if pages == nil || pages.Value == nil || *pages.Value != 30 || pages.Err != "" {
		t.Fatalf("marketing/pages = %+v", pages)
	}
	if pages.Pct() != 75 || pages.Stale {
		t.Errorf("pages progress %d stale %v, want 75 fresh", pages.Pct(), pages.Stale)
	}
	bugs := rep.Goal("product/bugs")
	if bugs.Pct() != 50 {
		t.Errorf("down goal 10 vs target 5: progress %d, want 50", bugs.Pct())
	}
	rev := rep.Goal("hq/revenue")
	if !rev.Stale || rev.Pct() != 40 || rev.ValueText() != "400 EUR" {
		t.Errorf("revenue %+v: want stale (September value in October), 40%%, 400 EUR", rev)
	}
	clients := rep.Goal("hq/clients")
	if !clients.RolledUp || clients.Pct() != 63 || len(clients.Children) != 2 || clients.RolledFrom != 2 {
		t.Errorf("clients rolled %v pct %d children %d, want mean of 50 and 75 = 63 over 2", clients.RolledUp, clients.Pct(), len(clients.Children))
	}
}

func TestResolveOwnValueBeatsRollUp(t *testing.T) {
	p := project(t)
	if err := Append(p, Entry{Goal: "hq/clients", Value: 5, Date: "2026-10-29"}); err != nil {
		t.Fatal(err)
	}
	clients := Resolve(p, now).Goal("hq/clients")
	if clients.RolledUp || clients.Pct() != 25 {
		t.Errorf("clients with its own value: rolled %v pct %d, want own 25%%", clients.RolledUp, clients.Pct())
	}
}

func TestResolveFileErrors(t *testing.T) {
	cases := map[string]struct{ body, source, want string }{
		"missing file": {"", "file:docs/none.json#x", "reading"},
		"missing id":   {`{"metrics":[{"id":"a","value":1}]}`, "file:run.json#b", `no metric "b"`},
		"not a number": {`{"metrics":[{"id":"a","value":"n/a"}]}`, "file:run.json#a", "not a number"},
		"no id":        {`{}`, "file:run.json", "want file:<path>#<metric id>"},
		"unknown kind": {``, "quickbooks", "unknown source"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := project(t)
			if c.body != "" {
				writeFile(t, filepath.Join(p.WorkDir, "run.json"), c.body)
			}
			p.Departments[0].Goals[0].Source = c.source
			g := Resolve(p, now).Goal("marketing/pages")
			if !strings.Contains(g.Err, c.want) || g.Value != nil {
				t.Errorf("err %q value %v, want an error containing %q and no value", g.Err, g.Value, c.want)
			}
		})
	}
}

func TestResolveNothingConfigured(t *testing.T) {
	p := &tracker.Project{IssueDir: t.TempDir()}
	rep := Resolve(p, now)
	if !rep.Empty() || rep.HQ != nil || len(rep.Keys()) != 0 {
		t.Errorf("empty project: %+v", rep)
	}
}

func TestResolveNoValueNoTarget(t *testing.T) {
	p := project(t)
	p.Company.Goals = append(p.Company.Goals, tracker.Goal{ID: "nps"})
	g := Resolve(p, now).Goal("hq/nps")
	if g.Value != nil || g.Progress != nil || g.ValueText() != "—" || g.Label() != "nps" {
		t.Errorf("nps %+v", g)
	}
	if g.SetCommand() != "issue-cli goal set hq/nps <value>" {
		t.Errorf("set command %q", g.SetCommand())
	}
}

func TestPeriodStart(t *testing.T) {
	sunday := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for period, want := range map[string]string{"week": "2026-10-26", "month": "2026-11-01", "quarter": "2026-10-01"} {
		if got := PeriodStart(period, sunday).Format("2006-01-02"); got != want {
			t.Errorf("%s start of %s = %s, want %s", period, sunday.Format("2006-01-02"), got, want)
		}
	}
	if !PeriodStart("none", sunday).IsZero() {
		t.Error("none has no start")
	}
}

func TestProgressBounds(t *testing.T) {
	for _, c := range []struct {
		v, target float64
		dir       string
		want      int
	}{{50, 40, "up", 100}, {-5, 40, "", 0}, {3, 5, "down", 100}, {0, 0, "up", 100}} {
		if got := progress(c.v, c.target, c.dir); got != c.want {
			t.Errorf("progress(%v, %v, %q) = %d, want %d", c.v, c.target, c.dir, got, c.want)
		}
	}
}
