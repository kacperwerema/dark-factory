package ship

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// redOutput is go test's text for TestA failing in example.com/m/pkg, as
// make validate's test step prints it (a cheap step's line beside it).
const redOutput = `[vet] ok
--- FAIL: TestA (0.00s)
    --- FAIL: TestA/sub (0.00s)
    a_test.go:5: boom
FAIL
FAIL	example.com/m/pkg	0.12s
ok  	example.com/m/other	0.01s
FAIL
`

// mainFixFixture is the ship fixture with a Go module: pkg/a_test.go holds
// TestA, on main and on the work branch alike.
func mainFixFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.commit(f.main, "go.mod", "module example.com/m\n", "chore: module")
	if err := mkdirAll(f.main, "pkg"); err != nil {
		t.Fatal(err)
	}
	f.commit(f.main, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Fatal(\"boom\") }\n", "test(pkg): TestA")
	git(t, f.wt, "merge", "-q", "-m", "chore(merge): main into cli/x", "main")
	return f
}

func TestAMainCommitThatFixesTheFailingTestIsNamed(t *testing.T) {
	f := mainFixFixture(t)
	f.commit(f.main, "pkg/a_test.go", "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n", "fix(pkg): TestA passes")
	sha := git(t, f.main, "rev-parse", "--short", "main")

	got := MainFixHints(f.wt, "main", redOutput)
	want := "main has " + sha + " fix(pkg): TestA passes touching pkg/a_test.go: merge main and re-run"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("hints = %q, want [%q]", got, want)
	}
}

func TestAMainCommitInTheFailingPackageIsNamedByItsFile(t *testing.T) {
	f := mainFixFixture(t)
	f.commit(f.main, "pkg/a.go", "package pkg\n", "fix(pkg): the helper")
	got := MainFixHints(f.wt, "main", redOutput)
	if len(got) != 1 || !strings.Contains(got[0], "fix(pkg): the helper touching pkg/a.go: merge main and re-run") {
		t.Fatalf("hints = %q", got)
	}
}

func TestNoMainFixHintWithoutACommitTouchingTheFailingTest(t *testing.T) {
	f := mainFixFixture(t)
	if got := MainFixHints(f.wt, "main", redOutput); len(got) != 0 {
		t.Errorf("main not ahead: hints = %q", got)
	}
	if err := mkdirAll(f.main, "pkg/sub"); err != nil {
		t.Fatal(err)
	}
	f.commit(f.main, "other.txt", "x\n", "docs: elsewhere")
	f.commit(f.main, "pkg/sub/b.go", "package sub\n", "feat(sub): a subpackage, not the failing one")
	if got := MainFixHints(f.wt, "main", redOutput); len(got) != 0 {
		t.Errorf("main ahead but not in the failing package: hints = %q", got)
	}
	f.commit(f.main, "pkg/a_test.go", "package pkg\n", "fix(pkg): TestA")
	if got := MainFixHints(f.wt, "main", "ok  \texample.com/m/pkg\t0.1s\n"); len(got) != 0 {
		t.Errorf("no failing test: hints = %q", got)
	}
}

// ship's red check says the hint before its verdict.
func TestShipsRedCheckNamesMainsFix(t *testing.T) {
	f := mainFixFixture(t)
	var out bytes.Buffer
	o := f.opts(&out)
	o.Now = time.Now
	r := &runner{o: o, out: &out}
	f.commit(f.main, "pkg/a_test.go", "package pkg\n", "fix(pkg): TestA passes")
	script := "printf '%s' '" + strings.ReplaceAll(redOutput, "'", "") + "'; echo 'validate: FAIL (test)'; exit 1"
	_, red, _ := r.check("check", []string{script}, git(t, f.wt, "rev-parse", "HEAD"))
	if !strings.HasPrefix(red, "test: pkg") {
		t.Errorf("red = %q", red)
	}
	if !strings.Contains(out.String(), "fix(pkg): TestA passes touching pkg/a_test.go: merge main and re-run") {
		t.Errorf("no main fix hint in:\n%s", out.String())
	}
}

func mkdirAll(root, dir string) error {
	return os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755)
}
