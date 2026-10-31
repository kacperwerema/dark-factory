package checks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The library's behaviour, from fixture scripts run under bash. Machine runs
// get stdin from a pipe; owner runs need a terminal, which script(1) gives.

type libRun struct {
	out  string
	code int
	root string
}

// needBash is the bash 4 or newer the scripts run with (FindBash, as
// check-script finds it), or skips: a stock Mac has only bash 3.2, which
// lib.sh refuses, and the owner-run tests then hung until the 10-minute
// test timeout.
func needBash(t *testing.T) string {
	t.Helper()
	bash, err := FindBash()
	if err != nil {
		t.Skip("no bash 4 or newer (brew install bash on macOS)")
	}
	return bash
}

// fakeIssueCLI writes an issue-cli stand-in that records its arguments and
// the environment the lib restored for it.
func fakeIssueCLI(t *testing.T, dir string) (bin, calls string) {
	t.Helper()
	calls = filepath.Join(dir, "calls.txt")
	bin = filepath.Join(dir, "issue-cli")
	body := `#!/bin/sh
echo "$* | TMUX_TMPDIR=${TMUX_TMPDIR:-unset} XDG_STATE_HOME=${XDG_STATE_HOME:-unset}" >> ` + calls + `
if [ "$1" = init ]; then mkdir -p issues docs && echo "statuses: []" > workflow.yaml; fi
`
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func libEnv(t *testing.T, root, bin string, extra ...string) []string {
	t.Helper()
	lib := filepath.Join(t.TempDir(), "lib.sh")
	if err := os.WriteFile(lib, Lib, 0o644); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") && !strings.HasPrefix(kv, "TMUX_TMPDIR=") && !strings.HasPrefix(kv, "CHECKS_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"CHECKS_LIB="+lib, "CHECKS_SLUG=demo/thing", "CHECKS_PROJECT=demo",
		"CHECKS_ROOT="+root, "CHECKS_ISSUE_CLI="+bin, "NO_COLOR=1",
		"XDG_STATE_HOME=/real/state", "TMPDIR="+t.TempDir())
	return append(env, extra...)
}

func runMachine(t *testing.T, body string, extra ...string) libRun {
	t.Helper()
	bash := needBash(t)
	root := t.TempDir()
	bin, _ := fakeIssueCLI(t, t.TempDir())
	path := writeScript(t, root, "demo", "#!/usr/bin/env bash\n# issue: demo/thing\nsource \"$CHECKS_LIB\"\n"+body)
	cmd := exec.Command(bash, path)
	cmd.Dir = root
	cmd.Env = libEnv(t, root, bin, extra...)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return libRun{out: string(out), code: code, root: root}
}

// runOwner runs body on a pseudo-terminal with input typed line by line.
func runOwner(t *testing.T, body, input string) (libRun, string) {
	t.Helper()
	bash := needBash(t)
	root := t.TempDir()
	bin, calls := fakeIssueCLI(t, t.TempDir())
	path := writeScript(t, root, "demo", "#!/usr/bin/env bash\n# issue: demo/thing\nsource \"$CHECKS_LIB\"\n"+body)
	cmd := ptyCommand(t, bash, path)
	cmd.Dir = root
	cmd.Env = libEnv(t, root, bin)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(calls)
	return libRun{out: string(out), code: code, root: root}, string(data)
}

// ptyCommand runs argv on a pseudo-terminal. util-linux script(1) does it on
// Linux; BSD script has no -c and sends EOF ahead of piped input, so macOS
// uses Python's pty module instead.
func ptyCommand(t *testing.T, argv ...string) *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("script"); err != nil {
			t.Skip("no script(1) for a pseudo-terminal")
		}
		return exec.Command("script", "-qec", strings.Join(argv, " "), "/dev/null")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3 for a pseudo-terminal")
	}
	return exec.Command("python3", append([]string{"-c", ptySpawn}, argv...)...)
}

// ptySpawn is pty.spawn that also ends when the child does: macOS's system
// Python (3.9, /usr/bin/python3) never sees EOF on the pty after the child
// exits, so a plain pty.spawn waited for the 10-minute test timeout on a
// PATH without a newer python3.
const ptySpawn = `import os, pty, select, sys
pid, fd = pty.fork()
if pid == 0:
    os.execvp(sys.argv[1], sys.argv[1:])
stdin, status, out = sys.stdin.fileno(), None, sys.stdout.buffer
while True:
    fds = [fd] + ([stdin] if stdin is not None else [])
    r, _, _ = select.select(fds, [], [], 0.1)
    if fd in r:
        try:
            data = os.read(fd, 4096)
        except OSError:
            data = b""
        if data:
            out.write(data); out.flush()
        elif status is not None:
            break
    if stdin is not None and stdin in r:
        data = os.read(stdin, 4096)
        if data:
            os.write(fd, data)
        else:
            stdin = None
    if status is None:
        done, st = os.waitpid(pid, os.WNOHANG)
        if done:
            status = st
    if status is not None and fd not in r:
        break
sys.exit(os.waitstatus_to_exitcode(status))
`

func record(t *testing.T, root, mode string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(root, RecordDir, "demo", "*-"+mode+".txt"))
	if len(files) != 1 {
		t.Fatalf("want one %s record, got %v", mode, files)
	}
	data, _ := os.ReadFile(files[0])
	return string(data)
}

func contains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}

func TestLibMachineRunVerdictsAndBoxes(t *testing.T) {
	r := runMachine(t, `
checks_intro "Demo."
step S1 "passes" --box TP2 --what "says hi"
run echo hi
expect '[[ $out == hi && $code == 0 ]]' "said hi"
step S2 "fails" --box TP3,TP4
run sh -c 'echo oops; exit 3'
expect '[ $code = 0 ]' "exit 0"
step S3 "needs eyes" --box TP5
ask "Looks right?"
step S1 "second part" --box TP6
expect true "fine"
summary --all TP1
`)
	if r.code != 1 {
		t.Fatalf("exit %d, want 1 (a machine check failed)\n%s", r.code, r.out)
	}
	contains(t, r.out,
		"Machine run: machine checks only",
		"━━ S1 · passes ━━  (box TP2)",
		"$ sh -c 'echo oops; exit 3'", "    oops", "(exit 3)", "✗ exit 0",
		"? Looks right? (needs your eyes)",
		"━━ S1 · second part ━━  (box TP2, TP6)",
		"✓ S1 pass", "✗ S2 fail: exit 0", "? S3 needs your eyes",
		"Boxes passed: TP2 TP6\n",
		"Machine run: 1 failed, 1 need the owner's eyes, 0 skipped. Left to the owner: TP5.")
	rec := record(t, r.root, "machine")
	contains(t, rec, "issue: demo/thing", "mode: machine", "step S1 pass boxes=TP2,TP6", "step S2 fail boxes=TP3,TP4 note=exit 0", "step S3 eyes boxes=TP5", "boxes passed: TP2 TP6")
}

func TestLibRepeatedStepFailsWhenEitherPartFails(t *testing.T) {
	r := runMachine(t, `
step TP7 "first"
expect false "first part"
step TP7 "again"
expect true "second part"
summary --all HT1
`)
	contains(t, r.out, "✗ TP7 fail: first part", "Boxes passed: none")
}

func TestLibAllBoxNeedsEveryStep(t *testing.T) {
	r := runMachine(t, `
step MV1 "a" --box HT2
expect true ok
step MV2 "b" --box HT3
expect true ok
summary --all HT1
`)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	contains(t, r.out, "Boxes passed: HT2 HT3 HT1")
}

func TestLibTouchesRealIsSkippedInAMachineRun(t *testing.T) {
	r := runMachine(t, `
if step MV9 "real" --touches-real "a window in your session"; then
	echo REAL-RAN
fi
run echo after-skip
expect false "never evaluated"
step MV10 "next"
expect true ok
summary
`)
	if strings.Contains(r.out, "REAL-RAN") || strings.Contains(r.out, "after-skip") || strings.Contains(r.out, "never evaluated") {
		t.Fatalf("a skipped step ran:\n%s", r.out)
	}
	contains(t, r.out, "Touches your real environment: a window in your session", "- MV9 skipped: touches your real environment", "✓ MV10 pass")
	if r.code != 0 {
		t.Fatalf("a skip is not a failure: exit %d", r.code)
	}
}

func TestLibYesForcesAMachineRun(t *testing.T) {
	// CHECKS_YES=1 wins even on a terminal; here stdin is a pipe anyway, so
	// check the mode line is there and no question blocks.
	r := runMachine(t, "step A x\nask 'q?'\nsummary\n", "CHECKS_YES=1")
	contains(t, r.out, "? q? (needs your eyes)")
}

func TestLibCleanupRunsLastFirstOnExit(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "order")
	r := runMachine(t, `
on_exit sh -c "echo one >> `+marker+`"
on_exit sh -c "echo two >> `+marker+`"
echo body
`)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	data, _ := os.ReadFile(marker)
	if string(data) != "two\none\n" {
		t.Fatalf("cleanup order = %q, want two then one", data)
	}
}

func TestLibCleanupRunsOnInterrupt(t *testing.T) {
	bash := needBash(t)
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "cleaned")
	bin, _ := fakeIssueCLI(t, t.TempDir())
	path := writeScript(t, root, "demo", "#!/usr/bin/env bash\nsource \"$CHECKS_LIB\"\non_exit touch "+marker+"\necho started\nsleep 30 & wait\n")
	cmd := exec.Command(bash, path)
	cmd.Env = libEnv(t, root, bin)
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	stdout.Read(buf) // "started": the traps are set
	cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
			t.Fatalf("exit = %v, want 130", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the script didn't stop on SIGINT")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("on_exit didn't run on SIGINT")
	}
}

func TestLibOwnerRunTicksPassedBoxesWithTheRealEnvironment(t *testing.T) {
	r, calls := runOwner(t, `
checks_intro "Owner."
step S1 "ok" --box TP2
private_tmux 2>/dev/null || true
expect true fine
ask "Fine?"
step S2 "bad" --box TP3
ask "Broken?"
summary --all TP1
`, "\ny\n\nn\n\ny\ny\n")
	contains(t, r.out, "Tick the passed boxes (TP2) on demo/thing?", "Comment the failures on demo/thing")
	contains(t, calls,
		"--project demo check demo/thing TP2 | TMUX_TMPDIR=unset XDG_STATE_HOME=/real/state",
		"--project demo comment demo/thing checks: demo: S2 failed: you answered no: Broken?")
	contains(t, record(t, r.root, "owner"), "mode: owner", "boxes passed: TP2")
}

func TestLibOwnerQStopsAndCleansUp(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cleaned")
	r, calls := runOwner(t, `
on_exit touch `+marker+`
step S1 "first"
ask "Go on?"
step S2 "never"
summary
`, "q\n")
	if r.code != 130 {
		t.Fatalf("exit %d, want 130\n%s", r.code, r.out)
	}
	if strings.Contains(r.out, "S2 · never") || calls != "" {
		t.Fatalf("ran past q:\n%s\ncalls: %s", r.out, calls)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("q didn't run the cleanup")
	}
	if files, _ := filepath.Glob(filepath.Join(r.root, RecordDir, "demo", "*-owner.txt")); len(files) != 0 {
		t.Fatal("a stopped run left an owner record")
	}
}

func TestLibOwnerTouchesRealRunsOnlyOnY(t *testing.T) {
	r, _ := runOwner(t, `
if step MV9 "real" --touches-real "a window"; then echo REAL-RAN; fi
if step MV10 "real too" --touches-real "another"; then echo REAL2-RAN; fi
summary
`, "y\n\n\n\nn\n\n")
	contains(t, r.out, "REAL-RAN", "- MV10 skipped: you chose not to run it")
	if strings.Contains(r.out, "REAL2-RAN") {
		t.Fatalf("ran without a y:\n%s", r.out)
	}
}

func TestLibCleanShellSkipsTheProfileAndFindsTheStandIn(t *testing.T) {
	// The owner's profile and rc put a "real" claude first on PATH; the
	// stand-in must still win, and the profile must not run.
	home := t.TempDir()
	realBin := filepath.Join(home, "real-bin")
	os.MkdirAll(realBin, 0o755)
	os.WriteFile(filepath.Join(realBin, "claude"), []byte("#!/bin/sh\necho REAL-CLAUDE\n"), 0o755)
	rc := "export PATH=" + realBin + ":$PATH\necho PROFILE-RAN\n"
	for _, f := range []string{".bash_profile", ".profile", ".bashrc"} {
		os.WriteFile(filepath.Join(home, f), []byte(rc), 0o644)
	}
	r := runMachine(t, `
step S1 "stand-in"
stand_in claude 'echo STAND-IN'
run clean_shell sh -c 'claude; $SHELL -lic claude'
expect '[[ $out != *REAL-CLAUDE* && $out != *PROFILE-RAN* ]]' "no profile, no real claude"
expect_stand_in_ran claude
summary
`, "HOME="+home)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	contains(t, r.out, "STAND-IN", "✓ the stand-in claude ran, not the real one")
}

func TestLibStandInCheckFailsWhenItNeverRan(t *testing.T) {
	r := runMachine(t, "step S1 x\nstand_in claude\nexpect_stand_in_ran claude \"the stand-in claude ran, not the real one\" 1\nsummary\n")
	contains(t, r.out, "✗ the stand-in claude ran, not the real one")
}

func TestLibSandboxProjectLivesOutsideWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	// A TMPDIR inside a .worktrees/ folder must not be used.
	wtTmp := filepath.Join(t.TempDir(), ".worktrees", "x", "tmp")
	os.MkdirAll(wtTmp, 0o755)
	r := runMachine(t, `
sandbox_project sbx
echo "DIR=$SANDBOX_PROJECT_DIR"
echo "CFG=$SANDBOX_CONFIG"
echo "STATE=$XDG_STATE_HOME VIEWER=$ISSUE_VIEWER_CONFIG"
cat "$SANDBOX_CONFIG"
git -C "$SANDBOX_PROJECT_DIR" log --oneline | wc -l
sic list
`, "TMPDIR="+wtTmp)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	if strings.Contains(r.out, "/.worktrees/") {
		t.Fatalf("the sandbox is under .worktrees/:\n%s", r.out)
	}
	contains(t, r.out, `slug: "sbx"`, "/sbx/issues", "workdir: ")
	dir := between(r.out, "DIR=", "\n")
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("the sandbox %s outlived the script", dir)
	}
	// Its own state dir: a viewer the script starts records itself there,
	// never in the shared viewer.json.
	if state := between(r.out, "STATE=", " "); state != filepath.Join(filepath.Dir(dir), "state") {
		t.Fatalf("XDG_STATE_HOME = %q, want the sandbox's own state dir next to %s", state, dir)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	if j := strings.Index(s, b); j >= 0 {
		return s[:j]
	}
	return s
}

func TestLibPrivateTmuxKillsOnlyItsOwnServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	// Another private server (standing in for the owner's) must survive.
	other, err := os.MkdirTemp("/tmp", "ckother-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(other)
	otherTmux := func(args ...string) error {
		c := exec.Command("tmux", args...)
		c.Env = append(os.Environ(), "TMUX_TMPDIR="+other)
		c.Env = append(c.Env, "TMUX=")
		return c.Run()
	}
	if err := otherTmux("new-session", "-d", "-s", "owner", "sleep 60"); err != nil {
		t.Skipf("tmux can't start here: %v", err)
	}
	defer otherTmux("kill-server")

	r := runMachine(t, `
private_tmux
echo "TMUXDIR=$TMUX_TMPDIR"
ptmux new-session -d -s sbx 'sleep 60'
ptmux has-session -t sbx && echo PRIVATE-UP
`, "TMUX_TMPDIR="+other)
	contains(t, r.out, "PRIVATE-UP")
	dir := between(r.out, "TMUXDIR=", "\n")
	if dir == other || dir == "" {
		t.Fatalf("private_tmux reused the caller's TMUX_TMPDIR: %q", dir)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("the private tmux dir %s outlived the script", dir)
	}
	if err := otherTmux("has-session", "-t", "owner"); err != nil {
		t.Fatal("the other tmux server was killed")
	}
}

func TestLibNeedsBash4(t *testing.T) {
	if !strings.Contains(string(Lib), `"${BASH_VERSINFO[0]}" -lt 4`) {
		t.Fatal("the bash 4 guard is gone")
	}
}

// bash -n catches what shellcheck would, on a machine without it: a quote
// in a ${var:-word} default once broke every helper after it.
func TestLibParses(t *testing.T) {
	bash := needBash(t)
	path := filepath.Join(t.TempDir(), "lib.sh")
	os.WriteFile(path, Lib, 0o644)
	if out, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("bash -n:\n%s", out)
	}
}

func TestLibPassesShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("no shellcheck")
	}
	path := filepath.Join(t.TempDir(), "lib.sh")
	os.WriteFile(path, Lib, 0o644)
	if out, err := exec.Command("shellcheck", "-s", "bash", "-S", "warning", path).CombinedOutput(); err != nil {
		t.Fatalf("shellcheck:\n%s", out)
	}
}

func TestLibDefaultBoxAndSeveralAllBoxes(t *testing.T) {
	r := runMachine(t, `
checks_default_box TP2
step S1 a
expect true ok
step S2 b --box TP9
expect true ok
summary --all TP3,TP4 --all TP5
`)
	contains(t, r.out, "(box TP2)", "(box TP9)", "Boxes passed: TP2 TP9 TP3 TP4 TP5\n")
}

func TestLibStepActiveAndStepEnd(t *testing.T) {
	r := runMachine(t, `
step_active && echo BEFORE-ANY-STEP
step S1 a
step_active && echo IN-S1
skip "not here"
step_active && echo AFTER-SKIP
step S2 b
expect true ok
step_end
echo AFTER-END
step_active && echo STILL-ACTIVE
summary
`)
	contains(t, r.out, "IN-S1", "- S1 skipped: not here")
	for _, bad := range []string{"BEFORE-ANY-STEP", "AFTER-SKIP", "STILL-ACTIVE"} {
		if strings.Contains(r.out, bad) {
			t.Errorf("step_active wrong: %s\n%s", bad, r.out)
		}
	}
	if i, j := strings.Index(r.out, "S2 passed"), strings.Index(r.out, "AFTER-END"); i < 0 || j < i {
		t.Errorf("step_end didn't close S2 before the next line:\n%s", r.out)
	}
}

func TestLibEventuallyRetries(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "later")
	r := runMachine(t, `
step S1 a
( sleep 0.5; touch `+flag+` ) &
eventually '[ -e `+flag+` ]' "the file appears" 5
eventually false "never" 1
summary
`)
	contains(t, r.out, "✓ the file appears", "✗ never")
}

func TestLibThenIsTheOwnersClosingLine(t *testing.T) {
	r, _ := runOwner(t, `
step S1 a
expect true ok
summary --then "Approve documentation when you're happy."
`, "\n\n")
	contains(t, r.out, "Approve documentation when you're happy.")
}

func TestLibSandboxProjectOptions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	wf := filepath.Join(t.TempDir(), "wf.yaml")
	os.WriteFile(wf, []byte("statuses: [{name: idea}]\n"), 0o644)
	r := runMachine(t, `
sandbox_project sand --workflow `+wf+` --name Sand
cat "$SANDBOX_CONFIG"
cat "$SANDBOX_PROJECT_DIR/workflow.yaml"
ls "$SANDBOX_PROJECT_DIR"
`)
	contains(t, r.out, `name: "Sand"`, `slug: "sand"`, `terminal: "none"`, "statuses: [{name: idea}]", "issues")
}

func TestLibAgentStandInStaysAndLogsInput(t *testing.T) {
	r := runMachine(t, `
step S1 a
stand_in --agent claude
mkfifo "$CHECKS_STAND_IN_DIR/fifo"
background pid clean_shell sh -c 'claude --model x < "$CHECKS_STAND_IN_DIR/fifo"'
exec 3> "$CHECKS_STAND_IN_DIR/fifo"
echo "the brief" >&3
expect_stand_in_ran claude
expect_stand_in_got claude "the brief"
expect 'kill -0 $pid' "the stand-in is still alive"
exec 3>&-
summary
`)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
}

func TestLibBackgroundPidIsTheCommand(t *testing.T) {
	r := runMachine(t, `
background pid clean_shell sleep 30
sleep 0.3
ps -o comm= -p "$pid"
free_port p
echo "PORT=$p"
`)
	contains(t, r.out, "sleep\n", "PORT=")
}

func TestLibCleanPrivateTmuxGivesPanesTheStandIns(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	r := runMachine(t, `
stand_in claude
private_tmux --clean
ptmux new-session -d -s w 'sh -c "command -v claude > $CHECKS_STAND_IN_DIR/which.tmp; mv $CHECKS_STAND_IN_DIR/which.tmp $CHECKS_STAND_IN_DIR/which; sleep 5"'
tries=100
while [ ! -e "$CHECKS_STAND_IN_DIR/which" ] && [ "$tries" -gt 0 ]; do sleep 0.1; tries=$((tries - 1)); done
cat "$CHECKS_STAND_IN_DIR/which"
`)
	if !strings.Contains(r.out, "/bin/claude") || !strings.Contains(r.out, "ck-") {
		t.Fatalf("the pane didn't find the stand-in first:\n%s", r.out)
	}
}

// wait_for_url …; expect '[ $? = 0 ]' is the common pattern: expect's own
// skip test must not overwrite the caller's $?.
func TestLibExpectSeesTheCallersExitCode(t *testing.T) {
	r := runMachine(t, `
step S1 a
true; expect '[ $? = 0 ]' "the last command passed"
false; expect '[ $? = 1 ]' "the last command failed"
false; eventually '[ $? = 1 ]' "eventually sees it too" 1
summary
`)
	contains(t, r.out, "✓ the last command passed", "✓ the last command failed", "✓ eventually sees it too")
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
}

// A screenshot in a sandbox_dir is gone after the run: show and keep copy it
// next to the run records first.
func TestLibShowKeepsAFileFromTheSandbox(t *testing.T) {
	r := runMachine(t, `
shot=; sandbox_dir shot
echo png > "$shot/page.png"
step S1 a
show "$shot/page.png"
keep "$shot/page.png" second.png
expect '[ -f "$kept" ]' "kept"
summary
`)
	files, _ := filepath.Glob(filepath.Join(r.root, RecordDir, "demo", "files", "*"))
	if len(files) != 2 {
		t.Fatalf("kept files = %v, want 2:\n%s", files, r.out)
	}
	contains(t, r.out, "Look at "+filepath.Join(r.root, RecordDir, "demo", "files"), "-second.png", "✓ kept")
}

func TestLibStandInViewerServesFilesAndSetsViewerURL(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	r := runMachine(t, `
sandbox_project sbx
stand_in_viewer
mkdir -p "$STAND_IN_VIEWER_DIR/p/sbx"
echo '{"milestone":{"version":"0.9"}}' > "$STAND_IN_VIEWER_DIR/p/sbx/release.json"
curl -fsS "$VIEWER_URL/p/sbx/release.json"
echo
stand_in_viewer
grep -c '^viewer_url:' "$SANDBOX_CONFIG"
head -n 2 "$SANDBOX_CONFIG"
echo "URL=$VIEWER_URL"
`)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	contains(t, r.out, `{"milestone":{"version":"0.9"}}`, "\n1\nviewer_url: \"http://127.0.0.1:", "\nprojects:\n")
	url := between(r.out, "URL=", "\n")
	if err := exec.Command("curl", "-fsS", "--max-time", "1", url+"/").Run(); err == nil {
		t.Fatalf("the stand-in viewer at %s outlived the script", url)
	}
}

func TestLibStandInClaudeBoxSwallowsTheFirstEnter(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	r := runMachine(t, `
step S1 box
private_tmux
stand_in_claude_box plain
stand_in_claude_box deaf --swallow-first-enter
for s in plain deaf; do
	ptmux send-keys -t "$s" -l 'approved: in progress'
	ptmux send-keys -t "$s" Enter
done
expect_box_submitted plain "approved: in progress"
expect_box_empty plain
sleep 0.5
expect '! grep -q approved "$CHECKS_BOX_DIR/deaf.submitted"' "the first Enter was swallowed"
expect 'ptmux capture-pane -p -t deaf | grep -q "^❯ approved: in progress"' "the text is still typed in the box"
ptmux send-keys -t deaf Enter
expect_box_submitted deaf "approved: in progress"
expect_box_empty deaf
summary
`)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	contains(t, r.out, "S1 passed")
}

func TestLibSandboxYoloOnWritesTheSandboxWindowOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	r := runMachine(t, `
sandbox_project sbx
sandbox_yolo_on
echo "DIR=$SANDBOX_PROJECT_DIR"
cat "$SANDBOX_PROJECT_DIR/.yolo/sbx.json"
`)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	contains(t, r.out, `"on": true`, `"forever": true`, `"by": "owner"`)
	var st struct {
		On      bool      `json:"on"`
		Since   time.Time `json:"since"`
		Forever bool      `json:"forever"`
	}
	raw := r.out[strings.Index(r.out, "{"):]
	if err := json.Unmarshal([]byte(raw), &st); err != nil || !st.On || !st.Forever || st.Since.IsZero() {
		t.Fatalf("not a YOLO window the engine reads (%v): %s", err, raw)
	}

	r = runMachine(t, "sandbox_yolo_on\necho NOT-REACHED\n")
	if r.code == 0 || strings.Contains(r.out, "NOT-REACHED") {
		t.Fatalf("sandbox_yolo_on ran without a sandbox:\n%s", r.out)
	}
	contains(t, r.out, "needs sandbox_project first")
}

func TestLibSeedDepartmentAppliesTheDryRunPlan(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	// The stand-in issue-cli answers the dry run with a plan, and writes the
	// workflow file workflow init --out asks for. A real run never happens.
	dir := t.TempDir()
	bin := filepath.Join(dir, "issue-cli")
	calls := filepath.Join(dir, "calls.txt")
	body := `#!/bin/sh
echo "$*" >> ` + calls + `
case "$*" in
*"init --template development") mkdir -p issues docs && echo "statuses: []" > workflow.yaml ;;
*"department add marketing --dry-run")
	cat << 'JSON'
{"name":"Marketing","key":"marketing","files":[
 {"path":"workflows/marketing.yaml","state":"write"},
 {"path":"departments/marketing/role.md","state":"write"},
 {"path":"departments/marketing/notes.md","state":"keep"}],
 "entry":"- name: Marketing\n  prompt: departments/marketing/role.md\n  workflow: workflows/marketing.yaml\n"}
JSON
	;;
*"department add nope --dry-run") echo "no template nope" >&2; exit 1 ;;
*"workflow init --template marketing --out workflows/marketing.yaml") echo "statuses: [{name: brief}]" > workflows/marketing.yaml ;;
esac
`
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl := filepath.Join(t.TempDir(), "departments")
	os.MkdirAll(filepath.Join(tmpl, "marketing"), 0o755)
	os.WriteFile(filepath.Join(tmpl, "marketing", "role.md"), []byte("# Marketing role\n"), 0o644)
	r := runMachine(t, `
sandbox_project sbx
seed_department marketing
cat "$SANDBOX_CONFIG"
cat "$SANDBOX_PROJECT_DIR/workflows/marketing.yaml" "$SANDBOX_PROJECT_DIR/departments/marketing/role.md"
ls "$SANDBOX_PROJECT_DIR/departments/marketing"
`, "SANDBOX_ISSUE_CLI="+bin, "SEED_DEPARTMENT_TEMPLATES="+tmpl)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	contains(t, r.out, "    terminal: \"none\"\n    departments:\n      - name: Marketing\n        prompt: departments/marketing/role.md\n",
		"statuses: [{name: brief}]", "# Marketing role")
	if strings.Contains(r.out, "notes.md") {
		t.Errorf("a file the plan keeps was written:\n%s", r.out)
	}
	data, _ := os.ReadFile(calls)
	if strings.Contains(string(data), "department add marketing\n") {
		t.Fatalf("seed_department ran department add itself:\n%s", data)
	}

	r = runMachine(t, "sandbox_project sbx\nseed_department nope\necho NOT-REACHED\n", "SANDBOX_ISSUE_CLI="+bin)
	if r.code == 0 || strings.Contains(r.out, "NOT-REACHED") {
		t.Fatalf("seed_department went on after a failed dry run:\n%s", r.out)
	}
	contains(t, r.out, "no template nope")
}

// TMPDIR ends in a slash on macOS (/var/folders/…/T/) and /var links to
// /private/var: sandbox_dir's path is the resolved one, as issue-cli and a
// viewer print it.
func TestLibSandboxDirPathIsResolved(t *testing.T) {
	tmp := t.TempDir()
	r := runMachine(t, `
sandbox_dir
echo "SBX=$SANDBOX"
echo "PHYS=$(cd "$SANDBOX" && pwd -P)"
`, "TMPDIR="+tmp+"/")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	sbx := between(r.out, "SBX=", "\n")
	if strings.Contains(sbx, "//") {
		t.Fatalf("sandbox_dir path has a double slash: %q", sbx)
	}
	if phys := between(r.out, "PHYS=", "\n"); sbx != phys {
		t.Fatalf("sandbox_dir path %q, want the resolved %q", sbx, phys)
	}
	resolved, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(sbx) != resolved {
		t.Fatalf("sandbox_dir path %q, want it under %q", sbx, resolved)
	}
}

// sandbox_dir alone keeps a viewer or issue-cli the script starts off the
// shared state and config; a later sandbox_dir keeps the first sandbox's,
// sandbox_project moves both into its own; ticking the real issue gets the
// caller's back.
func TestLibSandboxDirIsolatesState(t *testing.T) {
	r := runMachine(t, `
sandbox_dir
echo "SBX=$SANDBOX"
echo "STATE=$XDG_STATE_HOME"
echo "CONF=$XDG_CONFIG_HOME"
[ -d "$XDG_STATE_HOME" ] && [ -d "$XDG_CONFIG_HOME" ] && echo DIRS=ok
other=; sandbox_dir other
echo "STATE2=$XDG_STATE_HOME"
_ck_real sh -c 'echo "REAL=$XDG_STATE_HOME $XDG_CONFIG_HOME"'
`, "XDG_CONFIG_HOME=/real/config")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.out)
	}
	sbx := between(r.out, "SBX=", "\n")
	if got := between(r.out, "STATE=", "\n"); got != sbx+"/state" {
		t.Errorf("XDG_STATE_HOME = %q, want %q", got, sbx+"/state")
	}
	if got := between(r.out, "CONF=", "\n"); got != sbx+"/config" {
		t.Errorf("XDG_CONFIG_HOME = %q, want %q", got, sbx+"/config")
	}
	contains(t, r.out, "DIRS=ok", "STATE2="+sbx+"/state\n", "REAL=/real/state /real/config")

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	p := runMachine(t, `
sandbox_dir
sandbox_project sbx
echo "PDIR=$SANDBOX_PROJECT_DIR"
echo "STATE=$XDG_STATE_HOME"
echo "CONF=$XDG_CONFIG_HOME"
`, "XDG_CONFIG_HOME=/real/config")
	if p.code != 0 {
		t.Fatalf("exit %d\n%s", p.code, p.out)
	}
	dir := filepath.Dir(between(p.out, "PDIR=", "\n"))
	contains(t, p.out, "STATE="+dir+"/state\n", "CONF="+dir+"/config\n")
}
