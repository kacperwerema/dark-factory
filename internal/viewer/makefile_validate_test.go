package viewer

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
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
		cmd := exec.Command("make", "validate", "VALIDATE_STEPS="+steps)
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

// The steps that take seconds report before the suite that takes minutes, so
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
	want := "merge-check fmt-check vet catalog-check link-check leak-check codemap-check playtest-check test cover-cli"
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
