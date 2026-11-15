package connectors

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kacperwerema/dark-factory/internal/tracker"
	"github.com/kacperwerema/dark-factory/internal/viewerstate"
)

// sharedTool is one tool process on disk that several connectors start:
// each connector's commands write their own log (starts-<who>, stops-<who>),
// and all of them work on the one pid file, as two compose files naming
// one project would.
type sharedTool struct {
	t   *testing.T
	dir string
}

func newSharedTool(t *testing.T) *sharedTool {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := &sharedTool{t: t, dir: t.TempDir()}
	g := &standIn{t: t, dir: s.dir}
	g.memGuardSays(false)
	readyPoll = 10 * time.Millisecond
	t.Cleanup(func() {
		readyPoll = 500 * time.Millisecond
		if b, err := os.ReadFile(s.path("pid")); err == nil {
			exec.Command("kill", strings.TrimSpace(string(b))).Run()
		}
	})
	return s
}

func (s *sharedTool) path(name string) string { return filepath.Join(s.dir, name) }

func (s *sharedTool) count(name string) int {
	b, _ := os.ReadFile(s.path(name))
	return strings.Count(string(b), "\n")
}

func (s *sharedTool) running() bool {
	b, err := os.ReadFile(s.path("pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return err == nil && syscall.Kill(pid, 0) == nil
}

// connector is a per_run: connector that starts the tool, logging as who.
// pid names the tool's pid file: the same for connectors that start one
// tool, another for a tool of their own.
func (s *sharedTool) connector(who, tool, pid string) tracker.Connector {
	pidFile := s.path(pid)
	return tracker.Connector{
		Name: "analytics", Kind: "tool", Command: "true",
		PerRun: &tracker.PerRun{
			Start:   fmt.Sprintf("echo start >> %s; nohup sleep 300 >/dev/null 2>&1 & echo $! > %s", s.path("starts-"+who), pidFile),
			Ready:   fmt.Sprintf("test -f %s", pidFile),
			Stop:    fmt.Sprintf("echo stop >> %s; [ -f %[2]s ] && kill $(cat %[2]s); rm -f %[2]s", s.path("stops-"+who), pidFile),
			Timeout: 5,
			Tool:    tool,
		},
	}
}

func (s *sharedTool) project(slug string, depts ...tracker.Department) *tracker.Project {
	issues := filepath.Join(s.dir, slug, "issues")
	os.MkdirAll(issues, 0755)
	return &tracker.Project{Slug: slug, IssueDir: issues, Departments: depts}
}

func holdIn(t *testing.T, p *tracker.Project, dept int) *Lease {
	t.Helper()
	d := &p.Departments[dept]
	l, err := Hold(context.Background(), p, d.Name, &d.Connectors[0], time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func lastRun(p *tracker.Project, addr string) string {
	return LoadRuns(p)[addr].Event
}

func TestConnectorsNamingOneToolShareALease(t *testing.T) {
	s := newSharedTool(t)
	p := s.project("acme",
		tracker.Department{Name: "Marketing", Connectors: []tracker.Connector{s.connector("marketing", "analytics", "pid")}},
		tracker.Department{Name: "Sales", Connectors: []tracker.Connector{s.connector("sales", "analytics", "pid")}},
	)
	marketing := holdIn(t, p, 0)
	if !s.running() || s.count("starts-marketing") != 1 {
		t.Fatalf("Marketing's run didn't start the tool")
	}
	sales := holdIn(t, p, 1)
	if s.count("starts-sales") != 0 {
		t.Error("Sales started a tool Marketing's run already holds")
	}
	if up, runs := Up(p, "Sales", &p.Departments[1].Connectors[0]); !up || runs != 2 {
		t.Errorf("Sales sees up %v, %d runs; want both runs", up, runs)
	}
	if err := marketing.Release(); err != nil {
		t.Fatal(err)
	}
	if !s.running() || s.count("stops-marketing")+s.count("stops-sales") != 0 {
		t.Fatal("Marketing's run out stopped the tool under Sales' run")
	}
	if err := sales.Release(); err != nil {
		t.Fatal(err)
	}
	if s.running() || s.count("stops-sales") != 1 || s.count("stops-marketing") != 0 {
		t.Errorf("after the last run: running %v, Sales' stops %d, Marketing's %d; want Sales' own stop: once",
			s.running(), s.count("stops-sales"), s.count("stops-marketing"))
	}
	if got := lastRun(p, "sales/analytics"); got != RunStopped {
		t.Errorf("Sales' last run record = %q, want stopped", got)
	}
	if got := lastRun(p, "marketing/analytics"); got != RunStarted {
		t.Errorf("Marketing's last run record = %q, want started", got)
	}
}

func TestToolLeaseCrossesProjects(t *testing.T) {
	s := newSharedTool(t)
	a := s.project("acme", tracker.Department{Name: "Marketing", Connectors: []tracker.Connector{s.connector("acme", "analytics", "pid")}})
	b := s.project("orchard", tracker.Department{Name: "Marketing", Connectors: []tracker.Connector{s.connector("orchard", "analytics", "pid")}})

	first := holdIn(t, a, 0)
	second := holdIn(t, b, 0)
	if s.count("starts-acme") != 1 || s.count("starts-orchard") != 0 {
		t.Fatalf("starts: acme %d, orchard %d; want the second project to join", s.count("starts-acme"), s.count("starts-orchard"))
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*tracker.Project{a, b} {
		if errs := Sweep(p, "", time.Now); len(errs) != 0 {
			t.Fatal(errs)
		}
	}
	if !s.running() || s.count("stops-acme")+s.count("stops-orchard") != 0 {
		t.Fatal("a run out, or a sweep, stopped the tool under the other project's run")
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	if s.running() || s.count("stops-orchard") != 1 {
		t.Fatalf("the last run out left it: running %v, orchard's stops %d", s.running(), s.count("stops-orchard"))
	}

	// A run killed before its Release: the other project's sweep stops it.
	dead := holdIn(t, a, 0)
	dead.drop()
	if errs := Sweep(b, "", time.Now); len(errs) != 0 {
		t.Fatal(errs)
	}
	if s.running() {
		t.Error("the sweep left a tool nobody holds")
	}

	// The shared folder names the tool, no project.
	dir := runDir(a, "Marketing", &a.Departments[0].Connectors[0])
	if want := filepath.Join(viewerstate.Dir(), "connector-tools", "analytics"); dir != want {
		t.Errorf("folder = %s, want %s", dir, want)
	}
	if dir != runDir(b, "Marketing", &b.Departments[0].Connectors[0]) {
		t.Error("the two projects' connectors have two folders")
	}
}

func TestConnectorWithoutToolIDKeepsItsOwnLease(t *testing.T) {
	s := newSharedTool(t)
	p := s.project("acme",
		tracker.Department{Name: "Marketing", Connectors: []tracker.Connector{s.connector("marketing", "", "pid-marketing")}},
		tracker.Department{Name: "Sales", Connectors: []tracker.Connector{s.connector("sales", "", "pid-sales")}},
	)
	if got, want := runDir(p, "Marketing", &p.Departments[0].Connectors[0]), filepath.Join(viewerstate.Dir(), "connectors", "acme", "marketing-analytics"); got != want {
		t.Errorf("folder = %s, want %s (unchanged)", got, want)
	}
	marketing := holdIn(t, p, 0)
	sales := holdIn(t, p, 1)
	if s.count("starts-marketing") != 1 || s.count("starts-sales") != 1 {
		t.Fatalf("starts: marketing %d, sales %d; want one each", s.count("starts-marketing"), s.count("starts-sales"))
	}
	if err := marketing.Release(); err != nil {
		t.Fatal(err)
	}
	if s.count("stops-marketing") != 1 || s.count("stops-sales") != 0 {
		t.Errorf("stops: marketing %d, sales %d; want Marketing's own lease to stop its own tool", s.count("stops-marketing"), s.count("stops-sales"))
	}
	if err := sales.Release(); err != nil {
		t.Fatal(err)
	}
	if s.count("stops-sales") != 1 {
		t.Errorf("Sales' tool didn't stop after its run")
	}
}

func TestToolIDMustBeShort(t *testing.T) {
	s := newSharedTool(t)
	for _, id := range []string{"../escape", "Analytics", "a/b", ".hidden", strings.Repeat("a", 65)} {
		c := s.connector("bad", id, "pid")
		p := s.project("acme", tracker.Department{Name: "Marketing", Connectors: []tracker.Connector{c}})
		_, err := Hold(context.Background(), p, "Marketing", &p.Departments[0].Connectors[0], time.Now)
		if err == nil || !strings.Contains(err.Error(), "per_run: tool: must be a short id") {
			t.Errorf("tool %q: err = %v", id, err)
		}
		if runDir(p, "Marketing", &p.Departments[0].Connectors[0]) != "" {
			t.Errorf("tool %q has a folder", id)
		}
	}
	if s.count("starts-bad") != 0 {
		t.Error("a refused id started the tool")
	}
	if entries, _ := os.ReadDir(viewerstate.Dir()); len(entries) != 0 {
		t.Errorf("refused ids made state: %v", entries)
	}
	for _, id := range []string{"analytics", "openseo.v2", "a", strings.Repeat("a", 64)} {
		if why := (&tracker.PerRun{Tool: id}).ToolProblem(); why != "" {
			t.Errorf("tool %q refused: %s", id, why)
		}
	}
}
