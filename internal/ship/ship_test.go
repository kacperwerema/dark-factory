package ship

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a repository with a main checkout on main and a linked
// worktree on work/cli/x with one commit of its own.
type fixture struct {
	t        *testing.T
	main, wt string
	log      string // the checks append one word per run here
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOut(dir, args...)
	if err != nil {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return out
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		t.Setenv(k, v)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, main: filepath.Join(root, "repo"), wt: filepath.Join(root, "wt"), log: filepath.Join(root, "checks.log")}
	if err := os.MkdirAll(f.main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, f.main, "init", "-q", "-b", "main")
	f.commit(f.main, "a.txt", "a\n", "init")
	git(t, f.main, "worktree", "add", "-q", "-b", "work/cli/x", f.wt, "main")
	f.commit(f.wt, "b.txt", "b\n", "feat: b")
	return f
}

func (f *fixture) commit(dir, file, body, msg string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
	git(f.t, dir, "add", file)
	git(f.t, dir, "commit", "-q", "-m", msg)
}

func (f *fixture) mainHead() string { return git(f.t, f.main, "rev-parse", "main") }

// runs is the words the checks wrote, in order.
func (f *fixture) runs() []string {
	b, _ := os.ReadFile(f.log)
	return strings.Fields(string(b))
}

// pass is a check that logs word and prints make validate's PASS line.
func (f *fixture) pass(word string) string {
	return "echo " + word + " >> " + f.log + "; echo 'validate: test 1s'; echo 'validate: PASS'"
}

func (f *fixture) opts(out *bytes.Buffer) Options {
	return Options{
		Worktree: f.wt, MainDir: f.main, Main: "main", Branch: "work/cli/x", Slug: "cli/x",
		Check: []string{f.pass("full")}, Recheck: []string{f.pass("recheck")}, Out: out,
	}
}

func TestShipLandsAfterACleanMerge(t *testing.T) {
	f := newFixture(t)
	f.commit(f.main, "c.txt", "c\n", "feat: c on main")
	var out bytes.Buffer
	res := Run(f.opts(&out))
	if !res.Landed || res.ExitCode() != ExitLanded {
		t.Fatalf("not landed: %s\n%s", res.Verdict(), out.String())
	}
	head := git(t, f.wt, "rev-parse", "HEAD")
	if f.mainHead() != head || res.SHA != head {
		t.Errorf("main %s, branch %s, result %s: want all equal", f.mainHead(), head, res.SHA)
	}
	if subj := git(t, f.wt, "log", "-1", "--format=%s"); subj != "chore(merge): main into cli/x" {
		t.Errorf("merge subject %q", subj)
	}
	if got := f.runs(); strings.Join(got, ",") != "full" {
		t.Errorf("checks run %v, want [full]", got)
	}
	if !strings.HasPrefix(res.Verdict(), "ship: LANDED ") || strings.Contains(res.Verdict(), "FAIL") {
		t.Errorf("verdict %q", res.Verdict())
	}
	if !strings.Contains(out.String(), "  validate: test 1s") {
		t.Errorf("step lines not echoed:\n%s", out.String())
	}
	if len(res.Records) != 2 || !strings.HasPrefix(res.Records[0], "check PASS at ") {
		t.Errorf("records %q", res.Records)
	}
}

func TestShipReMergesAndReChecksWhenMainMoves(t *testing.T) {
	f := newFixture(t)
	o := f.opts(&bytes.Buffer{})
	moved := false
	o.BeforeFF = func() {
		if !moved {
			moved = true
			f.commit(f.main, "m.txt", "m\n", "feat: main moved")
		}
	}
	res := Run(o)
	if !res.Landed || res.ExitCode() != ExitLanded {
		t.Fatalf("not landed: %s", res.Verdict())
	}
	if got := strings.Join(f.runs(), ","); got != "full,recheck,full" {
		t.Errorf("checks run %s, want full,recheck,full (full, re-check after the clean re-merge, full on the landed tree)", got)
	}
	if !res.AfterRan || res.AfterRed != nil {
		t.Errorf("after-landing check: ran %v red %v", res.AfterRan, res.AfterRed)
	}
	if _, err := os.Stat(filepath.Join(f.main, "m.txt")); err != nil {
		t.Errorf("main lost its own commit: %v", err)
	}
	if !PassedAt(f.wt, f.mainHead()) {
		t.Error("the landed tree's PASS is not remembered")
	}
}

func TestShipLandedButRedAfterLandingExitsThree(t *testing.T) {
	f := newFixture(t)
	o := f.opts(&bytes.Buffer{})
	// The full check passes the first time and fails the second.
	o.Check = []string{"echo full >> " + f.log + "; if [ $(grep -c full " + f.log + ") -gt 1 ]; then echo 'validate: FAIL (test)'; exit 1; fi; echo 'validate: PASS'"}
	moved := false
	o.BeforeFF = func() {
		if !moved {
			moved = true
			f.commit(f.main, "m.txt", "m\n", "feat: main moved")
		}
	}
	res := Run(o)
	if !res.Landed || res.ExitCode() != ExitLandedButRed {
		t.Fatalf("exit %d, verdict %s", res.ExitCode(), res.Verdict())
	}
	if !strings.HasSuffix(res.Verdict(), ", check after landing FAIL (test)") {
		t.Errorf("verdict %q", res.Verdict())
	}
}

func TestShipStopsOnAConflictWithMainUntouched(t *testing.T) {
	f := newFixture(t)
	f.commit(f.main, "b.txt", "other\n", "feat: b on main")
	before := f.mainHead()
	res := Run(f.opts(&bytes.Buffer{}))
	if res.Landed || res.ExitCode() != ExitNotLanded {
		t.Fatalf("landed on a conflict: %s", res.Verdict())
	}
	if !strings.HasPrefix(res.Verdict(), "ship: NOT LANDED (conflict in b.txt") {
		t.Errorf("verdict %q", res.Verdict())
	}
	if f.mainHead() != before {
		t.Error("main moved")
	}
	if len(f.runs()) != 0 {
		t.Errorf("checks ran on a conflict: %v", f.runs())
	}
	// The merge is left for the worker; a second ship refuses until it is done.
	if res2 := Run(f.opts(&bytes.Buffer{})); !strings.Contains(res2.Reason, "a merge is in progress") {
		t.Errorf("second run: %q", res2.Reason)
	}
}

func TestShipRedCheckNamesItsStepsAndLeavesMain(t *testing.T) {
	f := newFixture(t)
	before := f.mainHead()
	o := f.opts(&bytes.Buffer{})
	o.Check = []string{"echo 'validate: red so far: vet'; echo 'validate: FAIL (vet, test)'; exit 2"}
	res := Run(o)
	if res.ExitCode() != ExitNotLanded || !strings.HasPrefix(res.Reason, "check FAIL (vet, test) at ") {
		t.Fatalf("exit %d, reason %q", res.ExitCode(), res.Reason)
	}
	if f.mainHead() != before {
		t.Error("main moved on a red check")
	}
}

// standInRed is the stand-in go test output with two red packages and a
// tmuxguard leftover line (internal/testresults/testdata).
func standInRed(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "testresults", "testdata", "go-test-two-red-packages.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestShipRedCheckNamesTheFailingPackagesAndTheLeftover(t *testing.T) {
	f := newFixture(t)
	f.commit(f.wt, "go.mod", "module example.com/standin\n", "chore: go.mod")
	var out bytes.Buffer
	o := f.opts(&out)
	o.Check = []string{"cat '" + standInRed(t) + "'; exit 2"}
	res := Run(o)
	if want := "check FAIL (fmt-check, test: internal/beta, internal/delta) at "; res.Landed || !strings.HasPrefix(res.Reason, want) {
		t.Fatalf("reason %q, want it to start %q", res.Reason, want)
	}
	if !strings.Contains(out.String(), "ship: tmuxguard: delta.test left dft-sock-1, scratch.txt in TMPDIR") || strings.Contains(out.String(), "second.txt") {
		t.Errorf("output does not say the first leftover line alone:\n%s", out.String())
	}
	if !strings.Contains(res.Verdict(), "test: internal/beta, internal/delta") {
		t.Errorf("verdict %q", res.Verdict())
	}
}

func TestShipLandedButRedNamesThePackagesInTheVerdict(t *testing.T) {
	r := Result{Landed: true, SHA: "abcdef1234", AfterRan: true, AfterRed: []string{"test"}, AfterRedText: "test: internal/beta, internal/delta"}
	if got := r.Verdict(); got != "ship: LANDED abcdef1, check after landing FAIL (test: internal/beta, internal/delta)" {
		t.Errorf("verdict %q", got)
	}
	r.AfterRedText = ""
	if got := r.Verdict(); !strings.HasSuffix(got, "FAIL (test)") {
		t.Errorf("verdict without text %q", got)
	}
}

func TestShipWithoutAVerdictLineReadsTheExitCode(t *testing.T) {
	f := newFixture(t)
	var out bytes.Buffer
	o := f.opts(&out)
	o.Check = []string{"exit 7"}
	res := Run(o)
	if res.Landed || !strings.Contains(res.Reason, "exit 7 exited 7") {
		t.Fatalf("reason %q", res.Reason)
	}
	if !strings.Contains(out.String(), "printed no validate: verdict line") {
		t.Errorf("output does not say the exit code decided:\n%s", out.String())
	}
}

func TestShipRefusedFastForwardIsNotRetried(t *testing.T) {
	f := newFixture(t)
	// The main checkout has an uncommitted b.txt, which the branch adds.
	if err := os.WriteFile(filepath.Join(f.main, "b.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, f.main, "add", "b.txt")
	before := f.mainHead()
	res := Run(f.opts(&bytes.Buffer{}))
	if res.ExitCode() != ExitNotLanded || !strings.HasPrefix(res.Reason, "ff refused: ") {
		t.Fatalf("exit %d, reason %q", res.ExitCode(), res.Reason)
	}
	if f.mainHead() != before {
		t.Error("main moved")
	}
	if got := strings.Join(f.runs(), ","); got != "full" {
		t.Errorf("checks run %s, want one full check and no retry", got)
	}
	// Once the main checkout is clean, the next ship reuses the PASS.
	git(t, f.main, "rm", "-q", "--cached", "b.txt")
	_ = os.Remove(filepath.Join(f.main, "b.txt"))
	var out bytes.Buffer
	res = Run(f.opts(&out))
	if !res.Landed {
		t.Fatalf("second run: %s", res.Verdict())
	}
	if got := strings.Join(f.runs(), ","); got != "full" {
		t.Errorf("checks run %s: the remembered PASS was not used", got)
	}
	if !strings.Contains(out.String(), "check already passed at ") {
		t.Errorf("no remembered-PASS line:\n%s", out.String())
	}
}

func TestShipRefusesWhenTheMainCheckoutIsOnAnotherBranch(t *testing.T) {
	f := newFixture(t)
	git(t, f.main, "checkout", "-q", "-b", "release/v1")
	res := Run(f.opts(&bytes.Buffer{}))
	if !strings.Contains(res.Reason, "has release/v1 checked out, not main") {
		t.Fatalf("reason %q", res.Reason)
	}
	if len(f.runs()) != 0 {
		t.Error("checks ran")
	}
}

func TestShipRefusesUncommittedChangesInTheWorktree(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.wt, "b.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Run(f.opts(&bytes.Buffer{}))
	if !strings.HasPrefix(res.Reason, "uncommitted changes in ") {
		t.Fatalf("reason %q", res.Reason)
	}
}

func TestShipGivesUpWhenMainKeepsMoving(t *testing.T) {
	f := newFixture(t)
	o := f.opts(&bytes.Buffer{})
	o.Rounds = 2
	n := 0
	o.BeforeFF = func() {
		n++
		f.commit(f.main, "m.txt", strings.Repeat("m", n)+"\n", "feat: main moved again")
	}
	res := Run(o)
	if res.Landed || res.Reason != "main moved 2 times" {
		t.Fatalf("verdict %s", res.Verdict())
	}
	if res.Verdict() != "ship: NOT LANDED (main moved 2 times)" {
		t.Errorf("verdict %q", res.Verdict())
	}
}

func TestPlanRunsNothing(t *testing.T) {
	f := newFixture(t)
	f.commit(f.main, "c.txt", "c\n", "feat: c on main")
	mainBefore, headBefore := f.mainHead(), git(t, f.wt, "rev-parse", "HEAD")
	lines := Plan(f.opts(&bytes.Buffer{}))
	joined := strings.Join(lines, "\n")
	for _, want := range []string{`merge main into work/cli/x: "chore(merge): main into cli/x"`, "check: ", "merge --ff-only work/cli/x", "re-check: "} {
		if !strings.Contains(joined, want) {
			t.Errorf("plan lacks %q:\n%s", want, joined)
		}
	}
	if f.mainHead() != mainBefore || git(t, f.wt, "rev-parse", "HEAD") != headBefore || len(f.runs()) != 0 {
		t.Error("the plan changed something")
	}
}

func TestRecheckForTouchedGoPackages(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.wt, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"a", "b/c"} {
		if err := os.MkdirAll(filepath.Join(f.wt, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.commit(f.wt, "a/x.go", "package a\n", "feat: a")
	f.commit(f.wt, "b/c/y.go", "package c\n", "feat: c")
	f.commit(f.wt, "a/z_test.go", "package a\n", "test: a")
	got := RecheckFor(f.wt, "main", []string{"make validate"})
	want := []string{"go build ./...", "go vet ./...", "go test ./a/... ./b/c/..."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("RecheckFor = %q, want %q", got, want)
	}
	// Without a go.mod the re-check is the full check.
	if got := RecheckFor(f.main, "main", []string{"make validate"}); strings.Join(got, "|") != "make validate" {
		t.Errorf("no go.mod: %q", got)
	}
}

// TestShipNeverForcesPushesOrSkipsHooks reads the package for the git
// words that would rewrite history, publish or skip the commit hook.
func TestShipNeverForcesPushesOrSkipsHooks(t *testing.T) {
	b, err := os.ReadFile("ship.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`"--force"`, `"-f"`, `"push"`, `"reset"`, `"--hard"`, `"--no-verify"`, `"-X"`, `"-s", "ours"`} {
		if strings.Contains(string(b), bad) {
			t.Errorf("ship.go passes %s to git", bad)
		}
	}
}
