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

	"github.com/kacperwerema/dark-factory/internal/standin"
	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// standIn is a tool on disk: start: launches a real background process and
// records it, ready: looks for its pid file, stop: kills it.
type standIn struct {
	t   *testing.T
	dir string
	p   *tracker.Project
}

func (s *standIn) path(name string) string { return filepath.Join(s.dir, name) }

func (s *standIn) count(name string) int {
	b, _ := os.ReadFile(s.path(name))
	return strings.Count(string(b), "\n")
}

// running reports whether the tool's process is alive.
func (s *standIn) running() bool {
	b, err := os.ReadFile(s.path("pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return err == nil && syscall.Kill(pid, 0) == nil
}

func (s *standIn) connector() *tracker.Connector { return &s.p.Departments[0].Connectors[0] }

// memGuardSays puts a stand-in mem-guard on PATH that counts its calls.
func (s *standIn) memGuardSays(blocked bool) {
	s.t.Helper()
	bin := s.path("bin")
	os.MkdirAll(bin, 0755)
	// The call log is dir/guard, beside bin/: read from $0, so the body is
	// the same in every test and standin.Write warms it once.
	body := "echo call >> \"${0%/*}/../guard\"\necho 'ok: memory used 50% (< 75%)'\n"
	if blocked {
		body = "echo call >> \"${0%/*}/../guard\"\necho 'BLOCKED: memory used 80% (>= 75%)'\nexit 1\n"
	}
	guard := filepath.Join(bin, "mem-guard")
	os.Remove(guard)
	standin.Write(s.t, guard, body)
	s.t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func newStandIn(t *testing.T) *standIn {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	s := &standIn{t: t, dir: dir}
	issues := filepath.Join(dir, "issues")
	os.MkdirAll(issues, 0755)
	s.p = &tracker.Project{Slug: "acme", IssueDir: issues, Departments: []tracker.Department{{Name: "Marketing", Connectors: []tracker.Connector{{
		Name: "tool", Kind: "tool", Command: "true",
		PerRun: &tracker.PerRun{
			Start:   fmt.Sprintf("echo start >> %s; nohup sleep 300 >/dev/null 2>&1 & echo $! > %s; echo leaked-start-output", s.path("starts"), s.path("pid")),
			Ready:   fmt.Sprintf("test -f %s", s.path("pid")),
			Stop:    fmt.Sprintf("echo stop >> %s; [ -f %[2]s ] && kill $(cat %[2]s); rm -f %[2]s", s.path("stops"), s.path("pid")),
			Timeout: 5,
		},
	}}}}}
	readyPoll = 10 * time.Millisecond
	t.Cleanup(func() {
		readyPoll = 500 * time.Millisecond
		if b, err := os.ReadFile(s.path("pid")); err == nil {
			exec.Command("kill", strings.TrimSpace(string(b))).Run()
		}
	})
	s.memGuardSays(false)
	return s
}

func (s *standIn) hold() (*Lease, error) {
	return Hold(context.Background(), s.p, "Marketing", s.connector(), time.Now)
}

func TestConnectorStartsForARunAndStopsAfter(t *testing.T) {
	s := newStandIn(t)
	first, err := s.hold()
	if err != nil {
		t.Fatal(err)
	}
	if !s.running() || s.count("starts") != 1 {
		t.Fatalf("the run's tool isn't up: running %v, starts %d", s.running(), s.count("starts"))
	}
	second, err := s.hold()
	if err != nil {
		t.Fatal(err)
	}
	if s.count("starts") != 1 {
		t.Errorf("a second run started it again: %d starts", s.count("starts"))
	}
	if st := List(s.p, "")[0]; st.Holders != 2 || st.RunWords() != "starts per run, running (2 runs)" {
		t.Errorf("listing = %d holders, %q", st.Holders, st.RunWords())
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if !s.running() || s.count("stops") != 0 {
		t.Error("the first run out stopped it under the second")
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil { // deferred twice is harmless
		t.Fatal(err)
	}
	if s.running() || s.count("stops") != 1 {
		t.Errorf("the last run out left it: running %v, stops %d", s.running(), s.count("stops"))
	}
	st := List(s.p, "")[0]
	if st.Running || st.RunWords() != "starts per run, stopped" || st.LastRun == nil || st.LastRun.Event != RunStopped || st.Failing() {
		t.Errorf("after the run: %+v", st)
	}
	// The next run starts it again.
	l, err := s.hold()
	if err != nil || s.count("starts") != 2 {
		t.Fatalf("next run: %v, %d starts", err, s.count("starts"))
	}
	l.Release()
}

func TestConnectorStartHoldsWhenMemGuardBlocks(t *testing.T) {
	s := newStandIn(t)
	s.memGuardSays(true)
	_, err := s.hold()
	if err == nil || !strings.Contains(err.Error(), "marketing/tool stays stopped, nothing was started: BLOCKED: memory used 80%") {
		t.Fatalf("err = %v", err)
	}
	if s.count("starts") != 0 || s.running() {
		t.Error("a held run started the tool")
	}
	st := List(s.p, "")[0]
	if st.LastRun == nil || st.LastRun.Event != RunHeld || st.Running || st.Failing() {
		t.Errorf("after a hold: %+v, %+v", st, st.LastRun)
	}
	if n := len(leaseFiles(t, s)); n != 0 {
		t.Errorf("a held run left %d leases", n)
	}

	// A run joining a tool already up costs no memory, so it isn't held.
	s.memGuardSays(false)
	l, err := s.hold()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	s.memGuardSays(true)
	calls := s.count("guard")
	joined, err := s.hold()
	if err != nil {
		t.Fatalf("a run joining a live tool was held: %v", err)
	}
	defer joined.Release()
	if s.count("guard") != calls {
		t.Error("a run joining a live tool asked mem-guard")
	}
}

func leaseFiles(t *testing.T, s *standIn) []string {
	t.Helper()
	names, _ := filepath.Glob(filepath.Join(runDir(s.p, "Marketing", s.connector()), "leases", "run-*"))
	return names
}

func TestConnectorStopsWhenTheRunFails(t *testing.T) {
	check := func(t *testing.T, s *standIn, err error, want string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %q", err, want)
		}
		if s.running() {
			t.Error("the tool is left running")
		}
		if s.count("stops") != 1 {
			t.Errorf("stop: ran %d times", s.count("stops"))
		}
		st := List(s.p, "")[0]
		if st.Running || st.LastRun == nil || st.LastRun.Event != RunStartFailed || st.Problem() != "start failing" {
			t.Errorf("after a failed start: %+v, %+v", st, st.LastRun)
		}
	}
	t.Run("start fails after launching", func(t *testing.T) {
		s := newStandIn(t)
		s.connector().PerRun.Start += "; exit 3"
		_, err := s.hold()
		check(t, s, err, "start: exit 3")
	})
	t.Run("ready never passes", func(t *testing.T) {
		s := newStandIn(t)
		s.connector().PerRun.Ready = "false"
		s.connector().PerRun.Timeout = 1
		_, err := s.hold()
		check(t, s, err, "did not start (ready:")
	})
	t.Run("the run is cancelled while starting", func(t *testing.T) {
		s := newStandIn(t)
		s.connector().PerRun.Ready = "false"
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { // the signal lands once the tool is launching
			for !fileThere(s.path("pid")) {
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
		}()
		_, err := Hold(ctx, s.p, "Marketing", s.connector(), time.Now)
		check(t, s, err, "did not start")
	})
	t.Run("stop fails, the sweep tries again", func(t *testing.T) {
		s := newStandIn(t)
		good := s.connector().PerRun.Stop
		s.connector().PerRun.Stop = "exit 4"
		l, err := s.hold()
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Release(); err == nil || !strings.Contains(err.Error(), "stop: exit 4") {
			t.Fatalf("release = %v", err)
		}
		if p := List(s.p, "")[0].Problem(); p != "stop failing" {
			t.Errorf("problem = %q", p)
		}
		if errs := Sweep(s.p, "", time.Now); len(errs) != 1 {
			t.Errorf("sweep errs = %v", errs)
		}
		b, _ := os.ReadFile(RunLogPath(s.p))
		if n := strings.Count(string(b), RunStopFailed); n != 1 {
			t.Errorf("a failing stop logged %d lines, want one per streak", n)
		}
		s.connector().PerRun.Stop = good
		if errs := Sweep(s.p, "", time.Now); len(errs) != 0 {
			t.Fatal(errs)
		}
		if st := List(s.p, "")[0]; s.running() || st.Running || st.Failing() {
			t.Errorf("after the sweep: running %v, %+v", s.running(), st)
		}
	})
}

func TestConnectorSweepStopsWhatADeadRunLeft(t *testing.T) {
	if os.Getenv("CONNECTOR_HOLD_HELPER") != "" {
		return
	}
	s := newStandIn(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestConnectorHoldHelper$")
	cmd.Env = append(os.Environ(), "CONNECTOR_HOLD_HELPER=1", "HELPER_DIR="+s.dir, "HELPER_ISSUES="+s.p.IssueDir)
	cmd.Stdout = nil
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	deadline := time.Now().Add(30 * time.Second)
	for !fileThere(s.path("held")) {
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			t.Fatal("the run never took the tool")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if errs := Sweep(s.p, "", time.Now); len(errs) != 0 || !s.running() {
		t.Fatalf("the sweep touched a live run: %v, running %v", errs, s.running())
	}
	if st := List(s.p, "")[0]; st.Holders != 1 {
		t.Errorf("holders = %d", st.Holders)
	}
	if err := cmd.Process.Kill(); err != nil { // SIGKILL: no Release
		t.Fatal(err)
	}
	cmd.Wait()
	if !s.running() {
		t.Fatal("the tool should outlive the killed run")
	}
	if errs := Sweep(s.p, "", time.Now); len(errs) != 0 {
		t.Fatal(errs)
	}
	if s.running() || s.count("stops") != 1 {
		t.Errorf("after the sweep: running %v, stops %d", s.running(), s.count("stops"))
	}
	if n := len(leaseFiles(t, s)); n != 0 {
		t.Errorf("%d leases left", n)
	}
}

func fileThere(path string) bool { _, err := os.Stat(path); return err == nil }

// TestConnectorHoldHelper is the run TestConnectorSweepStopsWhatADeadRunLeft
// kills: it takes the tool and waits for SIGKILL.
func TestConnectorHoldHelper(t *testing.T) {
	if os.Getenv("CONNECTOR_HOLD_HELPER") == "" {
		t.Skip("helper process")
	}
	s := &standIn{t: t, dir: os.Getenv("HELPER_DIR")}
	s.p = &tracker.Project{Slug: "acme", IssueDir: os.Getenv("HELPER_ISSUES"), Departments: []tracker.Department{{Name: "Marketing", Connectors: []tracker.Connector{{
		Name: "tool", Kind: "tool", Command: "true",
		PerRun: &tracker.PerRun{
			Start: fmt.Sprintf("echo start >> %s; nohup sleep 300 >/dev/null 2>&1 & echo $! > %s", s.path("starts"), s.path("pid")),
			Ready: fmt.Sprintf("test -f %s", s.path("pid")), Stop: "true", Timeout: 5,
		},
	}}}}}
	if _, err := s.hold(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(s.path("held"), nil, 0644)
	time.Sleep(time.Minute)
}

func TestConnectorLeavesTheOwnersToolRunning(t *testing.T) {
	s := newStandIn(t)
	if err := exec.Command("/bin/sh", "-c", "nohup sleep 300 >/dev/null 2>&1 & echo $! > "+s.path("pid")).Run(); err != nil {
		t.Fatal(err)
	}
	s.memGuardSays(true) // the owner's tool costs nothing new
	l, err := s.hold()
	if err != nil {
		t.Fatal(err)
	}
	if s.count("starts") != 0 {
		t.Error("the engine started a tool that was already up")
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if errs := Sweep(s.p, "", time.Now); len(errs) != 0 {
		t.Fatal(errs)
	}
	if !s.running() || s.count("stops") != 0 {
		t.Errorf("the owner's tool: running %v, stops %d", s.running(), s.count("stops"))
	}
}

func TestConnectorCheckWhileStopped(t *testing.T) {
	s := newStandIn(t)
	s.connector().Check = "echo ran >> " + s.path("checks") + "; test -f " + s.path("pid")
	r := Check(context.Background(), s.p, "Marketing", s.connector(), time.Now)
	if r.Result != ResultStopped || s.count("checks") != 0 || s.count("starts") != 0 {
		t.Fatalf("stopped tool: %+v, checks %d, starts %d", r, s.count("checks"), s.count("starts"))
	}
	if err := Append(s.p, r); err != nil {
		t.Fatal(err)
	}
	if st := List(s.p, "")[0]; st.Failing() {
		t.Errorf("a stopped tool reads as failing: %+v", st)
	}
	l, err := s.hold()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if r := Check(context.Background(), s.p, "Marketing", s.connector(), time.Now); r.Result != ResultOK || s.count("checks") != 1 {
		t.Errorf("a held tool isn't checked: %+v", r)
	}
}

func TestConnectorRunNeedsAllThreeCommands(t *testing.T) {
	s := newStandIn(t)
	s.connector().PerRun.Ready = " "
	s.connector().PerRun.Stop = ""
	if _, err := s.hold(); err == nil || !strings.Contains(err.Error(), "per_run: needs ready:, stop:") {
		t.Errorf("err = %v", err)
	}
	if s.count("starts") != 0 {
		t.Error("a partial block started the tool")
	}
}

func TestCodexAndClaudeWaitForAPerRunTool(t *testing.T) {
	servers := []Server{{Name: "a", Command: "issue-cli"}, {Name: "b", Command: "issue-cli", StartSeconds: 60}}
	flags := strings.Join(CodexFlags(servers), " ")
	if !strings.Contains(flags, "mcp_servers.b.startup_timeout_sec=70") || strings.Contains(flags, "mcp_servers.a.startup") {
		t.Errorf("codex flags = %s", flags)
	}
	if got := strings.Join(ClaudeStartEnv(servers), " "); got != "MCP_TIMEOUT=70000" {
		t.Errorf("claude env = %q", got)
	}
	if got := ClaudeStartEnv(servers[:1]); got != nil {
		t.Errorf("no per-run tool, env = %v", got)
	}
}

// TestCodexFlagsPerRunToolTimeout: a per_run: server's first tool call may
// carry the tool's start, so codex gives its calls its own 60 s plus the
// start's wait and margin; any other server keeps codex's default.
func TestCodexFlagsPerRunToolTimeout(t *testing.T) {
	servers := []Server{{Name: "a", Command: "issue-cli"}, {Name: "b", Command: "issue-cli", StartSeconds: 60}}
	flags := strings.Join(CodexFlags(servers), " ")
	if !strings.Contains(flags, "mcp_servers.b.tool_timeout_sec=130") || strings.Contains(flags, "mcp_servers.a.tool_timeout") {
		t.Errorf("codex flags = %s", flags)
	}
}
