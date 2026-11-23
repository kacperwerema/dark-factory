package viewer

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/standin"
)

// make validate ends with one line that says how it went: a run in the
// background, its output in a log and a wrapper's exit 0 after it, can't
// hide a red step. A red step doesn't stop the run: every step runs, each red
// one is named in `validate: FAIL (<step>, <step>)` and the exit is non-zero.
// No steps at all is `validate: FAIL (no steps ran)`.
func TestMakeValidateEndsWithOnePassOrFailLine(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	makefile, err := os.ReadFile(repoPath("Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	stubs := "\nstub-ok:\n\t@echo ok ran\n\nstub-red:\n\t@echo red ran; exit 3\n\nstub-red-too:\n\t@echo also red ran; exit 4\n\nstub-after:\n\t@echo after ran\n"
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), append(makefile, stubs...), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(steps string) (stdout, combined string, err error) {
		t.Helper()
		// The cover profiles go in the test's folder, not TMPDIR, which
		// the guard holds to empty (internal/tmuxguard).
		cmd := exec.Command("make", "validate", "VALIDATE_STEPS="+steps, "CLI_COVER_PROFILE="+filepath.Join(root, "cover.out"))
		cmd.Dir = root
		var out strings.Builder
		both := &lockedBuilder{}
		cmd.Stdout = io.MultiWriter(&out, both)
		cmd.Stderr = both
		err = cmd.Run()
		return out.String(), both.b.String(), err
	}
	lastLine := func(s string) string {
		lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
		return lines[len(lines)-1]
	}

	out, both, err := run("stub-ok stub-red stub-after")
	if err == nil {
		t.Fatalf("a red step: make validate exited 0:\n%s", both)
	}
	if got := lastLine(out); got != "validate: FAIL (stub-red)" {
		t.Errorf("a red step: last line %q, want %q:\n%s", got, "validate: FAIL (stub-red)", both)
	}
	if !strings.Contains(out, "ok ran") || !strings.Contains(out, "after ran") {
		t.Errorf("a red step must not stop the steps after it:\n%s", both)
	}

	soFar := "validate: red so far: stub-red\n"
	if i, j := strings.Index(out, soFar), strings.Index(out, "after ran"); i < 0 || j < 0 || i > j {
		t.Errorf("a red step must say so before the next step runs, not only at the end:\n%s", both)
	}

	out, both, err = run("stub-red stub-ok stub-red-too stub-after")
	if err == nil {
		t.Fatalf("two red steps: make validate exited 0:\n%s", both)
	}
	if got, want := lastLine(out), "validate: FAIL (stub-red, stub-red-too)"; got != want {
		t.Errorf("two red steps: last line %q, want %q:\n%s", got, want, both)
	}
	for _, ran := range []string{"red ran", "ok ran", "also red ran", "after ran"} {
		if !strings.Contains(out, ran) {
			t.Errorf("every step runs, %q is missing:\n%s", ran, both)
		}
	}
	if !strings.Contains(out, soFar) || !strings.Contains(out, "validate: red so far: stub-red, stub-red-too\n") {
		t.Errorf("each red step adds to the red so far line:\n%s", both)
	}

	out, both, err = run("stub-ok stub-after")
	if err != nil {
		t.Fatalf("green steps: %v\n%s", err, both)
	}
	if got := lastLine(out); got != "validate: PASS" {
		t.Errorf("green steps: last line %q, want %q:\n%s", got, "validate: PASS", both)
	}
	if got := lastLine(both); got != "validate: PASS" {
		t.Errorf("green steps: last line with stderr %q:\n%s", got, both)
	}
	if strings.Contains(out, "red so far") {
		t.Errorf("green steps print no red so far line:\n%s", both)
	}
	// Each step prints its wall time when it ends, the total comes just
	// before the verdict (local CI reads them: internal/ci ParseSteps).
	timed := regexp.MustCompile(`(?m)^validate: (stub-ok|stub-after|total) \d+s$`).FindAllStringSubmatch(out, -1)
	if len(timed) != 3 || timed[0][1] != "stub-ok" || timed[1][1] != "stub-after" || timed[2][1] != "total" {
		t.Errorf("want a time line per step, then the total:\n%s", both)
	}
	if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); len(lines) < 2 || !strings.HasPrefix(lines[len(lines)-2], "validate: total ") {
		t.Errorf("the total must come just before the verdict:\n%s", both)
	}

	// No step ran: nothing was checked, so it can't read as a PASS.
	for _, steps := range []string{"", "   "} {
		out, both, err = run(steps)
		if err == nil {
			t.Fatalf("VALIDATE_STEPS=%q: make validate exited 0:\n%s", steps, both)
		}
		if got, want := lastLine(out), "validate: FAIL (no steps ran)"; got != want {
			t.Errorf("VALIDATE_STEPS=%q: last line %q, want %q:\n%s", steps, got, want, both)
		}
	}
}

// The steps that take seconds run beside the suite that takes minutes and report when they end, so
// a worker watching the log can stop early, and leak-check's and
// codemap-check's tests exist: go test -run on a name that matches nothing
// passes.
func TestValidateRunsItsCheapStepsFirst(t *testing.T) {
	makefile, err := os.ReadFile(repoPath("Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^VALIDATE_STEPS \?= (.*)$`).FindSubmatch(makefile)
	if m == nil {
		t.Fatal("no VALIDATE_STEPS ?= line in the Makefile")
	}
	want := "merge-check fmt-check vet catalog-check link-check leak-check codemap-check guard-check playtest-check test cover-cli"
	if got := string(m[1]); got != want {
		t.Errorf("VALIDATE_STEPS = %q, want %q", got, want)
	}

	m = regexp.MustCompile(`(?m)^leak-check:\n\t.* -run '\^\((.*)\)\$\$' -count=1$`).FindSubmatch(makefile)
	if m == nil {
		t.Fatal("no leak-check recipe with a -run '^(...)$$' list in the Makefile")
	}
	tests, err := filepath.Glob(repoPath("internal/privacy/*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	for _, f := range tests {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	for _, name := range strings.Split(string(m[1]), "|") {
		if !strings.Contains(src.String(), "func "+name+"(t *testing.T)") {
			t.Errorf("leak-check runs %s, which internal/privacy doesn't have: the step would pass on nothing", name)
		}
	}

	m = regexp.MustCompile(`(?m)^codemap-check:\n\t.* \./internal/codemap -run '\^(\w+)\$\$' -count=1$`).FindSubmatch(makefile)
	if m == nil {
		t.Fatal("no codemap-check recipe running one ./internal/codemap test in the Makefile")
	}
	b, err := os.ReadFile(repoPath("internal/codemap/codemap_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "func "+string(m[1])+"(t *testing.T)") {
		t.Errorf("codemap-check runs %s, which internal/codemap doesn't have: the step would pass on nothing", m[1])
	}

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	// The cheap steps run beside the suite, not before it: the stub suite
	// waits for the slow cheap step to have started and the slow cheap step
	// waits for the suite to have started, so one after the other runs out
	// of time. A red cheap step says so while the suite still runs, the
	// steps after test wait for both, and the verdict names the red steps in
	// VALIDATE_STEPS order.
	root := t.TempDir()
	marks := t.TempDir()
	wait := func(file string) string {
		return fmt.Sprintf("i=0; while [ ! -f %s/%s ]; do i=$$((i+1)); [ $$i -lt 600 ] || { echo gave up on %s; exit 9; }; sleep 0.1; done", marks, file, file)
	}
	stubs := "\nstub-red:\n\t@echo cheap red ran; exit 3\n" +
		"\nstub-slow:\n\t@touch " + marks + "/cheap; " + wait("suite") + "; echo slow cheap ran\n" +
		"\ntest:\n\t@touch " + marks + "/suite; " + wait("cheap") + "; " + wait("red-seen") + "; echo suite ran with $(GUARD_SKIP)/$(COVER_FROM_TEST); exit 5\n" +
		"\nstub-after:\n\t@echo after ran\n" +
		"\nguard-check:\n\t@echo guard stub ran\n"
	if err := os.WriteFile(filepath.Join(root, "Makefile"), append(makefile, stubs...), 0o644); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "MAKEFLAGS=") && !strings.HasPrefix(kv, "MFLAGS=") && !strings.HasPrefix(kv, "MAKELEVEL=") {
			env = append(env, kv)
		}
	}
	cmd := exec.Command("make", "validate", "VALIDATE_STEPS=stub-red stub-slow guard-check test stub-after", "GUARD_COVER_PROFILE="+filepath.Join(marks, "guard.out"), "CLI_COVER_PROFILE="+filepath.Join(marks, "cover.out"))
	cmd.Dir = root
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := stdout.Read(buf)
		out.Write(buf[:n])
		// The suite ends only once the red line is out: it is printed at
		// once, not when the suite is done.
		if strings.Contains(out.String(), "validate: red so far: stub-red\n") {
			if _, err := os.Stat(filepath.Join(marks, "red-seen")); err != nil {
				os.WriteFile(filepath.Join(marks, "red-seen"), nil, 0o644)
			}
		}
		if rerr != nil {
			break
		}
	}
	err = cmd.Wait()
	got := out.String()
	if err == nil {
		t.Fatalf("red steps: make validate exited 0:\n%s", got)
	}
	if strings.Contains(got, "gave up on") {
		t.Fatalf("the cheap steps ran one after the other with the suite, not beside it:\n%s", got)
	}
	for _, line := range []string{"[stub-red] cheap red ran\n", "[stub-slow] slow cheap ran\n", "[guard-check] guard stub ran\n", "suite ran with 1/1\n", "after ran\n"} {
		if !strings.Contains(got, line) {
			t.Errorf("missing %q (each cheap step's output whole, led by its name; the suite gets GUARD_SKIP and COVER_FROM_TEST):\n%s", line, got)
		}
	}
	if i, j := strings.Index(got, "slow cheap ran"), strings.Index(got, "after ran"); i < 0 || j < i {
		t.Errorf("the steps after test must wait for the cheap steps:\n%s", got)
	}
	for _, s := range []string{"stub-red", "stub-slow", "guard-check", "test", "stub-after", "total"} {
		if !regexp.MustCompile(`(?m)^validate: ` + s + ` \d+s$`).MatchString(got) {
			t.Errorf("no time line for %s (a cheap step's too, printed beside the suite):\n%s", s, got)
		}
	}
	if i, j := strings.Index(got, "[stub-slow] slow cheap ran"), strings.Index(got, "validate: stub-slow "); i < 0 || j < i {
		t.Errorf("a cheap step's time comes after its output:\n%s", got)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	verdict := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "validate: FAIL") || strings.HasPrefix(l, "validate: PASS") {
			verdict = l
		}
	}
	if want := "validate: FAIL (stub-red, test)"; verdict != want {
		t.Errorf("verdict %q, want %q:\n%s", verdict, want, got)
	}
}

// The guard tests run once in make validate: guard-check runs them, and the
// suite skips each one guard-check lists (GUARD_SKIP=1 adds -skip on
// GUARD_TESTS) only when validate runs guard-check too; make test alone runs
// them all. Their cmd/issue-cli coverage goes from guard-check's profile into
// cover-cli's number.
func TestValidateRunsTheGuardTestsOnce(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "MAKEFLAGS=") && !strings.HasPrefix(kv, "MFLAGS=") && !strings.HasPrefix(kv, "MAKELEVEL=") && !strings.HasPrefix(kv, "GUARD_SKIP=") && !strings.HasPrefix(kv, "COVER_FROM_TEST=") {
			env = append(env, kv)
		}
	}
	dry := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("make", append([]string{"-n", "--no-print-directory"}, args...)...)
		cmd.Dir = repoPath()
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("make -n %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	listed, err := readGuardList(repoPath("scripts", "guard-tests.txt"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`-skip '\^\(([^)]*)\)\$'`).FindStringSubmatch(dry("test", "GUARD_SKIP=1"))
	if m == nil {
		t.Fatal("make test GUARD_SKIP=1 passes no -skip '^(...)$' to go test")
	}
	skipped := map[string]bool{}
	for _, name := range strings.Split(m[1], "|") {
		skipped[name] = true
	}
	for line := range listed {
		_, name, _ := strings.Cut(line, " ")
		if !skipped[name] {
			t.Errorf("guard-check runs %s but the suite doesn't skip it: it runs twice", name)
		}
	}
	if len(skipped) != len(nameSet(listed)) {
		t.Errorf("the suite skips %d tests, guard-check runs %d: it would skip one no step runs", len(skipped), len(nameSet(listed)))
	}
	if strings.Contains(dry("test"), "-skip") {
		t.Error("make test alone skips tests: only validate with guard-check among its steps may")
	}
	if out := dry("guard-check", "COVER_FROM_TEST=1", "GUARD_COVER_PROFILE=/x/guard.out"); !strings.Contains(out, "GUARD_COVER_PKG=./cmd/issue-cli/... GUARD_COVER_PROFILE=/x/guard.out scripts/guard-check.sh") {
		t.Errorf("guard-check in validate writes no cmd/issue-cli coverage profile:\n%s", out)
	}
	makefile, err := os.ReadFile(repoPath("Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^VALIDATE_FLAGS = .*\$\(filter guard-check,\$\(VALIDATE_STEPS\)\),GUARD_SKIP=1`).Match(makefile) {
		t.Error("validate doesn't pass GUARD_SKIP=1 to its steps when guard-check is one")
	}
}

// nameSet is the test names of guard-tests.txt lines, without packages.
func nameSet(listed map[string]bool) map[string]bool {
	out := map[string]bool{}
	for line := range listed {
		_, name, _ := strings.Cut(line, " ")
		out[name] = true
	}
	return out
}

// In a linked worktree make validate writes its whole output to the
// worktree's own git dir (git rev-parse --git-path validate.log) and names
// it on its first line: one file per worktree, never a shared /tmp name
// another session's run can overwrite, and outside the work tree, so no
// test walking the checkout sees it grow. A run already redirected into that
// file (the briefing's shell note) isn't written twice; VALIDATE_LOG= turns
// it off; the main checkout has none. The verdict and exit code don't change.
func TestValidateWritesItsLogIntoTheWorktreesGitDir(t *testing.T) {
	for _, tool := range []string{"make", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	makefile, err := os.ReadFile(repoPath("Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	stubs := "\nstub-ok:\n\t@echo ok ran; echo ok to stderr >&2\n\nstub-red:\n\t@echo red ran; exit 3\n"
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main, wt, wt2 := filepath.Join(base, "main"), filepath.Join(base, "wt"), filepath.Join(base, "wt2")
	env := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "VALIDATE_LOG=") && !strings.HasPrefix(kv, "GIT_DIR=") && !strings.HasPrefix(kv, "GIT_WORK_TREE=") {
			env = append(env, kv)
		}
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(main, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(main, "Makefile"), append(makefile, stubs...), 0o644); err != nil {
		t.Fatal(err)
	}
	git(main, "add", "Makefile")
	git(main, "commit", "-q", "-m", "makefile")
	git(main, "worktree", "add", "-q", "-b", "one", wt)
	git(main, "worktree", "add", "-q", "-b", "two", wt2)
	logOf := func(dir string) string {
		return git(dir, "rev-parse", "--path-format=absolute", "--git-path", "validate.log")
	}
	wtLog, wt2Log := logOf(wt), logOf(wt2)
	if wtLog == wt2Log || strings.HasPrefix(wtLog, wt+string(filepath.Separator)) || !strings.HasPrefix(wtLog, filepath.Join(main, ".git")) {
		t.Fatalf("each worktree's log must be its own, in its git dir: %s, %s", wtLog, wt2Log)
	}
	run := func(dir string, stdout io.Writer, args ...string) (combined string, err error) {
		t.Helper()
		cmd := exec.Command("make", append([]string{"validate", "CLI_COVER_PROFILE=" + filepath.Join(base, "cover.out")}, args...)...)
		cmd.Dir, cmd.Env = dir, env
		both := &lockedBuilder{}
		if stdout == nil {
			cmd.Stdout = both
		} else {
			cmd.Stdout = stdout
		}
		cmd.Stderr = both
		err = cmd.Run()
		return both.b.String(), err
	}
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return string(b)
	}
	lastValidate := func(s string) string {
		last := ""
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(l, "validate: ") {
				last = l
			}
		}
		return last
	}

	out, err := run(wt, nil, "VALIDATE_STEPS=stub-ok")
	if err != nil {
		t.Fatalf("green run: %v\n%s", err, out)
	}
	if first := strings.SplitN(out, "\n", 2)[0]; first != "validate: log "+wtLog {
		t.Errorf("first line %q, want it to name the log %s:\n%s", first, wtLog, out)
	}
	log := read(wtLog)
	if lastValidate(log) != "validate: PASS" || !strings.Contains(log, "ok ran") || !strings.Contains(log, "ok to stderr") || !strings.HasPrefix(log, "validate: log ") {
		t.Errorf("the log must hold the whole run, stderr too, and its verdict:\n%s", log)
	}
	if st := git(wt, "status", "--porcelain", "--ignored"); st != "" {
		t.Errorf("the log must stay out of the work tree, git status says:\n%s", st)
	}

	out, err = run(wt, nil, "VALIDATE_STEPS=stub-ok stub-red")
	if err == nil {
		t.Fatalf("red run exited 0:\n%s", out)
	}
	if got := lastValidate(read(wtLog)); got != "validate: FAIL (stub-red)" {
		t.Errorf("red run: the log's verdict is %q:\n%s", got, read(wtLog))
	}

	// The other worktree's run writes its own file, not this one's.
	before := read(wtLog)
	if out, err := run(wt2, nil, "VALIDATE_STEPS=stub-ok"); err != nil {
		t.Fatalf("second worktree: %v\n%s", err, out)
	}
	if read(wtLog) != before || lastValidate(read(wt2Log)) != "validate: PASS" {
		t.Errorf("a run in another worktree touched this one's log, or wrote none of its own")
	}

	// make validate > <that file>: written once, not teed over itself.
	f, err := os.Create(wtLog)
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(wt, f, "VALIDATE_STEPS=stub-ok")
	f.Close()
	if err != nil {
		t.Fatalf("redirected run: %v", err)
	}
	if log := read(wtLog); strings.Contains(log, "validate: log ") || strings.Count(log, "validate: PASS") != 1 || strings.Count(log, "ok ran") != 1 {
		t.Errorf("a run redirected into its log must write it once:\n%s", log)
	}

	// VALIDATE_LOG= in a worktree, and the main checkout: no log.
	for _, c := range []struct {
		name, dir string
		args      []string
		path      string
	}{
		{"VALIDATE_LOG=", wt2, []string{"VALIDATE_LOG="}, wt2Log},
		{"the main checkout", main, nil, filepath.Join(main, ".git", "validate.log")},
	} {
		_ = os.Remove(c.path)
		out, err := run(c.dir, nil, append(c.args, "VALIDATE_STEPS=stub-ok")...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.name, err, out)
		}
		if strings.Contains(out, "validate: log ") || lastValidate(out) != "validate: PASS" {
			t.Errorf("%s: want no log line and the verdict:\n%s", c.name, out)
		}
		if _, err := os.Stat(c.path); err == nil {
			t.Errorf("%s: wrote %s", c.name, c.path)
		}
	}
}

// lockedBuilder takes stdout and stderr, which exec copies in two
// goroutines, in the order they come.
type lockedBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuilder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// make validate checks cmd/issue-cli's coverage floor without running its
// tests twice: the test step writes the profile (-coverpkg on
// cmd/issue-cli's packages, tools/test-shards merging its shards' profiles)
// and cover-cli, the step after it, reads it.
// The floor line and the red on a low or missing profile stay, and
// cover-cli alone still runs the tests itself.
func TestValidateCoverageFloorReadsTheTestRun(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	makefile, err := os.ReadFile(repoPath("Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, re := range []string{
		`(?m)^CLI_COVER_PKG := \./cmd/issue-cli/\.\.\.$`,
		`(?m)^test:\n(\t.*\n)*\tgo run \./tools/test-shards .* -- .* -coverpkg=\$\(CLI_COVER_PKG\) -coverprofile=\$\(CLI_COVER_PROFILE\)`,
		`(?m)^VALIDATE_STEPS \?= .* test cover-cli$`,
	} {
		if !regexp.MustCompile(re).Match(makefile) {
			t.Errorf("the Makefile has no line matching %s: the floor check would no longer read the suite's own run", re)
		}
	}

	// The make running this test passes its own variables down (MAKEFLAGS):
	// the makes below start without them.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "MAKEFLAGS=") && !strings.HasPrefix(kv, "MFLAGS=") && !strings.HasPrefix(kv, "MAKELEVEL=") && !strings.HasPrefix(kv, "COVER_FROM_TEST=") {
			env = append(env, kv)
		}
	}

	// validate tells cover-cli to read the profile only when test is a step.
	root := t.TempDir()
	stubs := "\ntest:\n\t@echo stub test\n\nstub-from:\n\t@echo from-test=$(COVER_FROM_TEST).\n"
	if err := os.WriteFile(filepath.Join(root, "Makefile"), append(makefile, stubs...), 0o644); err != nil {
		t.Fatal(err)
	}
	for steps, want := range map[string]string{"test stub-from": "from-test=1.", "stub-from": "from-test=."} {
		cmd := exec.Command("make", "validate", "VALIDATE_STEPS="+steps, "CLI_COVER_PROFILE="+filepath.Join(root, "cover.out"))
		cmd.Dir = root
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), want+"\n") {
			t.Errorf("VALIDATE_STEPS=%q: want %q, got %v:\n%s", steps, want, err, out)
		}
	}

	// cover-cli on a profile of two blocks in main.go's first two funcs, one
	// of them run: 50%.
	src, err := os.ReadFile(repoPath("cmd/issue-cli/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	var funcs []int
	for i, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(line, "func ") && strings.HasSuffix(line, "{") {
			funcs = append(funcs, i+1)
		}
	}
	if len(funcs) < 2 {
		t.Fatalf("cmd/issue-cli/main.go has %d funcs, want 2 to build a profile", len(funcs))
	}
	const main = "github.com/kacperwerema/dark-factory/cmd/issue-cli/main.go"
	block := func(line, count int) string {
		return fmt.Sprintf("%s:%d.1,%d.2 1 %d\n", main, line+1, line+1, count)
	}
	profile := filepath.Join(t.TempDir(), "cover.out")
	if err := os.WriteFile(profile, []byte("mode: set\n"+block(funcs[0], 1)+block(funcs[1], 0)), 0o644); err != nil {
		t.Fatal(err)
	}
	cover := func(floor, prof string) (string, error) {
		t.Helper()
		cmd := exec.Command("make", "--no-print-directory", "cover-cli", "COVER_FROM_TEST=1", "CLI_COVER_PROFILE="+prof, "CLI_COVERAGE_FLOOR="+floor)
		cmd.Dir = repoPath()
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := cover("40", profile); err != nil || !strings.Contains(out, "cmd/issue-cli coverage: 50.0% (floor: 40%)\n") {
		t.Errorf("above the floor: want the coverage line and exit 0, got %v:\n%s", err, out)
	}
	if out, err := cover("60", profile); err == nil || !strings.Contains(out, "cmd/issue-cli coverage: 50.0% (floor: 60%)\nFAIL: coverage below floor\n") {
		t.Errorf("below the floor: want the line, FAIL and a red exit, got %v:\n%s", err, out)
	}
	missing := filepath.Join(t.TempDir(), "none.out")
	if out, err := cover("40", missing); err == nil || !strings.Contains(out, "cover-cli: no coverage profile at "+missing) {
		t.Errorf("no profile from the test step: want a red exit naming it, got %v:\n%s", err, out)
	}
}

// A red test step hands its output to MAIN_FIX_HINT (tools/main-fix-hint,
// which names main's commits touching a failing test: ship.MainFixHints),
// and the hint comes before the verdict; a green suite runs no hint.
func TestValidateHandsARedSuitesOutputToTheMainFixHint(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	makefile, err := os.ReadFile(repoPath("Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	hint := filepath.Join(root, "hint.sh")
	standin.Write(t, hint, "#!/bin/sh\ngrep -q 'FAIL: TestA' \"$1\" && echo 'validate: main has abc1234 fix: TestA touching a_test.go: merge main and re-run'\n")
	stubs := "\nstub-ok:\n\t@echo ok ran\n" +
		"\ntest:\n\t@echo '--- FAIL: TestA (0.00s)'; [ -z \"$(SUITE_GREEN)\" ] || exit 0; exit 1\n"
	if err := os.WriteFile(filepath.Join(root, "Makefile"), append(makefile, stubs...), 0o644); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "MAKEFLAGS=") && !strings.HasPrefix(kv, "MFLAGS=") && !strings.HasPrefix(kv, "MAKELEVEL=") {
			env = append(env, kv)
		}
	}
	run := func(extra ...string) (string, error) {
		args := append([]string{"validate", "VALIDATE_STEPS=stub-ok test", "VALIDATE_LOG=", "MAIN_FIX_HINT=" + hint, "CLI_COVER_PROFILE=" + filepath.Join(root, "cover.out"), "GUARD_COVER_PROFILE=" + filepath.Join(root, "guard.out")}, extra...)
		cmd := exec.Command("make", args...)
		cmd.Dir = root
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	const want = "validate: main has abc1234 fix: TestA touching a_test.go: merge main and re-run\n"
	got, err := run()
	if err == nil {
		t.Fatalf("a red suite: make validate exited 0:\n%s", got)
	}
	i, j := strings.Index(got, want), strings.Index(got, "validate: FAIL (test)")
	if i < 0 || j < i {
		t.Errorf("want the hint before the verdict:\n%s", got)
	}
	if !strings.Contains(got, "--- FAIL: TestA") {
		t.Errorf("the suite's own output must still show:\n%s", got)
	}
	got, err = run("SUITE_GREEN=1")
	if err != nil {
		t.Fatalf("a green suite: %v\n%s", err, got)
	}
	if strings.Contains(got, "main has") {
		t.Errorf("a green suite runs no hint:\n%s", got)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "validate: PASS") {
		t.Errorf("a green suite: want validate: PASS last:\n%s", got)
	}
}
