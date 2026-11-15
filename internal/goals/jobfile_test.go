package goals

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/jobs"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// salesProject has a Sales department whose weekly-funnel job (Mondays
// 08:30) writes the metrics file its goals read. now is Thursday
// 2026-10-29: the latest run was due Mon 10-26, the one before Mon 10-19.
func salesProject(t *testing.T) *tracker.Project {
	t.Helper()
	p := project(t)
	src := "file:" + JobFileSource("Sales", "weekly-funnel")
	p.Departments = append(p.Departments, tracker.Department{
		Name: "Sales",
		Goals: []tracker.Goal{
			{ID: "visits", Name: "Visits", Target: num(500), Period: "week", RollsUp: "clients", Source: src + "#visits"},
			{ID: "signups", Name: "Sign-ups", Target: num(25), Source: src + "#signups"},
		},
		Jobs: []tracker.Job{{ID: "weekly-funnel", Name: "Weekly funnel review", Every: "week", On: "mon", At: "08:30"}},
	})
	return p
}

func jobFilePath(p *tracker.Project) string {
	return filepath.Join(p.IssueDir, ".goals", "sales", "weekly-funnel.json")
}

func TestJobFileSourceShape(t *testing.T) {
	if got := JobFileSource("Sales", "weekly-funnel"); got != "<issues>/.goals/sales/weekly-funnel.json" {
		t.Errorf("JobFileSource = %q", got)
	}
	for path, want := range map[string]string{
		"<issues>/.goals/sales/weekly-funnel.json": "sales/weekly-funnel",
		"<issues>/.goals/sales/x/y.json":           "",
		"<issues>/.goals/sales/weekly-funnel.csv":  "",
		"departments/sales/funnel.json":            "",
		"<issues>/other/sales/weekly-funnel.json":  "",
	} {
		got, ok := JobKeyOf(path)
		if got != want || ok != (want != "") {
			t.Errorf("JobKeyOf(%q) = %q, %v; want %q", path, got, ok, want)
		}
	}
}

// AC1: a goal reading a job-written metrics file shows its value; one a
// whole run late says "no value since <date>" instead of a number.
func TestJobFileFreshShowsValue(t *testing.T) {
	p := salesProject(t)
	writeFile(t, jobFilePath(p), `{"date":"2026-10-26","metrics":[{"id":"visits","value":400},{"id":"signups","value":30}]}`)
	g := Resolve(p, now).Goal("sales/visits")
	if g == nil || g.Err != "" || g.Value == nil || *g.Value != 400 {
		t.Fatalf("sales/visits = %+v, want 400", g)
	}
	if g.Pct() != 80 || g.Stale || g.NoValueSince != "" || g.Job != "sales/weekly-funnel" || g.ValueText() != "400" {
		t.Errorf("sales/visits = %+v: want 80%%, fresh, job sales/weekly-funnel", g)
	}
}

func TestJobFileStaleShowsNoValueSince(t *testing.T) {
	p := salesProject(t)
	// The run of 10-19 wrote it, the run due 10-26 hasn't yet: the period
	// moved on (muted, number kept), no whole run missed.
	writeFile(t, jobFilePath(p), `{"date":"2026-10-19","metrics":[{"id":"visits","value":300}]}`)
	g := Resolve(p, now).Goal("sales/visits")
	if g.Value == nil || *g.Value != 300 || !g.Stale || g.NoValueSince != "" {
		t.Errorf("one run late: %+v, want 300 marked stale", g)
	}

	// Written before the 10-19 run was due: the 10-19 and 10-26 runs both
	// missed. No number, no progress, out of the roll-up.
	writeFile(t, jobFilePath(p), `{"date":"2026-10-18T21:00:00Z","metrics":[{"id":"visits","value":300},{"id":"signups","value":9}]}`)
	rep := Resolve(p, now)
	g = rep.Goal("sales/visits")
	if g.Value != nil || g.Progress != nil || g.NoValueSince != "2026-10-18T21:00:00Z" {
		t.Fatalf("a run missed: %+v, want no value since 2026-10-18", g)
	}
	if got := g.ValueText(); got != "no value since 2026-10-18T21:00:00Z" {
		t.Errorf("ValueText = %q", got)
	}
	// A goal without a period is still caught: the job decides.
	if s := rep.Goal("sales/signups"); s.Value != nil || s.NoValueSince == "" {
		t.Errorf("sales/signups (no period) = %+v, want no value since", s)
	}
	clients := rep.Goal("hq/clients")
	for _, c := range clients.Children {
		if c.Key == "sales/visits" && c.Progress != nil {
			t.Errorf("a stale child counts in the roll-up: %+v", c)
		}
	}
}

func TestJobFileMissingNamesTheJob(t *testing.T) {
	p := salesProject(t)
	g := Resolve(p, now).Goal("sales/visits")
	want := "no value yet: <issues>/.goals/sales/weekly-funnel.json is not written yet (job sales/weekly-funnel writes it with issue-cli goal write sales/weekly-funnel; next run Mon 2026-11-02 08:30)"
	if g.Value != nil || g.Err != want {
		t.Errorf("missing job file:\n got %q\nwant %q", g.Err, want)
	}
}

func TestJobFileWithoutItsJobIsReadLikeAnyFile(t *testing.T) {
	p := salesProject(t)
	p.Departments[1].Jobs = nil
	writeFile(t, jobFilePath(p), `{"date":"2026-09-01","metrics":[{"id":"signups","value":9}]}`)
	g := Resolve(p, now).Goal("sales/signups")
	if g.Value == nil || *g.Value != 9 || g.Job != "" || g.NoValueSince != "" {
		t.Errorf("no job: %+v, want the old value read as is", g)
	}
}

func TestResolvePathRefusesEscape(t *testing.T) {
	p := project(t)
	for _, bad := range []string{"<issues>/../secret.json", "<issues>/", "<issues>/.goals/../../x.json"} {
		if _, err := ResolvePath(p, bad); err == nil {
			t.Errorf("ResolvePath(%q): want refused", bad)
		}
	}
	got, err := ResolvePath(p, "<issues>/.goals/a/b.json")
	if err != nil || got != filepath.Join(p.IssueDir, ".goals", "a", "b.json") {
		t.Errorf("ResolvePath = %q, %v", got, err)
	}
	if got, _ := ResolvePath(p, "docs/run.json"); got != filepath.Join(p.Root(), "docs", "run.json") {
		t.Errorf("a root path = %q", got)
	}
	p.Departments[0].Goals[0].Source = "file:<issues>/../x.json#pages"
	if g := Resolve(p, now).Goal("marketing/pages"); !strings.Contains(g.Err, "climbs out of the issues folder") {
		t.Errorf("escaping source: %+v", g)
	}
}

func salesJob(t *testing.T, p *tracker.Project) *jobs.Ref {
	t.Helper()
	r, err := jobs.Find(p, "sales/weekly-funnel")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWriteMetricsAtomic(t *testing.T) {
	p := salesProject(t)
	r := salesJob(t, p)
	path, err := WriteMetrics(p, r, "2026-10-26", []Metric{{ID: "visits", Value: 410}, {ID: "signups", Value: 12}})
	if err != nil || path != jobFilePath(p) {
		t.Fatalf("WriteMetrics = %q, %v", path, err)
	}
	g := Resolve(p, now).Goal("sales/visits")
	if g.Value == nil || *g.Value != 410 || g.Date != "2026-10-26" {
		t.Fatalf("read back: %+v", g)
	}

	// A write that fails leaves the old file whole and no temp file
	// (jsonfile's TestWrite covers a failed rename).
	before, _ := os.ReadFile(path)
	if _, err := WriteMetrics(p, r, "2026-10-27", []Metric{{ID: "visits", Value: math.NaN()}}); err == nil || !strings.Contains(err.Error(), "NaN") {
		t.Fatalf("failed write: err %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Errorf("a failed write changed the file:\n%s", after)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}

	// The file is replaced whole: a metric not read this run is gone.
	if _, err := WriteMetrics(p, r, "2026-10-27", []Metric{{ID: "visits", Value: 2}}); err != nil {
		t.Fatal(err)
	}
	if s := Resolve(p, now).Goal("sales/signups"); s.Value != nil || !strings.Contains(s.Err, `no metric "signups"`) {
		t.Errorf("signups after a run that didn't read it: %+v", s)
	}
}

func TestWriteMetricsRefuses(t *testing.T) {
	p := salesProject(t)
	r := salesJob(t, p)
	for name, c := range map[string]struct {
		date string
		ms   []Metric
		want string
	}{
		"none":     {"2026-10-26", nil, "no metrics"},
		"bad date": {"26/10/2026", []Metric{{ID: "visits", Value: 1}}, "want YYYY-MM-DD"},
		"twice":    {"2026-10-26", []Metric{{ID: "visits", Value: 1}, {ID: "visits", Value: 2}}, "appears twice"},
		"no id":    {"2026-10-26", []Metric{{Value: 1}}, "no id"},
	} {
		if _, err := WriteMetrics(p, r, c.date, c.ms); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", name, err, c.want)
		}
	}
	if _, err := os.Stat(jobFilePath(p)); !os.IsNotExist(err) {
		t.Errorf("a refused write wrote the file: %v", err)
	}
}
