package checks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
	"gopkg.in/yaml.v3"
)

const linuxCheckScript = "../../scripts/linux-check.sh"

// linuxBin is a folder holding only the named tools from the real PATH, so a
// run sees no docker, mem-guard or shellcheck but the ones a test writes.
func linuxBin(t *testing.T, tools ...string) string {
	t.Helper()
	bin := t.TempDir()
	for _, name := range tools {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("no %s", name)
		}
		if err := os.Symlink(p, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(bin, ".log"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// standIn writes (or replaces) a stand-in in bin. Stand-ins log into
// .log beside themselves ("${0%/*}/.log"), so their bodies stay the same
// from test to test and run to run (internal/standin).
func standIn(t *testing.T, bin, name, body string) {
	t.Helper()
	path := filepath.Join(bin, name)
	os.Remove(path)
	standin.Write(t, path, body+"\n")
}

// standInLog is where bin's stand-ins log.
func standInLog(bin string) string { return filepath.Join(bin, ".log") }

// linuxRepo is a git checkout holding a copy of the script, one tracked file,
// and one untracked and one ignored file.
func linuxRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	script, err := os.ReadFile(linuxCheckScript)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"scripts/linux-check.sh": string(script),
		"tracked.txt":            "tracked\n",
		"gone.txt":               "deleted in the working tree\n",
		".gitignore":             "ignored.txt\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgSign=false", "commit", "-qm", "base"},
	} {
		cmd := exec.Command("git", append([]string{"-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.Remove(filepath.Join(root, "gone.txt"))
	os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("new\n"), 0o644)
	os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("no\n"), 0o644)
	return root
}

func runLinuxCheck(t *testing.T, root, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{filepath.Join(root, "scripts", "linux-check.sh")}, args...)...)
	cmd.Env = []string{"PATH=" + bin, "HOME=" + root, "TMPDIR=" + t.TempDir()}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	e, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatal(err)
	}
	return string(out), e.ExitCode()
}

var linuxTools = []string{"sh", "dirname", "git", "mktemp", "rm", "tar", "cat"}

const dockerStandIn = `log=${0%/*}/.log
case $1 in
info) ;;
build) cat > "$log/dockerfile" ;;
run) printf '%s\n' "$@" > "$log/run-args"; cat > "$log/stream" ;;
esac`

func TestLinuxCheckRefusesWhenMemGuardIsBlocked(t *testing.T) {
	root := linuxRepo(t)
	bin := linuxBin(t, linuxTools...)
	log := standInLog(bin)
	standIn(t, bin, "mem-guard", `echo "BLOCKED: memory used 80%"; exit 1`)
	standIn(t, bin, "docker", `: > "${0%/*}/.log/docker-ran"`)

	out, code := runLinuxCheck(t, root, bin)
	if code != 2 || !strings.Contains(out, "BLOCKED: memory used 80%") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(log, "docker-ran")); err == nil {
		t.Fatal("docker ran past a blocked mem-guard")
	}
}

func TestLinuxCheckSaysWhenDockerIsMissingOrDown(t *testing.T) {
	root := linuxRepo(t)
	bin := linuxBin(t, linuxTools...)
	out, code := runLinuxCheck(t, root, bin)
	if code != 2 || !strings.Contains(out, "docker is not installed") {
		t.Fatalf("no docker: exit %d:\n%s", code, out)
	}
	standIn(t, bin, "docker", `exit 1`)
	out, code = runLinuxCheck(t, root, bin)
	if code != 2 || !strings.Contains(out, "daemon doesn't answer") {
		t.Fatalf("docker down: exit %d:\n%s", code, out)
	}
}

// The image is the What of the issue: ubuntu:24.04, apt tmux and shellcheck,
// Go through GOTOOLCHAIN, a non-root user.
func TestLinuxCheckImageAndRun(t *testing.T) {
	root := linuxRepo(t)
	bin := linuxBin(t, linuxTools...)
	log := standInLog(bin)
	standIn(t, bin, "mem-guard", `exit 0`)
	standIn(t, bin, "docker", dockerStandIn)

	out, code := runLinuxCheck(t, root, bin)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	dockerfile, err := os.ReadFile(filepath.Join(log, "dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	want := []*regexp.Regexp{
		regexp.MustCompile(`(?m)^FROM ubuntu:24\.04$`),
		regexp.MustCompile(`apt-get install .*\bshellcheck\b`),
		regexp.MustCompile(`apt-get install .*\btmux\b`),
		regexp.MustCompile(`(?m)^USER ci$`),
		regexp.MustCompile(`(?m)^ENV .*GOTOOLCHAIN=go\d+\.\d+\.\d+`),
	}
	for _, re := range want {
		if !re.Match(dockerfile) {
			t.Errorf("Dockerfile lacks %s:\n%s", re, dockerfile)
		}
	}
	args, err := os.ReadFile(filepath.Join(log, "run-args"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "\n-v\n") || strings.Contains(string(args), "--volume") {
		t.Errorf("the checkout is mounted:\n%s", args)
	}
	if !strings.Contains(string(args), "--memory\n6g\n") {
		t.Errorf("no memory cap:\n%s", args)
	}

	stream := filepath.Join(log, "stream")
	unpack := exec.Command("tar", "-xf", stream, "-C", log)
	if out, err := unpack.CombinedOutput(); err != nil {
		t.Fatalf("stream: %v\n%s", err, out)
	}
	list, err := exec.Command("tar", "-tf", filepath.Join(log, "tree.tar")).CombinedOutput()
	if err != nil {
		t.Fatalf("tree.tar: %v\n%s", err, list)
	}
	files := "\n" + string(list)
	for _, f := range []string{"tracked.txt", "untracked.txt", "scripts/linux-check.sh"} {
		if !strings.Contains(files, "\n"+f+"\n") {
			t.Errorf("the working tree lacks %s:\n%s", f, list)
		}
	}
	for _, f := range []string{"ignored.txt", "gone.txt"} {
		if strings.Contains(files, "\n"+f+"\n") {
			t.Errorf("the working tree carries %s:\n%s", f, list)
		}
	}
	if _, err := os.Stat(filepath.Join(log, "head.bundle")); err != nil {
		t.Error(err)
	}
}

func TestLinuxCheckExitsWithTheContainersVerdict(t *testing.T) {
	root := linuxRepo(t)
	bin := linuxBin(t, linuxTools...)
	standIn(t, bin, "docker", dockerStandIn+`
[ "$1" = run ] && exit 1
exit 0`)
	if out, code := runLinuxCheck(t, root, bin); code != 1 {
		t.Fatalf("a red step: exit %d:\n%s", code, out)
	}
	standIn(t, bin, "docker", dockerStandIn+`
[ "$1" = run ] && exit 125
exit 0`)
	out, code := runLinuxCheck(t, root, bin)
	if code != 2 || !strings.Contains(out, "without a verdict") {
		t.Fatalf("docker failed: exit %d:\n%s", code, out)
	}
}

// Inside, every step runs after a red one and the last line names the red
// ones. Chrome is off for go test, as in ci.yml.
func TestLinuxCheckStepsAndVerdict(t *testing.T) {
	root := linuxRepo(t)
	bin := linuxBin(t, "sh", "dirname", "env")
	log := standInLog(bin)
	standIn(t, bin, "make", `echo "make $*" >> "${0%/*}/.log/calls"; exit 3`)
	standIn(t, bin, "go", `echo "go $* chrome=$PLAYTEST_CHROME" >> "${0%/*}/.log/calls"; [ "$1" = vet ]`)

	out, code := runLinuxCheck(t, root, bin, "--inside")
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	calls, err := os.ReadFile(filepath.Join(log, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	want := "make fmt-check\ngo vet ./... chrome=\ngo test ./... -count=1 chrome=none\n"
	if string(calls) != want {
		t.Errorf("calls:\n%s\nwant:\n%s", calls, want)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if got := lines[len(lines)-1]; got != "linux-check: FAIL (gofmt, go test)" {
		t.Errorf("last line %q", got)
	}

	standIn(t, bin, "make", `exit 0`)
	standIn(t, bin, "go", `exit 0`)
	out, code = runLinuxCheck(t, root, bin, "--inside")
	if code != 0 || !strings.HasSuffix(out, "linux-check: PASS\n") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// PKG and RUN (LINUX_CHECK_PKG, LINUX_CHECK_RUN) run only that go test inside,
// say so, keep the verdict line, and reach the container through docker -e.
func TestLinuxCheckSubset(t *testing.T) {
	root := linuxRepo(t)
	bin := linuxBin(t, "sh", "dirname", "env")
	log := standInLog(bin)
	standIn(t, bin, "make", `echo "make $*" >> "${0%/*}/.log/calls"`)
	standIn(t, bin, "go", `echo "go $* chrome=$PLAYTEST_CHROME" >> "${0%/*}/.log/calls"; exit 1`)

	inside := func(pkg, run string) (string, int, string) {
		t.Helper()
		os.Remove(filepath.Join(log, "calls"))
		cmd := exec.Command("sh", filepath.Join(root, "scripts", "linux-check.sh"), "--inside")
		cmd.Env = []string{"PATH=" + bin, "HOME=" + root, "LINUX_CHECK_PKG=" + pkg, "LINUX_CHECK_RUN=" + run}
		out, err := cmd.CombinedOutput()
		code := 0
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		calls, _ := os.ReadFile(filepath.Join(log, "calls"))
		return string(out), code, string(calls)
	}
	for _, c := range []struct{ pkg, run, want string }{
		{"./internal/x", "TestY|TestZ", "go test ./internal/x -run TestY|TestZ -count=1 chrome=none\n"},
		{"./internal/x", "", "go test ./internal/x -count=1 chrome=none\n"},
		{"", "TestY", "go test ./... -run TestY -count=1 chrome=none\n"},
		{"./internal/a ./internal/b", "", "go test ./internal/a ./internal/b -count=1 chrome=none\n"},
		{" ./internal/a\t./internal/b ", "TestY", "go test ./internal/a ./internal/b -run TestY -count=1 chrome=none\n"},
	} {
		out, code, calls := inside(c.pkg, c.run)
		if calls != c.want {
			t.Errorf("PKG=%q RUN=%q calls:\n%s\nwant:\n%s", c.pkg, c.run, calls, c.want)
		}
		if code != 1 || !strings.Contains(out, "subset") {
			t.Errorf("PKG=%q RUN=%q: exit %d, no subset note:\n%s", c.pkg, c.run, code, out)
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if got := lines[len(lines)-1]; got != "linux-check: FAIL (go test)" {
			t.Errorf("PKG=%q RUN=%q: last line %q", c.pkg, c.run, got)
		}
	}
	standIn(t, bin, "go", `exit 0`)
	if out, code, _ := inside("./internal/x", "TestY"); code != 0 || !strings.HasSuffix(out, "linux-check: PASS\n") {
		t.Errorf("green subset: exit %d:\n%s", code, out)
	}

	// From outside, the variables reach docker run and the run says it is a subset.
	outerBin := linuxBin(t, linuxTools...)
	standIn(t, outerBin, "docker", dockerStandIn)
	cmd := exec.Command("sh", filepath.Join(root, "scripts", "linux-check.sh"))
	cmd.Env = []string{"PATH=" + outerBin, "HOME=" + root, "TMPDIR=" + t.TempDir(),
		"LINUX_CHECK_PKG=./internal/x", "LINUX_CHECK_RUN=TestY"}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "a subset") {
		t.Fatalf("%v:\n%s", err, out)
	}
	args, err := os.ReadFile(filepath.Join(standInLog(outerBin), "run-args"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\n-e\nLINUX_CHECK_PKG=./internal/x\n", "\n-e\nLINUX_CHECK_RUN=TestY\n"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("docker run lacks %q:\n%s", want, args)
		}
	}

	// make linux-check PKG= RUN= hands them to the script.
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(makefile), "LINUX_CHECK_PKG='$(PKG)' LINUX_CHECK_RUN='$(RUN)' scripts/linux-check.sh") {
		t.Error("make linux-check doesn't pass PKG and RUN to the script")
	}
}

// ci.yml is the source: a step it gains that the script doesn't run fails here.
func TestLinuxCheckRunsCIsSteps(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var ci struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Run  string            `yaml:"run"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &ci); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(linuxCheckScript)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, job := range ci.Jobs {
		for _, s := range job.Steps {
			// Installing is the image's job.
			if s.Run == "" || strings.Contains(s.Run, "apt-get") {
				continue
			}
			checked++
			var env strings.Builder
			for k, v := range s.Env {
				fmt.Fprintf(&env, "env %s=%s ", k, v)
			}
			if want := " " + env.String() + s.Run + "\n"; !strings.Contains(string(script), want) {
				t.Errorf("ci.yml step %q: the script has no run_step ending %q", s.Name, want)
			}
		}
	}
	if checked < 3 {
		t.Errorf("read %d steps from ci.yml", checked)
	}
}

func TestLinuxCheckGoFollowsGoMod(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(linuxCheckScript)
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`(?m)^go (\d+\.\d+)`).FindSubmatch(mod)
	version := regexp.MustCompile(`(?m)^go_version=go(\d+\.\d+)\.\d+$`).FindSubmatch(script)
	if line == nil || version == nil {
		t.Fatalf("go.mod line %q, script version %q", line, version)
	}
	if string(line[1]) != string(version[1]) {
		t.Errorf("go.mod is go %s, the script names go%s.x", line[1], version[1])
	}
}

func TestLinuxCheckScriptPassesShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip(noShellcheck)
	}
	if out, err := exec.Command("shellcheck", "-s", "sh", "-S", "warning", linuxCheckScript).CombinedOutput(); err != nil {
		t.Fatalf("shellcheck:\n%s", out)
	}
}

// go test hides a passing run's output, skips included: the test names what
// went unchecked under -v, and make test prints it on every run.
func TestShellcheckSkipSaysSo(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestLibPassesShellcheck$", "-test.v")
	cmd.Env = []string{"PATH=" + t.TempDir(), "HOME=" + t.TempDir()}
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "SKIP: TestLibPassesShellcheck") || !strings.Contains(string(out), noShellcheck) {
		t.Errorf("the skip says:\n%s", out)
	}
}

func TestMakeTestSaysShellcheckIsMissing(t *testing.T) {
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), makefile, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := linuxBin(t, "make", "sh", "git", "rm")
	standIn(t, bin, "go", `exit 0`)
	run := func() string {
		t.Helper()
		cmd := exec.Command("make", "test")
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + bin, "HOME=" + root}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("make test: %v\n%s", err, out)
		}
		return string(out)
	}
	const note = "TestLibPassesShellcheck skips"
	if out := run(); !strings.Contains(out, note) || !strings.Contains(out, "make linux-check") {
		t.Errorf("no shellcheck, no note:\n%s", out)
	}
	standIn(t, bin, "shellcheck", `exit 0`)
	if out := run(); strings.Contains(out, note) {
		t.Errorf("shellcheck on PATH, still the note:\n%s", out)
	}
}
