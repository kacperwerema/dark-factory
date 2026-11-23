package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/checks"
	"github.com/kacperwerema/dark-factory/internal/testbin"
)

// is_worker in the check library asks this issue-cli's lead whoami, the
// worker guard's own test: 0 for a worker, 1 for a clean shell.
func TestCheckLibIsWorkerAsksTheGuard(t *testing.T) {
	bash, err := checks.FindBash()
	if err != nil {
		t.Skip("no bash 4 or newer")
	}
	if testing.Short() {
		t.Skip("builds issue-cli")
	}
	// A real build: this test binary's init() drops a worker's identity
	// (lead_test.go), so it can't stand in for issue-cli here.
	dir := t.TempDir()
	cli := testbin.Path(t, "./cmd/issue-cli")
	lib := filepath.Join(dir, "lib.sh")
	if err := os.WriteFile(lib, checks.Lib, 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "probe.sh")
	os.WriteFile(script, []byte("source \"$CHECKS_LIB\"\nis_worker\necho \"RC=$?\"\n"), 0o644)
	root := t.TempDir() // not under .worktrees/: the folder alone isn't a worker signal

	run := func(extra ...string) string {
		t.Helper()
		var env []string
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			if k == "TMUX" || k == "AGENT_NAME" || strings.HasPrefix(k, "ISSUE_VIEWER_") || strings.HasPrefix(k, "CHECKS_") {
				continue
			}
			env = append(env, kv)
		}
		env = append(env, "CHECKS_LIB="+lib, "CHECKS_ROOT="+root, "CHECKS_YES=1", "NO_COLOR=1",
			"ISSUE_CLI="+cli, "TMPDIR="+t.TempDir())
		cmd := exec.Command(bash, script)
		cmd.Dir = root
		cmd.Env = append(env, extra...)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	if out := run("ISSUE_VIEWER_ISSUE_SLUG=cli/some-issue"); !strings.Contains(out, "RC=0\n") {
		t.Fatalf("worker env: want RC=0\n%s", out)
	}
	if out := run(); !strings.Contains(out, "RC=1\n") {
		t.Fatalf("clean env: want RC=1\n%s", out)
	}
}

// AC2/AC3 end to end: a worker known only by its folder under .worktrees/
// runs a check script whose sic goes through clean_shell (a stand-in
// exists). The identity survives clean_shell, the guard sees a worker, and
// on the sandbox sandbox_project marked the worker's owner-only steps pass
// (claim of another issue, yolo on, department add) while dispatch stays
// refused. With the marker gone the same steps are refused.
//
// One script and one sandbox walk all three outcomes in turn (folder only,
// then a known issue, then the marker removed): a sandbox per outcome
// tripled the setup, 34.4 s of a loaded validate.
func TestCheckScriptWorkerRunsOwnerOnlyStepsOnAMarkedSandbox(t *testing.T) {
	bash, err := checks.FindBash()
	if err != nil {
		t.Skip("no bash 4 or newer")
	}
	if testing.Short() {
		t.Skip("builds issue-cli")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	cli := testbin.Path(t, "./cmd/issue-cli")
	lib := filepath.Join(dir, "lib.sh")
	if err := os.WriteFile(lib, checks.Lib, 0o644); err != nil {
		t.Fatal(err)
	}
	// Each phase's output starts at its PHASE line. A worker known by its
	// folder alone has no issue, so phase 1 leaves the claim to phase 2;
	// the guard refuses phase 3's steps before they meet phase 1's state.
	body := `source "$CHECKS_LIB"
sandbox_project sbx
stand_in claude 'echo STAND-IN'
printf -- '---\ntitle: "Other"\nstatus: "backlog"\n---\n\nx\n' > "$SANDBOX_PROJECT_DIR/issues/other.md"
echo "PHASE folder"
echo "ENV: $(clean_shell sh -c 'echo "$ISSUE_VIEWER_WORKER"')"
sic yolo on --until 4h --quote "guided check"; echo "YOLO=$?"
sic department add marketing; echo "DEPT=$?"
sic dispatch other --quote go; echo "DISPATCH=$?"
echo "PHASE known"
export ISSUE_VIEWER_ISSUE_SLUG=cli/x
sic claim other; echo "CLAIM=$?"
echo "PHASE unmarked"
rm -f "$SANDBOX/.issue-cli-sandbox"
sic claim other; echo "CLAIM=$?"
sic yolo on --until 4h --quote "guided check"; echo "YOLO=$?"
sic department add marketing; echo "DEPT=$?"
sic dispatch other --quote go; echo "DISPATCH=$?"
`
	script := filepath.Join(dir, "probe.sh")
	os.WriteFile(script, []byte(body), 0o644)
	root := filepath.Join(t.TempDir(), ".worktrees", "cli", "x") // a worker by its folder alone
	os.MkdirAll(root, 0o755)
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "TMUX" || k == "AGENT_NAME" || k == "CLAUDECODE" || strings.HasPrefix(k, "ISSUE_VIEWER_") || strings.HasPrefix(k, "CHECKS_") || strings.HasPrefix(k, "XDG_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "CHECKS_LIB="+lib, "CHECKS_ROOT="+root, "CHECKS_YES=1", "NO_COLOR=1",
		"ISSUE_CLI="+cli, "TMPDIR="+t.TempDir(), "ISSUE_CLI_TELEMETRY=off")
	cmd := exec.Command(bash, script)
	cmd.Dir = root
	cmd.Env = env
	all, _ := cmd.CombinedOutput()
	phase := map[string]string{}
	for _, part := range strings.Split(string(all), "PHASE ")[1:] {
		name, rest, _ := strings.Cut(part, "\n")
		phase[name] = rest
	}
	if len(phase) != 3 {
		t.Fatalf("want 3 phases, got %d\n%s", len(phase), all)
	}

	out := phase["folder"]
	for _, want := range []string{
		"ENV: working directory under .worktrees/",
		"sandbox: the worker guard passed yolo for worker",
		"YOLO=0", "DEPT=0",
		"sandbox: dispatch stays refused in a sandbox (owner's rule, D-4)",
		"workers can't dispatch",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("marked sandbox: missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "DISPATCH=0") {
		t.Errorf("dispatch passed on a sandbox:\n%s", out)
	}

	// A worker whose issue is known: claiming another issue passes too.
	out = phase["known"]
	for _, want := range []string{"sandbox: the worker guard passed claim for worker", "CLAIM=0"} {
		if !strings.Contains(out, want) {
			t.Errorf("marked sandbox, known issue: missing %q\n%s", want, out)
		}
	}

	out = phase["unmarked"]
	for _, want := range []string{"you work on cli/x only", "CLAIM=1", "YOLO=1", "DEPT=1", "workers never add departments", "workers can't dispatch"} {
		if !strings.Contains(out, want) {
			t.Errorf("unmarked sandbox: missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "the worker guard passed") {
		t.Errorf("unmarked sandbox passed:\n%s", out)
	}
}
