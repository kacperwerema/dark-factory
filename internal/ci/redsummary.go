package ci

import (
	"regexp"
	"strings"

	"github.com/kacperwerema/dark-factory/internal/testresults"
)

// validateVerdict is make validate's last line on red.
var validateVerdict = regexp.MustCompile(`(?m)^validate: FAIL \((.*)\)\s*$`)

// RedSummary reads a red command's output: red is its verdict with the
// failing go test packages named, "FAIL (test: internal/ci,
// internal/ship)" after make validate's "validate: FAIL (test)", "FAIL
// (internal/ci)" for a command without that line; "" when the output names
// neither. leftover is the first tmuxguard leftover line, so a file a test
// left in TMPDIR reads apart from a failing test (or a load flake) without
// the log. module is cut off the package paths.
func RedSummary(output, module string) (red, leftover string) {
	rt := testresults.ParseRedText(output, module)
	var steps []string
	if all := validateVerdict.FindAllStringSubmatch(output, -1); len(all) > 0 {
		steps = strings.Split(all[len(all)-1][1], ", ")
	}
	switch {
	case len(steps) > 0:
		red = "FAIL (" + testresults.NameTestStep(steps, rt.Packages) + ")"
	case len(rt.Packages) > 0:
		red = "FAIL (" + testresults.PackageList(rt.Packages) + ")"
	}
	return red, rt.Leftover
}

// FailedText is the failed command with its red summary: "make validate
// FAIL (test: internal/ci)", or the command alone without one.
func FailedText(failed, red string) string {
	if red == "" {
		return failed
	}
	return failed + " " + red
}
