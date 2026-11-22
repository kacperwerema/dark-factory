package testresults

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The stand-in go test output: two red packages (beta twice, as two shards
// print it), a binary that left files in TMPDIR, and a cheap validate step's
// prefixed lines that are not the suite's.
func standInRedOutput(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "go-test-two-red-packages.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseRedTextNamesTwoFailingPackagesAndTheFirstLeftover(t *testing.T) {
	rt := ParseRedText(standInRedOutput(t), "example.com/standin")
	if want := []string{"internal/beta", "internal/delta"}; !reflect.DeepEqual(rt.Packages, want) {
		t.Errorf("packages = %q, want %q", rt.Packages, want)
	}
	if want := "tmuxguard: delta.test left dft-sock-1, scratch.txt in TMPDIR"; len(rt.Leftover) < len(want) || rt.Leftover[:len(want)] != want {
		t.Errorf("leftover = %q, want it to start %q", rt.Leftover, want)
	}
	if got := NameTestStep([]string{"fmt-check", "test"}, rt.Packages); got != "fmt-check, test: internal/beta, internal/delta" {
		t.Errorf("NameTestStep = %q", got)
	}
}

func TestParseRedTextReadsBuildFailuresAndKeepsPathsWithoutAModule(t *testing.T) {
	out := "FAIL\texample.com/standin/cmd/tool [build failed]\nFAIL example.com/standin [setup failed]\nFAIL\n"
	if got := ParseRedText(out, "").Packages; !reflect.DeepEqual(got, []string{"example.com/standin/cmd/tool", "example.com/standin"}) {
		t.Errorf("no module: %q", got)
	}
	if got := ParseRedText(out, "example.com/standin").Packages; !reflect.DeepEqual(got, []string{"cmd/tool", "."}) {
		t.Errorf("with module: %q", got)
	}
	if rt := ParseRedText("ok  \tx/y\t0.1s\nPASS\n", ""); rt.Packages != nil || rt.Leftover != "" {
		t.Errorf("green output read as red: %+v", rt)
	}
}

func TestPackageListCapsAtFive(t *testing.T) {
	if got := PackageList([]string{"a", "b", "c", "d", "e", "f", "g"}); got != "a, b, c, d, e (+2)" {
		t.Errorf("PackageList = %q", got)
	}
	if got := NameTestStep([]string{"vet", "test"}, nil); got != "vet, test" {
		t.Errorf("no packages: %q", got)
	}
}

func TestGoModuleReadsTheModuleLine(t *testing.T) {
	dir := t.TempDir()
	if GoModule(dir) != "" {
		t.Error("a folder without go.mod has a module")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("// x\nmodule example.com/standin\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := GoModule(dir); got != "example.com/standin" {
		t.Errorf("GoModule = %q", got)
	}
}
