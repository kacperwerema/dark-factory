package viewer

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kacperwerema/dark-factory/internal/agentrun"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// The company page at scale: the company-scale playtest scenario (15
// departments in three groups, 26 connections, handoffs and jobs) loaded as
// a project, so the Go tests and the playtest session read the same board.

// scaleScenario is the part of playtest/scenarios/<name>/scenario.yaml these
// tests read.
type scaleScenario struct {
	Project struct {
		Name, Slug, Extra string
	}
	Generate []struct {
		Count              int
		Dir, Title, Status string
	}
}

// scenarioProject builds playtest/scenarios/<name> as a project in a temp
// folder: its tracker, generated issues, workflow and projects.yaml entry.
func scenarioProject(t testing.TB, name string) tracker.Project {
	t.Helper()
	src := repoPath("playtest", "scenarios", name)
	raw, err := os.ReadFile(filepath.Join(src, "scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var sc scaleScenario
	if err := yaml.Unmarshal(raw, &sc); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	issues := filepath.Join(root, "issues")
	if err := os.CopyFS(issues, os.DirFS(filepath.Join(src, "tracker"))); err != nil {
		t.Fatal(err)
	}
	for _, g := range sc.Generate {
		for i := 1; i <= g.Count; i++ {
			title := fmt.Sprintf(g.Title, i)
			body := fmt.Sprintf("---\ntitle: %q\nstatus: %q\nsystem: %q\n---\n", title, g.Status, g.Dir)
			p := filepath.Join(issues, g.Dir, strings.ReplaceAll(strings.ToLower(title), " ", "-")+".md")
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	wf, err := os.ReadFile(filepath.Join(src, "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflow.yaml"), wf, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("projects:\n  - name: %q\n    slug: %q\n    issues: %q\n    workdir: %q\n    terminal: \"none\"\n", sc.Project.Name, sc.Project.Slug, issues, root)
	for _, line := range strings.Split(strings.TrimRight(sc.Project.Extra, "\n"), "\n") {
		cfg += "    " + line + "\n"
	}
	path := filepath.Join(root, "projects.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	projects, err := tracker.LoadProjects(path)
	if err != nil {
		t.Fatal(err)
	}
	return projects[0]
}

func timedGet(t testing.TB, url string) (string, time.Duration) {
	t.Helper()
	start := time.Now()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	d := time.Since(start)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d", url, resp.StatusCode)
	}
	return string(b), d
}

func TestCompanyAtScaleNestsGroupsAndMarksTheFloorDense(t *testing.T) {
	withMockTmuxSessions(t, nil)
	proj := scenarioProject(t, "company-scale")
	if n := len(proj.Departments); n != 15 {
		t.Fatalf("scenario has %d departments, want 15", n)
	}
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	body, _ := timedGet(t, ts.URL+"/p/acme/company?classic=1")
	for _, c := range []struct {
		what string
		n    int
	}{
		{`data-testid="company.room.group"`, 3},
		{`data-testid="company.room.group.departments"`, 3},
		{`data-testid="company.room.dept"`, 12},
		{`data-testid="company.connections.row"`, 26},
		{`data-connections data-dense`, 1},
	} {
		if got := strings.Count(body, c.what); got != c.n {
			t.Errorf("%s: %d, want %d", c.what, got, c.n)
		}
	}
	// Each group's departments are inside its room, before the next group.
	gtm, ops := strings.Index(body, `data-box="gtm"`), strings.Index(body, `data-box="ops"`)
	for _, d := range []string{"marketing", "sales", "partnerships"} {
		if i := strings.Index(body, `data-box="`+d+`"`); i < gtm || i > ops {
			t.Errorf("%s is not inside GTM's room", d)
		}
	}
	if !strings.Contains(body, "21 roads: hover, focus or tap a room or a road to see what moves on it") {
		t.Error("dense floor has no caption")
	}
}

func TestSmallCompanyFloorIsNotDense(t *testing.T) {
	for _, c := range []struct {
		depts, rows int
		want        bool
	}{
		{0, 0, false}, {3, 40, false}, {4, 7, false}, {4, 8, true}, {15, 26, true},
	} {
		if got := connDense(c.depts, c.rows); got != c.want {
			t.Errorf("connDense(%d, %d) = %v, want %v", c.depts, c.rows, got, c.want)
		}
	}
	withMockTmuxSessions(t, nil)
	proj := scenarioProject(t, "declared-connections")
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	body, _ := timedGet(t, ts.URL+"/p/acme/company?classic=1")
	if strings.Contains(body, "data-dense") {
		t.Error("a 3-department company's floor is dense")
	}
	if !strings.Contains(body, `data-testid="company.connections.focus" hidden></p>`) {
		t.Error("a 3-department company shows the focus caption before a room is picked")
	}
}

func TestMapSkylineFitsTheLot(t *testing.T) {
	proj := scenarioProject(t, "company-scale")
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	bs := mapDepartmentsOf(&proj, issues)
	if got := skylineTotal(bs); got > skylineWidth {
		t.Errorf("15 departments stand %dpx wide on a %dpx skyline", got, skylineWidth)
	}
	groups := 0
	for _, b := range bs {
		if len(b.Rooms) > 0 {
			groups++
			w := groupPad
			for _, r := range b.Rooms {
				w += r.Width + groupGap
				if r.Width < skylineMin {
					t.Errorf("%s in %s is %dpx", r.Name, b.Name, r.Width)
				}
			}
			if w != b.Width {
				t.Errorf("group %s is %dpx, its rooms take %dpx", b.Name, b.Width, w)
			}
		}
	}
	if groups != 3 {
		t.Errorf("%d groups on the lot, want 3", groups)
	}
	// A small company's skyline is left as it was.
	small := []WbBuilding{{Name: "HQ", Width: 38}, {Name: "Product", Width: 70}, {Name: "Marketing", Width: 32}}
	if got := fitSkyline(append([]WbBuilding(nil), small...)); got[1].Width != 70 || got[2].Width != 32 {
		t.Errorf("a skyline that fits was narrowed: %+v", got)
	}
}

// companyVsIssues is how many times /issues.json the company page may take
// on a slow machine (the CLAUDE.md busy-machine rule: an absolute limit for
// a quiet machine, else a cheaper request timed in the same run).
const companyVsIssues = 12

func TestCompanyPageFastAtScale(t *testing.T) {
	if testing.Short() {
		t.Skip("renders the 15-department company several times")
	}
	withMockTmuxSessions(t, nil)
	proj := scenarioProject(t, "company-scale")
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	// The floor (the default) renders a hidden inspector for every room;
	// the classic page stays at ?classic=1. Both keep the budget.
	for _, page := range []struct{ name, path string }{
		{"floor", "/p/acme/company"},
		{"classic", "/p/acme/company?classic=1"},
	} {
		_, cold := timedGet(t, ts.URL+page.path)
		best := time.Hour
		for i := 0; i < 3; i++ {
			var tries []string
			for try := 0; try < 3; try++ {
				_, base := timedGet(t, ts.URL+"/p/acme/issues.json")
				limit := max(150*time.Millisecond, companyVsIssues*base)
				_, d := timedGet(t, ts.URL+page.path)
				best = min(best, d)
				if d <= limit {
					tries = nil
					break
				}
				tries = append(tries, fmt.Sprintf("%v (limit %v: /issues.json took %v)", d, limit, base))
			}
			if tries != nil {
				t.Errorf("company page (%s): %s; want under 150 ms, or %d× /issues.json on a slow machine", page.name, strings.Join(tries, ", "), companyVsIssues)
			}
		}
		t.Logf("15 departments, %s: cold %v, warm best %v", page.name, cold, best)
	}
}

// The company page reads its department leads from the session list the
// agents model already read: a tmux has-session per room was 15 process
// starts a render, about a third of the page's time (ui/company-page-runs-
// at-its-150-ms-limit-…). The live lead still shows as live.
func TestCompanyPageAsksTmuxOnceNotPerRoom(t *testing.T) {
	proj := scenarioProject(t, "company-scale")
	lead := tracker.DepartmentLeadName(&proj, proj.Departments[0].Name)
	withMockTmuxSessions(t, []AgentSession{{Name: lead}})
	var mu sync.Mutex
	var asked []string
	orig := agentrun.HasSession
	agentrun.HasSession = func(target string) bool {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, target)
		return false
	}
	t.Cleanup(func() { agentrun.HasSession = orig })
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()
	body, _ := timedGet(t, ts.URL+"/p/acme/company?classic=1")
	var rooms []string
	for _, d := range proj.Departments {
		name := tracker.DepartmentLeadName(&proj, d.Name)
		for _, a := range asked {
			if strings.HasSuffix(a, name) {
				rooms = append(rooms, name)
			}
		}
	}
	if len(rooms) > 0 {
		t.Errorf("the page asked tmux about %d department leads one by one: %v", len(rooms), rooms)
	}
	if !strings.Contains(body, `data-testid="company.room.dept.lead.talk"`) {
		t.Errorf("the live lead %s has no Talk link", lead)
	}
}
