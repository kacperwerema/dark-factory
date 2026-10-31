package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/michal-franc/issue-viewer/internal/clilog"
	"github.com/michal-franc/issue-viewer/internal/telemetry"
)

// readSamples is one read-only call per command that has a read mode; each
// must be read-only by commandAccess. TestReadSamplesCoverEveryReadCommand
// makes a new read command add one, so the sandbox test runs it.
var readSamples = map[string][]string{
	"agents":          {"agents", "status"},
	"autostart":       {"autostart", "status"},
	"queue":           {"queue", "list"},
	"balance":         {"balance", "--list"},
	"bench":           {"bench", "candidates"},
	"check-script":    {"check-script", "--lib"},
	"checklist":       {"checklist", "cli/sample"},
	"clarify":         {"clarify", "cli/sample"},
	"ci":              {"ci", "status"},
	"connections":     {"connections"},
	"connectors":      {"connectors"},
	"context":         {"show", "cli/sample"},
	"data":            {"data", "list", "cli/sample"},
	"decisions":       {"decisions", "cli/sample"},
	"department":      {"department", "list"},
	"dispatch-prompt": {"dispatch-prompt", "cli/sample"},
	"experiment":      {"experiment", "candidates"},
	"postmortem":      {"postmortem", "list"},
	"goals":           {"goals"},
	"handoffs":        {"handoffs"},
	"health":          {"health", "lint"},
	"help":            {"help", "workflow"},
	"job":             {"job", "due"},
	"jobs":            {"jobs"},
	"lead":            {"lead", "whoami"},
	"list":            {"list"},
	"memory":          {"memory", "list"},
	"next":            {"next"},
	"output":          {"output", "show", "nothing"},
	"outputs":         {"outputs"},
	"privacy":         {"privacy", "check"},
	"process":         {"process", "transitions"},
	"projects":        {"projects"},
	"search":          {"search", "sample"},
	"spend":           {"spend"},
	"stats":           {"stats"},
	"status":          {"status"},
	"telemetry":       {"telemetry", "path"},
	"transition":      {"transition", "cli/sample", "--to", "in progress", "--dry-run"},
	"upgrade":         {"upgrade"},
	"version":         {"version"},
	"workflow":        {"workflow", "upgrade"},
	"yolo":            {"yolo", "status"},
}

func isWrites(rule readOnly) bool {
	return reflect.ValueOf(rule).Pointer() == reflect.ValueOf(writes).Pointer()
}

func TestEveryCommandHasAccess(t *testing.T) {
	for _, name := range commandNames() {
		if commandAccess[name] == nil {
			t.Errorf("command %q is not in commandAccess (readonly.go): mark it reads, writes or by subcommand", name)
		}
	}
	for name := range commandAccess {
		if commandRegistry[name] == nil {
			t.Errorf("commandAccess lists %q, which is not a command", name)
		}
	}
}

func TestReadSamplesCoverEveryReadCommand(t *testing.T) {
	for name, rule := range commandAccess {
		sample, ok := readSamples[name]
		if isWrites(rule) {
			if ok {
				t.Errorf("%q is marked writes but has a read sample", name)
			}
			continue
		}
		if !ok {
			t.Errorf("%q has a read mode but no readSamples entry: add a read-only call so the sandbox test runs it", name)
			continue
		}
		cmd := lookupCommand(sample[0])
		if cmd == nil || cmd.Name != name {
			t.Errorf("readSamples[%q] calls %q", name, sample[0])
			continue
		}
		if !commandAccess[name](sample[1:]) {
			t.Errorf("readSamples[%q] = %v is not read-only by commandAccess", name, sample)
		}
	}
}

func TestReadOnlyRules(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"transition", "x", "--to", "done"}, false},
		{[]string{"transition", "x", "--to", "done", "--dry-run"}, true},
		{[]string{"workflow", "scan"}, false},
		{[]string{"workflow", "scan", "--dry-run"}, true},
		{[]string{"workflow", "upgrade"}, true},
		{[]string{"department", "week", "gtm"}, true},
		{[]string{"department", "week", "gtm", "--write"}, false},
		{[]string{"department", "add", "gtm"}, false},
		{[]string{"yolo"}, true},
		{[]string{"yolo", "on"}, false},
		{[]string{"health"}, true},
		{[]string{"health", "snapshot"}, false},
		{[]string{"comment", "x", "--text", "--dry-run"}, false},
	}
	for _, c := range cases {
		inv := &invocation{cmd: lookupCommand(c.args[0]), args: c.args[1:]}
		if got := isReadOnly(inv); got != c.want {
			t.Errorf("isReadOnly(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

// sandboxRepo is a committed git repo with a workflow and one issue
// assigned to agent-sample.
func sandboxRepo(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "issues", "CLI"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "workflow.yaml"), "statuses:\n  - name: \"backlog\"\n  - name: \"in progress\"\ntransitions:\n  - from: \"backlog\"\n    to: \"in progress\"\n")
	mustWrite(t, filepath.Join(root, "issues", "CLI", "sample.md"), "---\ntitle: \"sample\"\nstatus: \"backlog\"\nsystem: \"CLI\"\nassignee: \"agent-sample\"\n---\n\nbody\n")
	git(t, root, "init", "-q")
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

type twoProjects struct{ home, other, outside string }

// newTwoProjects: two sandbox repos, "home" (the caller's) and "other",
// in one projects.yaml outside both; the caller starts in home.
func newTwoProjects(t *testing.T) twoProjects {
	t.Helper()
	base := realTempDir(t)
	p := twoProjects{home: filepath.Join(base, "home"), other: filepath.Join(base, "other"), outside: filepath.Join(base, "outside")}
	for _, dir := range []string{p.home, p.other, p.outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sandboxRepo(t, p.home)
	sandboxRepo(t, p.other)
	cfg := filepath.Join(base, "projects.yaml")
	var b strings.Builder
	b.WriteString("projects:\n")
	for _, s := range []struct{ slug, root string }{{"home", p.home}, {"other", p.other}} {
		fmt.Fprintf(&b, "  - name: %s\n    slug: %s\n    workdir: %q\n    issues: %q\n    workflow: %q\n",
			s.slug, s.slug, s.root, filepath.Join(s.root, "issues"), filepath.Join(s.root, "workflow.yaml"))
	}
	mustWrite(t, cfg, b.String())
	t.Setenv("ISSUE_VIEWER_CONFIG", cfg)
	t.Setenv("ISSUE_CLI_LOG", "")
	t.Setenv("HOME", filepath.Join(base, "home-dir"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("TMPDIR", filepath.Join(base, "tmp"))
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", "/tmp")
	t.Setenv(telemetry.DisableEnv, "on")
	t.Setenv(telemetry.SkipEnv, "")
	t.Chdir(p.home)
	return p
}

// runAsMain runs args the way main does: the call, its clilog record, then
// its telemetry event.
func runAsMain(t *testing.T, args ...string) *callLog {
	t.Helper()
	start := time.Now()
	inv := &invocation{}
	l := newCallLog(args, start)
	var out, errw strings.Builder
	err := runTraced(args, strings.NewReader(""), &out, &errw, inv)
	l.write(inv, err, false)
	recordTelemetry(inv, args, err, start, false)
	return l
}

// The bug: any call with --project <other> appended to the other repo's
// .agent-logs/. Every read sample, run from the home repo, now leaves the
// other repo's git status clean; its records land in home.
func TestReadCommandsLeaveAnotherProjectUnchanged(t *testing.T) {
	p := newTwoProjects(t)
	for name, sample := range readSamples {
		runAsMain(t, append([]string{"--project", "other"}, sample...)...)
		if st := git(t, p.other, "status", "--porcelain", "--untracked-files=all"); st != "" {
			t.Fatalf("%s (%v) changed the other repo:\n%s", name, sample, st)
		}
	}
	if recs := readRecords(t, clilog.ProjectPath(p.home)); len(recs) != len(readSamples) {
		t.Fatalf("home clilog has %d records, want %d", len(recs), len(readSamples))
	}
	if _, err := os.Stat(telemetry.ProjectPath(p.home)); err != nil {
		t.Fatalf("telemetry did not go to the caller's project: %v", err)
	}
	if _, err := os.Stat(clilog.AgentPath(p.home, "agent-sample")); err == nil {
		t.Fatal("the other issue's assignee log was written in home")
	}
}

func TestReadCommandFromNoProjectGoesToMachineLog(t *testing.T) {
	p := newTwoProjects(t)
	t.Chdir(p.outside)
	l := runAsMain(t, "--project", "other", "show", "cli/sample")
	if l.shared != clilog.GlobalPath() || len(readRecords(t, l.shared)) != 1 {
		t.Fatalf("shared %q, want the machine log %q with one record", l.shared, clilog.GlobalPath())
	}
	if st := git(t, p.other, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Fatalf("other repo changed:\n%s", st)
	}
}

// Working in the project (its checkout or a worktree under it) keeps
// logging there, read or write: the Timeline and agents canvas read it.
func TestCallsInsideTheProjectStillLogThere(t *testing.T) {
	p := newTwoProjects(t)
	wt := filepath.Join(p.other, ".worktrees", "cli", "sample")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wt)
	runAsMain(t, "--project", "other", "show", "cli/sample")
	if n := len(readRecords(t, clilog.AgentPath(p.other, "agent-sample"))); n != 1 {
		t.Fatalf("agent clilog has %d records, want 1", n)
	}
	if n := len(readRecords(t, clilog.ProjectPath(p.other))); n != 1 {
		t.Fatalf("project clilog has %d records, want 1", n)
	}
}

// A write on another project changes it anyway, so its record stays with it.
func TestWriteCommandOnAnotherProjectLogsThere(t *testing.T) {
	p := newTwoProjects(t)
	runAsMain(t, "--project", "other", "comment", "cli/sample", "--text", "progress: x")
	if n := len(readRecords(t, clilog.ProjectPath(p.other))); n != 1 {
		t.Fatalf("other clilog has %d records, want 1", n)
	}
	if _, err := os.Stat(clilog.ProjectPath(p.home)); err == nil {
		t.Fatal("write call logged in the caller's project")
	}
}

func TestTelemetryOptOutOfTargetHoldsForReadCalls(t *testing.T) {
	p := newTwoProjects(t)
	cfg := os.Getenv("ISSUE_VIEWER_CONFIG")
	mustWrite(t, cfg, strings.Replace(readFile(t, cfg), "  - name: other\n", "  - name: other\n    telemetry: false\n", 1))
	runAsMain(t, "--project", "other", "list")
	if _, err := os.Stat(telemetry.ProjectPath(p.home)); err == nil {
		t.Fatal("telemetry written for a project that opted out")
	}
}
