package testresults

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Red text: what a red run's plain go test output says about why, so a
// one-line verdict can tell a real red from a load flake without the log:
// the packages that failed (each `FAIL\t<pkg>` line) and the first line a
// test binary's tmuxguard printed about what it left in its TMPDIR.

// maxRedPackages caps the packages a verdict names; the rest are "(+n)".
const maxRedPackages = 5

// RedText is the failing packages and the first tmuxguard leftover line in
// a go test run's text output.
type RedText struct {
	// Packages are the failing packages in the order go test printed them,
	// each once, the module path cut off (internal/ci, not
	// <module>/internal/ci).
	Packages []string
	// Leftover is the first `tmuxguard: <bin> left ...` line, "" when none.
	Leftover string
}

// failPkgLine is go test's per-package verdict for a red package:
// "FAIL\t<pkg>\t1.2s", "FAIL\t<pkg> [build failed]", "FAIL <pkg> [setup failed]".
var failPkgLine = regexp.MustCompile(`^FAIL\s+(\S+)(?:\s+(?:\[[^\]]*\]|\d+(?:\.\d+)?s))?\s*$`)

// leftoverLine is tmuxguard's message for what a binary left in TMPDIR.
var leftoverLine = regexp.MustCompile(`^tmuxguard: \S+ left .* in TMPDIR`)

// ParseRedText reads go test's text output; module is cut off the package
// paths ("" keeps them whole). Lines a parallel step prefixed with
// `[<step>] ` (make validate's cheap steps) are not the suite's and are
// skipped.
func ParseRedText(output, module string) RedText {
	var r RedText
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(output))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if r.Leftover == "" && leftoverLine.MatchString(line) {
			r.Leftover = line
			continue
		}
		m := failPkgLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pkg := ShortPackage(m[1], module)
		if !seen[pkg] {
			seen[pkg] = true
			r.Packages = append(r.Packages, pkg)
		}
	}
	return r
}

// ShortPackage is pkg without the module prefix: "internal/ci" for
// "<module>/internal/ci", "." for the module itself.
func ShortPackage(pkg, module string) string {
	if module == "" {
		return pkg
	}
	if pkg == module {
		return "."
	}
	if rest, ok := strings.CutPrefix(pkg, module+"/"); ok {
		return rest
	}
	return pkg
}

// PackageList is "a, b, c" for up to five packages, then "(+n)".
func PackageList(pkgs []string) string {
	if len(pkgs) <= maxRedPackages {
		return strings.Join(pkgs, ", ")
	}
	return strings.Join(pkgs[:maxRedPackages], ", ") + " (+" + strconv.Itoa(len(pkgs)-maxRedPackages) + ")"
}

// NameTestStep is a validate verdict's red steps with the test step's
// failing packages named: steps ["fmt-check", "test"] and packages
// [internal/ci internal/ship] give "fmt-check, test: internal/ci,
// internal/ship". Without packages the steps are joined as they are.
func NameTestStep(steps []string, pkgs []string) string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s
		if s == "test" && len(pkgs) > 0 {
			out[i] = "test: " + PackageList(pkgs)
		}
	}
	return strings.Join(out, ", ")
}

// GoModule is the module path in dir's go.mod, "" without one.
func GoModule(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}
